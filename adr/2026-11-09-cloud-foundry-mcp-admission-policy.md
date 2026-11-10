---
title: Use backend host exceptions for Cloud Foundry MCP admission
description: Preserve global SSRF blocking while admitting approved private MCP listeners.
status: draft
tier: 2
last_updated: 2026-11-09
nist_controls: ["AC-6", "SC-7", "SI-10", "SA-11", "CM-3"]
related_files:
  - pkg/mcp/manager.go
  - pkg/mcp/cloudfoundry.go
  - pkg/mcp/http.go
  - docs/docs/operations/cloudgov-mcp-admission.md
---

# 2026-11-09: Cloud Foundry MCP admission policy

- **Status:** Draft — implementation plan approved; acceptance requires human review and live verification
- **Date:** 2026-11-09
- **Supersedes:** None
- **Superseded by:** None
- **Decision maker:** Fork maintainer approving this scoped repair
- **Compliance:** Least privilege and SSRF mitigation (AC-6, SC-7, SI-10), regression verification (SA-11), scoped change control (CM-3). Impact level and ATO applicability require owner confirmation; no authorization is asserted here.

## Related issues

No issue was supplied. No issue or pull request was created by the AI agent.

## Related ODPs

None. This is a local restoration of the fork's existing private-route policy,
not a new backend, API, authentication flow, or deployment architecture.

## Context

Fork commit `0e154905c` attached backend host exceptions to strict remote MCP
admission. Upstream `ecdab0c742` introduced `ValidationOptions()` and switched
catalog initialization to it; merge `440feffb7` retained its raw startup policy,
losing the fork's exceptions. Internal components were rejected before C2C was
used, then dependent vMCPs could not resolve their component entries.

At fork revision `80ca4b428`, Cloud Foundry allowed only
`*.apps.internal:8080`. DIU's verified application listener is instead
`mcp-diu-solutions.apps.internal:8001`. Upstream `d2d8508c7` was inspected on
2026-11-09: its only change after `27e2aed1e` is CI configuration, not this repair.

## Decision

1. Have `ValidationOptions()` use `RemoteMCPURLValidationConfig()`: preserve
   the operator's flags and attach backend hosts, **not** Docker's relaxed flags.
2. Retain `*.apps.internal:8080` and add only
   `mcp-diu-solutions.apps.internal:8001`. Keep port-scoped C2C and private-only
   component routes as independent enforcement layers.
3. Preserve the OAuth client in `pkg/mcp/http.go`: only the configured backend
   server's exact host and port are exempt, including across redirects. Do not
   attach MCP exceptions to external OAuth client-metadata or generic catalog
   source/OpenAPI fetching.
4. Align remote admission's link-local policy with the existing outbound
   `IsCloudMetadata` classification, including metadata addresses outside
   link-local ranges.

## Rationale

The exact DIU exception is the smallest reviewable repair. A wildcard on port
8001 would exempt unrelated services. Unrestricted host ports, disabling global
private-IP blocking, public component routes, and weakened TLS verification are
not acceptable alternatives. A configurable exception registry is unnecessary
for one nonstandard listener and would add configuration and validation work.

The backend remains the single source of host exceptions for admission and MCP
outbound clients. Reusing the fork's existing accessor minimizes upstream merge
conflicts. Controlled private DNS responses in tests prove range enforcement;
failure to resolve a hostname is not sufficient evidence of SSRF blocking.

## Consequences

- Existing 8080 components and the exact DIU 8001 listener can pass admission
  without globally relaxing private, loopback, or link-local blocking.
- A DIU hostname/port change requires another reviewed fork change. New
  nonstandard listeners must not silently gain wildcard exceptions.
- Docker and Kubernetes admission now consistently see their existing named
  backend exceptions; broad backend address relaxations remain outbound-only.
- Address admission does not prove service health, C2C authorization, tool
  policy, authentication, or gateway usability. Live verification is a release gate.
- This record supplements, rather than replaces, the original backend and
  packaging decisions. Their historical claims about no internal exceptions
  describe earlier implementations, not the current network policy.

## References

- [Original backend decision](2026-09-03-cloud-foundry-mcp-runtime-backend.md)
- [Deployment packaging decision](2026-10-05-cloud-foundry-deployment-packaging.md)
- [Verification and rollout runbook](../docs/docs/operations/cloudgov-mcp-admission.md)
- `pkg/mcp/manager.go`, `pkg/mcp/cloudfoundry.go`, `pkg/mcp/http.go`
- `pkg/controller/handlers/mcpcatalog/cloudfoundry_validation_test.go`

Prepared with OpenCode AI assistance. Human review is required before merge or deployment.
