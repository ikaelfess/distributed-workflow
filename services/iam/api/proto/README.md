# IAM protobuf contracts

Producer-owned gRPC contracts for Identity and Access live under `proto/`.

## ValidateToken

`iam/v1/validate_token.proto` defines `TokenValidationService.ValidateToken` for trusted internal callers. Generated Go lives in `../gen/iam/v1`.

## Tooling

From `services/iam/api`:

```bash
buf lint
buf generate
buf breaking --against '.git#branch=main,subdir=services/iam/api/proto'
```

`buf.yaml` enables the STANDARD lint rules and FILE breaking-change rules. CI gates for these commands land with the production-readiness ticket.
