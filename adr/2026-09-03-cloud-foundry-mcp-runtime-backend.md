# 2026-09-03: Cloud Foundry MCP runtime backend

- **Status:** Accepted
- **Date:** 2026-09-03
- **Supersedes:** None
- **Superseded by:** None
- **Amended:** 2026-10-05 — the gateway-served runtime set is `RuntimeRemote` +
  `RuntimeVMCP`, not `RuntimeRemote` + `RuntimeComposite`. See "Amendment"
  below. Deployment packaging is recorded separately in
  [`2026-10-05-cloud-foundry-deployment-packaging.md`](2026-10-05-cloud-foundry-deployment-packaging.md).

## Related issues

None. This decision was made as part of the GSA-TTS MCP Server Hub effort and is
recorded here during that project's wind-down so the approach and its scope
boundaries survive. See `mcp-server-hub/planning/cloud_gov_plan.md` and
`cloud_gov_roadmap.md` in the sibling `GSA-TTS/mcp-server-hub` repo.

## Related ODPs

None.

## Context

The GSA-TTS fork needed to run Obot as a **Cloud Foundry application** (on
cloud.gov) rather than on raw AWS EC2/Docker or Kubernetes. cloud.gov shortens
the ATO path by inheriting an existing FedRAMP boundary, which was the primary
driver for evaluating a move off the hand-rolled EC2 deployment.

Obot's existing MCP runtime backends (`docker`, `kubernetes`/`k8s`) both assume
the gateway can start and orchestrate MCP server *workloads* on a container
platform it controls. Cloud Foundry has no equivalent local orchestration
primitive that Obot drives directly, so those backends do not fit.

However, not every MCP runtime needs orchestration. The `remote` and `composite`
runtimes are served by Obot's own gateway process — the gateway proxies to an
already-running remote URL or composes other servers in-process. These need no
workload deployment at all and can run unchanged on Cloud Foundry.

## Decision

Add a new MCP runtime backend, `cloudfoundry`, that implements the `backend`
interface for **exactly the gateway-served runtimes** (`RuntimeRemote`,
`RuntimeComposite`) and reports `ErrNotSupportedByBackend` for every runtime that
requires deploying a workload (`uvx`, `npx`, `containerized`). The API layer
translates that error into an HTTP 404 rather than a 500.

Deploying MCP servers as Cloud Foundry applications through the Cloud Controller
v3 API is **deliberately out of scope** for this backend. The stub methods
`getServerDetails`, `streamServerLogs`, and `restartServer` are the seams where
that work would grow. `shutdownServer` is an intentional no-op (nothing is ever
deployed, and it must not error on delete/finalizer/idle paths).
`transformObotHostname` is the identity function and `remoteConfig` returns the
operator's global validation config unchanged (no localhost/private-IP
exceptions, since this backend talks to nothing internal).

Implemented in:
- `pkg/mcp/backend.go` — `RuntimeBackendCloudFoundry` constant
- `pkg/mcp/manager.go` — wired into `NewSessionManager`; usage string updated
- `pkg/mcp/cloudfoundry.go` — the backend implementation

## Rationale

- **Minimal, honest surface.** Rather than fake or partially implement
  orchestration on a platform that has none of the same primitives, the backend
  supports only what genuinely needs no deployment and fails cleanly (404) for
  the rest. This keeps the ATO-relevant behavior easy to reason about.
