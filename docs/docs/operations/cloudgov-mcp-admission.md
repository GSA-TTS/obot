---
title: cloud.gov private MCP catalog admission
description: Verify and roll out the fork's scoped private-route admission repair.
status: draft
tier: 2
last_updated: 2026-11-09
---

# cloud.gov private MCP catalog admission

This runbook applies to the GSA-TTS Obot fork's `cloudfoundry` backend, not
standard upstream Docker or Kubernetes deployments. It restores the existing
private-route model; it does not deploy MCP applications or change the catalog.
Prepared with OpenCode AI assistance; human review and live verification are
required before declaring the repair complete.

## Policy and failure mode

Catalog admission must receive the global address-blocking flags **plus** the
backend's host exceptions. Returning only the startup policy from
`SessionManager.ValidationOptions()` rejects private remote components, causing
dependent vMCP component-not-found errors.

Cloud Foundry exceptions are:

- `*.apps.internal:8080` — existing standard MCP listeners;
- `mcp-diu-solutions.apps.internal:8001` — exact DIU listener only.

Other internal services on 8001 and unapproved ports are not exempt. Routes are
private-only and C2C admits Obot only on each approved application port. The
host exceptions do not create or widen C2C policies. The DIU exception is
deliberately code-reviewed rather than a new configurable wildcard registry;
changing its host or port requires another reviewed change.

Keep `OBOT_SERVER_DISALLOW_PRIVATE_IPMCP`,
`OBOT_SERVER_DISALLOW_LOCALHOST_MCP`, and
`OBOT_SERVER_DISALLOW_LINK_LOCAL_MCP` enabled. In this fork the first name is
intentionally `IPMCP` without an underscore between `IP` and `MCP`.
Never use a global blocking bypass, public component route, or disabled TLS
certificate verification as a workaround.

MCP outbound HTTP uses the backend exceptions. OAuth discovery, registration,
and token exchange allow only the configured backend server's exact host and
port, not another internal component, even across redirects. Generic catalog
source/OpenAPI fetchers and external OAuth client-metadata fetchers do not
receive backend exceptions. Admission also blocks the same cloud metadata
addresses as the outbound transport under link-local blocking.

## Local verification — operator execution only

The agent sandbox must not execute Go tests/builds or Cloud Foundry commands.
Run these commands in the repository root using the project's existing local
toolchain. Commands below do not install dependencies; missing tools or cached
modules are **BLOCKED**, not a reason to install without approval.

```bash
git diff --check
gofmt -l pkg/mcp/manager.go pkg/mcp/cloudfoundry.go pkg/mcp/cloudfoundry_test.go pkg/mcp/manager_test.go pkg/controller/handlers/mcpcatalog/cloudfoundry_validation_test.go
GOPROXY=off GOTOOLCHAIN=local go test -count=1 -v ./pkg/mcp -run 'Test(CloudFoundry|ValidationOptions|OAuthServerAllowList|OAuthClientForServer|HTTPClientForServer)'
GOPROXY=off GOTOOLCHAIN=local go test -count=1 -v ./pkg/controller/handlers/mcpcatalog -run TestCloudFoundry
GOPROXY=off GOTOOLCHAIN=local go test -race -count=1 ./pkg/mcp ./pkg/controller/handlers/mcpcatalog ./pkg/mcpcatalog ./pkg/safehttp ./pkg/api/handlers/mcpgateway/oauth
GOPROXY=off GOTOOLCHAIN=local make test
GOPROXY=off GOTOOLCHAIN=local golangci-lint run
GOPROXY=off GOTOOLCHAIN=local go build ./...
npm --prefix docs run verify
```

Use the repository-pinned golangci-lint version (`v2.14.0`). `make lint`
can install a missing linter, so use the already-installed binary directly.
Full integration verification, after approval for its local Docker servers and
registry access, is `make test-integration`; it is not executed by this repair.

With already-approved security tools available:

```bash
gitleaks dir . --redact
GOPROXY=off GOTOOLCHAIN=local govulncheck ./...
```

`govulncheck` needs read-only vulnerability-database network access; obtain that
approval first. Capture tool versions, UTC timestamps, exit statuses, and actual
outputs. Redact secrets before sharing. Do not treat controlled DNS, a fake
storage client, or an in-process test proxy as a live cloud.gov pass.

### Verification transcript

Initial record: source baseline `80ca4b428`, upstream comparison `d2d8508c7`,
2026-11-09. Replace BLOCKED rows with actual dated observations as gates run.

