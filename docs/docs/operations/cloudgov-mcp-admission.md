---
title: cloud.gov private MCP catalog admission
description: Verify and roll out the fork's scoped private-route admission repair.
status: draft
tier: 2
last_updated: 2026-11-10
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

### Dependency-review build hold (2026-11-10)

The admission repair merged in PR 42 at `6b8dc90`. Image publication remains
paused for the separately approved security patch verification sequence.
The local verification branch `fix/security-patch-verification` stages Go JOSE
v4.1.6 and UI DOMPurify 3.4.17, using the existing Dependabot update checksums
without unrelated PostCSS churn. Exact docs overrides select DOMPurify 3.4.17,
Joi 17.13.9, fast-uri 3.1.9, and Undici 7.30.1. The narrower Undici approach
preserves Wrangler 4.147.0 and Miniflare 5.20261001.0-alpha, avoiding PR 38's
beta Miniflare upgrade. This retains existing prerelease tooling; it does not
remove prerelease dependencies. Docs lockfile regeneration is complete; static
comparison confirms only the four approved package entries changed. This branch is a combined verification staging
area, not a merged replacement for the six existing PRs (35, 37, 40, 41, 39, 38).
Those PRs remain the durable review/tracking items; final integration into them
and any commits/pushes require confirmation after verification.

Dependency-patch verification (operator-run, 2026-11-10):

| Gate | Status | Observed result |
|---|---|---|
| Docs locked install and resolved versions | PASS | 1411 packages; DOMPurify 3.4.17, Joi 17.13.9, fast-uri 3.1.9, Undici 7.30.1; Wrangler/Miniflare unchanged |
| Docs TypeScript and production build | PASS | Client/server compiled; static files generated |
| UI frozen install and version resolution | PASS | pnpm 12.9.1; lockfile supply-chain check passed; one DOMPurify version, 3.4.17 |
| UI check/lint | PASS | Svelte check: zero errors/warnings; Prettier and ESLint completed |
| UI browser/node tests | PASS with logged diagnostics | 188 files, 1843 tests passed; 334.84s |
| UI production build | PASS | Static adapter wrote `build`; SSR/client compilation completed |
| Go module integrity | PASS | Operator `go mod verify`: `all modules verified` after downloading Go JOSE v4.1.6 |
| Go OAuth race tests | PASS | Operator package result `ok`, 47.707s |
| Go repository-wide tests | PASS with skips/diagnostics | Operator `make test` completed main module, apiclient, and logger; cached results included; PostgreSQL tests skipped without OBOT_TEST_POSTGRES_DSN; database cleanup/error-path diagnostics retained |
| Go lint | PASS | Operator repository-pinned golangci-lint: `0 issues.` |
| Go build | PASS | Operator separately confirmed `GOPROXY=off GOTOOLCHAIN=local go build ./...` completed silently and returned to the shell prompt; numeric exit status not separately supplied |
| Vulnerability scans and final image scan | BLOCKED | Not yet executed |

Docs audit subsequently ran and returned findings, not a clean pass: zero
critical, two high, two moderate, and 23 low package entries (27 total, including
ancestor propagation). The four targeted docs packages are absent from this
report. Additional findings are tracked here pending scope approval: body-parser
1.20.6 → 1.20.9, follow-redirects 1.16.1 → 1.16.2, qs 6.16.0 → 6.16.1, and
serialize-javascript 7.0.5 → 7.1.3, using exact docs overrides after compatibility
verification. deepmerge 4.3.1 has no patched version listed in the advisory;
retain it for authorized applicability review rather than downgrading Docusaurus
or suppressing the audit. The ancestor entries are not 27 distinct advisories.
The operator approved the four additional docs patches. Exact override
declarations and lockfile regeneration are complete. Static comparison confirms
only the eight approved docs package entries changed; the body-parser update's
qs dependency range also changed as part of its published metadata. Wrangler
and Miniflare entries are unchanged. Renewed operator install/version listing
confirmed body-parser 1.20.9, follow-redirects 1.16.2, qs 6.16.1, and
serialize-javascript 7.1.3. TypeScript checking and Docusaurus production build
passed, with install-script/localStorage warnings retained. The latest docs
audit reports zero critical/high/moderate and five low package entries tracing
to the single deepmerge advisory GHSA-gcqx-w48m-w2h4. This is improvement, not
a zero-finding audit pass. deepmerge remains pending authorized disposition;
no downgrade, dismissal, or automatic audit fix was applied. UI audit and
provider/OS image scanning remain pending.

