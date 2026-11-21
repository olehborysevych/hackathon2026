# Find the Human

A multiplayer social-deduction game: 2 humans + 3 AI players at one table. See `SPEC.md`.

## Run locally

Needs Go, Node and Docker.

```bash
make run     # Postgres in docker, builds the web app into the Go binary, serves http://localhost:8080
make dev     # hot reload: Go on :8080 + next dev on :3000 (use http://localhost:3000)
make test
make export  # JSONL per finished game (bot_human games excluded)
```

Play alone: a short game where a `bot_human` takes the second human seat after 5 seconds.

```bash
T_INTRO=15 T_ASK=15 T_ANSWER=20 T_REVEAL=10 T_VOTE=20 DEV_BOT_HUMAN_AFTER_SEC=5 make run
```

AI seats play with the real pipeline (`internal/ai`, prompt v3) when `OPENROUTER_API_KEY` is set, and use canned
stub lines otherwise (or with `AI_MODE=stub`). Models, prompt version, critic and venue come from env; see
`.env.example`. Turn on zero data retention in the OpenRouter privacy settings first: player messages are in the
prompts. `LIVE_LLM=1 go test ./server/internal/agent -run TestLiveTable -v` runs one seat through every phase
against the real model (about $0.001).

Store integration tests need a disposable database:
`TEST_DATABASE_URL=postgres://… go test ./server/internal/store/` (it drops and recreates the schema).

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

Game settings are env vars on the app service (SPEC §10): `HUMANS_PER_GAME`, `AI_PER_GAME`, `ROUNDS`, `T_INTRO`, `T_ASK`,
`T_ANSWER`, `T_REVEAL`, `T_VOTE` (seconds, minimum 5). Leave `DEV_BOT_HUMAN_AFTER_SEC` unset in production.
