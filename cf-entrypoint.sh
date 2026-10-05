#!/bin/bash
#
# Cloud Foundry entrypoint for Obot (cloud.gov deployment).
#
# Cloud Foundry injects service credentials as a VCAP_SERVICES JSON blob rather
# than through a secrets manager the app queries. This script translates that
# blob into the OBOT_SERVER_* / OBOT_ARTIFACT_* environment variables Obot's
# config package already understands, then hands off to the upstream run.sh.
#
# Design rules:
#   - Fail closed. A missing binding aborts the boot rather than silently
#     starting Obot with an in-container Postgres (upstream run.sh starts one
#     when OBOT_SERVER_DSN is empty) or with encryption disabled. Losing the
#     external database or writing plaintext credentials are both worse than
#     failing to start. (AGENTS.md 14.5)
#   - Never echo secret values. Only names and presence are logged.
#   - Touch no upstream file. run.sh and the Dockerfile ENTRYPOINT are
#     unchanged so the fork stays trivially rebasable onto upstream.
#
# Required services (names configurable via the *_SERVICE_NAME vars below):
#   obot-db         aws-rds     PostgreSQL
#   obot-artifacts  s3          artifact storage
#   obot-secrets    user-provided  API keys, tokens, encryption key
#
set -euo pipefail

log() { echo "[cf-entrypoint] $*"; }
fatal() {
  echo "[cf-entrypoint] FATAL: $*" >&2
  exit 1
}

DB_SERVICE_NAME="${DB_SERVICE_NAME:-obot-db}"
S3_SERVICE_NAME="${S3_SERVICE_NAME:-obot-artifacts}"
SECRETS_SERVICE_NAME="${SECRETS_SERVICE_NAME:-obot-secrets}"

log "starting Obot on Cloud Foundry"
log "app=${VCAP_APPLICATION:+set} instance=${CF_INSTANCE_INDEX:-0}"

command -v jq >/dev/null 2>&1 || fatal "jq is required to parse VCAP_SERVICES but is not installed in this image"
[[ -n "${VCAP_SERVICES:-}" ]] || fatal "VCAP_SERVICES is empty; no services are bound to this app"

# --- Listen port -------------------------------------------------------------
# Cloud Foundry assigns the port and routes traffic to it. Obot defaults to
# 8080, which matches CF's usual assignment, but relying on that coincidence
# breaks the day the platform changes it.
#
# UNVERIFIED: the env var name is derived from the `http-listen-port` flag in
# pkg/services/config.go by the obot-platform/cmd library's OBOT_SERVER_ prefix
# convention (the same convention that produces OBOT_SERVER_DSN from `DSN`).
# This has not been confirmed against a running binary. If it is wrong AND
# $PORT is not 8080, the app will listen on the wrong port and the /api/healthz
# health check will fail loudly rather than silently -- which is the acceptable
# failure mode. Confirm on first deploy with:
#   cf ssh <app> -c 'obot server --help | grep -i listen'
[[ -n "${PORT:-}" ]] || fatal "PORT is not set; Cloud Foundry must assign a listen port"
export OBOT_SERVER_HTTP_LISTEN_PORT="$PORT"
log "listen port: $PORT"

