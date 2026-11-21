# CLAUDE.md

Read `SPEC.md` first. It is the source of truth; if something is ambiguous, prefer the simplest
option that preserves the invariants below, and note the decision in `DECISIONS.md`.

## Repo layout
```
/server
  cmd/server        main: HTTP + WS + embedded web
  cmd/harness       M0: red-team the AI seat prompt from the CLI
  cmd/export        JSONL export of games
  internal/auth     register/login, bcrypt, sessions
  internal/lobby    FIFO matchmaker
  internal/game     PURE state machine: phases, timers (injected clock), scoring
  internal/hub      one goroutine per game, inbox channel, WS fan-out
  internal/ai       director, seat, critic, analysis, persona generation
  internal/llm      LLM interface + one adapter: openai_compat (OpenRouter by default)
  internal/store    pgx queries, migrations (embedded SQL)
  internal/ws       envelope types, (de)serialization
/web                Next.js (App Router, TS, Tailwind), output: 'export'
/prompts            seat.v1.md, critic.v1.md, director.v1.md, analysis.v1.md
docker-compose.yml  postgres
Dockerfile          multi-stage: build web → embed into Go binary → distroless
Makefile
```

## Commands
```
make dev        # postgres up, migrations, go run server, next dev with proxy to :8080
make test       # go test ./...
make harness    # go run ./server/cmd/harness  (needs OPENROUTER_API_KEY)
make build      # web export + go build with embed
make export     # dump games as JSONL
```

## Build order
Follow SPEC §11: M0 harness first, then M1→M4. Don't start UI polish before M4 works end-to-end
with `DEV_BOT_HUMAN_AFTER_SEC` so one person can test alone.

## Invariants (write tests for each)
1. Before `game.result`, no server→client message reveals seat `kind`, `model_id`, `user_id`,
   whether/when other seats submitted, or typing state.
2. Submissions are sealed until phase end; reveal is a single message per phase.
3. Usernames never appear in game messages; only aliases.
4. Every submission, skip, note change, vote and AI call is persisted with server timestamps.
5. `bot_human` seats are excluded from leaderboard, stats and export.
6. AI pipeline failures/timeouts produce a skip, never a stuck phase.
7. AI character breaks are shown as-is and flagged, not filtered.

## Conventions
- Go: stdlib first. No ORM, no web framework. `context` everywhere; LLM calls get a deadline
  = phase deadline − 3s.
- `internal/game` has no I/O: takes events + clock, returns new state + effects. Hub executes effects.
- Prompts live in `/prompts`, versioned by filename; log `prompt_version` on every AI call.
- Config only via env (SPEC §10). No secrets in repo.
- Frontend is client-only (static export): no server components with data fetching, no API routes,
  no dynamic route segments — use query params (`/game?id=`).
- Visual style per SPEC §8: original pixel art only, procedural sprites, `image-rendering: pixelated`,
  Press Start 2P + VT323.
- Keep the disclosure banner (SPEC §9) visible on login, lobby and table screens.

## Don't
- Don't add Redis, queues, or multiple instances. Single process is a design choice.
- Don't use Llama models (license forbids presenting outputs as human-generated).
- Don't add typing indicators, read receipts, or "X has answered" UI.
