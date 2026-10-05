# 2026-10-05: Cloud Foundry deployment packaging for cloud.gov

- **Status:** Accepted
- **Date:** 2026-10-05
- **Supersedes:** None
- **Superseded by:** None

<!--
Federal compliance metadata (per the federal-decision-records skill). Recorded
in-body rather than as frontmatter because this repository's ADR convention
(adr/template.md) does not use YAML frontmatter, and diverging would break the
fork's consistency with upstream.

category:       deployment-infrastructure
nist_controls:  ["SC-8", "SC-12", "SC-13", "SC-28", "CM-2", "CM-6", "SA-15", "SI-17", "AC-4", "SC-7"]
impact_level:   moderate
ato_relevance:  yes-boundary
risk_treatment: mitigate
-->

## Related issues

None yet. Follow-on work is tracked as issues enumerated in
`GSA-TTS/mcp-server-hub` → `cloudgov/README.md` "Known gaps".

## Related ODPs

None.

## Context

[ADR 2026-09-03](2026-09-03-cloud-foundry-mcp-runtime-backend.md) added the
`cloudfoundry` MCP runtime backend so Obot could run as a Cloud Foundry
application. It left the actual deployment unbuilt: no manifest, no credential
delivery, no pipeline. The wind-down notes recorded this as "planned, not
built."

Two things changed that make the deployment worth building now:

1. **A credited cloud.gov space with 40 GB of memory and 150 routes.** The
   previously dominant risk — that a 1 GB sandbox quota might not boot Obot at
   all — is gone. The question shifts from feasibility to right-sizing.
2. **HTTPS.** cloud.gov terminates TLS on every route. This is the single
   blocker that prevented **login.gov** from working on the AWS deployment,
   whose ALB was HTTP-only.

The deployment must solve three problems that have no equivalent in the AWS
kit:

- **Credential delivery.** AWS fetched secrets from Secrets Manager with the
  AWS CLI at boot. Cloud Foundry instead injects a `VCAP_SERVICES` JSON blob.
  Obot has no knowledge of it.
- **Credential encryption.** Obot's `custom` encryption provider requires an
  `EncryptionConfiguration` *file*, which upstream generates in a Helm **init
  container**. Cloud Foundry has no init containers.
- **MCP server hosting.** The `cloudfoundry` backend cannot deploy
  `containerized` servers, but the two pilot servers (CDC PLACES, NIH RePORTER)
  are catalogued as `containerized`.

## Decision

**1. Translate `VCAP_SERVICES` in an entrypoint wrapper, not in Obot.**

Add `cf-entrypoint.sh` to the image. It parses `VCAP_SERVICES` with `jq`, maps
credentials onto the `OBOT_SERVER_*` / `OBOT_ARTIFACT_*` variables Obot already
understands, and `exec`s upstream's unmodified `run.sh`. `manifest.yml` selects
it with `command:`; the image `ENTRYPOINT` is unchanged, so the same image still
works for docker and Kubernetes deployments.

**2. Fail closed on every missing binding.**

Any absent credential aborts the boot. This is load-bearing rather than
stylistic: upstream `run.sh` starts an **in-container PostgreSQL** whenever
`OBOT_SERVER_DSN` is empty. A soft failure would therefore produce an app that
looks healthy while writing all state to an ephemeral container filesystem that
is destroyed on every restart. The entrypoint also rejects a non-`https://`
hostname and a runtime backend other than `cloudfoundry`.

**3. Require credential encryption, generating the config file in the
entrypoint.**

`OBOT_SERVER_ENCRYPTION_PROVIDER=custom` with an AES-GCM key, over the same nine
resource types as `chart/templates/deployment.yaml`. The entrypoint writes the
file to `/tmp` with `umask 077` and unsets the key variable afterward. A missing
key is fatal.

This is a **deliberate posture change from the AWS deployment**, which ran with
`OBOT_SERVER_ENCRYPTION_PROVIDER=none` — credentials and OAuth tokens stored as
plaintext in PostgreSQL.

**4. Take cloud.gov's default PostgreSQL version (18), and verify rather than
assume.**

`02-services.sh` provisions `aws-rds` with no version pin. Obot documents
PostgreSQL 17+ and embeds 17 in its own image, so 18 is untested upstream.
Instead of assuming compatibility, `04-smoke.sh` asserts migrations applied by
counting tables in the `public` schema and reporting `SHOW server_version`. The
remedy (a one-line pin to 17) is documented in advance.

**5. Deploy MCP servers as internal-only CF apps registered as `remote`.**

Each server becomes its own CF app with an `apps.internal` route, **no public
route**, and a container-to-container network policy admitting only the gateway
on its port. It is then registered in Obot as a `remote` server.

**6. Start at 2 GB memory and measure.**

Deliberately generous for a 40 GB space. `05-measure.sh` writes a timestamped
report and the manifest is then right-sized from observation.

## Rationale