| Gate | Status | Observed result |
|---|---|---|
| Read-only source/history/upstream inspection | PASS | Both reported defects confirmed; newer upstream commit is CI-only |
| Formatting and whitespace | PASS | 2026-11-09T22:07:11Z: `gofmt -l` and `git diff --check` returned no output after formatting |
| Static documentation checks | PASS | 2026-11-09T22:07:11Z: required frontmatter keys, local link targets, unique sidebar registration, and syntax of nine bash blocks passed; conflict-marker checks passed earlier; commands were not executed and this is not a Docusaurus build |
| MCP package race tests | PASS | Operator output recorded 2026-11-09T22:17:28Z: full MCP package `ok`, 13.730s, covering all new Cloud Foundry and ValidationOptions tests; earlier focused run passed (2.040s) before test corrections |
| Catalog controller race tests | PASS | Operator output recorded 2026-11-09T22:17:28Z: full catalog controller package `ok`, 19.316s, including subprocess-isolated admission/sync tests |
| Catalog validation and safehttp race tests | PASS | Operator output recorded 2026-11-09T22:17:28Z: catalog validation `ok`, 3.128s; safehttp `ok`, 2.108s |
| Corrected fixture focused race tests | PASS | Operator output: 5.072s; Kubernetes succeeded-pod, HTTP client, OpenAPI lifecycle and destination-policy tests passed. The supplied trailing `$` selected exact names and did not select CloudFoundry/ValidationOptions-prefixed tests; those ran in the full package check |
| OAuth storage-failure focused race test | PASS | Operator output recorded 2026-11-09T22:21:08Z: both status and callback subtests passed; package `ok`, 5.714s |
| Combined race check | PASS | Latest operator rerun after lint corrections: all five packages `ok`: MCP 15.991s, catalog controller 17.512s, catalog validation 3.706s, safehttp 3.941s, OAuth 46.386s; no race reports in supplied output |
| Full unit tests | PASS | Operator reports repository-wide tests passed; full command output, timestamp, and numeric exit status not supplied |
| Lint/SAST | PASS | Existing GOPATH binary verified as v2.14.0 (built with Go 1.27.3); latest operator rerun reported `0 issues.` after test style corrections |
| Secrets detection and SCA | BLOCKED | Operator execution and approved tools/access required |
| Go build | PASS | Operator supplied `GOPROXY=off GOTOOLCHAIN=local go build ./...`, no output, returned to shell prompt; numeric exit status not separately supplied |
| Documentation verification | PASS | Operator restored locked dependencies with `npm ci --no-audit --no-fund` (1411 packages), then `npm run verify`: TypeScript check passed, client/server compiled, Docusaurus generated static files successfully |
| Local integration tests | BLOCKED | Operator approval and execution required |
| Image build/publish | BLOCKED | Not authorized or executed |
| Live cloud.gov/gateway/external-client gates | BLOCKED | Not executed |

Documentation setup/build warnings retained: npm reported install scripts not
covered by `allowScripts` for core-js@3.49.0, esbuild@0.28.1, fsevents@2.3.3,
and workerd@1.20261001.1. No blanket script approval or allowScripts change was
performed by the agent. Node emitted an experimental localStorage warning
because no `--localstorage-file` was provided. Neither prevented the supplied
typecheck/build from completing. The agent's subsequent
`git diff -- docs/package.json docs/package-lock.json` returned no output:
dependency declarations and lockfile are unchanged. The successful build is
not a vulnerability or install-script review.

After the successful race run, lint requested style-only changes in the test
artifacts: integer-range syntax and direct embedded DNS header selectors. These
were applied without production changes. The operator subsequently reran lint
(`0 issues.`) and the complete five-package race gate (all `ok`); both now pass
against the style-corrected tree. Earlier focused rows describe prior runs; the
latest combined gate covers every test in all five packages. Exact operator run
timestamps and numeric shell exit statuses were not separately supplied.

The focused outputs include allowed 8080/DIU 8001 endpoints, unapproved ports
and hosts, local/private/metadata rejection, preserved global flags across
backends, catalog component resolution and six-tool profile persistence, and
scoped outbound/OAuth checks. These are local controlled-fixture results, not
live application or external-client verification. Numeric shell exit statuses
and toolchain versions were not separately supplied; the test outputs report
successful completion. The initial focused outputs are historical; the latest
full-package race results now verify all five affected packages after the
test-only corrections. The previous failed runs remain documented below.

