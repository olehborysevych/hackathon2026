# Find the Human

A multiplayer social-deduction game: 2 humans + 3 AI players at one table. See `SPEC.md`.

## Run locally

Needs Go, Node and Docker.

```bash
make run     # Postgres in docker, builds the web app into the Go binary, serves http://localhost:8080
make dev     # hot reload: Go on :8080 + next dev on :3000 (use http://localhost:3000)
make test
```

## Deploy (Railway)

1. Push this repo to GitHub.
2. In your Railway project: **+ Create → GitHub Repo** and pick this repo. Railway builds the root `Dockerfile`
   and reads `railway.json` (one replica, `/healthz` check).
3. **+ Create → Database → Add PostgreSQL**.
4. In the app service, **Variables → New Variable**: `DATABASE_URL` = `${{Postgres.DATABASE_URL}}`
   (replace `Postgres` with the DB service's name if you renamed it).
5. App service **Settings → Networking → Generate Domain**, then open `https://<domain>/healthz`.
   It should answer `{"db":"ok","status":"ok"}`.

Railway sets `PORT` itself. Keep the service at one replica (SPEC §5).
