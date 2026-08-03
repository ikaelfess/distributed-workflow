# Use Authelia and Caddy instead of a custom IAM service and Envoy

We need browser sign-in for humans only—not service identities, platform Access Levels, public signup, or a custom token validator. Authelia replaces the Go IAM service (file/CLI-provisioned users, password-only for now); Caddy replaces Envoy and uses forward-auth against Authelia. Auth lives under `services/auth/` as packaging, not as a bounded context. Rejected alternatives: keep a slim custom IAM, Authelia with public signup (unsupported), Envoy ext_authz for the same job.
