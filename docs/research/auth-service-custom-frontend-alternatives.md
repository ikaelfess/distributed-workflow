# Auth service alternatives with a custom frontend (vs Authelia)

Date: 2026-08-14  
Scope: Self-hosted identity/auth companions that could replace this repo’s Authelia 4.39 + Caddy `forward_auth` packaging (`services/authelia/`, portal `https://auth.workflow.tech`), **if and only if** the team can ship its own login/signup/account UI — not logo/color theming. Product decisions are unchanged; this is research only.  
Sources: First-party docs, GitHub README/docs, first-party APIs, and specs only (see [Sources](#sources)). Roundup blogs are not used as evidence.

Custom UI models used below:

- **(a)** Headless APIs / bring-your-own frontend (your HTML/JS/React talks to documented APIs).
- **(b)** Replaceable server templates (you supply full HTML/FTL/NJK for login and related pages).
- **(c)** CSS / theme / branding / logo only. **Insufficient** for the user’s ask.

This repo’s Authelia job (from ADR-0002 and current wiring): human browser sessions only; users created out-of-band (file backend); Caddy `forward_auth` to Authelia `/api/authz/forward-auth`; password-only for now; passkeys researched separately; auth is packaging, not a bounded context.

**Any recommendation to swap Authelia contradicts ADR-0002** (`docs/adr/0002-authelia-caddy-instead-of-custom-iam.md`). Flagged in the recommendation section.

## Executive summary

Authelia 4.39 **cannot** officially replace the login portal with your own HTML/JS/React. First-party customization is **(c)**: logo, favicon, translation overrides, and four built-in themes (`light`/`dark`/`grey`/`oled`/`auto`). A runtime “portal templates” PR exists on GitHub but is **open, unpublished** (docs URL 404 as of this date) and even then is CSS/JS overlay on Authelia’s React forms — still not (a)/(b). POSTing `/api/firstfactor` from a custom page is **not an intended use case** (maintainer, 2024). CORS documented for Authelia is OIDC-endpoint CORS, not a first-party custom-SPA login story.

If the custom frontend is a **hard** requirement **and** you still need a Caddy “is this human logged in?” gate without each app speaking OIDC, the realistic options for *this* stack are:

| Rank | Option | Why it fits this repo | Cost vs Authelia |
|------|--------|------------------------|------------------|
| 1 | **Ory Kratos + your UI + Oathkeeper** (or Caddy `forward_auth` to `/sessions/whoami` with caveats) | True **(a)** BYO UI. Admin/Identity API for out-of-band users. Oathkeeper `/decisions` is the documented reverse-proxy decision API (Traefik first-party; Caddy is the same HTTP pattern). Passkeys first-party. | **Much heavier**: Kratos + UI service + usually Oathkeeper. **Contradicts ADR-0002.** |
| 2 | **Keycloak login Freemarker theme (b) + oauth2-proxy** | Official **(b)** for login HTML. Admin-created users. Passkeys. oauth2-proxy has a first-party Caddy `forward_auth` guide. | **Much heavier** enterprise IAM. Extra hop. **Contradicts ADR-0002.** |
| 3 | **ZITADEL custom Login UI (a) + oauth2-proxy** | First-party **(a)** session/OIDC APIs + official Login app as a reference. Admin users. Passkeys. No native Caddy forward-auth; oauth2-proxy supplies the gate. | **Much heavier**. **Contradicts ADR-0002.** |
| — | Authentik (not ranked as satisfying the UI ask by default) | First-party Caddy `forward_auth` is the closest Authelia-shaped drop-in. Official UI is **(c)** branding/CSS/flow layout. A **custom flow executor** against the shared flow API is documented as possible **(a)**, but there is no first-party “replace the login SPA” guide. | Heavier (server + worker + Postgres + outpost). **Contradicts ADR-0002.** |

What Authelia cannot do (for this ask): host a team-owned login/signup/account UI. It can theme the existing portal and copy `Remote-*` headers after `forward_auth`. That is the whole official UI surface.

**Recommendation for this repo:** stay Authelia unless custom frontend is non-negotiable. A swap is an ADR-0002 reversal, not a config tweak. If you do swap for UI control, Kratos+(Oathkeeper) is the only candidate that is *designed* as headless identity plus a proxy decision API; Keycloak/ZITADEL get you a custom UI by becoming a full IdP and adding oauth2-proxy in front of Caddy.

Close-but-insufficient (do not swap for the UI ask): Tinyauth, Rauthy, Kanidm, Kotauth, Casdoor, Logto OSS branding, Authentik branding — all **(c)** or IdP-without-forward-auth. Pocket ID is passkey-only and not an Authelia-shaped gate. Pomerium is a proxy in front of an IdP, not an IdP. SuperTokens is in-app session SDK auth, not gateway forward-auth. Parako.ID has real **(b)** Nunjucks overrides but no first-party Caddy forward-auth.

## Authelia custom-frontend reality (primary sources)

This repo: Authelia `4.39`, file users, Caddy `forward_auth` → `/api/authz/forward-auth`, portal `auth.workflow.tech`. Authelia GitHub latest release at fetch time: **v4.39.20** (2026-05-26). License: **Apache-2.0**.

### Official UI surface is (c)

Server asset overrides (`server.asset_path`) allow replacing **favicon.ico**, **logo.png**, and locale JSON. Structure documented as:

```
/config/assets/
├── favicon.ico
├── logo.png
└── locales/<lang>[-[variant]]/<namespace>.json
```

Only the `portal` translation namespace is listed. Logo is auto-resized. Locales are not API-stable across releases (versioning policy called out on that page).

Built-in `theme`: `light` (default), `dark`, `grey`, `oled`, or `auto` (system preference). That is palette selection, not a custom frontend.

Notification email templates can be overridden (`template_path`). That is email, not the login SPA.

### No published portal-template / custom-frontend feature in 4.39

`https://www.authelia.com/reference/guides/portal-templates/` returns **404** (fetched 2026-08-14).

GitHub PR **#10686** “Feature/runtime templates” (opened 2025-11-06, **state: open** as of this research): filesystem CSS/JS bundles (`style.css`, optional `effect.js`, manifest), `portal_template` config key, headline/subtitle. The PR summary says it “keeps the original Authelia appearance intact” and uses CSS as the “primary styling hook.” That is still Authelia’s React login forms with skins — **(c)**, not (a)/(b). It is not in this repo’s 4.39 image.

### API-driven login is unofficial

The portal’s own first-factor form POSTs JSON to `/api/firstfactor` (OpenAPI: “The firstfactor endpoint allows a user to login and generates an authentication cookie”). pam_authelia uses the same HTTP API. That does **not** constitute a supported BYO frontend.

Maintainer **james-d-elliott**, Authelia discussion #7197 (2024-04-17), on using your own login page and copying `Set-Cookie`:

> You can technically do this by copying the Set-Cookie header as per normal web semantics, this however is **not an intended use case so YMMV**. […] I’d recommend using something like [OIDC] to perform logins instead.

Authelia CORS configuration is under **OIDC provider** (`identity_providers.oidc.cors` for authorization/token/userinfo/etc.). There is no first-party CORS guide for a cross-origin custom login SPA calling `/api/firstfactor`. A same-origin custom page reverse-proxied under `auth.workflow.tech` would still be the unofficial path.

### Forward-auth and provisioning (what Authelia *does* well)

Caddy integration is first-party. Default authz endpoint: `/api/authz/forward-auth` (ForwardAuth implementation). This repo already matches the documented Caddyfile pattern (`copy_headers Remote-User Remote-Groups Remote-Email Remote-Name`).

File backend: YAML users file, hashed passwords, no public signup. Password reset/change can be disabled. Authelia does not currently ship a registration workflow (discussion #7797: admin user-management API discussed for ~4.40, explicitly not self-registration).

Passkeys: first-party since 4.39 (`webauthn.enable_passkey_login`). See `docs/research/authelia-webauthn-passwordless.md`.

**Bottom line:** staying Authelia means accepting Authelia’s portal (plus logo/theme). There is no official Authelia path that satisfies the user’s custom-frontend ask.

## Comparison table

UI model **(a)/(b)/(c)** as defined above. “Forward-auth” means a reverse-proxy subrequest (“is this session allowed?”) equivalent to Caddy `forward_auth` / Traefik ForwardAuth / nginx `auth_request`, **without each app implementing OIDC**. “Caddy story” is first-party docs unless noted.

| Product | License | Custom UI | Forward-auth | Out-of-band users | Passkeys | Caddy story | Ops vs Authelia | Primary sources |
|---------|---------|-----------|--------------|-------------------|----------|-------------|-----------------|-----------------|
| **Authelia 4.39** | Apache-2.0 | **(c)** only. Unofficial `/api/firstfactor`. PR #10686 open, still (c). | **Yes**, first-party `/api/authz/forward-auth` | File YAML / LDAP | Yes, 4.39+ | First-party `forward_auth` | Baseline (1 container + DB already here) | authelia.com asset overrides, theme, Caddy, OpenAPI, discussion #7197 |
| **Authentik** | MIT + EE dir proprietary | **(c)** branding/CSS/layout. Custom **flow executor** = possible **(a)**, not a drop-in UI | **Yes**, proxy outpost `/outpost.goauthentik.io/auth/caddy` | Admin Directory + invitations | Yes (WebAuthn stage; passkey autofill 2025.12+) | First-party Caddy template | Much heavier (server, worker, Postgres, outpost) | docs.goauthentik.io branding, flows, Caddy, users |
| **Ory Kratos** (+ Oathkeeper) | Apache-2.0 | **(a)** headless; `selfservice.flows.*.ui_url` | Kratos: session check. Oathkeeper: **`/decisions`**. whoami ≠ Authelia headers | Admin Identity API create/import | Yes (`passkey` / `webauthn` methods) | No first-party Caddy page; Traefik whoami + Oathkeeper Traefik are first-party | Heavier (Kratos + UI + usually Oathkeeper) | ory.com BYO UI, login `ui_url`, whoami, Oathkeeper Traefik |
| **Keycloak** | Apache-2.0 | Login: **(b)** Freemarker themes. Account/Admin: custom React consoles. Not a headless login API | Not native; use **oauth2-proxy** (or similar) | Admin Console / Admin REST | Yes (WebAuthn passwordless / Enable Passkeys) | Via oauth2-proxy first-party Caddy guide | Much heavier | keycloak.org server_development themes, ui-customization consoles |
| **ZITADEL** | AGPL-3.0 (exceptions in LICENSING.md) | **(a)** session + OIDC APIs; official Login app | No native forward-auth; OIDC + proxy | Admin Console / APIs | Yes (Login app lists passkeys) | oauth2-proxy or IdP-only | Much heavier | zitadel.com login-ui, login-app, oidc-standard |
| **SuperTokens** | Apache-2.0 core (EE dir separate) | **(a)** `supertokens-web-js` / custom forms; also prebuilt overrides | **No** gateway forward-auth. Sessions verified in **your** API SDK | Possible via overrides / disable signup in UI | Node/Python SDKs; **no Go SDK passkeys** (docs) | Reverse proxy path prefix only (`apiGatewayPath`) | Different job (in-app auth) | supertokens.com frontend-setup, self-host, architecture |
| **Pocket ID** | BSD-2-Clause | **(c)** at best; no first-party “replace login HTML”. REST API for admin/portals | **No** built-in proxy provider. OIDC + caddy-security / oauth2-proxy / Tinyauth | Admin UI, LDAP, signup tokens | **Passkey-only** (no password) | caddy-security plugin, not stock Caddy `forward_auth` | Lighter IdP + extra gate | pocket-id.org proxy-services, user-management, GitHub README |
| **Tinyauth** | AGPL-3.0 | **(c)** title, background image, forgot-password message | **Yes** `/api/auth/caddy` | `TINYAUTH_AUTH_USERS` / users file | Not a first-party passkey IdP (can delegate OIDC) | First-party community Caddy guide | Similar / lighter | tinyauth.app configuration, community/caddy, getting-started |
| **Rauthy** | Apache-2.0 (project README) | **(c)** per-client color/logo/favicon. Registration CORS for custom **register** UI only | **Yes**, `/auth/v1/clients/{id}/forward_auth` | Admin UI | Yes (passkey-only accounts + UV) | First-party simple Caddy example | Similar / slightly heavier | sebadob.github.io/rauthy intro, forward_auth, passkeys |
| **Kanidm** | MPL-2.0 (project) | **(c)** display name, image, `override.css` | **No**; OIDC + **oauth2-proxy** (first-party example) | CLI (`kanidm` person/account) | WebAuthn as IdP (origin/domain required) | oauth2-proxy, not native | Heavier IdP | kanidm.github.io customising, oauth2 examples |
| **Logto OSS** | MPL-2.0 (typical; Cloud vs OSS split) | Cloud: zip **(a)/(b)** “Bring your UI”. **OSS: that feature is listed as Cloud-only**; OSS path is **fork `packages/ui`** | Protected App = Cloud/Cloudflare. **No** Caddy forward-auth | Admin Console | Yes in product | Reverse-proxy to Logto only | Heavier | docs.logto.io bring-your-ui, logto-oss |
| **Casdoor** | Apache-2.0 | **(c)** + HTML snippets injected into *their* login page (not full replace). SDKs/OIDC for apps | Casdoor **Sites** reverse-proxy (Casdoor in the path), not Caddy `forward_auth` | Admin + SCIM | Product has WebAuthn; not verified as Authelia-equivalent here | Reverse-proxy *to* Casdoor | Heavier IAM | casdoor docs UI customization, Sites, how-to-connect |
| **Pomerium** | Apache-2.0 | N/A — **not an IdP**. Login UI is the upstream IdP (or hosted Pomerium IdP) | Identity-aware **proxy** (replaces Caddy’s job, or sits as the gateway) | Via IdP | Via IdP | Would replace Caddy, not Authelia alone | Different job | pomerium.com authentication: “not an IdP” |
| **oauth2-proxy** | MIT | **(b)** `custom_templates_dir` for `sign_in.html` / `error.html` (interstitial). Real login is the OIDC IdP | **Yes**, `/oauth2/auth` | Via IdP | Via IdP | **First-party** Caddy `forward_auth` | Extra hop in front of an IdP | oauth2-proxy Caddy + overview templates |
| **Kotauth** | See repo (first-party docs exist) | **(c)** CSS tokens, logo, favicon, split layout. Explicitly not full HTML replace | **No**. Caddy only `reverse_proxy` to Kotauth | Admin console + REST | Docs list passkeys | TLS terminator only | Heavier OIDC IdP | docs.kotauth.com theming, production Caddy |
| **Parako.ID** | MIT (docs footer) | **(b)** Nunjucks view overrides (`auth/login.njk`, OIDC login, account, …) | **Not documented** as Caddy/Traefik forward-auth | Admin / API implied | MFA WebAuthn templates exist | Not documented | Unknown / young | docs.parako.id branding |

## Per-candidate notes

### Authelia

Covered above. Fits the current *job* (portal + forward-auth + file users). Fails the *UI* ask.

### Authentik

Docker Compose is first-party (server + worker; “small-scale production”). Proxy provider modes include **Forward auth (single application)** and **Forward auth (domain level)**. Caddy template (both modes):

- `reverse_proxy /outpost.goauthentik.io/*` to the outpost
- `forward_auth` to `/outpost.goauthentik.io/auth/caddy`
- `copy_headers X-Authentik-Username X-Authentik-Groups …` (capitalization required)

Users: Admin **Directory > Users** create; invitations for enrollment without public signup (`Continue flow without invitation: false`).

UI: Brands set title, logo, favicon, default flow background, **custom CSS**. First-party Custom CSS guide (not a 404): override `--ak-` / `--pf-` variables and `::part` on Lit web components (flow executor included). Direct element selectors are unsupported across upgrades. Per-flow: background + layout (stacked / content left|right / sidebar). That is still **(c)** — skins on authentik’s executor, not your HTML.

“All flow executors use the same API, which allows for the implementation of custom flow executors.” Headless executor is for LDAP/Radius outposts (identification, password, authenticator validation only) — not a browser login SPA. Building your own web executor would be **(a)** and is a product you’d own.

Passkeys: WebAuthn authenticator setup/validation stages; Identification-stage passkey autofill from **authentik 2025.12.0+**.

Ops: full IdP (flows, outposts, admin UI). Far above Authelia packaging.

### Ory Kratos (+ Oathkeeper)

Kratos is headless by design. Self-hosted login UI is configured, not themed:

```yaml
selfservice:
  flows:
    login:
      ui_url: http://127.0.0.1:4455/auth/login
```

Kratos 303s the browser to that URL with `?flow=`. Your page fetches the flow JSON and renders whatever you want **(a)**. Same pattern for settings/registration/recovery. Ory Network also has a hosted Account Experience; this repo would self-host OSS Kratos.

Out-of-band users: Admin **create identity** and **import identities** APIs (password hashes, passkeys, etc.). Public registration is a separate self-service flow. OSS Kratos source (`ViperKeySelfServiceRegistrationEnabled`) and `embedx/config.schema.json` define `selfservice.flows.registration.enabled` (boolean, default `true`). Ory’s OEL changelog documents the same key: with `enabled: false`, new sign-ups stay blocked while OIDC/SAML account linking to an existing identity can still run. For this repo: set that flag false **and** do not ship a registration UI.

Session check: `GET /sessions/whoami` → 200 + session JSON or 401. First-party Traefik ForwardAuth example points at whoami. **Caveat for this Caddyfile:** Authelia sets `Remote-User` / `Remote-Groups` / `Remote-Email` / `Remote-Name` **headers**. whoami puts identity in the **JSON body**. Caddy `copy_headers` will not invent Authelia-compatible headers. For header injection, Oathkeeper mutators after a cookie-session authenticator are the documented mechanism.

Oathkeeper Access Control Decision API: send the request to `/decisions` (API port). Traefik:

`forwardauth.address=http://oathkeeper:4456/decisions`  
`authResponseHeaders=X-Id-Token,Authorization`

Caddy would use the same URL with `forward_auth` (no first-party Caddy page found). Oathkeeper is a second binary and access-rule YAML.

Passkeys: `selfservice.methods.passkey` / `webauthn` in Kratos config (RP id/origins).

Ops: you must run a UI (your React/HTML) on a cookie-compatible site with Kratos public API. This is the honest “custom frontend” stack. It is not Authelia-shaped.

### Keycloak

Themes: Keycloak “provides theme support for web pages and emails” so end-user pages can be integrated with applications. Login pages are **Freemarker** (`.ftl`) in a login theme — **(b)**. You can replace templates, not only CSS. Account Console and Admin Console are React; first-party path is `@keycloak/keycloak-account-ui` / `create-keycloak-theme` to build your own **account** console. That is account management, not the login form. There is no first-party “headless login API replace the browser flow” equivalent to Kratos; Resource Owner Password is not the supported browser SSO path.

Provisioning: Admin Console and Admin REST API. Disable realm registration.

Passkeys: Server Admin “Passkeys” — WebAuthn passwordless policy + realm **Enable Passkeys**.

Forward-auth: Keycloak is an OIDC IdP. Pair with **oauth2-proxy** Caddy `forward_auth` to `/oauth2/auth` (first-party oauth2-proxy docs). Gatekeeper is historical; don’t plan on it.

Ops: full enterprise IAM. Contradicts ADR-0002’s “not custom/heavy IAM.”

### ZITADEL

First-party **Build your own Login UI**: create/update sessions via APIs; for OIDC, proxy authorize/token/userinfo to ZITADEL, finalize auth request with a session, redirect to `callbackUrl`. Requires a confidential backend and a service user with `IAM_LOGIN_CLIENT`. **(a)**.

Official **Login app** (Next.js) is the reference implementation (OIDC proxy middleware, passkeys, password, MFA, self-service). You can fork it or write your own.

No first-party Caddy/Traefik forward-auth IdP endpoint comparable to Authelia. Gateway story is OIDC → oauth2-proxy / Pomerium.

Self-hosted Docker is first-party (not expanded here). License: **AGPL-3.0** on GitHub (Apache/MIT exceptions in LICENSING.md).

### SuperTokens

Self-host: Docker `supertokens/supertokens-postgresql`. Frontend: prebuilt UI **or** custom forms calling SDK `signIn` / `signUp` **(a)**. Component overrides still wrap prebuilt widgets — that is not full replace unless you use the custom-UI path.

Architecture: frontend talks to **your** backend SDK, which talks to Core. Session verification is `verifySession` on **application APIs**, not a reverse-proxy authz endpoint. `apiGatewayPath` is for path prefixes, not Authelia-style forward-auth.

Different job than this repo’s Caddy gate. Do not swap Authelia for SuperTokens unless you also put session checks in every backend (rejected by ADR-0002).

Passkeys: documented for Node and Python SDKs; **Go SDK has no passkeys support** at the passkeys setup page fetched.

### Pocket ID

OIDC/OAuth2 provider, **passkey-only** (“doesn’t need a password”). Does not match this repo’s password-only-for-now Authelia role without changing the auth factor.

“The goal of Pocket ID is to function exclusively as an OIDC provider. As such, we don't have a built-in proxy provider.” Official proxy guides: Tinyauth, **caddy-security** (not stock Caddy `forward_auth`), oauth2-proxy, Traefik plugins.

Users: admin UI create, LDAP, signup tokens (v1.5.0+). Admins cannot enroll passkeys for users; users enroll via login-code / email link.

No first-party custom login HTML replacement. API docs mention building custom **dashboards/portals** (`pocket-id-portal` example) — admin/API, not a documented replacement for the passkey login page.

### Tinyauth

Authelia-shaped: users hash list / file, Caddy `forward_auth` to `/api/auth/caddy`, `copy_headers Remote-User Remote-Name Remote-Email Remote-Groups`. Community Caddy guide uses `caddy-docker-proxy` snippets; the directive is the same as this repo’s Caddyfile.

UI config: `TINYAUTH_UI_TITLE`, `TINYAUTH_UI_FORGOTPASSWORDMESSAGE`, `TINYAUTH_UI_BACKGROUNDIMAGE`, warnings toggle. **(c)**. No first-party HTML/React replace.

License: **AGPL-3.0**. Analytics enabled by default (`TINYAUTH_ANALYTICS_ENABLED`).

Does not satisfy the custom-frontend ask. Closest *ops* clone of Authelia if you only needed theming.

### Rauthy

Lightweight OIDC IdP (Rust, Hiqlite or Postgres). **Client branding:** color theme, logo, favicon per client — **(c)**. README: “Simple per-client branding for the login page.” Custom **registration** frontend via CORS + `/auth/v1/users/register` is documented; that is signup, not replacing login.

Forward auth: simple JWT type (usually wrong) and **advanced** cookie session:

- `/auth/v1/clients/{id}/forward_auth`
- `/auth/v1/clients/{id}/forward_auth/callback`

First-party examples: nginx `auth_request`, Traefik ForwardAuth, **simple Caddy** `forward_auth` with `redirect_state=302` plus a handle to proxy `/auth/v1/clients/*` back to Rauthy. Hairier than Authelia (callback injection, cookie binding).

Passkeys first-party (`webauthn.rp_id` / `rp_origin`). Admin UI user management.

Fails the full custom login UI ask; interesting if branding + forward-auth were enough.

### Kanidm

IdP with CLI provisioning. Customising docs: display name, site image (size/type limits), per-OAuth2-client image, **Custom CSS** bind-mount `/hpkg/override.css`. **(c)**.

Forward-auth: none. First-party **OAuth2 Proxy** example (`oidc_issuer_url = https://idm.example.com/oauth2/openid/<client>`).

WebAuthn/OAuth2 require correct `domain`/`origin`. Human browser SSO is OIDC to Kanidm’s own UI, then oauth2-proxy for apps that lack OIDC.

### Logto

Cloud **Bring your UI**: upload a zip SPA (`index.html` at zip root), hosted by Logto, talks to **Experience API**. That is **(a)/(b)** on Cloud.

OSS feature table: **“Bring your UI” is listed under Cloud-only limitations**; OSS “can fork the code on GitHub to customize the sign-in experience directly.” Forking `packages/ui` is a maintained-fork tax.

**Logto Protected App** (non-SDK gate) is Cloud/Cloudflare-only. OSS Caddy story is reverse-proxying Logto itself, not Authelia-style forward-auth.

Out-of-band: Console user management (standard IdP). Hide-Logto-branding is also Cloud-only per that table.

### Casdoor

Login UI customization: background URL, Form CSS, panel position, side-panel HTML, header/footer/signin/signup **HTML injected into Casdoor’s page**, theme colors. Export/import of those fields only. **(c)** plus snippets — you do not replace the app with your React SPA. Embedded `<script>` in those HTML fields is executed (trust boundary).

Connecting apps: OIDC client, Casdoor SDKs, plugins. **Sites**: Casdoor can *be* the reverse proxy (domain → upstream, optional Casdoor app for auth). That replaces Caddy’s routing, not a `forward_auth` subrequest to an Authelia-like companion.

Docker first-party. Heavier IAM than Authelia.

### Pomerium

First-party: **“While Pomerium itself is not an IdP”**. It authenticates against OIDC IdPs (or a hosted Pomerium authenticate service). Login UI is the IdP’s. Would replace or sit in front of **Caddy**, not replace Authelia’s portal unless you also pick an IdP with a custom UI.

Out of scope as an Authelia replacement by itself.

### oauth2-proxy (+ any headless IdP)

Not an IdP. Complements Kratos/Hydra, ZITADEL, Keycloak, Pocket ID, Kanidm.

Caddy (oauth2-proxy docs, v7.15.x): `handle /oauth2/*` reverse_proxy to oauth2-proxy; other paths `forward_auth` `uri /oauth2/auth`; on 401 `redir` to `/oauth2/sign_in?rd=...`. Requires `--reverse-proxy=true`.

Templates: `--custom-templates-dir` for **sign_in.html** and **error.html** **(b)** for the interstitial “login with …” page. The password/passkey form still lives at the IdP.

Useful glue; not a standalone Authelia replacement.

### Kotauth

First-party docs and Docker (`ghcr.io/inumansoul/kotauth`). OIDC IdP, admin console, REST API, invitations.

Theming: CSS custom properties injected into `:root` (accent, backgrounds, radius), logo, favicon, CENTERED vs SPLIT layout. Docs section **“What is not themeable”**: admin console, typography, functional colors. **(c)**.

Caddy: `auth.yourdomain.com { reverse_proxy kotauth:8080 }` — TLS in front of the IdP, **not** forward-auth for `api.workflow.tech`.

Do not treat as an Authelia-shaped companion.

### Parako.ID

First-party docs at `docs.parako.id` (not marketing-only). **(b)**: `branding.ui.customization.enabled` + `rootPath`; override Nunjucks including `auth/login`, OIDC `login`, account pages. Example custom login form POSTs to `routes.authFull.login`.

No first-party Caddy/Traefik forward-auth found. Young project relative to Authelia/Kratos/Keycloak. Could pair with oauth2-proxy if OIDC is complete; not verified end-to-end here.

## Integration sketches (top two)

Current pattern (`infra/caddy/Caddyfile`): portal host reverse-proxies Authelia; protected hosts `forward_auth authelia:9091` with `uri /api/authz/forward-auth` and `copy_headers Remote-User Remote-Groups Remote-Email Remote-Name`. High-level deltas only — not a rewrite.

### 1) Ory Kratos + Oathkeeper (best actual custom UI)

Keep Caddy as TLS/router. Add:

- **Kratos** public API (e.g. `:4433`) and admin API (internal only).
- **Your UI** at `https://auth.workflow.tech` (login/settings). Point `selfservice.flows.login.ui_url` (and settings) at that origin. Same-site cookies with Kratos public URL (typically also under `auth.workflow.tech` via Caddy path split, e.g. `/self-service/*`, `/sessions/*` → Kratos; `/` → UI).
- **Oathkeeper** with a cookie-session authenticator against Kratos whoami and a header mutator (`X-User-Id` / email / …).

Protected hosts become:

```caddyfile
api.workflow.tech {
	forward_auth oathkeeper:4456 {
		uri /decisions
		copy_headers X-User-Id X-User-Email
		# map names if apps still expect Remote-User
	}
	reverse_proxy /* <backend>
}
```

401 handling: Oathkeeper/Caddy must send the browser to your login `ui_url` with a return URL (oauth2-proxy-style `handle_response` if the decision API returns 401 without Location). Authelia today emits the portal redirect itself; you own that redirect.

Create users with Kratos Admin `POST /admin/identities` (and import hashes if migrating `users.yml`). Do not deploy a registration page.

**Header gap:** if you skip Oathkeeper and `forward_auth` Kratos `/sessions/whoami` directly, you get a 200/401 gate and a JSON body Caddy will not copy into `Remote-*`. Fine only if backends don’t need those headers.

### 2) Keycloak (b) + oauth2-proxy

Replace Authelia with Keycloak (login theme JAR/FTL = your HTML). oauth2-proxy as the Caddy companion:

```caddyfile
auth.workflow.tech {
	reverse_proxy keycloak:8080
}

api.workflow.tech {
	handle /oauth2/* {
		reverse_proxy oauth2-proxy:4180
	}
	handle {
		forward_auth oauth2-proxy:4180 {
			uri /oauth2/auth
			header_up X-Real-IP {remote_host}
			@error status 401
			handle_response @error {
				redir * /oauth2/sign_in?rd={scheme}://{host}{uri}
			}
		}
		reverse_proxy <backend>
	}
}
```

Users live in the Keycloak realm (admin-created). Passkeys via realm WebAuthn passwordless. Heavier than (1) if you only wanted a custom login page + a gate.

Authentik Caddy would look closer to today’s file (`forward_auth` + `copy_headers`), with `/outpost.goauthentik.io/*` extra on each app host — but that does **not** by itself give you a custom frontend.

## Recommendation for this repo

**Stay Authelia** for the ADR-0002 job (human SSO portal + Caddy forward-auth + file-provisioned users, packaging not a bounded context). Authelia will not grow a first-party custom frontend in 4.39; don’t pretend asset overrides or an open templates PR are one.

**Swap only if** own login/signup/account UI is a hard product requirement. That **contradicts ADR-0002** (Authelia + Caddy instead of custom IAM / Envoy). The least-wrong swap that actually provides **(a)** plus a proxy decision API is **Kratos + your UI + Oathkeeper**. Keycloak+oauth2-proxy is the **(b)** alternative if you would rather theme-replace HTML inside a classic IdP than own flow JSON. ZITADEL is the other real **(a)** IdP, still needing oauth2-proxy for Caddy.

Do not swap to Authentik, Tinyauth, Rauthy, Kanidm, Kotauth, Casdoor, or Logto OSS **for this UI ask**: their official login customization is branding, a fork, or a different gateway model. Pocket ID drops passwords. Pomerium and SuperTokens are different jobs. Parako.ID’s templates are real **(b)** but the forward-auth story is not there yet.

If the UI ask can be reduced to logo + theme, Authelia already does that; no ADR change.

## Caveats / primary-source gaps

- **Authelia portal-templates.md**: 404. PR #10686 open; last update on the PR page was 2025-11-06. Not in 4.39.
- **Kratos disable-registration**: Confirmed in OSS source, not on the user-registration narrative page. `selfservice.flows.registration.enabled` is `ViperKeySelfServiceRegistrationEnabled` in `driver/config/config.go`; schema default `true` since [kratos#2081](https://github.com/ory/kratos/commit/864b00d6ecddefdb06ac22fda04670bfa43f2fd5). OEL changelog: linking still works when registration is disabled. Still do not ship a registration UI.
- **Kratos + Caddy**: no first-party Caddy page; Traefik whoami / Oathkeeper Traefik only. whoami **headers** vs Authelia `Remote-*` not documented as equivalent. Caddy `forward_auth` to Oathkeeper `/decisions` is the same HTTP pattern as Traefik, not a first-party Caddy snippet.
- **Authentik custom CSS**: first-party page is https://docs.goauthentik.io/brands/custom-css/ (working). Flow executor “same API” is still one sentence — no first-party custom login SPA tutorial.
- **SuperTokens custom UI**: `https://supertokens.com/docs/quickstart/frontend-setup` is the Custom vs Pre-Built switcher (includes `signInClicked` for custom forms). A guessed `/authentication/emailpassword/custom-ui` URL 404s. Architecture page remains the source for “not a gateway IdP.”
- **Logto OSS vs Cloud**: Bring-your-UI zip upload is documented as Cloud hosting; OSS table says fork the experience. Do not assume OSS has the zip upload.
- **Rauthy Caddy example** is labeled “very simple”; nginx/Traefik sections warn the advanced flow needs callback injection. Treat the Caddy snippet as incomplete vs Authelia’s guide.
- **Kotauth / Parako.ID**: first-party docs exist; no forward-auth equivalent found. Kotauth is branding-only by its own “not themeable” list.
- **Casdoor “Signin HTML”**: injection into Casdoor’s page, not a headless app. Sites proxy is Casdoor-as-gateway.
- **Tinyauth `RESOURCES_PATH`**: resource server exists; official UI keys are still title/background/message. Not counted as (b).
- **Licenses** checked from first-party LICENSE/README where fetched; Authentik and SuperTokens have separate **EE** trees. ZITADEL AGPL may affect packaging policy independent of ADR-0002.
- **Passkeys** for several IdPs are first-party but orthogonal; this repo’s Authelia path is documented in `docs/research/authelia-webauthn-passwordless.md`.

## Sources

- Authelia server asset overrides: https://www.authelia.com/reference/guides/server-asset-overrides/
- Authelia miscellaneous `theme`: https://www.authelia.com/configuration/miscellaneous/introduction/
- Authelia server `asset_path`: https://www.authelia.com/configuration/miscellaneous/server/
- Authelia Caddy: https://www.authelia.com/integration/proxies/caddy/
- Authelia file backend: https://www.authelia.com/configuration/first-factor/file/
- Authelia first-factor (password reset/change disable): https://www.authelia.com/configuration/first-factor/introduction/
- Authelia OIDC CORS: https://www.authelia.com/configuration/identity-providers/openid-connect/provider/
- Authelia proxy intro / session cookie: https://www.authelia.com/integration/proxies/introduction/
- Authelia OpenAPI `/api/firstfactor`: https://github.com/authelia/authelia/blob/master/api/openapi.yml
- Authelia unofficial custom login (maintainer): https://github.com/authelia/authelia/discussions/7197
- Authelia portal templates PR (open): https://github.com/authelia/authelia/pull/10686
- Authelia Apache-2.0 README: https://github.com/authelia/authelia
- Authelia pam_authelia (same HTTP API): https://www.authelia.com/integration/guides/pam-authelia/
- Authelia passkeys (existing note): `docs/research/authelia-webauthn-passwordless.md`
- ADR-0002: `docs/adr/0002-authelia-caddy-instead-of-custom-iam.md`
- Current Caddyfile: `infra/caddy/Caddyfile`

- Authentik branding: https://docs.goauthentik.io/branding/
- Authentik brands: https://docs.goauthentik.io/brands/
- Authentik custom CSS (`::part` / CSS variables, flow executor): https://docs.goauthentik.io/brands/custom-css/
- Authentik customize flow appearance: https://docs.goauthentik.io/customize/interfaces/flow/
- Authentik default flow executor: https://docs.goauthentik.io/add-secure-apps/flows-stages/flow/executors/if-flow/
- Authentik headless executor: https://docs.goauthentik.io/add-secure-apps/flows-stages/flow/executors/headless/
- Authentik proxy provider: https://docs.goauthentik.io/add-secure-apps/providers/proxy/
- Authentik forward auth: https://docs.goauthentik.io/add-secure-apps/providers/proxy/forward_auth/
- Authentik Caddy: https://docs.goauthentik.io/add-secure-apps/providers/proxy/server_caddy/
- Authentik create users: https://docs.goauthentik.io/users-sources/user/user_basic_operations/
- Authentik invitations: https://docs.goauthentik.io/users-sources/user/invitations/
- Authentik WebAuthn stage: https://docs.goauthentik.io/add-secure-apps/flows-stages/stages/authenticator_webauthn/
- Authentik identification / passkey autofill: https://docs.goauthentik.io/add-secure-apps/flows-stages/stages/identification/
- Authentik 2025.12 passkey autofill: https://docs.goauthentik.io/releases/2025.12/
- Authentik Docker Compose: https://docs.goauthentik.io/install-config/install/docker-compose/
- Authentik LICENSE (MIT + EE): https://github.com/goauthentik/authentik/blob/HEAD/LICENSE

- Ory custom UI overview: https://www.ory.com/docs/kratos/bring-your-own-ui/custom-ui-overview
- Ory configure UI URLs (Network Console; self-host analogue is yaml `ui_url`): https://www.ory.com/docs/kratos/bring-your-own-ui/configure-ory-to-use-your-ui
- Ory login flow `ui_url`: https://www.ory.com/docs/kratos/self-service/flows/user-login
- Ory registration flow: https://www.ory.com/docs/kratos/self-service/flows/user-registration
- Ory create identities: https://www.ory.com/docs/kratos/manage-identities/create-users-identities
- Ory import identities: https://www.ory.com/docs/kratos/manage-identities/import-user-accounts-identities
- Kratos `selfservice.flows.registration.enabled` (source): https://github.com/ory/kratos/blob/master/driver/config/config.go
- Kratos registration-disable schema (commit): https://github.com/ory/kratos/commit/864b00d6ecddefdb06ac22fda04670bfa43f2fd5
- Ory OEL changelog (registration disabled + account linking): https://www.ory.com/docs/self-hosted/oel/kratos/changelog
- Ory whoami / session check: https://www.ory.com/docs/identities/sign-in/check-session-token-cookie-api
- Ory Traefik (whoami + Oathkeeper): https://www.ory.com/docs/integrates-with/api-gateways/traefik
- Oathkeeper Traefik `/decisions`: https://www.ory.com/docs/oathkeeper/guides/traefik-proxy-integration
- Oathkeeper decision API: https://www.ory.com/docs/oss/oathkeeper/quickstarts
- Ory passkeys: https://www.ory.com/docs/kratos/passwordless/passkeys
- Ory Kratos config reference: https://www.ory.com/docs/kratos/reference/configuration

- Keycloak Server Developer Guide (themes / Freemarker): https://www.keycloak.org/docs/latest/server_development/index.html
- Keycloak creating your own Account/Admin console: https://www.keycloak.org/ui-customization/creating-your-own-console
- Keycloak npm UI packages: https://www.keycloak.org/ui-customization/themes-react
- Keycloak passkeys (source adoc): https://github.com/keycloak/keycloak/blob/main/docs/documentation/server_admin/topics/authentication/passkeys.adoc

- ZITADEL build your own login UI: https://zitadel.com/docs/guides/integrate/login-ui
- ZITADEL OIDC in custom login UI: https://zitadel.com/docs/guides/integrate/login-ui/oidc-standard
- ZITADEL Login app: https://zitadel.com/docs/guides/integrate/login-ui/login-app
- ZITADEL GitHub / AGPL: https://github.com/zitadel/zitadel

- SuperTokens self-host: https://supertokens.com/docs/deployment/self-host-supertokens
- SuperTokens frontend setup (prebuilt vs custom forms): https://supertokens.com/docs/quickstart/frontend-setup
- SuperTokens architecture: https://supertokens.com/docs/community/architecture
- SuperTokens passkeys: https://supertokens.com/docs/authentication/passkeys/initial-setup.md
- SuperTokens core LICENSE: https://raw.githubusercontent.com/supertokens/supertokens-core/master/LICENSE.md

- Pocket ID GitHub: https://github.com/pocket-id/pocket-id
- Pocket ID proxy services: https://pocket-id.org/docs/guides/proxy-services
- Pocket ID user management: https://pocket-id.org/docs/setup/user-management
- Pocket ID API (custom dashboards note): https://pocket-id.org/docs/api
- Pocket ID LICENSE: https://raw.githubusercontent.com/pocket-id/pocket-id/main/LICENSE

- Tinyauth getting started: https://tinyauth.app/docs/getting-started/
- Tinyauth configuration (UI keys, users): https://tinyauth.app/docs/reference/configuration/
- Tinyauth Caddy: https://tinyauth.app/docs/community/caddy/
- Tinyauth API: https://tinyauth.app/docs/reference/api
- Tinyauth LICENSE (AGPL-3.0): https://raw.githubusercontent.com/tinyauthapp/tinyauth/main/LICENSE

- Rauthy intro / branding: https://sebadob.github.io/rauthy/intro.html
- Rauthy forward auth (incl. Caddy): https://sebadob.github.io/rauthy/work/forward_auth.html
- Rauthy passkeys: https://sebadob.github.io/rauthy/config/passkeys.html
- Rauthy GitHub: https://github.com/sebadob/rauthy

- Kanidm customising: https://kanidm.github.io/kanidm/stable/customising.html
- Kanidm OAuth2 proxy example: https://kanidm.github.io/kanidm/master/integrations/oauth2/examples.html
- Kanidm OAuth2 book: https://github.com/kanidm/kanidm/blob/master/book/src/integrations/oauth2.md

- Logto Bring your UI: https://docs.logto.io/customization/bring-your-ui
- Logto OSS vs Cloud feature table: https://docs.logto.io/logto-oss
- Logto Experience API (core service): https://docs.logto.io/concepts/core-service

- Casdoor login UI customization: https://casdoor.github.io/docs/application/ui-customization/
- Casdoor application terminology (HTML injection fields): https://casdoor.github.io/docs/application/terminology
- Casdoor Sites reverse proxy: https://casdoor.github.io/docs/site/overview
- Casdoor how to connect: https://casdoor.github.io/docs/how-to-connect/overview
- Casdoor GitHub: https://github.com/casdoor/casdoor

- Pomerium authentication (not an IdP): https://www.pomerium.com/docs/capabilities/authentication
- Pomerium identity providers: https://www.pomerium.com/docs/integrations/user-identity/identity-providers

- oauth2-proxy Caddy: https://oauth2-proxy.github.io/oauth2-proxy/configuration/integrations/caddy
- oauth2-proxy configuration overview (templates): https://oauth2-proxy.github.io/oauth2-proxy/configuration/overview

- Kotauth quickstart: https://docs.kotauth.com/getting-started/quickstart/
- Kotauth theming: https://docs.kotauth.com/customization/theming/
- Kotauth production / Caddy reverse_proxy: https://docs.kotauth.com/deployment/production/
- Kotauth Docker: https://docs.kotauth.com/deployment/docker/

- Parako.ID branding + Nunjucks overrides: https://docs.parako.id/guides/branding/
- Parako.ID view resolver (source): https://github.com/Dahkenangnon/Parako.ID/blob/main/src/utils/view-resolver.ts