- **Unblocks the cloud.gov path** for the common case (remote/composite servers,
  which cover the catalog's `remote` entries and any in-process composition)
  without committing to the larger Cloud Controller v3 integration up front.
- **Preserves operator security posture.** `remoteConfig` grants no network
  exceptions, unlike the docker backend which must relax private-IP blocking to
  reach bridge-network containers.

Alternatives considered: (1) forcing all servers through the `docker`/`k8s`
backends on cloud.gov — rejected, cloud.gov apps are not a Docker/K8s host Obot
controls; (2) implementing full CF app deployment via Cloud Controller v3 now —
deferred as too large for the POC and unnecessary for remote/composite servers.

## Consequences

- **Easier:** running Obot itself as a CF app and serving `remote`/`composite`
  MCP servers on cloud.gov.
- **Harder / deferred:** hosting `containerized`/`uvx`/`npx` MCP servers on
  cloud.gov. Any such server must instead be deployed as its own CF app and
  registered as a `remote` catalog entry, **or** this backend must be extended at
  the three stub seams to deploy via Cloud Controller v3.
- **Missing pieces (not in this change):** there is no CF `manifest.yml` for
  Obot, and no automated deploy pipeline. Those were planned but not built before
  wind-down (see `mcp-server-hub/planning/cloud_gov_plan.md`).
- This is **WIP preserved for wind-down**, not a validated production backend.

## References

- `pkg/mcp/cloudfoundry.go`
- `pkg/mcp/manager.go`, `pkg/mcp/backend.go`
- `GSA-TTS/mcp-server-hub` → `planning/cloud_gov_plan.md`,
  `planning/cloud_gov_roadmap.md`
- `WINDDOWN.md` (this repo)

## Amendment (2026-10-05)

When this work was rebased from its original base (upstream `3fabdab1b`) onto
`v0.26.2`, the gateway-served runtime set turned out to be named incorrectly.

**What changed:** `runtimeIsGatewayServed` tested for `RuntimeRemote ||
RuntimeComposite`. The correct set is `RuntimeRemote || RuntimeVMCP`.

**Why the original was wrong:** upstream retains `RuntimeComposite` *only* to
identify legacy resources during migration — `apiclient/types/mcpserver.go`
says so explicitly, and `pkg/controller/handlers/compositemigration/` rewrites
those resources into vmcp servers. The runtime that is actually gateway-served
is `RuntimeVMCP`, which `pkg/mcp/vmcp.go` constructs and which both the docker
and Kubernetes backends short-circuit alongside `RuntimeRemote`:

```go
// pkg/mcp/docker.go, pkg/mcp/kubernetes.go
if server.Runtime == types.RuntimeRemote || server.Runtime == types.RuntimeVMCP {
```

**Impact of the original error:** composite/vmcp servers would have been
rejected with a 404 by a backend that is in fact capable of serving them, while
a legacy `composite` resource mid-migration would have been accepted. Neither
was observed, because the backend was never run. This is exactly the class of
defect that the "builds successfully" verification standard at wind-down could
not catch.

**Also added in the same change:** `pkg/mcp/cloudfoundry_test.go`, covering the
runtime partition, the unsupported-operation error type (the API layer
type-asserts on `*ErrNotSupportedByBackend` to return 404 rather than 500), the
`shutdownServer` never-errors invariant, `transformObotHostname` identity, and
`remoteConfig` granting no network exceptions. The original change shipped with
no tests, which is how the runtime-set error survived.

**Verification (2026-10-05, branch `cloudgov-phase1` @ `v0.26.2`):**
`go build ./...`, `go test ./pkg/mcp/...`, `go test ./pkg/services/...`,
`make lint-go` (0 issues, golangci-lint v2.13.0), and `pnpm check` (0 errors)
all pass. The wind-down-era gate was `go build ./pkg/mcp/` alone, which is
exactly what let the runtime-set error through.

**Incidental fix:** `make lint-go` never actually linted in a fresh
environment. The `setup-env` target installs golangci-lint with `go install`
(writing to `GOBIN`/`GOPATH/bin`) and `lint-go` then invoked it by **bare
name**, so on any runner without that directory on `PATH` the target failed
with `No such file or directory` *before* linting — presenting as a tooling
error rather than a skipped gate. The `Makefile` now resolves the install
directory explicitly and invokes the binary by absolute path, so the target
either lints or fails loudly. This is an upstream defect and a candidate for
upstreaming.