The subsequent combined `go test -race -count=1` command above did not pass.
The new DNS helper's writes to `net.DefaultResolver` raced with lingering HTTP
dial goroutines from `TestHTTPClientForServer`; the same goroutines also raced
with the existing OpenAPI cache test's resolver replacement. The new policy
accessor exposed `openAPILifecycleBackend`'s missing `remoteConfig` implementation
(nil embedded backend), causing a panic. A separate existing Kubernetes fixture
starts `deployment.DeepCopy()` concurrently with fake-client initialization,
which mutates that Deployment's resource version. The operator approved two
additional test files for correction: `pkg/mcp/openapi_test.go` now implements
the test backend's policy method; `pkg/mcp/kubernetes_test.go` takes an independent
watcher snapshot after fake-client initialization and before starting the watcher.
Controlled-DNS tests and the asynchronous HTTP retry subtest now run in isolated
copies of the existing test binary, retaining race instrumentation and propagating
child failures/output. No tests are skipped, race flags disabled, or timing sleeps
added. Production repair code is unchanged. The subsequent five-package rerun
passed, recorded 2026-11-09T22:21:08Z; earlier failures were corrected rather
than waived or hidden by the focused passes.

Latest tracked blocker (2026-11-09T22:17:28Z):
`pkg/api/handlers/mcpgateway/oauth/local_login_storage_test.go` registers a GORM
query callback after `gatewayclient.New` starts background workers. The race
report shows callback-registry mutation concurrent with retention queries. The
owner approved an eleventh scoped file for a test-only correction: register the
callback before constructing the gateway client, arm failure injection after
pending-state setup, and use atomic injection state/counters. That correction
is applied, preserving storage-failure/no-retry/recovery assertions. Both the
focused OAuth race test and combined race gate subsequently passed, recorded
2026-11-09T22:21:08Z. No production OAuth code changed. This tracked blocker is
resolved by the test correction and successful operator reruns.
Database-closed/context-canceled cleanup messages also appeared; they are not
silently counted as clean output or assumed to be caused by this repair. No
broader worker-lifecycle changes are proposed without separate investigation.

## Build and publish — instructions, not execution

After human review, passing verification, and approval to commit/push, use a
reviewed fork revision. Do not build from a dirty tree or an unrelated upstream
merge. Preserve the deployed login.gov providers layer; obtain its exact digest
from the previous release record, not a floating `latest` tag.

The existing `.github/workflows/cloudgov-image.yml` supports manual dispatch;
this repair does not change it. It builds `linux/amd64` and tags the image
`ghcr.io/gsa-tts/obot:cloudgov-<seven-character-sha>`.

After separate build/publication approval, set `REVIEWED_REF` to the reviewed
fork branch containing the repair and `PROVIDERS_IMAGE` to its approved digest:

```bash
gh workflow run cloudgov-image.yml --repo GSA-TTS/obot --ref "$REVIEWED_REF" -f providers_image="$PROVIDERS_IMAGE"
gh run list --repo GSA-TTS/obot --workflow cloudgov-image.yml --branch "$REVIEWED_REF" --limit 5
gh run watch "$RUN_ID" --repo GSA-TTS/obot --exit-status
```

Confirm the run's full `headSha`, successful image/provider verification, image
digest, and `linux/amd64` platform. Do not infer success from a tag existing.
Check registry visibility/disclosure approval: this deployment currently expects
a publicly pullable image. Record an immutable `ghcr.io/gsa-tts/obot@sha256:...`
reference, not `latest`, for rollout. Preserve the previous image digest for rollback.

## Rollout and rollback — authenticated local terminal

Obtain change-window approval first. Set `OBOT_APP`, `DIU_APP`, `CF_ORG`,
`CF_SPACE`, and `OBOT_MANIFEST` from the existing deployment record;
`NEW_IMAGE` and `OLD_IMAGE` must be verified immutable image references.
Do not print `cf env`, `VCAP_SERVICES`, or bound credentials into shared logs.

Read-only preflight:

```bash
cf target
cf app "$OBOT_APP"
cf app "$DIU_APP"
cf network-policies --source "$OBOT_APP"
cf routes
cf curl "/v3/apps/$(cf app "$OBOT_APP" --guid)/revisions?order_by=-created_at&per_page=1"
```

Record the image from the current revision/deployment record and confirm it
matches `OLD_IMAGE`. The manifest must preserve the current command,
service bindings, routes, health checks, resources, and global security flags.
Review the delta before pushing; the intended change is the Obot image only.

After explicit live deployment approval:

```bash
cf push "$OBOT_APP" -f "$OBOT_MANIFEST" --docker-image "$NEW_IMAGE" --strategy rolling
cf app "$OBOT_APP"
```

If rolling deployment is unavailable, stop and agree on a downtime or existing
blue/green procedure; do not invent one that changes bindings/routes. Do not
scale DIU, replace its image, or modify C2C during this rollout. If a missing
C2C policy is discovered, stop for separate infrastructure approval. The exact
proposed DIU policy would be:

