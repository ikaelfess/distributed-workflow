# Auth local testing

Smoke-check Authelia + Caddy with the root compose stack.

## Prerequisites

Map local hosts (or change domains in `config/configuration.yml` and `infra/caddy/Caddyfile`):

```text
127.0.0.1 auth.example.com api.example.com
```

## Start

From the repo root:

```bash
docker compose up -d auth auth_database caddy
docker compose ps
docker compose logs -f auth caddy
```

Authelia listens on `:9091` (direct) and via Caddy at `http://auth.example.com:8080`.  
Protected API placeholder: `http://api.example.com:8080`.

Default file user (change after first successful run):

| Username | Password  |
|----------|-----------|
| `dev`    | `authelia` |

## Checks

1. **Portal loads**

   ```bash
   curl -sS -o /dev/null -w '%{http_code}\n' http://auth.example.com:8080/
   ```

   Expect `200` (or a redirect into the Authelia UI).

2. **Unauthenticated API is blocked**

   ```bash
   curl -sS -o /dev/null -w '%{http_code}\n' http://api.example.com:8080/
   ```

   Expect a non-`200` (typically redirect/`401`/`403` from forward-auth).

3. **Sign in in the browser**

   Open `http://auth.example.com:8080`, sign in as `dev` / `authelia`, then visit `http://api.example.com:8080/`.  
   Expect body `authenticated`.

4. **Identity headers (optional)**

   After login, from a session that has the Authelia cookie, confirm Caddy would forward identity (inspect Authelia/Caddy logs, or add a temporary upstream that echoes `Remote-User` / `Remote-Email`).

## Stop

```bash
docker compose down
```

Postgres data for Authelia is in the `auth_data` volume; remove it with `docker compose down -v` if you want a clean storage schema.
