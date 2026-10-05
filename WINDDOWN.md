# Wind-Down Notes — GSA-TTS fork of Obot

> **⚠️ SUPERSEDED (2026-10-05): this work has RESUMED.**
>
> A credited cloud.gov space with **40 GB of memory and 150 routes** removed the
> memory quota that blocked deployment, and cloud.gov's automatic HTTPS removed
> the blocker on login.gov. Work resumed on branch **`cloudgov-phase1`**
> (rebased onto upstream `v0.26.2`).
>
> **What changed since wind-down:**
> - The Cloud Foundry backend was **rebased** onto `v0.26.2` (the fork had
>   drifted 193 commits) and an `upstream` remote now exists.
> - A **latent bug was found and fixed**: the backend tested for
>   `RuntimeComposite`, which upstream retains only for legacy migration. The
>   live gateway-served runtime is `RuntimeVMCP`. See the Amendment in
>   [`adr/2026-09-03-cloud-foundry-mcp-runtime-backend.md`](adr/2026-09-03-cloud-foundry-mcp-runtime-backend.md).
> - **Unit tests were added** (`pkg/mcp/cloudfoundry_test.go`) — their absence
>   is how the bug above survived wind-down.
> - A **Cloud Foundry entrypoint** (`cf-entrypoint.sh`), image build workflow
>   (`.github/workflows/cloudgov-image.yml`), and **deployment kit** (in
>   `GSA-TTS/mcp-server-hub` → `cloudgov/`) now exist.
> - New ADR: [`adr/2026-10-05-cloud-foundry-deployment-packaging.md`](adr/2026-10-05-cloud-foundry-deployment-packaging.md).
>
> The §4 gap table below is **historical** — most entries are now closed. For
> current status see `GSA-TTS/mcp-server-hub` → `cloudgov/README.md`.
>
> The rest of this file is preserved as the wind-down-era record.

---

