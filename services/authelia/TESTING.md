# Auth local testing

Smoke-check Authelia + Caddy over **HTTPS** (`workflow.tech`) with the root compose stack.

Authelia expects HTTPS even for local development (secure session cookies). Caddy terminates TLS with an internal CA.

## Prerequisites

- Map local hosts:

```text
127.0.0.1 auth.workflow.tech api.workflow.tech
```

- Port **443** must be free on the host (Docker publishes `443:443`).

## Start

From the repo root:

```bash
docker compose up -d
docker compose ps
docker compose logs -f authelia caddy
```

Caddy is the edge: it does **not** `depends_on` Authelia. Authelia is reached on the compose network from Caddy. When domain services are added, they should `depends_on: caddy` (and not publish their own public ports unless needed).

Wait until `authelia` and `caddy` are running with no restart loops. Forward-auth may 502 until Authelia is ready — retry briefly.

| URL | Role |
|-----|------|
| `https://auth.workflow.tech` | Authelia portal (via Caddy) |
| `https://api.workflow.tech` | Protected placeholder (forward-auth) |

Do not call Authelia on a host port; it is not published. Use the HTTPS portal only.

Default file user (change after first successful run):

| Username | Password   |
|----------|------------|
| `dev`    | `authelia` |

## Trust Caddy’s local CA (once per machine)

Do this after the first successful `caddy` start, then restart the browser:

```bash
docker compose cp caddy:/data/caddy/pki/authorities/local/root.crt /tmp/caddy-root.crt
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain /tmp/caddy-root.crt
```

Until the CA is trusted, use `curl -k` for CLI checks. After trusting, omit `-k`.

## Checks

1. **Portal loads**

   ```bash
   curl -sS -o /dev/null -w '%{http_code}\n' https://auth.workflow.tech/
   ```

   Expect `200` (or a redirect into the Authelia UI).

2. **Unauthenticated API is blocked**

   ```bash
   curl -sS -o /dev/null -w '%{http_code}\n' -L --max-redirs 0 https://api.workflow.tech/ || true
   curl -sS -o /dev/null -w '%{http_code}\n' https://api.workflow.tech/
   ```

   Expect a non-`200` success page (typically a redirect to the Authelia portal, or `401`/`403`). Do not expect the body `authenticated` yet.

3. **Sign in in the browser**

   Open `https://auth.workflow.tech`, sign in as `dev` / `authelia`, then visit `https://api.workflow.tech/`.
   Expect body `authenticated`.

4. **Identity headers (optional)**

   After login, confirm Caddy forwards identity (Authelia/Caddy logs, or a temporary upstream that echoes `Remote-User` / `Remote-Email`).

## Stop

```bash
docker compose down
```

Volumes: `authelia_data` (Postgres), `caddy_data` (PKI/certs). Use `docker compose down -v` for a clean slate (you will need to re-trust the CA if the local CA is regenerated).