UI audit (operator-run, 2026-11-10): zero critical, three high, 15 moderate,
and six low entries. DOMPurify is absent from the refreshed report, but the UI
security gate is not clear. Additional tracked patch candidates, pending approval:
Vite 8.3.3; aligned Vitest/browser-playwright family 5.0.5; Hono 4.13.11;
@hono/node-server 2.1.3; body-parser 2.4.0 (the UI uses v2, unlike docs' v1);
ip-address 10.7.4; fast-uri 3.1.9; qs 6.16.1. Existing workspace overrides
pin vulnerable ip-address and fast-uri versions and must be updated rather than
merely refreshing the install. Vite patch tracking already exists in PR 29.
Zod 3.25.76 → 4.6.6 and minimatch v9/v10 → v11 are major-version changes,
not approved patch updates. The locked MCP SDK/zod-to-json-schema peers declare
Zod v4 compatibility, but that does not establish a safe migration without
validation. SvelteKit v3 remains in PR 36; deepmerge has no listed patch.
These findings remain recorded for separate compatibility/applicability decisions.
The operator approved the same-major UI update set. Exact package declarations
now select Vite 8.3.3 and Vitest/browser-playwright 5.0.5; workspace overrides
select Hono 4.13.11, @hono/node-server 2.1.3, body-parser 2.4.0, ip-address
10.7.4, fast-uri 3.1.9, and qs 6.16.1. UI lockfile regeneration and all UI
checks/audit must be rerun locally. Vitest's resolved browser/UI/mocker family
must be verified as aligned, not inferred from the top-level version pins.
No major migration or alert dismissal was applied. Earlier UI passes predate
this expanded dependency set and are historical only.

After the same-major UI updates, the operator supplied the full verification
output: frozen install completed, Vite resolved to 8.3.3, and browser/UI/mocker
plus Vitest/browser-playwright each resolved to one aligned 5.0.5 version.
Svelte check reported zero errors/warnings; Prettier/ESLint passed; all 188 test
files and 1843 tests passed (355.34s); the static production build completed.
Refreshed audit: zero critical, one high (Zod), one moderate (minimatch), three
low (two SvelteKit advisories and deepmerge). Same-major targeted findings are
absent from that report. Lockfile generation reported unspecified peer issues;
`pnpm peers check` identified @trivago/prettier-plugin-sort-imports 6.0.2 wanting
prettier-plugin-svelte 3.x while 4.1.1 is installed. Both versions predate the
security patch; formatter/lint succeeded, but that is not peer compatibility
clearance. No formatter downgrade or peer-policy bypass was applied. Svelte,
SSE, MSW missing-handler, and cookie SSR diagnostics remain documented above;
passing test totals do not prove those diagnostics expected. The dependency-review
gate remains open, particularly Zod's high finding. Major migrations have not
been authorized, and no remaining findings were dismissed.

Warnings retained: npm's unapproved install-script notices and Node localStorage
warning; Vite-specific mock-interceptor hooks ignored; Svelte `derived_inert`
and history navigation warnings; SSE/error-path logs; MSW missing-handler errors
in audit-log/guardrail/model tests; an SSR cookie module `exports is not defined`
diagnostic during tests; large-chunk/build timing warnings. The runner reported
all tests passing, but that does not establish these diagnostics as expected or
prove they are caused by DOMPurify. Existing specs were not edited or weakened.
The reported pnpm installation changes describe synchronization of an older
local node_modules tree; the Git dependency diff remains the narrow patch set.
No pnpm upgrade, install-script policy bypass, or broad tooling fix is authorized.

SvelteKit's major upgrade remains tracked in PR 36, outside this patch scope.
Withdrawn braces advisory GHSA-vfj7-8cjw-p6xm remains in alert 59 until an
authorized maintainer reconciles it; no dismissal or dependency workaround was
performed. Final provider-layer/OS image scanning and remaining alert disposition
are required before rollout. No security clearance follows merely from version
updates or a successful build.

#### Zod compatibility candidate — security gate remains blocked

OpenCode AI-agent read-only review, 2026-11-10T14:18:54Z, confirmed that
GHSA-mrhx-c7xc-m3w5 lists Zod versions below 4.6.6 as affected. However, the
referenced upstream fix protects leading upper-size constraints on v4 arrays,
maps, and sets; it does not automatically bound unbounded collection validation.
The advisory description mentions unbounded arrays, so a version-only audit
disappearance is not evidence that the full reported behavior is remediated.

Evidence reviewed over approved read-only HTTPS:

- https://github.com/advisories/GHSA-mrhx-c7xc-m3w5
- https://github.com/colinhacks/zod/pull/6652
- https://github.com/colinhacks/zod/commit/a0ebf970f2d752e5f58527cd2942ff48be4f46bd
- https://registry.npmjs.org/zod/4.6.6
- Release source at npm's published gitHead,
  `284243fb0d6532665afdc9239188773bb624f73c`, under
  `packages/zod/src/v4/core/schemas.ts` and `packages/zod/src/v3/types.ts`.

The installed MCP SDK 1.32.1 builds protocol schemas through `zod/v4`, even
with package version 3.25.76. Its compatibility helper also imports `zod/v3`
and `zod/v4-mini`. The published 4.6.6 v3 array implementation still walks
all elements after a size violation. The release v4 source contains an opt-in
`abortEarly` path, but the installed SDK helper does not pass that option.
SDK result schemas for tools, resources/templates, prompts/messages, and tool
content use unbounded arrays. Obot's TesterClient additionally filters malformed
tool entries using schema transforms/catches; its behavior requires renewed
verification. Tool structured-output JSON-schema validation uses AJV separately.
These are source findings, not a live memory-exhaustion reproduction or a formal
applicability dismissal.

The operator approved a dependency-only compatibility candidate: exact workspace
override `zod: 4.6.6`, required lockfile snapshots only, and this durable record.
SDK/converter versions and all previous security patches must remain unchanged.
Source, tests, shared fixtures, CI, architecture, commits, publication, and rollout
are not authorized by that approval. Stop for additional approval if any becomes
necessary. The unbounded-response finding is tracked in this dependency-review
record and continues to block security clearance pending remediation or an
evidence-based, authorized disposition.

| Candidate gate | Status | Observed result |
|---|---|---|
| Local baseline inspection | PASS | Working branch HEAD is `6b8dc90ed8fcb76c6680958e6cbf34a94c8a7d1f`; exactly eight expected modified files; initial whitespace check passed; local main is older and was not moved |
| Advisory/release source review | PASS with security limitation | Published fix and remaining unbounded/v3 behavior inspected; no exploit or OOM test executed |
| Exact override declaration | PASS | Exact Zod 4.6.6 candidate declared and operator frozen install/version listing subsequently confirmed resolution |
| Lockfile regeneration | PASS, operator-run | 2026-11-10T14:21:30Z: Node v26.13.0, pnpm 12.9.1; `pnpm install --lockfile-only --ignore-scripts` completed in 363ms; supply-chain policy result reused verification from 35 minutes earlier; peer warning retained; numeric exit status not separately supplied. Earlier sandbox attempt was BLOCKED because pnpm was absent |
| Candidate static verification | PASS | 2026-11-10T14:20:52Z: `git diff --check` and Python assertions for required frontmatter, balanced fences, bash-block syntax via `bash -n`, exact override, and unchanged vulnerable lockfile passed; no dependency execution or behavioral verification |
| Lockfile candidate inspection | PASS with comparison limitation | Zod 4.6.6 entry and integrity match approved npm metadata; SDK 1.32.1 and converter 3.25.2 unchanged; no Zod 3.25.76 resolution remains. Cumulative Git diff counts changed from 257/239 to 267/248 additions/deletions, consistent with required Zod lines; no byte-for-byte pre-Zod snapshot available. Cumulative Vite/Vitest and earlier overrides must not be mistaken for new Zod churn |
| Frozen install and resolved version | PASS, operator-run | Output recorded 2026-11-10T14:33:52Z: frozen install skipped resolution, completed in 1.4s, packages +3/-3; MSW postinstall completed in 279ms under existing policy; supply-chain policy verification reused from 3 minutes earlier; `pnpm why zod` reports one version, 4.6.6, through SDK 1.32.1 and converter 3.25.2 |
| Formatter peer compatibility | FAIL, pre-existing | `pnpm peers check` still reports prettier-plugin-svelte 4.1.1 installed versus 3.x wanted by @trivago/prettier-plugin-sort-imports 6.0.2; no bypass, downgrade, or policy change |
| Renewed UI check/lint | PASS, operator-run | Svelte check zero errors/warnings; Prettier reported all matched files compliant; ESLint completed without reported issues |
| Affected MCP tests | PASS, operator-run | 17 files, 224 tests; Node and Chromium projects; 22.11s; runner start 09:26:23 with timezone not separately supplied |
| Full UI tests | PASS with logged diagnostics, operator-run | 188 files, 1843 tests; 348.21s; runner start 09:26:46 with timezone not separately supplied; repeated SSE, Svelte, MSW, and cookie SSR diagnostics retained |
| UI production build | PASS with warnings, operator-run | Vite 8.3.3 SSR/client compilation completed; client 7.81s, overall 19.39s; static adapter wrote build; large-chunk and plugin-timing warnings retained |
| Refreshed UI vulnerability audit | Findings remain, no high/critical | Supplied JSON: zero high/critical, one moderate minimatch advisory, three low advisories (two SvelteKit, one deepmerge); GHSA-mrhx-c7xc-m3w5 absent. This is not zero-finding security clearance |
| Published/installed code and live boundary security verification | BLOCKED | Version resolution, existing tests and build do not independently prove bounded untrusted-response validation or exhaustive subpath coverage; no live or memory-exhaustion reproduction performed |
| Unbounded-response security gate | BLOCKED | Package update alone does not establish bounded validation; separate scope/disposition approval required |
| Image publication and live rollout | BLOCKED | Remain paused; not authorized or executed |

#### Decoded-message structure guard — approved scoped implementation

The operator approved defensive per-message limits of 1,000 entries in any
array/object, 50,000 value nodes including the root, and depth 64 with root at
zero. These are tester policy choices, not MCP protocol limits or measured
production maxima. A read-only static sizing pass over 1,600 complete literals
found maxima of 136 nodes, depth 7, 29 entries, and 5,428 serialized bytes;
dynamic fixtures were excluded. Inspected tests also include a 200-item array
and 300 KiB text. JSON represented inside a string is not traversed or changed.
A bounded installed-code probe at 2026-11-10T14:36:27.181Z confirmed input-scaled
issue counts for invalid SDK prompts/resources/tool content at 32/64/128 items;
v4 constrained arrays produced one issue, v3 constrained arrays produced n+1.
The probe used sandbox Node 22.22.1, not the operator's Node 26; it was not a
live endpoint or OOM reproduction. An initial require-path probe failed because
the SDK symlink path did not resolve pnpm peers, then passed with the real path.
The sizing script initially failed syntax parsing before inspecting fixtures,
then passed after correction at 2026-11-10T14:39:20.979Z.

Approved source scope is message-limits.ts and its new spec, logging-transport.ts
and its new spec, tester.svelte.ts and its existing spec, all four mcps locale
catalogs, and this runbook. No dependencies, lockfiles, shared fixtures, Go, CI,
authentication, or deployment configuration are changed by the guard. Operator
affected/full test passes above provide the pre-edit baseline for the existing
session spec; existing assertions are preserved and new cases added.

The guard uses iterative traversal with bounded queued work. Every incoming
decoded message is checked in LoggingTransport before its payload reaches the
MCP log or operation-specific SDK validation. It rejects rather than truncates,
records a localized MCP_MESSAGE_LIMIT_EXCEEDED error without the rejected
payload, signals closure immediately to settle SDK requests, and closes the
underlying transport to abort streams. Subsequent frames are ignored, sends
fail, session status becomes error, and explicit retry creates a new connection.
Existing validation/partial-tool handling is unchanged within the bounds.

| Structure-guard gate | Status | Observed result |
|---|---|---|
| Implementation | Applied, pending human review | Approved thresholds and locale strings wired at the shared incoming JSON/SSE message callback; structured failure preserved in session operations |
| Local guard execution | PASS, limited sandbox diagnostic | 2026-11-10T14:43:53.050Z: actual TypeScript-transpiled guard accepted exact nodes/container/depth thresholds, rejected limit-plus-one, and accepted 300 KiB text; Node 22.22.1; not project-standard Vitest verification |
| Scoped formatting/lint | PASS, sandbox executables | Installed Prettier --check passed for all ten approved UI files; installed ESLint passed for six source/spec files. Initial ESLint run found no-ex-assign in initialization handling; corrected using a separate failure variable and rerun successfully. Full operator lint remains required |
| New regression tests | PASS after test typing correction | Latest operator output recorded 2026-11-10T16:47:06Z: affected run 19 files/237 tests, 22.37s; runner start 11:45:03, timezone not separately supplied. Earlier affected run passed in 21.32s; full run 190 files/1856 tests, 382.05s predates correction. All new guard/session regressions passed; no tests weakened |
| Guard type checking | PASS after correction; earlier failure retained | Latest operator Svelte check: zero errors/warnings. Earlier run failed with one error: logging-transport.spec.ts called the concrete SDK one-argument onmessage property with metadata. Test now references the installed callback through LoggingTransport's precise callback type, preserving metadata assertions without any/suppression; production code unchanged |
| Guard full lint | PASS after test typing correction | Latest operator Prettier/ESLint completed without reported issues; all matched files conform |
| Guard full UI suite | PASS with diagnostics after correction | Latest operator output recorded 2026-11-10T16:55:23Z: 190 files/1856 tests, 380.49s; runner start 11:47:40, timezone not separately supplied. Earlier full run passed in 382.05s before test typing correction |
| Guard production build | PASS with warnings after correction | Latest operator Vite 8.3.3 built client in 9.43s, overall 22.96s; static adapter wrote build; large-chunk/timing warnings retained. Earlier build passed in 22.82s; build does not replace type checking |
| Guard vulnerability audit | Findings remain, refreshed after correction | Latest operator JSON: zero high/critical, one moderate minimatch and three low (two SvelteKit, deepmerge); Zod advisory absent; no dismissal or security clearance |
| Security clearance / live execution | BLOCKED | No live validation, production approval, or exhaustive DoS proof |

The latest guard run's UTC execution times and numeric statuses were not
separately supplied except the typecheck's explicit exit 1; runner starts were
11:34:21 and 11:34:44 with timezone not separately supplied. Full test diagnostics
again include SSE errors, derived_inert/history warnings, and MSW missing-handler
500 responses. This run also reports GET /oauth/mcp/login/layout as unhandled;
it is retained without attributing it to the guard or declaring it harmless.
Renewed operator typecheck, lint, affected tests and whitespace checks passed
after the test-only typing correction, recorded 2026-11-10T16:47:06Z. Individual
UTC execution times and numeric statuses were not separately supplied. The
operator subsequently reran full tests/build/audit/whitespace after correction,
recorded 2026-11-10T16:55:23Z: full suite and build passed, audit retained four
advisories, and whitespace had no reported output. These are current guard
verification results, not merely dependency-only historical passes. Repeated
SSE, Svelte and MSW diagnostics remained; cookie@0.7.0 SSR exports is not defined
appeared again at runner time 11:53:13. No warning was suppressed or established
harmless. Human review, security disposition, CI, secrets scanning, documentation
production verification and live validation remain pending; passing local
behavioral/build checks do not declare every rollout gate clear.

Residual limitations are tracked here: pre-decoding HTTP JSON/SSE buffering,
string byte size, aggregate log/cache retention, and arbitrary JSON-schema/AJV
computational complexity are not bounded by this increment. The inspected
gateway JSON hook limit is conditional on enabled hooks; the SSE event reader
has no explicit event-size cap. Neither constitutes universal protection for
the browser. Separate remediation scope or authorized evidence-based disposition
is required before comprehensive security clearance. Do not publish public
security issues or dismiss advisories based on this guard alone.

Run locally from ui/user, capture UTC times, exit statuses and full diagnostics:

```bash
pnpm run check
pnpm run lint
pnpm exec vitest run --browser.headless src/lib/services/mcp src/lib/components/mcp/tester
pnpm run test
pnpm run build
pnpm audit --json
git diff --check
```

The check command regenerates ignored Paraglide outputs. Missing tools are
BLOCKED; no global installs, blanket script approvals, peer bypasses, or weakened
tests are permitted. Report failures before widening the approved file scope.

Latest dependency-only operator output was recorded by the AI agent at 2026-11-10T14:33:52Z;
individual UTC execution timestamps and numeric shell exit statuses were not
separately supplied. These renewed results apply to the installed Zod 4.6.6
candidate, unlike the historical same-major passes. No source or tests were
modified for this migration. Existing affected tests cover schema forms/results,
discovery, output validation, errors, and capabilities, but their pass is not
an exhaustive security regression or live MCP acceptance test.

Repeated diagnostics in the supplied full run include Svelte `derived_inert`
and history warnings, SSE errors, MSW missing-handler/500 responses for egress
audit logs, catalog reveal, guardrails/providers, and model access policies, and
cookie@0.7.0 SSR `exports is not defined`. Build warnings include oversized
chunks and callback timing. They were not suppressed or established harmless,
and no new attribution to Zod is claimed. The Zod version-based high finding is
absent from the audit, while the source-evidenced unbounded-validation concern
remains tracked and blocks clearance pending separately approved remediation
or an authorized evidence-based disposition. Major minimatch/SvelteKit changes,
deepmerge disposition, formatter compatibility, alert 59 reconciliation, and
provider/OS image scans remain outside this candidate's approval.

Run the following in the authenticated local development environment from
`ui/user/`, using the existing pnpm 12.9.1 toolchain. Regeneration is limited to
the candidate; inspect the diff before installing. If unrelated package versions
change, stop and share the diff rather than discarding existing work.

```bash
date -u +%Y-%m-%dT%H:%M:%SZ
node --version
pnpm --version
pnpm install --lockfile-only --ignore-scripts
git diff -- pnpm-workspace.yaml pnpm-lock.yaml
git diff --check
```

After the lockfile delta is reviewed, use the existing approved installation
script policy; do not broaden it or bypass the known formatter peer warning:

```bash
pnpm install --frozen-lockfile
pnpm why zod
pnpm peers check
pnpm run check
pnpm run lint
pnpm exec vitest run --browser.headless src/lib/services/mcp src/lib/components/mcp/tester
pnpm run test
pnpm run build
pnpm audit --json
```

Capture each command's actual output, UTC time, and exit status. Audit findings
and the existing formatter peer failure must be retained, not converted into
clean passes. Verify schema parsing/conversion, tool discovery/calls,
resources/prompts, validation errors, and browser bundling. Missing coverage or
behavioral failures require reapproval for tests/source, not weakened checks.
Human review and separately approved live validation remain required.

#### Draft PR submission authorization

2026-11-10T17:01:34Z: the operator confirmed GSA-TTS/obot as the push and PR
destination, approved commit/push/draft creation, and explicitly approved public
disclosure of the complete 18-file patch after the AI agent verified the
repository is PUBLIC. Remote main remains
`6b8dc90ed8fcb76c6680958e6cbf34a94c8a7d1f`; GitHub's branch response reports
protected=false. No matching existing branch or PR was found. No upstream push,
merge, alert dismissal, image publication or live change is authorized.
Draft status preserves pending human review, CI, documentation production
verification and security disposition; it is not production approval.

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
