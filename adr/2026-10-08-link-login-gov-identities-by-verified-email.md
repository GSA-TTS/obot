# 2026-10-08: Link Login.gov identities by verified email

- **Status:** Accepted
- **Date:** 2026-10-08
- **Supersedes:** None
- **Superseded by:** None

<!--
Federal compliance metadata (per the federal-decision-records skill). Recorded
in-body rather than as frontmatter because this repository's ADR convention
(adr/template.md) does not use YAML frontmatter.

category:       authentication-identity
nist_controls:  ["IA-2", "IA-5", "IA-8", "AC-2", "AC-3"]
impact_level:   moderate
ato_relevance:  yes-internal
risk_treatment: mitigate
-->

## Related issues

None.

## Related ODPs

None.

## Context

Obot links identities from explicitly trusted providers to an existing user when their verified email addresses match. The Login.gov provider rejects userinfo responses with a missing identity, missing email, or false `email_verified` claim, but Obot did not classify that provider as verified. A user signing in through Login.gov with an email already owned by another provider therefore caused Obot to create a duplicate username and fail its unique constraint instead of linking the identity.

## Decision

Treat only `default/login-gov-auth-provider` as a verified-email provider, alongside the existing Google and GitHub providers. Link its identities to an existing active user only when the email hash matches a user whose email is already verified or predates explicit verification tracking. Do not link identities by username, Login.gov subject, or an unverified email.

## Rationale

The Login.gov provider establishes the trust needed by validating the signed ID token, retrieving userinfo from Login.gov, requiring a non-empty subject and email, and rejecting `email_verified=false`. Reusing Obot's existing verified-email linking path preserves the established account model and avoids destructive database repair. Restricting trust to the exact provider namespace and name prevents an arbitrary provider from opting into account linking.

## Consequences

A Login.gov identity with the same verified email as an existing Google, GitHub, or previously verified Obot user inherits that user's account and role. Operators must preserve the provider's userinfo verification behavior; weakening it requires revisiting this decision. Users with different emails remain separate even if their provider usernames collide, and the unique username constraint continues to reject that collision rather than linking accounts.

## References

- [`pkg/gateway/client/identity.go`](../pkg/gateway/client/identity.go)
- [`pkg/gateway/client/identity_user_limit_test.go`](../pkg/gateway/client/identity_user_limit_test.go)
- [Login.gov OpenID Connect userinfo](https://developers.login.gov/oidc/user-info/)
