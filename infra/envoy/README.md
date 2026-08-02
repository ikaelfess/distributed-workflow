# Envoy trust boundary

Envoy is the first trusted boundary for browser requests into the platform.

## Configurations

- `envoy.local.yaml` — local/insecure plaintext transport to IAM and the
  protected echo upstream. Used for development and most integration tests.
- `envoy.mtls.yaml` — production-like mTLS from Envoy to IAM gRPC and the
  protected echo upstream. Generate certificates with `./generate-certs.sh`.

Both configs:

- call IAM `envoy.service.auth.v3.Authorization/Check` with a **250 ms**
  timeout and `failure_mode_allow: false`
- overwrite caller-supplied `x-identity-*` headers via Check response headers
  (Envoy default `append=false`; see ADR 0004)
- bypass authorization for health and public authentication routes
- apply coarse per-IP local rate limits on public authentication routes
  (`envoy.local.yaml`, `remote_address` descriptors)

## Trusted identity headers

Documented in `services/iam/docs/adr/0004-trusted-identity-headers.md`.
