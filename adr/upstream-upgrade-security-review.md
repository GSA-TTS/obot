---
title: "Upstream upgrade dependency security review"
description: "Tracks unresolved dependency findings for the November 2026 upstream integration"
status: draft
tier: 2
last_updated: "2026-11-13"
---

# Upstream upgrade dependency security review

AI-assisted working security record; requires human review. This is not a risk
acceptance or an accepted architecture decision.

## SEC-1 — Hono advisories

- Status: lockfile reconciliation and operator-run audit passed for these
  advisories; affected UI tests and build pending.
- Evidence: operator-run pnpm audit reported seven advisories across `hono`
  4.13.15 and `@hono/node-server` 2.1.5.
- Change: workspace overrides select `hono` 4.13.16 and `@hono/node-server`
  2.1.6. npm metadata confirms compatible Node requirements, MIT licenses,
  and a compatible Hono peer range.
- Completion: reconcile the application lockfile, rerun the full audit, and
  pass affected UI tests and build before deployment.
- Operator evidence: after reconciliation with pnpm 12.9.1, the audit reported
  only the separate deepmerge advisory; all seven Hono/node-server findings
  were absent.

## SEC-2 — deepmerge prototype poisoning

- Status: open; deployment disposition required.
- Advisory: GHSA-gcqx-w48m-w2h4 / CVE-2026-93753 / CWE-1321.
- Evidence: pnpm audit reports affected versions through Svelte Vite tooling;
  the advisory lists no patched version. No direct reference to `deepmerge`
  was found under `ui/user/src`.
- Docs evidence: operator-run npm lockfile reconciliation succeeded. npm audit
  reported five low-severity package findings rooted in the same deepmerge
  advisory through Docusaurus's SVGR/SVGO dependency chain. Its suggested force
  fix downgrades preset-classic to 3.6.3 and is not approved; the project pins
  the Docusaurus family to 3.10.2.
- Uncertainty: absence of a direct import does not prove absence from generated
  runtime bundles or absence of untrusted input at transitive call sites.
- UI call-site evidence: operator inspected vite-plugin-svelte options.js;
  deepmerge combines defaults, loaded Svelte configuration, inline plugin
  options, and build-context options during configuration resolution. This
  supports configuration-time use, not an established request-handling path.
- Required investigation: inspect resolved dependency call sites and build
  outputs, determine input provenance and runtime reachability, and obtain a
  reviewed remediation or risk disposition. Do not suppress the audit finding.
- Pickup trigger: dependency installation/build evidence becomes available.

## COMPAT-1 — Prettier plugin peer mismatch

- Status: open; formatter verification required.
- Evidence: import-sort plugin 6.0.2 declares Svelte Prettier plugin 3.x;
  upstream selects 4.1.1. No versions changed solely to silence the warning.
- Operator evidence: Prettier checks passed on three merged TypeScript files
  and Tester.svelte. This sample demonstrates execution compatibility, not
  comprehensive compatibility or maintainer approval of the peer range.
- Further operator evidence: full UI lint (i18n tests, catalog validation,
  Prettier, ESLint) passed. Full UI tests passed: 206 files, 2096 tests.
  Svelte derived_inert and MSW error/unhandled-request warnings remain in the
  transcript and have not been established as pre-existing or suppressed.
- Completion: verify formatting compatibility and select a supported combination
  or obtain a documented maintainer-backed disposition before final verification.

## COMPAT-2 — SvelteKit cookie API

- Status: resolved graph and complete UI build verified by operator;
  post-change type checks and tests pending.
- Evidence: operator UI build failed during prerendering because cookie 0.7.0
  lacks `parseCookie`. Installed SvelteKit 3.1.1 declares cookie ^2.0.1 and
  imports parseCookie, parseSetCookie, and stringifySetCookie.
- Change: retain the legacy cookie override for other consumers and override
  SvelteKit's cookie dependency specifically to 2.0.1.
- Review: npm metadata lists MIT and Node >=22; operator Node 26 satisfies it.
  The targeted npm advisory query returned no findings for cookie 2.0.1 or
  0.7.0. This is not a full dependency security pass.
- Completion: verify the resolved SvelteKit dependency uses 2.0.1, audit the
  full graph, and rerun type checking, tests, and the complete UI build.
- Operator evidence: frozen installation passed; dependency trace shows
  SvelteKit using cookie 2.0.1 and Express/MSW retaining 0.7.0. Audit reports
  only the separate deepmerge advisory. UI SSR/client bundling, prerendering,
  and adapter-static output to build completed successfully. Chunk-size
  warnings remain unsuppressed.
- Post-change operator evidence: svelte-check reported zero errors and warnings;
  all 68 focused tests passed again, followed by full UI lint and all 2096
  UI tests. Cookie compatibility checks are complete for this local build;
  deployed authentication still requires live verification.