```bash
cf add-network-policy "$OBOT_APP" "$DIU_APP" --protocol tcp --port 8001
```

Existing 8080 services retain separate destination-specific policies. Do not
grant a port range, all destinations, or reciprocal policies.

Rollback on boot failures, unexpected admission widening, authentication
regressions, or failed gateway checks:

```bash
cf push "$OBOT_APP" -f "$OBOT_MANIFEST" --docker-image "$OLD_IMAGE" --strategy rolling
cf app "$OBOT_APP"
```

Rollback changes only the Obot image, not catalog entries, DIU, routes, C2C,
bindings, or persisted data. The old image may restore the known admission
failure; record that honestly. No schema migration is introduced by this repair,
but a live rollback is not presumed safe without the prior revision record.

## Live acceptance checklist

Capture UTC time, deployed image/revision, actor, request, observed response,
and PASS/FAIL/BLOCKED for each gate in the approved private change record.
Do not copy authentication tokens into transcripts.

1. **Identity and backend.** Confirm the deployed image digest maps to the
   reviewed repair revision. From Obot's application instance (if SSH is already
   authorized), read only the backend and security flags:

   ```bash
   cf ssh "$OBOT_APP" -c 'printf "backend=%s private=%s localhost=%s linklocal=%s\n" "$OBOT_SERVER_MCP_RUNTIME_BACKEND" "${OBOT_SERVER_DISALLOW_PRIVATE_IPMCP:-default:true}" "${OBOT_SERVER_DISALLOW_LOCALHOST_MCP:-default:true}" "${OBOT_SERVER_DISALLOW_LINK_LOCAL_MCP:-default:true}"'
   ```

   Confirm `cloudfoundry`; check the running process/deployment configuration
   for overrides rather than relying on environment alone.

2. **C2C and readiness from Obot.** Confirm DIU has one healthy instance, no
   public route, listener 8001, and source Obot → destination DIU TCP 8001 only.
   Check each 8080 component's separate policy. With already-approved SSH:

   ```bash
   cf ssh "$OBOT_APP" -c 'python3 -c "import urllib.request; r=urllib.request.urlopen(\"http://mcp-diu-solutions.apps.internal:8001/health\", timeout=10); print(r.status); print(r.read(4096).decode())"'
   ```

   Confirm the application's documented readiness endpoint too, if distinct
   from `/health`. A missing path is not proof of network failure; a healthy
   process is not proof of MCP readiness. Verify Obot `/api/healthz` over HTTPS.

3. **Reconciliation.** Ensure the configured catalog source actually contains
   the DIU entries (the inspected local catalog was on a feature branch).
   Use the authenticated administrator catalog refresh action. Poll until
   synchronization finishes; require empty relevant sync errors, current
   component URLs, resolved component IDs, and the DIU profile's exact six-tool
   list. Do not accept merely disappearing logs or retained stale entries.

4. **Real MCP gateway.** Use the authenticated gateway Inspector or an approved
   MCP client against `https://<obot-host>/mcp-connect/<vmcp-id>`; do not append
   `/mcp`. Complete MCP initialization, list all pages of tools, and call real
   read-only tools with schema-valid arguments. For DIU, test search plus detail
   retrieval using an ID returned by the live search. Capture observed data,
   not mocked responses. Account for any gateway tool-name prefixes/overrides.

5. **Tool restriction.** DIU's allowed upstream tools are exactly
   `search_solutions`, `get_solution`, `list_solutions`, `catalog_facets`,
   `search`, and `fetch`. Confirm `catalog_status` is absent from gateway
   `tools/list` and an explicit gateway `tools/call` to it is denied. Confirm
   no extra effective profile grants all tools or exposes the component directly.

6. **External authenticated client.** Connect a real external MCP client using
   its normal Obot OAuth flow and assigned profile. Require successful
   `tools/list` and a real `tools/call`, plus denial for unauthenticated access.
   A manually supplied admin token alone does not validate the client OAuth flow.

7. **Negative destinations.** In an approved nonproduction test workspace,
   attempt remote admission for another internal host on 8001, DIU on 8002,
   private IPs, localhost, link-local/metadata endpoints, and lookalike domains.
   Require rejection before any launch or request. Confirm outbound and OAuth
   policy through approved test fixtures; never actively query a live metadata
   service or unrelated private endpoint to prove blocking. A DNS failure alone
   is not a pass; inspect the blocking reason with controlled private DNS.

Human review, local test outputs, and all applicable live gates are required.
Do not label this repair complete based on static analysis or simulated tests.