> **Status (wind-down era):** The GSA-TTS MCP Server Hub effort is being
> **wound down.** This file explains what this fork of
> [Obot](https://github.com/obot-platform/obot) customizes and how to resume
> it. It is a snapshot for developers, not GSA policy.
>
> **This repo is one of four.** See the cross-repo map in the
> [`GSA-TTS/mcp-server-hub`](https://github.com/GSA-TTS/mcp-server-hub) repo
> (`planning/WINDDOWN-INDEX.md`) for how `mcp-server-hub`, this `obot` fork,
> `mcp-server-hub-catalog`, and `mcp-server-hub-tools` fit together.

---

## 1. What this repo is

A GSA-TTS **fork of upstream `obot-platform/obot`** (an open-source MCP/LLM
governance gateway). The fork exists to hold **one** code customization:

> A new **Cloud Foundry MCP runtime backend** (`cloudfoundry`) so Obot can run as
> a Cloud Foundry application on **cloud.gov** without a container-orchestration
> runtime for MCP servers.

Everything else in the repo is upstream Obot. The fork otherwise tracks upstream
with no divergence.

## 2. Fork status vs. upstream

- **Remote (`origin`):** `https://github.com/GSA-TTS/obot.git` (the fork).
- There is **no `upstream` remote configured**. To pull upstream changes, add
  one: `git remote add upstream https://github.com/obot-platform/obot.git`.
- The only GSA content is the Cloud Foundry backend (§3), now merged to the
  fork's `main` (branch `cloudfoundry-backend`, PR #3). Aside from that, history
  is upstream commits.

## 3. The GSA customization — Cloud Foundry runtime backend

**Files:**
- `pkg/mcp/backend.go` — adds the `RuntimeBackendCloudFoundry = "cloudfoundry"`
  constant.
- `pkg/mcp/manager.go` — wires `case RuntimeBackendCloudFoundry:` into
  `NewSessionManager` and updates the `MCPRuntimeBackend` usage string.
- `pkg/mcp/cloudfoundry.go` — the backend implementation (116 lines).

**What it does / does not do:**
- **Supports only gateway-served runtimes:** `RuntimeRemote` and
  `RuntimeComposite`. These need no workload deployment — Obot's own process
  serves them.
  > **Corrected 2026-10-05:** the second runtime is **`RuntimeVMCP`**, not
  > `RuntimeComposite`. Upstream retains `RuntimeComposite` only to identify
  > legacy resources during migration. See the Amendment in the ADR.
- **Everything else returns `ErrNotSupportedByBackend`** (→ HTTP 404):
  `uvx`, `npx`, `containerized`. This backend does **not** deploy MCP server
  workloads.
- **Deliberately out of scope:** deploying MCP servers as Cloud Foundry apps via
  the **Cloud Controller v3 API**. The stubs `getServerDetails`,
  `streamServerLogs`, and `restartServer` are the seams where that work would
  grow. `shutdownServer` is an intentional no-op; `transformObotHostname` is
  identity; `remoteConfig` grants no network exceptions.

**Why:** see the ADR — [`adr/2026-09-03-cloud-foundry-mcp-runtime-backend.md`](adr/2026-09-03-cloud-foundry-mcp-runtime-backend.md).
The driver was moving Obot onto cloud.gov to shorten the ATO path.

## 4. Known gaps / what is missing

| Gap | Notes |
|-----|-------|
| **No CF `manifest.yml`** | ~~There is no Cloud Foundry manifest to actually deploy Obot as a CF app. Planned, not built.~~ **CLOSED 2026-10-05** — `mcp-server-hub` → `cloudgov/manifest.yml`. |
| **No deploy pipeline** | **PARTIAL 2026-10-05** — image build is automated (`.github/workflows/cloudgov-image.yml`); `cf push` is still run by hand via `cloudgov/scripts/03-push.sh`. |
| **`containerized`/`uvx`/`npx` unsupported on this backend** | **STILL OPEN.** Phase 2 deploys such servers as their own CF apps on `apps.internal` and registers them as `remote` (see `cloudgov/scripts/06-mcp-apps.sh`). Implementing the three stub seams against Cloud Controller v3 is Phase 3, tracked separately. |
| **Not validated in production** | **STILL OPEN.** All local gates now pass, but no script has been run against a live cloud.gov space. |
| **No unit tests added** | ~~The backend is stubs + trivial passthroughs; behavior is documented in the ADR and doc-comments.~~ **CLOSED 2026-10-05** — `pkg/mcp/cloudfoundry_test.go`. Their absence is precisely how the `RuntimeComposite`/`RuntimeVMCP` bug survived to wind-down. |

The full cloud.gov plan (externalized Postgres/S3, memory-footprint testing,
egress tiers, phased validation) lives in the `mcp-server-hub` repo:
`planning/cloud_gov_plan.md` and `planning/cloud_gov_roadmap.md`. Current
implementation status lives in `mcp-server-hub` → `cloudgov/README.md`.

## 5. Verification (as of wind-down)

- `go build ./pkg/mcp/` — **passes** (confirmed 2026-09-03).
- No behavior change to existing backends; `docker` remains the default runtime
  backend, so existing (EC2) deployments are unaffected.

> **Updated 2026-10-05** (branch `cloudgov-phase1`, rebased onto `v0.26.2`):
> `go build ./...`, `go test ./pkg/mcp/...`, `go test ./pkg/services/...`,
> `make lint-go` (0 issues), and `pnpm check` (0 errors) all pass. "It builds"
> was never a sufficient gate — it is what let the runtime-set bug through.

## 6. If you resume this work — start here

1. Read the ADR (`adr/2026-09-03-cloud-foundry-mcp-runtime-backend.md`) and the
   `mcp-server-hub` cloud.gov plan/roadmap.
2. Add an `upstream` remote and rebase onto current `obot-platform/obot` before
   building on the backend — the fork will have drifted.
3. Author a Cloud Foundry **`manifest.yml`** for Obot (externalize Postgres and
   S3 per the plan) and validate the app boots on a cloud.gov sandbox.
4. Decide whether `remote`/`composite`-only is sufficient. If MCP servers must be
   hosted *by* the gateway on cloud.gov, implement the three stub seams
   (`getServerDetails`, `streamServerLogs`, `restartServer`) plus deployment
   against the **Cloud Controller v3 API**.
5. Add tests once the backend does real work.
6. Note the sibling **login.gov** work: it is **not in this repo** — it lives in
   `GSA-TTS/mcp-server-hub-tools` (`login.gov-auth-provider/`). The tools repo's
   provider depends on a forked `obot-platform/oauth2-proxy`.

## 7. Related repos

- **`GSA-TTS/mcp-server-hub`** — the umbrella project: AWS deploy kit + all
  planning docs (ROADMAP, PIVOT, cloud.gov + login.gov plans). Start there.
- **`GSA-TTS/mcp-server-hub-catalog`** — the catalog of MCP server entries the
  gateway indexes.
- **`GSA-TTS/mcp-server-hub-tools`** — fork of Obot's `tools`, adds the
  **login.gov auth provider**.

## 8. Provenance

- **Remote:** `https://github.com/GSA-TTS/obot.git`
- Point of contact: _(fill in team/POC before archiving)_
- The Cloud Foundry backend and this wind-down documentation were AI-assisted and
  require human review before being relied upon operationally.
