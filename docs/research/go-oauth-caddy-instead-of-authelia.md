# Replacing Authelia + Caddy `forward_auth` with a small Go OAuth service (Google / GitHub)

Date: 2026-08-14  
Scope: How a small Go backend doing OAuth 2.0 / OIDC login via **Google** and **GitHub** would sit behind this repo’s Caddy `forward_auth` in place of Authelia 4.39 (`services/authelia/`, portal `https://auth.workflow.tech`, gate on `api.workflow.tech`). Research only; not an implementation. Not “which IdP to buy.”  
Sources: First-party Caddy / Authelia / oauth2-proxy docs, `golang.org/x/oauth2` (pkg.go.dev + module source), Google Identity / GitHub OAuth docs, RFC 6749 / RFC 6265 / RFC 7636, OIDC Core, and this repo’s Caddyfile / ADRs (see [Sources](#sources)). Roundup blogs are not used as evidence.

This repo’s Authelia job (from ADR-0002 and current wiring): human browser sessions only; users created out-of-band (file backend, no public signup); Caddy `forward_auth authelia:9091 { uri /api/authz/forward-auth; copy_headers Remote-User Remote-Groups Remote-Email Remote-Name }`; password-only for now; auth is packaging, not a bounded context. ADR-0001: the gateway only proves a human is logged in; resource permissions stay in owning domains.

**Any recommendation to write a Go OAuth / session service contradicts ADR-0002** (`docs/adr/0002-authelia-caddy-instead-of-custom-iam.md`), which chose Authelia + Caddy **instead of a custom IAM** and explicitly rejected “keep a slim custom IAM.” Flagged in the ranking and [§8](#what-you-lose-vs-authelia-adr-0002). Swapping Authelia for **oauth2-proxy** is also an ADR-0002 reversal (different packaging, still not Authelia), but it is not the rejected “write a Go IAM” alternative.

## Executive summary

Caddy `forward_auth` does not speak OAuth. It clones the incoming request as a **GET** to an upstream verification URI. **2xx** → copy listed response headers onto the original request and continue. **Anything else** → copy the upstream response back to the browser (typically a **302 Location** to a login page). That is the whole contract. Authelia’s `/api/authz/forward-auth` is one implementation of that contract; a Go process can be another; oauth2-proxy’s `/oauth2/auth` is a third (401 instead of 302, so Caddy must `redir` on 401).

The loop, if you replaced Authelia with a Go service on `auth.workflow.tech`:

1. Browser hits `https://api.workflow.tech/…` (no session cookie, or invalid cookie).
2. Caddy `forward_auth` GETs the Go **decision** endpoint (Authelia-shaped: e.g. `/api/authz/forward-auth`), forwarding `Cookie` and `X-Forwarded-*`.
3. Go sees no valid session → **302** `Location: https://auth.workflow.tech/login?rd=<encoded original URL>` (or **401** if you copy oauth2-proxy and let Caddy redirect).
4. Browser follows to `/login`. Go stores CSRF `state` (+ PKCE verifier) in a short-lived cookie, redirects to Google or GitHub authorization-code endpoint.
5. Provider redirects to `/callback?code=&state=`. Go checks `state`, exchanges `code` (`golang.org/x/oauth2` `Config.Exchange`), loads identity (Google: ID token / userinfo; GitHub: `GET /user` + `GET /user/emails`), **rejects anyone not on an allowlist**, then `Set-Cookie` a session (HttpOnly, Secure, SameSite, `Domain=workflow.tech`) and 302s back to `rd`.
6. Browser retries `api.workflow.tech`. Caddy GETs the decision endpoint again; Go returns **200** plus `Remote-User` / `Remote-Email` / …; Caddy copies those headers and serves the app.

**Allowlist is mandatory for this repo.** Google or GitHub login without an email/login allowlist **is public signup**. Authelia file users and ADR-0002 reject that.

| Rank | Option | Why | Cost vs Authelia | ADR-0002 |
|------|--------|-----|------------------|----------|
| 1 | **Stay Authelia** | Already the Caddy `forward_auth` companion, first-party Caddy guide, file users, session cookie on `workflow.tech`, access_control. **No Google/GitHub login** (OIDC RP not supported). | Baseline | Matches |
| 2 | **oauth2-proxy** | Same providers (Google default, GitHub), first-party Caddy `forward_auth` to `/oauth2/auth`, `--authenticated-emails-file` / `--email-domain` allowlist, cookie + optional Redis. You do not write a Go authz server. | Extra hop; interstitial UI; **one provider per instance** (multi-provider “not complete”) | **Contradicts** (swap Authelia; not a custom IAM) |
| 3 | **Custom Go + x/oauth2** | Authelia-shaped `/authz` + `/login` `/callback` `/logout`. Needed only if you insist on **both** Google and GitHub in one cookie **and** Authelia `Remote-*` headers **and** owning the login HTML. | You own CSRF, `rd=` open-redirect, session store, allowlist, cookie theft, provider identity mix-up | **Contradicts** (the rejected slim custom IAM) |

**Recommendation for this repo:** stay Authelia **if** the password/passkey portal is enough. Authelia is an OIDC **Provider** (open beta) and first-party docs say it does **not** implement the OpenID Connect **Relying Party** role and does not intend to — so it cannot log humans in with Google or GitHub. If the actual product ask is those providers rather than “write Go,” use **oauth2-proxy** (one provider + email allowlist + Caddy snippet already documented). Do **not** write a Go OAuth service unless oauth2-proxy’s incomplete multi-provider story is a hard blocker — and even then it is an ADR-0002 reversal, not a config tweak.

YAGNI: oauth2-proxy already speaks Caddy `forward_auth` and Google/GitHub. The Go service is a reimplementation of that gate.

## Caddy `forward_auth` contract (primary docs)

First-party: [forward_auth (Caddyfile directive)](https://caddyserver.com/docs/caddyfile/directives/forward_auth). Caddy’s own Authelia example is the same shape as this repo’s `infra/caddy/Caddyfile`.

### What Caddy sends

The directive is an opinionated wrapper over `reverse_proxy`. Expanded form (Caddy docs):

- Always **GET** (so the incoming body is not consumed).
- **Rewrite** the URI to the configured verification path (`uri`).
- `header_up X-Forwarded-Method {method}` and `header_up X-Forwarded-Uri {uri}`, **in addition to** the `X-Forwarded-*` headers `reverse_proxy` already sets (`For`, `Proto`, `Host`).
- The clone still carries the original **Cookie** header (normal reverse-proxy behavior). The Go decision endpoint authenticates from that cookie, not from OAuth.

### Status codes

| Upstream status | Caddy behavior |
|-----------------|----------------|
| **2xx** | Access granted. Headers listed in `copy_headers` are copied from the **auth response** onto the **original request** (then the backend). Handling continues. |
| **Any other status** | The upstream **response is copied back to the client**. Caddy says this “should typically involve a redirect to login page of the authentication gateway.” |

Caddy does **not** require 302 specifically. Authelia uses 302/303/401 depending on method/XHR (see below). oauth2-proxy `/oauth2/auth` returns **202** (ok) or **401** (not); the first-party Caddy snippet catches 401 and `redir`s to `/oauth2/sign_in?rd=…`. A Go service can pick either shape; Authelia-shaped 302 needs no `handle_response` in Caddy; oauth2-proxy-shaped 401 does.

**202 is 2xx.** oauth2-proxy’s 202 is treated as success by Caddy’s `@good status 2xx`. 401 is not.

### `copy_headers` vs `Set-Cookie`

- **`copy_headers`** (Caddy): on 2xx only, copy named fields from the auth **response** onto the original **request** (to the app). This is how `Remote-User` reaches the backend. Those headers are **not** meant for the browser (Authelia Trusted Header SSO: inject into the backend request, not the client).
- **`Set-Cookie` on a 2xx authz response is not copied to the browser** by the stock `forward_auth` shortcut (success path only copies listed request headers). Session cookies must be set on **browser-facing** routes (`/callback`, `/logout`), which Caddy `reverse_proxy`s, not on the 200 decision.
- **`Set-Cookie` on a non-2xx authz response** *is* copied to the client (full response passthrough). Authelia’s validating-forwarded-auth example shows a 302 with `Location` **and** `set-cookie: authelia-session=…; domain=example.com; path=/; HttpOnly; secure; SameSite=Lax`. A Go service *may* set/clear cookies on 302 from `/authz`, but the login session itself is created on `/callback`.

### `Location`

On non-2xx, Caddy forwards `Location` as-is. Authelia builds `https://auth.example.com/?rd=<original URL>&rm=GET`. The Go service must do the equivalent: send the browser to **its own** `/login` (or provider chooser) with a return URL. Caddy will not invent that redirect unless you add oauth2-proxy-style `handle_response`.

### What Caddy does *not* require

No OAuth, no PKCE, no userinfo. The upstream is a **session gate**. Login HTML and token exchange happen on other routes that are **not** behind `forward_auth` (portal host `reverse_proxy`s Authelia today).

## OAuth / OIDC for Google vs GitHub

Browser sign-in here is RFC 6749 **authorization code** (`response_type=code`): redirect to the provider, get `code` + `state`, POST the code to the token endpoint with `client_secret` (confidential web client). OIDC Core’s authorization-code flow is that grant plus an **ID token** (and usually userinfo).

| | **Google** | **GitHub OAuth app** |
|---|------------|----------------------|
| Protocol | OAuth 2.0 **and** OpenID Connect (OpenID Certified) | OAuth 2.0 authorization code. **Does not implement OIDC in its OAuth flows and does not issue ID tokens** (GitHub auth discovery docs, intended for MCP; Actions OIDC is a different issuer) |
| Auth URL | `https://accounts.google.com/o/oauth2/v2/auth` (Discovery) / `google.Endpoint` uses `https://accounts.google.com/o/oauth2/auth` | `https://github.com/login/oauth/authorize` |
| Token URL | `https://oauth2.googleapis.com/token` | `https://github.com/login/oauth/access_token` |
| Identity | **ID token** JWT (`sub`, optional `email` / `email_verified` / `name` / `hd`) and/or userinfo `https://openidconnect.googleapis.com/v1/userinfo` | No ID token. `GET https://api.github.com/user` (`id`, `login`, `name`, `email` **nullable**). Private emails: `GET /user/emails` with `user:email` |
| Scopes for login | Must start with `openid`; add `email` and/or `profile`. Basic example: `openid email` | `read:user` and/or `user:email`. Empty scope = public profile only; `email` on `/user` is often `null` if private |
| `state` | Strongly recommended (CSRF). Google OIDC table also marks **`nonce` Required** (replay); OIDC Core treats nonce as optional for code flow | Strongly recommended (`state`). Abort if mismatch |
| PKCE | Supported (S256). Google **web-server** docs do **not** list `code_challenge`; confidential clients still send `client_secret`. x/oauth2 still recommends PKCE | **Strongly recommended**. `S256` only (`plain` unsupported). Send `code_verifier` on token POST if challenge was sent |
| Unique user key | Google: **`sub`**, never email (email can change; not unique) | GitHub: numeric **`id`** (stable); `login` can change. Re-fetch `/user` on every sign-in (“risk mixing user data”) |
| Email for allowlist | ID token / userinfo `email` when `email` scope granted; check `email_verified` | `/user/emails`: `email`, `primary`, `verified`, `visibility`. Need `user:email`. Prefer **verified** (+ typically primary) |

Google token response also includes `access_token`, `id_token`, `expires_in`. Google: if you received the ID token **directly** from Google over HTTPS using the client secret, you may treat it as valid; if you pass it to other components, validate `iss` / `aud` / `exp` / signature (JWKS from Discovery). For this gate, prefer `sub` + verified email from the ID token (or userinfo) and **do not** keep Google/GitHub access tokens in the session unless you need to call their APIs later.

GitHub: after `Exchange`, use the access token as `Authorization: Bearer` against the REST API. Do not put the GitHub token in a cookie the app needs; the session is **your** cookie, not GitHub’s.

x/oauth2 `AuthCodeURL` already sets `response_type=code`, `client_id`, `redirect_uri`, `scope`, `state`. Add PKCE with `S256ChallengeOption(verifier)` / `VerifierOption(verifier)`. Add Google `nonce` with `oauth2.SetAuthURLParam("nonce", …)` if you follow Google’s OIDC parameter table.

## Go service shape (routes, cookie, allowlist) using x/oauth2

Official library: **`golang.org/x/oauth2`** (RFC 6749 client; BSD-3-Clause). Endpoints: `google.Endpoint` in `golang.org/x/oauth2/google` (`AuthURL` / `TokenURL` / `DeviceAuthURL`, `AuthStyleInParams`); `github.Endpoint` in `golang.org/x/oauth2/github` (alias of `endpoints.GitHub`). Goth is **not** needed: two `oauth2.Config` values + `net/http` cookies cover this. Goth would wrap sessions/providers you can write in a few handlers.

`google` extra (ADC, JWT, `ConfigFromJSON`) is for Google **APIs / service accounts**, not required for a confidential web login.

### Routes

Portal host (`auth.workflow.tech`, **not** behind `forward_auth` — same as Authelia portal bypass):

| Path | Job |
|------|-----|
| `GET /login` | Optional chooser (Google / GitHub). Create unguessable `state` (RFC 6749 §10.12 **MUST** CSRF-protect the redirect URI; `state` SHOULD carry the binding). Generate PKCE verifier (`oauth2.GenerateVerifier`). Store `state`, verifier, provider, and intended `rd` in a **short-lived** HttpOnly cookie (or server-side stash keyed by `state`). `302` to `conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), …)`. |
| `GET /callback` | Compare `state` query to stored value; abort on mismatch. `conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))`. Load identity (Google ID token/userinfo vs GitHub `/user` + `/user/emails`). **Allowlist check** (below). Issue **session** cookie. `302` to safe `rd`. Clear the CSRF cookie. |
| `GET /logout` | `Set-Cookie` session empty / expired. Does **not** log the user out of Google/GitHub (same as oauth2-proxy `/oauth2/sign_out`). Optional later: Google `end_session` / GitHub has no standard RP-initiated logout for OAuth apps. |

Decision endpoint (Caddy `forward_auth` target; Authelia-shaped name keeps the Caddyfile familiar):

| Path | Job |
|------|-----|
| `GET /api/authz/forward-auth` (or `/authz`) | Read session cookie. Valid → **200** + `Remote-User`, `Remote-Groups`, `Remote-Email`, `Remote-Name` (empty groups is fine; ADR-0001 does not centralize permissions). Missing/invalid → **302** `Location: https://auth.workflow.tech/login?rd=<https URL of original request reconstructed from X-Forwarded-Proto/Host/Uri>`. Optional: 401 for `X-Requested-With: XMLHttpRequest` like Authelia. **403** if you ever deny an authenticated user (Authelia access_control); a Go MVP can skip that and only do “logged in?”. |

Do not run OAuth on the decision endpoint. Caddy rewrites the URI to `/authz`; the browser never sees that path.

### Session cookie

Match Authelia’s observed attributes (validating-forwarded-auth + session config): name of your choosing; `Domain=workflow.tech` (this repo’s Authelia cookie domain is `workflow.tech`, **no** leading dot); `Path=/`; **HttpOnly**; **Secure**; **SameSite=Lax**. RFC 6265: a leading dot on `Domain` is ignored; `Domain=workflow.tech` is sent to `auth.workflow.tech` and `api.workflow.tech`. Omitting `Domain` would **not** share the cookie across those hosts — the gate would never see the login cookie.

Authelia: session cookie is HttpOnly + Secure (HTTPS only). SameSite default **lax** (strict breaks many SSO redirects; none is discouraged).

Store **your** session id / signed claims (user key, email, name), not the provider access token, unless you have a later API need.

| Store | When | Ceiling |
|-------|------|---------|
| **Memory** map | Single replica, local compose | Lost on restart; not HA. `ponytail:` fine for a spike; upgrade to signed cookie or Postgres |
| **Signed + encrypted cookie** (oauth2-proxy default) | Stateless replicas | Size limits; concurrent refresh races (oauth2-proxy documents this). Needs a strong `cookie-secret` |
| **Server session in Postgres** (Authelia already has Postgres) | Revocation, inactivity timeout like Authelia | You re-own a session table Authelia already provides |

### Allowlist (not public signup)

Google’s own OIDC “authenticate the user” step: if the user is **not** in your database, send them to **sign-up**. That is public signup. This repo’s Authelia file backend and ADR-0002 forbid it.

After identity resolution, **before** `Set-Cookie` session:

- Google: allow if `email_verified` and email is on the list (and/or `hd` matches a Workspace domain — Google: do not trust the `hd` **request** parameter; trust the ID token `hd` claim).
- GitHub: allow if verified email is on the list **or** `login` / numeric `id` is on a login allowlist (`/user/emails` is required for private addresses).

Reject with 403 + no session cookie. Do not auto-insert rows.

Identity mix-up: **do not** key users by email across Google and GitHub. Same mailbox can be two people; Google forbids using email as the unique id (`sub` only). Store `google:<sub>` or `github:<id>` separately; if one human uses both, map them **out-of-band** on the allowlist (two entries, or an explicit link you maintain). GitHub: re-validate `/user` on every login so a changed `login` does not attach the session to the wrong person.

### CSRF `state` and `rd=`

- **`state`**: unguessable, one-time, bound to the browser (cookie or server store). RFC 6749 §10.12: client **MUST** CSRF-protect the redirect URI; `state` SHOULD carry the binding. GitHub: if `state` mismatches, abort (third party created the request).
- **`rd=` (return URL)**: open-redirect if you 302 to an attacker URL after login. oauth2-proxy: `--whitelist-domain` (e.g. `.workflow.tech`) or the `rd` is **ignored**. Authelia `default_redirection_url` is a same-cookie-domain https URL, not equal to `authelia_url`. Go must allow only `https` + host `api.workflow.tech` / `*.workflow.tech` (or a fixed fallback `https://api.workflow.tech`). No `//evil`, no `https://evil.workflow.tech.attacker.com`, no leftover `javascript:`.
- Provider `redirect_uri` is a **fixed** registered callback (`https://auth.workflow.tech/callback` or `/callback/google` + `/callback/github`), not `rd`. GitHub: disable callback wildcard matching unless you fully control every subdomain (they warn this sends codes to attacker-controlled paths). Google: `redirect_uri` must **exactly** match Cloud Console.

### PKCE

RFC 7636; x/oauth2 since v0.13.0: `GenerateVerifier`, `S256ChallengeOption`, `VerifierOption`. GitHub strongly recommends it (`S256` only). Google web-server applications still use `client_id` + `client_secret` on the token POST (PKCE is extra, not a secret replacement for this confidential client). Use PKCE **and** `state`. Store the verifier next to `state` (GitHub: cookie or session).

## Caddyfile sketch vs current Authelia wiring

Today (`infra/caddy/Caddyfile`): `auth.workflow.tech` reverse-proxies Authelia; `api.workflow.tech` `forward_auth authelia:9091` `/api/authz/forward-auth` + `copy_headers Remote-User Remote-Groups Remote-Email Remote-Name`. Authelia `access_control`: portal `bypass`, `*.workflow.tech` → `one_factor`. Session `domain: workflow.tech`, `authelia_url: https://auth.workflow.tech`.

High-level deltas only — not a rewrite.

### Authelia-shaped Go (302 from `/authz`)

```caddyfile
auth.workflow.tech {
	reverse_proxy go-oauth:8080
}

api.workflow.tech {
	forward_auth go-oauth:8080 {
		uri /api/authz/forward-auth
		copy_headers Remote-User Remote-Groups Remote-Email Remote-Name
	}
	reverse_proxy /* <backend>
}
```

Register Google/GitHub redirect URIs as `https://auth.workflow.tech/callback` (or per-provider paths still on that host). Cookie `Domain=workflow.tech` so Caddy’s cloned GET to `/authz` includes it.

### oauth2-proxy-shaped Go (401 from `/authz`, Caddy redirects)

Same as oauth2-proxy’s first-party Caddyfile: `handle` login/callback/logout without auth; other paths `forward_auth` + `@error status 401` → `redir * /login?rd={scheme}://{host}{uri}`. Only needed if the decision endpoint returns 401 without `Location`.

Drop Authelia compose, `users.yml`, and Authelia access_control. The Go allowlist replaces file users. `*.workflow.tech` portal bypass becomes “auth host has no `forward_auth`.”

## oauth2-proxy as the smaller alternative

oauth2-proxy **v7.15.x** first-party Caddy integration: `handle /oauth2/*` → proxy; other paths `forward_auth` `uri /oauth2/auth`; `--reverse-proxy=true`; on 401 `redir` to `/oauth2/sign_in?rd={scheme}://{host}{uri}`. Optional `copy_headers X-Auth-Request-User X-Auth-Request-Email` with `--set-xauthrequest` (names differ from Authelia `Remote-*`; rename with Caddy `copy_headers X-Auth-Request-Email>Remote-Email` if backends care).

Endpoints: `/oauth2/start`, `/oauth2/callback`, `/oauth2/sign_out`, `/oauth2/auth` (**202** or **401**). Cookie: `--cookie-secure=true` (default), `--cookie-httponly=true` (default), `--cookie-samesite`, `--cookie-domain=.yourcompany.com` (leading-dot form in their docs; RFC 6265 ignores the dot), `--cookie-secret` required for cookie store.

**Allowlist (first-party):** `--authenticated-emails-file` (one email per line) or `--email-domain=yourcompany.com`. `--email-domain=*` is “any email” = public signup — do not use here. GitHub extra: `--github-org` / `--github-team` / `--github-user` (usually with `--email-domain=*`, which this repo should not do unless org restriction is the only gate). Google extra: `--google-group` (Admin SDK; heavier).

**Session store:** cookie (default, signed/encrypted, stateless) or Redis. Same tradeoff as a Go cookie vs Postgres.

**Google and GitHub together:** provider list includes both; “the feature to implement multiple providers is **not complete**.” One oauth2-proxy process ≈ one IdP. Two providers ⇒ two instances (awkward cookies) or wait for alpha multi-provider — or the custom Go service. If one IdP is enough, oauth2-proxy wins on YAGNI.

Logout: `/oauth2/sign_out` clears **oauth2-proxy** cookies only; `rd=` for IdP logout must be on `--whitelist-domain` or it is ignored.

This is still an ADR-0002 swap. It is not a new bounded context if you treat it as packaging like Authelia — but it is not Authelia.

## What you lose vs Authelia; ADR-0002

| Authelia (here) | Go OAuth / oauth2-proxy |
|-----------------|-------------------------|
| First-party Caddy guide naming Authelia | Caddy generic `forward_auth`; oauth2-proxy has its own Caddy page; Go has none |
| Cannot be a Google/GitHub login client (OIDC RP unsupported, not planned) | Google/GitHub **are** the login |
| File users (`users.yml`), no public signup | You must **build** the allowlist; providers will happily authenticate anyone |
| Portal password (passkeys researched, 4.39+) | MFA/passkeys are **Google’s / GitHub’s**, not yours. No Authelia TOTP/WebAuthn enrollment |
| `access_control` (domain, path, subject, network, `one_factor` / `two_factor` / `deny`) | Decision endpoint is boolean “session valid?” unless you reimplement rules. ADR-0001 says the gateway should not own resource permissions anyway — losing Authelia ACL is acceptable if Caddy only gates “logged in” |
| Session inactivity / expiration / remember-me, Postgres session backend | You pick memory / signed cookie / Redis / Postgres and get the bugs |
| Authelia `Remote-*` metadata | You must emit the same headers or change backends |
| Packaging, not a bounded context (ADR-0002) | A Go service **is** a custom IAM (rejected). oauth2-proxy is another package, still a reversal |

ADR-0002 one-liner: Authelia replaces the Go IAM; Caddy replaces Envoy; humans only; no public signup; auth is packaging. Writing `services/go-oauth/` reopens the rejected alternative. oauth2-proxy is the honest “we want Google/GitHub instead of file passwords” packaging change — still update or supersede ADR-0002 before doing it.

## Caveats / primary-source gaps

- **Caddy `forward_auth` + `Set-Cookie` on 2xx:** inferred from the documented success path (copy listed headers onto the request only). No first-party sentence “Set-Cookie on 200 is dropped.” Treat session issuance as a browser-facing `/callback` concern.
- **Google PKCE for web applications:** Google’s *Using OAuth 2.0 for Web Server Applications* page (fetched 2026-08-14) does not mention `code_challenge`. GitHub and x/oauth2 do. Confidential client + PKCE is still the right default; do not drop `client_secret`.
- **Google `nonce`:** OIDC Core optional for code flow; Google’s authentication URI table marks it Required. Confirm against current Discovery / a real token request if implementing.
- **`google.Endpoint` AuthURL** is `https://accounts.google.com/o/oauth2/auth`; Google OIDC narrative uses `/o/oauth2/v2/auth` from Discovery. Both appear in first-party Google/Go docs; Discovery is the OIDC-correct source if they ever diverge.
- **GitHub OIDC discovery** is public preview, **MCP-oriented**, and states GitHub does **not** issue ID tokens on OAuth flows. Do not configure GitHub as an OIDC IdP for this login.
- **oauth2-proxy multi-provider** “not complete” (7.15.x providers page). Dual Google+GitHub in one cookie is the only first-party gap that argues for custom Go.
- **Header names:** Authelia `Remote-*` vs oauth2-proxy `X-Auth-Request-*`. This repo’s Caddyfile copies Authelia names; backends that only `respond "authenticated"` today do not care.
- **SameSite:** RFC 6265 has Domain / Secure / HttpOnly; SameSite lives in later cookie drafts. Authelia documents `same_site: lax` and points at MDN. Use Lax unless you measure a breakage.
- **Goth:** not evaluated beyond “x/oauth2 already has both endpoints.” No first-party reason to add it.
- **This note does not implement anything.** Cookie theft (XSS still sends HttpOnly cookies on `fetch`; bind session to something you accept losing, short expiry, Secure), provider token storage, and refresh-token handling are sketched only.

## Sources

- Caddy `forward_auth`: https://caddyserver.com/docs/caddyfile/directives/forward_auth
- Caddy `reverse_proxy` (pre-check / `forward_auth` shortcut mention): https://caddyserver.com/docs/caddyfile/directives/reverse_proxy
- Authelia Caddy integration: https://www.authelia.com/integration/proxies/caddy/
- Authelia OIDC Provider (no Relying Party / no Google-GitHub login): https://www.authelia.com/configuration/identity-providers/openid-connect/provider/
- Authelia proxy introduction (statuses 200/302/303/401/403, Location, cookie HttpOnly+Secure, forwarded headers): https://www.authelia.com/integration/proxies/introduction/
- Authelia proxy authorization / ForwardAuth metadata (`X-Forwarded-Method` / `Host` / `URI`): https://www.authelia.com/reference/guides/proxy-authorization/
- Authelia validating forwarded authentication (example 302 + `Location` `rd=` + `Set-Cookie`): https://www.authelia.com/reference/guides/validating-forwarded-authentication/
- Authelia Trusted Header SSO (`Remote-User` / `Remote-Groups` / `Remote-Email` / `Remote-Name`): https://www.authelia.com/integration/trusted-header-sso/introduction/
- Authelia session / cookie (`domain`, `authelia_url`, `same_site` lax, inactivity/expiration): https://www.authelia.com/configuration/session/introduction/
- Authelia access control: https://www.authelia.com/configuration/security/access-control/
- This repo Caddyfile: `infra/caddy/Caddyfile`
- This repo Authelia config: `services/authelia/config/configuration.yml`
- ADR-0001: `docs/adr/0001-domain-owned-resource-permissions.md`
- ADR-0002: `docs/adr/0002-authelia-caddy-instead-of-custom-iam.md`
- Related research: `docs/research/auth-service-custom-frontend-alternatives.md`, `docs/research/authelia-webauthn-passwordless.md`

- RFC 6749 (authorization code §4.1, `state` §4.1.1, CSRF §10.12): https://datatracker.ietf.org/doc/html/rfc6749
- RFC 7636 PKCE: https://datatracker.ietf.org/doc/html/rfc7636
- RFC 6265 cookies (`Domain` leading-dot ignored, HttpOnly, Secure): https://www.rfc-editor.org/rfc/rfc6265.html
- OIDC Core 1.0: https://openid.net/specs/openid-connect-core-1_0.html

- `golang.org/x/oauth2` docs (Config, AuthCodeURL, Exchange, PKCE): https://pkg.go.dev/golang.org/x/oauth2
- Module README: https://github.com/golang/oauth2/blob/master/README.md
- `oauth2.go` (`google.Endpoint` / `github.Endpoint` comments, CSRF/PKCE notes): https://raw.githubusercontent.com/golang/oauth2/master/oauth2.go
- `golang.org/x/oauth2/google` `Endpoint`: https://pkg.go.dev/golang.org/x/oauth2/google — source https://raw.githubusercontent.com/golang/oauth2/master/google/google.go
- `golang.org/x/oauth2/github` `Endpoint`: https://pkg.go.dev/golang.org/x/oauth2/github — source https://raw.githubusercontent.com/golang/oauth2/master/github/github.go
- Shared endpoints: https://pkg.go.dev/golang.org/x/oauth2/endpoints

- Google OIDC (server / authorization-code flow, `state`, token + ID token, userinfo, `sub` vs email): https://developers.google.com/identity/openid-connect/openid-connect
- Google OIDC API reference (userinfo fields): https://developers.google.com/identity/openid-connect/reference
- Google OAuth 2.0 for web server applications: https://developers.google.com/identity/protocols/oauth2/web-server
- Google Discovery: `https://accounts.google.com/.well-known/openid-configuration`

- GitHub authorizing OAuth apps (web flow, `state`, PKCE, token exchange, redirect URLs / wildcard warning): https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps
- GitHub OAuth scopes (`user:email`, `read:user`): https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps
- GitHub REST authenticated user (`GET /user`, `email` nullable): https://docs.github.com/en/rest/users/users#get-the-authenticated-user
- GitHub REST emails (`GET /user/emails`, `user:email`): https://docs.github.com/en/rest/users/emails
- GitHub authentication discovery (no OIDC ID tokens on OAuth flows): https://docs.github.com/en/apps/github-authentication-discovery-endpoints

- oauth2-proxy Caddy (v7.15.x): https://oauth2-proxy.github.io/oauth2-proxy/configuration/integrations/caddy
- oauth2-proxy providers + email file / `--email-domain=*`: https://oauth2-proxy.github.io/oauth2-proxy/configuration/providers/
- oauth2-proxy Google: https://oauth2-proxy.github.io/oauth2-proxy/configuration/providers/google
- oauth2-proxy GitHub: https://oauth2-proxy.github.io/oauth2-proxy/configuration/providers/github
- oauth2-proxy endpoints (`/oauth2/auth` 202/401, sign_out `rd` + whitelist): https://oauth2-proxy.github.io/oauth2-proxy/features/endpoints/
- oauth2-proxy configuration overview (cookie flags, whitelist-domain, authenticated-emails-file): https://oauth2-proxy.github.io/oauth2-proxy/configuration/overview
- oauth2-proxy session storage (cookie vs Redis): https://oauth2-proxy.github.io/oauth2-proxy/configuration/session_storage
