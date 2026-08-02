# Propagate trusted identity headers after Envoy Check

Envoy is the first trusted boundary for browser requests. After IAM's
`Authorization.Check` adapter validates the Access Token online through the
same use case as `ValidateToken`, Check overwrites any caller-supplied identity
headers with trusted values (Envoy's default append=false). User ID, subject
kind, Access Level, Authentication Session ID, and Access Token expiry are
propagated. Wire names are `x-identity-user-id`, `x-identity-subject-kind`,
`x-identity-access-level`, `x-identity-authentication-session-id`, and
`x-identity-access-token-expires-at`. Downstream services must treat these
headers as authoritative only on authenticated transport from Envoy.