**Why an entrypoint wrapper rather than teaching Obot about `VCAP_SERVICES`?**
Platform credential discovery is deployment concern, not application logic.
Adding Cloud Foundry awareness to `pkg/services/config.go` would put
GSA-specific code on the fork's hottest rebase path for no behavioral gain. The
wrapper keeps the fork's Go diff to the backend plus its tests.

**Why fail closed rather than degrade?** The in-container PostgreSQL fallback
makes a soft failure *worse* than a hard one: the operator sees a running app
and discovers the data loss later. Per AGENTS.md §14.5, ambiguity halts.

**Why require encryption when AWS did not?** Obot stores OAuth refresh tokens,
user identities, and per-user API credentials in these tables. Plaintext at rest
is indefensible for a system intended to carry an ATO (NIST SC-28), and the cost
is one environment variable.

**Why `remote`-over-`apps.internal` instead of implementing Cloud Controller
v3 deployment now?** It achieves the same security property as `containerized`
on AWS — reachable only through the gateway — with zero new Go code, and
validates the network model before any automation is written against it.
Arguably it is *stronger*: on AWS the servers were sibling containers on one
shared host with no isolation between them. Cloud Controller v3 deployment
(Phase 3) is deferred, and the three stub seams in `cloudfoundry.go` remain its
growth point.

**Why not pin PostgreSQL 17?** Tracking the platform default avoids a pinned
version silently aging out of support, and the compatibility question is cheap
to answer empirically. The risk is accepted *with* a test that detects it and a
pre-written remedy, rather than accepted blindly.

Alternatives considered and rejected:

1. **A Go `vcap` subcommand instead of shell + `jq`** — more testable, but puts
   deployment-specific code in the fork's Go tree. Held as the fallback if `jq`
   proves unavailable in the Wolfi base image.
2. **Buildpack instead of a Docker image** — would discard the baked-in
   providers layer, which is exactly where the login.gov provider lives.
3. **Secrets in `manifest.yml` env** — visible in source control and `cf env`.
4. **Keeping `OBOT_SERVER_ENCRYPTION_PROVIDER=none` for parity with AWS** —
   rejected; parity with a weaker posture is not a reason.

## Consequences

**Easier:**

- HTTPS on day one, which unblocks login.gov and MCP OAuth.
- Credentials and OAuth tokens encrypted at rest (SC-28).
- Platform-managed, encrypted PostgreSQL and S3 instead of hand-provisioned
  RDS/EFS/S3 with manual KMS wiring.
- Every script idempotent, fixing a documented papercut in the AWS kit.
- Two-instance scaling is testable for the first time.

**Harder / deferred:**

- **Gateway-hosted MCP servers remain unavailable.** Every server needs its own
  CF app and a manual `remote` registration. This does not scale past a pilot
  and is the strongest argument for Phase 3.
- **Encryption key custody is unresolved.** cloud.gov does not broker AWS KMS
  the way it brokers RDS and S3, so the key lives in a user-provided service
  with no rotation, escrow, or HSM backing. **This is a production gate**
  (SC-12), tracked separately.
- **`cf ssh` is required** by the smoke test to read in-container environment
  state. Production hardening normally disables SSH; the smoke test will need
  another path then.
- **PostgreSQL 18 is unproven.** Mitigated by an explicit assertion, not solved.

**Compliance consequences:**

- **SC-8** — `sslmode=require` on the database DSN; platform TLS on all ingress.
- **SC-28** — AES-GCM encryption of credentials, identities, OAuth tokens, and
  audit logs at rest.
- **SC-12** — key management is **partially** addressed; custody is an open gate.
- **AC-4 / SC-7** — MCP servers have no public route; C2C policy admits only the
  gateway. `06-mcp-apps.sh` asserts both directions, failing hard if a server is
  publicly reachable.
- **CM-2 / CM-6** — the manifest and scripts are the versioned baseline; no
  configuration is applied by hand.
- **SI-17** — fail-closed boot on any missing credential.
- **SA-15** — one-command bootstrap (`make setup`) and verify (`make verify`).
- **ATO boundary** — this moves the authorization boundary from a bespoke AWS
  footprint into cloud.gov's FedRAMP boundary. The ATO package needs updating,
  and the key-custody gap must be closed or formally accepted first.

## References

- `cf-entrypoint.sh`, `Dockerfile`, `.github/workflows/cloudgov-image.yml`
- `pkg/mcp/cloudfoundry.go`, `pkg/mcp/cloudfoundry_test.go`
- [ADR 2026-09-03](2026-09-03-cloud-foundry-mcp-runtime-backend.md) — the backend
  itself, and the amendment correcting its gateway-served runtime set
- `GSA-TTS/mcp-server-hub` → `cloudgov/` (manifest, scripts, runbook),
  `planning/cloud_gov_plan.md`, `planning/datagov_subdomain_plan.md`,
  `planning/login_auth_roadmap.md`
- Upstream init-container encryption setup: `chart/templates/deployment.yaml`
- `apiclient/types/mcpserver.go` — runtime constants
