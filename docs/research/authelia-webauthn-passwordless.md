# Authelia WebAuthn passwordless (passkeys): what's needed vs what's here

Date: 2026-08-04  
Scope: Simple passwordless login via passkeys for this repo's Authelia packaging (`authelia/authelia:4.39`).  
Sources: Authelia first-party docs / release notes only (see [Sources](#sources)).

## Executive summary

Passwordless passkey login is a **first-party Authelia 4.39+ feature**. For this stack it is mostly a config flip: keep WebAuthn enabled, set `webauthn.enable_passkey_login: true`, enroll a discoverable credential (passkey) per user after a normal first-factor login, then sign in from the portal with the passkey.

This repo already has the hard prerequisites for that path: Authelia **4.39**, HTTPS via Caddy, Postgres storage, file users, and access-control policies at **`one_factor`**. What is **not** present: any `webauthn:` block, passkey enrollment/testing docs, or intentional passkey UX. TOTP is already disabled.

Important product nuance from Authelia: a passkey login counts as **one factor**. That matches this repo's current `one_factor` rules. If you later raise policies to `two_factor`, Authelia will ask for a password after the passkey unless you turn on the **experimental** UV-as-two-factor option (unsupported, will be replaced).

## What's needed (minimal path)

Checklist for the *simple* passwordless path Authelia documents:

1. **Authelia ≥ 4.39.0** — passwordless passkeys shipped in 4.39.
2. **HTTPS / secure context** — WebAuthn and Authelia session cookies expect HTTPS (already true behind Caddy).
3. **Persistent storage** — WebAuthn credentials live in Authelia storage (Postgres here), not in `users.yml`.
4. **WebAuthn not disabled** — default is enabled (`webauthn.disable: false`); do not set `disable: true`.
5. **`webauthn.enable_passkey_login: true`** — shows passwordless passkey login on the portal; counts as a single factor.
6. **Discoverable credentials** — for passkeys, prefer `selection_criteria.discoverability: preferred` (default) or `required`.
7. **User verification preference** — default `preferred` is fine for simple use; use `required` if you want UV always prompted.
8. **Enrollment flow** — user must already exist and complete first-factor (password) once, then register a Security Key / Passkey from the portal (identity confirmation via notifier email/link).
9. **Working notifier** — enrollment sends an identity-verification email/link; this repo already has filesystem notifier (`notification.txt`).
10. **Access control aligned with factor model** — keep `one_factor` if you want passkey-only access without a follow-up password. Use `two_factor` only if you accept password-after-passkey or experimental UV counting.
11. **Modern browser / platform authenticator** — WebAuthn-capable browser; platform passkeys (e.g. iCloud/Google/Windows Hello) or a hardware key.
12. **Optional (stricter / NIST-ish)** — MDS metadata validation, `attestation_conveyance_preference: direct`, `prohibit_backup_eligibility: true` — not required for a simple local setup; can break consumer synced passkeys.

Not required for the simple path:

- Duo / mobile push
- LDAP (file backend is fine)
- Changing Caddy forward-auth wiring
- Custom RP ID config in Authelia for the default same-site portal case (RP is the Authelia portal origin: `https://auth.workflow.tech`)

## Config sketch (official defaults + minimal enable)

From Authelia's WebAuthn configuration docs; only the enable flag is required beyond defaults:

```yaml
webauthn:
  disable: false
  enable_passkey_login: true
  display_name: 'Authelia'
  # defaults below are usually enough for local/simple use
  attestation_conveyance_preference: 'indirect'
  timeout: '60 seconds'
  selection_criteria:
    attachment: ''
    discoverability: 'preferred'
    user_verification: 'preferred'
```

Stricter NIST-oriented sketch from Authelia's WebAuthn reference guide (more friction; not "simple"):

```yaml
webauthn:
  enable_passkey_login: true
  attestation_conveyance_preference: 'direct'
  filtering:
    prohibit_backup_eligibility: true
  metadata:
    enabled: true
    validate_trust_anchor: true
    validate_entry: true
    validate_status: true
    validate_entry_permit_zero_aaguid: false
```

Experimental (only if you need passkey alone to satisfy `two_factor`):

```yaml
webauthn:
  enable_passkey_login: true
  experimental_enable_passkey_uv_two_factors: true  # unsupported; will be replaced by custom policies
```

Env equivalents exist (`AUTHELIA_WEBAUTHN_ENABLE_PASSKEY_LOGIN`, etc.) per Authelia environment docs.

## Passwordless vs WebAuthn-as-2FA

| Mode | Authelia support | Version | Factor weight |
|------|------------------|---------|---------------|
| WebAuthn security key as **second factor** after password | Yes (long-standing) | pre-4.39 | Second factor |
| **Passwordless** login via Passkey | Yes | **≥ 4.39.0** | **One factor** |
| Passkey with user verification counting as `two_factor` | Experimental only | 4.39+ option | Experimental |
| Multiple WebAuthn credentials per user | Yes | ≥ 4.38.0 | n/a |

FAQ implications:

- After passkey login, Authelia may still ask for a **password** when the access-control rule is `two_factor`. That is intentional: passkey is treated as a single factor today.
- Enrollment of a security key/passkey still goes through the portal after first-factor success + identity verification (email link via notifier).

## What's already here

Inventory of this repository (`services/authelia/`):

| Piece | Status | Notes |
|-------|--------|-------|
| Authelia image `4.39` | Present | `services/authelia/compose.yml` — version that includes passwordless passkeys |
| HTTPS / Authelia portal | Present | Caddy terminates TLS; portal `https://auth.workflow.tech` |
| Forward-auth | Present | `infra/caddy/Caddyfile` → `/api/authz/forward-auth`, copies Remote-* headers |
| Postgres storage | Present | Credentials/session/WebAuthn state can persist |
| File user backend | Present | `users.yml` with `dev` sample user + password hash |
| Access policy `one_factor` | Present | `*.workflow.tech` → `one_factor` (compatible with passkey-as-one-factor) |
| Portal bypass | Present | `auth.workflow.tech` → `bypass` |
| TOTP | Disabled | `totp.disable: true` — leaves WebAuthn as the natural 2FA/passkey path |
| Notifier | Present | Filesystem `notification.txt` (needed for enrollment identity check) |
| Session cookie domain | Present | `workflow.tech` / `authelia_url: https://auth.workflow.tech` |
| `webauthn:` config | **Absent** | Relies on Authelia defaults (`enable_passkey_login: false`) |
| Passkey enrollment/test docs | **Absent** | `TESTING.md` only covers password login |
| ADR stance | Password-only for now | `docs/adr/0002-...` says Authelia is "password-only for now" |
| Duo / mobile push | Absent | Not needed for passkeys |
| MDS / credential filtering | Absent | Fine for simple local path |

Current auth config highlights (`services/authelia/config/configuration.yml`):

- No `webauthn` section
- `totp.disable: true`
- `access_control` default `deny`; `auth.workflow.tech` bypass; `*.workflow.tech` → `one_factor`
- File backend + Postgres + filesystem notifier

## Gap to "simple passwordless"

Smallest change set (not implemented by this note):

1. Add to Authelia config:

   ```yaml
   webauthn:
     enable_passkey_login: true
   ```

2. Keep access rules at `one_factor` (already true) so passwordless passkey alone is enough for `api.workflow.tech`.
3. Document enrollment: password login once → register passkey via portal (check `notification.txt` for the verify link in local compose) → subsequent logins via passkey.
4. Optionally update ADR / TESTING.md language from "password-only" to "password + optional passkey".
5. Avoid NIST-strict filtering locally unless you want to ban synced/backup-eligible passkeys.

Do **not** need for the simple path: raise policies to `two_factor`, enable experimental UV-as-two-factor, MDS metadata, or Duo.

## Caveats / open questions

- **Enrollment still needs a password (or other first factor) once** — Authelia's documented security-key registration starts after first-factor success; there is no "register passkey with zero prior credential" public signup (and public signup is explicitly out of scope for this project).
- **Synced passkeys vs NIST sketch** — `prohibit_backup_eligibility: true` conflicts with many consumer passkeys (iCloud/Google Password Manager). Prefer the minimal enable for local/dev.
- **RP ID / origins** — rely on Authelia serving WebAuthn from `https://auth.workflow.tech`; ensure browsers trust Caddy's local CA (already in TESTING.md) or WebAuthn will fail in the browser.
- **Experimental options** — `experimental_enable_passkey_uv_two_factors` and `experimental_enable_passkey_upgrade` are explicitly unsupported and scheduled for replacement/removal; avoid unless you accept churn.
- **Open**: whether Authelia requires any extra `server` / CORS / origins knobs beyond portal HTTPS for passkey login in this Caddy layout — not called out as required in the WebAuthn config page for the standard reverse-proxy portal setup; validate with one enrollment smoke test after enabling the flag. See Addendum: proxy forwarded headers matter for RP ID.

## Sources

- Security Key and Passkeys overview (passwordless since 4.39; one-factor semantics; UV experimental note; enrollment screenshots/flow): https://www.authelia.com/overview/authentication/security-key/
- WebAuthn configuration (`enable_passkey_login`, experimental flags, selection criteria, metadata): https://www.authelia.com/configuration/second-factor/webauthn/
- WebAuthn reference / recommended Passkeys config: https://www.authelia.com/reference/guides/webauthn/
- 4.39 release notes (Passkeys + passwordless; non-MFA by default; MDS; filtering; platform attachment): https://www.authelia.com/blog/4.39-release-notes/
- Environment variable mapping for WebAuthn options: https://www.authelia.com/configuration/methods/environment/
- Local packaging: `services/authelia/config/configuration.yml`, `services/authelia/compose.yml`, `infra/caddy/Caddyfile`, `docs/adr/0002-authelia-caddy-instead-of-custom-iam.md`

## Addendum

Primary-source gaps / corrections only (2026-08-04 follow-up). Does not change the executive summary or minimal enable path.

### Proxy headers are required for WebAuthn RP ID

Official proxy integration docs: without the required forwarded headers Authelia may be unable to “Properly identify the WebAuthn Relying Party Identifier making WebAuthn inoperable.” For the portal itself that means correct scheme/host detection (`X-Forwarded-Proto` / `X-Forwarded-Host`, with documented fallbacks). This closes the open question above: no separate WebAuthn `origins` / CORS config knob is documented for the standard reverse-proxy portal; RP identity is derived from the request origin Authelia sees. Smoke-test still wise after enabling the flag.

Source: https://www.authelia.com/integration/proxies/introduction/

### RP ID vs origin (precision)

Authelia v4.39 source (`GetWebAuthnProvider`): **RP ID** = request origin **hostname**; **RP origins** = the full origin URL string(s). There is no documented YAML key to set RP ID manually. W3C WebAuthn: an RP ID is a valid domain string; a credential is usable only with the same RP ID it was registered under.

So for this stack: RP ID ≈ `auth.workflow.tech`, RP origin ≈ `https://auth.workflow.tech` (not “the origin” as the RP ID itself). Changing portal hostname after enrollment breaks existing credentials for that RP ID.

Sources:
- https://github.com/authelia/authelia/blob/v4.39.0/internal/middlewares/authelia_context.go (RPID / RPOrigins construction)
- https://www.w3.org/TR/webauthn-3/#rp-id

### Template comments vs runtime defaults (gotcha)

`config.template.yml` for v4.39 comments `selection_criteria.discoverability: 'discouraged'` and `attachment: 'cross-platform'`, while the published WebAuthn config page / JSON schema defaults are `discoverability: preferred` and empty `attachment`. Authelia’s validator **warns** when `enable_passkey_login: true` and discoverability is `discouraged` (prefer `preferred` or `required`). Do not copy the template’s commented discoverability value when enabling passkeys.

Sources:
- https://raw.githubusercontent.com/authelia/authelia/v4.39.0/config.template.yml
- https://www.authelia.com/configuration/second-factor/webauthn/
- https://www.authelia.com/schemas/latest/json-schema/configuration.json (`$defs.WebAuthn` / `WebAuthnSelectionCriteria`)
- https://github.com/authelia/authelia/blob/v4.39.0/internal/configuration/validator/webauthn.go

### Schema references (explicit)

- Latest configuration JSON Schema: https://www.authelia.com/schemas/latest/json-schema/configuration.json
- Versioned (4.39) configuration schema path pattern used by Authelia docs: https://www.authelia.com/schemas/v4.39/json-schema/configuration.json
- First-party v4.39 config template (commented WebAuthn block): https://raw.githubusercontent.com/authelia/authelia/v4.39.0/config.template.yml