# --- Helpers -----------------------------------------------------------------
# Look a credential up by service instance name first, falling back to service
# label. cloud.gov keys VCAP_SERVICES by label (e.g. "aws-rds"), but matching on
# the bound instance name is what actually disambiguates two instances of the
# same label.
vcap_cred() {
  local service_name="$1" key="$2"
  jq -r --arg name "$service_name" --arg key "$key" '
    [.[] | .[] | select(.name == $name)] as $byName
    | (if ($byName | length) > 0 then $byName[0] else null end)
    | if . == null then "" else (.credentials[$key] // "") end
  ' <<<"$VCAP_SERVICES"
}

vcap_cred_by_label() {
  local label="$1" key="$2"
  jq -r --arg label "$label" --arg key "$key" '
    (.[$label] // []) as $svc
    | if ($svc | length) == 0 then "" else ($svc[0].credentials[$key] // "") end
  ' <<<"$VCAP_SERVICES"
}

# Resolve by instance name, then by label. Returns empty if neither has it.
cred() {
  local service_name="$1" label="$2" key="$3" value
  value="$(vcap_cred "$service_name" "$key")"
  if [[ -z "$value" ]]; then
    value="$(vcap_cred_by_label "$label" "$key")"
  fi
  printf '%s' "$value"
}

require_cred() {
  local value="$1" description="$2"
  [[ -n "$value" ]] || fatal "$description not found in VCAP_SERVICES"
  printf '%s' "$value"
}

# Percent-encode a DSN userinfo component. RDS-generated passwords routinely
# contain characters that are structurally meaningful in a URI (@ : / ? # &),
# and an unencoded one silently truncates the DSN into something that points at
# the wrong host.
urlencode() {
  local string="$1" index char out=""
  for ((index = 0; index < ${#string}; index++)); do
    char="${string:index:1}"
    case "$char" in
      [a-zA-Z0-9.~_-]) out+="$char" ;;
      *) out+="$(printf '%%%02X' "'$char")" ;;
    esac
  done
  printf '%s' "$out"
}

# --- Database (aws-rds) ------------------------------------------------------
log "resolving database credentials from service '$DB_SERVICE_NAME'"

DB_HOST="$(require_cred "$(cred "$DB_SERVICE_NAME" aws-rds host)" "database host")"
DB_PORT="$(cred "$DB_SERVICE_NAME" aws-rds port)"
DB_NAME="$(require_cred "$(cred "$DB_SERVICE_NAME" aws-rds db_name)" "database name")"
DB_USER="$(require_cred "$(cred "$DB_SERVICE_NAME" aws-rds username)" "database username")"
DB_PASSWORD="$(require_cred "$(cred "$DB_SERVICE_NAME" aws-rds password)" "database password")"
DB_PORT="${DB_PORT:-5432}"

# sslmode=require is mandatory: cloud.gov RDS rejects unencrypted connections,
# and Go's pq defaults to "prefer", which would silently downgrade rather than
# fail if that ever changed. (NIST SC-8)
export OBOT_SERVER_DSN="postgres://$(urlencode "$DB_USER"):$(urlencode "$DB_PASSWORD")@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=require"
log "database: ${DB_USER}@${DB_HOST}:${DB_PORT}/${DB_NAME} (sslmode=require)"

# --- Artifact storage (s3) ---------------------------------------------------
# Obot requires provider and bucket to be set together or not at all
# (pkg/services/config.go rejects exactly one of the pair).
log "resolving artifact storage credentials from service '$S3_SERVICE_NAME'"

S3_BUCKET="$(require_cred "$(cred "$S3_SERVICE_NAME" s3 bucket)" "S3 bucket")"
S3_REGION="$(require_cred "$(cred "$S3_SERVICE_NAME" s3 region)" "S3 region")"
S3_ACCESS_KEY_ID="$(require_cred "$(cred "$S3_SERVICE_NAME" s3 access_key_id)" "S3 access key ID")"
S3_SECRET_ACCESS_KEY="$(require_cred "$(cred "$S3_SERVICE_NAME" s3 secret_access_key)" "S3 secret access key")"
S3_ENDPOINT="$(cred "$S3_SERVICE_NAME" s3 endpoint)"

export OBOT_ARTIFACT_STORAGE_BUCKET="$S3_BUCKET"
export OBOT_ARTIFACT_S3_REGION="$S3_REGION"
export OBOT_ARTIFACT_S3_ACCESS_KEY_ID="$S3_ACCESS_KEY_ID"
export OBOT_ARTIFACT_S3_SECRET_ACCESS_KEY="$S3_SECRET_ACCESS_KEY"

# cloud.gov brokers S3 through AWS proper, so the plain "s3" provider works and
# needs no endpoint. If the broker ever hands back a non-AWS endpoint (GovCloud
# or a FIPS endpoint), switch to the "custom" provider, which is the only one
# that honors OBOT_ARTIFACT_S3_ENDPOINT.
if [[ -n "$S3_ENDPOINT" && "$S3_ENDPOINT" != *".amazonaws.com" ]]; then
  export OBOT_ARTIFACT_STORAGE_PROVIDER="custom"
  export OBOT_ARTIFACT_S3_ENDPOINT="$S3_ENDPOINT"
  log "artifact storage: custom S3 provider, bucket=${S3_BUCKET} endpoint=${S3_ENDPOINT}"
else
  export OBOT_ARTIFACT_STORAGE_PROVIDER="s3"
  log "artifact storage: s3 provider, bucket=${S3_BUCKET} region=${S3_REGION}"
fi

# --- Application secrets (user-provided) -------------------------------------
# Delivered via `cf cups obot-secrets -p '{...}'` so they never appear in
# manifest.yml, the repository, or `cf env` output for non-bound apps.
log "resolving application secrets from service '$SECRETS_SERVICE_NAME'"

secret() {
  cred "$SECRETS_SERVICE_NAME" user-provided "$1"
}

OPENAI_API_KEY_VALUE="$(require_cred "$(secret openai_api_key)" "openai_api_key (model provider key)")"
export OPENAI_API_KEY="$OPENAI_API_KEY_VALUE"

BOOTSTRAP_TOKEN_VALUE="$(require_cred "$(secret bootstrap_token)" "bootstrap_token")"
export OBOT_BOOTSTRAP_TOKEN="$BOOTSTRAP_TOKEN_VALUE"

ENCRYPTION_KEY_VALUE="$(secret encryption_key)"

# --- Credential encryption ---------------------------------------------------
# Obot's "custom" provider takes an EncryptionConfiguration file rather than a
# key. Upstream generates that file in a Helm init container; Cloud Foundry has
# no init containers, so it is generated here over the same resource list as
# chart/templates/deployment.yaml.
#
# The AWS EC2 deployment ran with OBOT_SERVER_ENCRYPTION_PROVIDER=none, meaning
# credentials and OAuth tokens sat in Postgres as plaintext. Requiring a key
# here is a deliberate posture improvement. (NIST SC-28)
if [[ -z "$ENCRYPTION_KEY_VALUE" ]]; then
  fatal "encryption_key not found in service '$SECRETS_SERVICE_NAME'.
  Obot would otherwise store credentials and OAuth tokens unencrypted.
  Generate one with:  openssl rand -base64 32
  then add it to the secrets service (see cloudgov/scripts/02-services.sh)."
fi

ENCRYPTION_CONFIG_FILE="/tmp/obot-encryption.yaml"
umask 077
cat >"$ENCRYPTION_CONFIG_FILE" <<EOF
kind: EncryptionConfiguration
apiVersion: apiserver.config.k8s.io/v1
resources:
  - resources:
      - credentials.obot.obot.ai
      - users.obot.obot.ai
      - identities.obot.obot.ai
      - mcpoauthtokens.obot.obot.ai
      - mcpauditlogs.obot.obot.ai
      - llmauditlogs.obot.obot.ai
      - mcpoauthpendingstates.obot.obot.ai
      - policyviolations.obot.obot.ai
      - properties.obot.obot.ai
    providers:
      - aesgcm:
          keys:
            - name: key0
              secret: "${ENCRYPTION_KEY_VALUE}"
      - identity: {}
EOF
unset ENCRYPTION_KEY_VALUE

export OBOT_SERVER_ENCRYPTION_PROVIDER="custom"
export OBOT_SERVER_ENCRYPTION_CONFIG_FILE="$ENCRYPTION_CONFIG_FILE"
log "credential encryption: custom AES-GCM over 9 resource types"

# --- login.gov auth provider (optional) --------------------------------------
# The provider binary is baked into the image at build time (from the
# mcp-server-hub-tools fork). These variables only configure it. When the JWT
# key is absent the variables are omitted entirely, so the image still boots
# with bootstrap-token auth.
LOGINGOV_JWT_KEY_VALUE="$(secret logingov_jwt_key)"
if [[ -n "$LOGINGOV_JWT_KEY_VALUE" ]]; then
  export OBOT_LOGINGOV_AUTH_PROVIDER_JWT_KEY="$LOGINGOV_JWT_KEY_VALUE"

  LOGINGOV_COOKIE_SECRET_VALUE="$(secret auth_cookie_secret)"
  if [[ -z "$LOGINGOV_COOKIE_SECRET_VALUE" ]]; then
    fatal "logingov_jwt_key is set but auth_cookie_secret is missing; the login.gov session cookie cannot be signed"
  fi
  export OBOT_AUTH_PROVIDER_COOKIE_SECRET="$LOGINGOV_COOKIE_SECRET_VALUE"
  unset LOGINGOV_COOKIE_SECRET_VALUE

  # Validate before logging success. Logging "configured" and then failing on
  # the next line is actively misleading in a crash-loop transcript.
  if [[ -z "${OBOT_LOGINGOV_AUTH_PROVIDER_CLIENT_ID:-}" ]]; then
    fatal "logingov_jwt_key is set but OBOT_LOGINGOV_AUTH_PROVIDER_CLIENT_ID is not; set it in manifest.yml"
  fi
  log "login.gov auth provider: configured (client_id=${OBOT_LOGINGOV_AUTH_PROVIDER_CLIENT_ID})"
else
  log "login.gov auth provider: not configured (no logingov_jwt_key); bootstrap-token auth only"
fi
unset LOGINGOV_JWT_KEY_VALUE

# --- Hostname sanity check ---------------------------------------------------
# Obot derives OAuth issuer, authorization, token, and callback URLs from this
# value. A wrong or http:// hostname produces an app that boots fine and then
# fails every OAuth and login.gov round-trip with an opaque redirect mismatch.
if [[ -z "${OBOT_SERVER_HOSTNAME:-}" ]]; then
  fatal "OBOT_SERVER_HOSTNAME is not set; set it to the app's HTTPS route in manifest.yml"
fi
if [[ "$OBOT_SERVER_HOSTNAME" != https://* ]]; then
  fatal "OBOT_SERVER_HOSTNAME must be an https:// URL (got '${OBOT_SERVER_HOSTNAME}').
  login.gov rejects non-HTTPS redirect URIs and Obot sets a Secure session cookie."
fi
log "hostname: ${OBOT_SERVER_HOSTNAME}"

# --- Runtime backend sanity check --------------------------------------------
# The cloudfoundry backend is the only one that can work here: docker needs a
# Docker socket and kubernetes needs a cluster, neither of which a CF app has.
# Defaulting silently to docker produces a confusing crash loop at session
# manager init.
if [[ "${OBOT_SERVER_MCPRUNTIME_BACKEND:-}" != "cloudfoundry" ]]; then
  fatal "OBOT_SERVER_MCPRUNTIME_BACKEND must be 'cloudfoundry' (got '${OBOT_SERVER_MCPRUNTIME_BACKEND:-unset}')"
fi
log "MCP runtime backend: cloudfoundry (remote/vmcp servers only)"

log "configuration complete; handing off to run.sh"
exec /bin/run.sh "$@"
