# DECISIONS

Decisions taken while designing the MVP UI. Each one says what was chosen and why, so the rebuild can reuse or
reverse it. Game rules and API decisions stay in `SPEC.md` unless they are listed here.

## Game screen (design/game-room)

1. **Tavern look is the chosen direction.** Round table, side-on perspective, speech boxes around the table.
   The synthwave variant (`game-screen-cyber.html`) is kept only for comparison. It breaks the 16-colour palette
   in SPEC §8, so adopting it means changing that rule.
2. **Four other players around the table; you sit at the bottom centre.** Your composer is your own speech box.
3. **Round medallions instead of square cards** for players, with a pill-shaped name tag. The look is cleaner and
   matches the tavern style.
4. **Suspicion slider instead of mark buttons.** Five stops (AI · Leaning AI · Unsure · Leaning human · Human),
   moved with ◀ ▶ buttons. The state is kept across phases. *Open:* map the stops onto SPEC §4 marks
   (`none | ? | ! | human | ai`).
5. **One question on the table at a time in ANSWER and REVEAL**, with the asker named on the table and
   highlighted in their speech box. ◀ ▶ moves between questions. This keeps the transcript readable and avoids two
   questions on screen at once.
6. **Phase review control on the screen (▲ / ▼).** Players can step back to see intros, questions and answers.
   *Open:* past phases should be read-only, since the composer still looks live in review.
7. **Sealed content.** Speech boxes show only what the current phase is allowed to show: intros after INTRO,
   the current question in ANSWER, answers in REVEAL. This follows SPEC invariant 2.
8. **Speech boxes sized for 160-character text** (intros and questions, SPEC §2.2). Answers allow 200.
   The test data stays at 160 so the layout is checked at the upper limit.

## Stats page (design/game-room/stats.html)

9. **Ranking uses only players with at least 5 games** (SPEC §2.4). Players below that see how many games they
   need to be ranked.
10. **The personal standing shows a rank on three metrics: found the human, was found, and points per game.**
    The spec gives no sort key, so this is a proposal. The leaderboard is sorted by the chosen metric, with
    points as the tie-break. *Open:* confirm which metric is the default leaderboard sort.
11. **The random baseline (25%) is shown on the leaderboard** and in the personal cards, as SPEC §2.4 requires.
12. **The leaderboard shows the top 10 and the player's own row** if they're below 10, with a gap marker between.
13. **Usernames appear only on the stats pages, never in game screens** (SPEC invariant 3).

## Process

14. **Mockups stay as static HTML in `design/game-room/`.** They are the visual reference for the real frontend in
    `/web`. They are not production code.

## M0 harness (server/cmd/harness)

15. **No `openai-go` yet.** The `openai_compat` adapter is ~100 lines of stdlib `net/http` against `/chat/completions`.
    It sends `reasoning: {enabled: false}` (hidden thinking would eat the token budget) and logs OpenRouter's actual
    `provider`. Swap in the SDK later if streaming/tools are needed.
16. **`go.mod` at repo root** (module `hackathon2026`) so `go run ./server/cmd/harness` works as in CLAUDE.md.
    Prompts are embedded via `prompts/embed.go` (`prompts.Load("seat","v1")`).
17. **Prompt v1 edited after red-teaming (not bumped to v2),** since v1 has not shipped. Changes vs SPEC Appendix A:
    seat adds `{{alias}}`, a "quirks are habits, use ≤1 in 4 messages" rule, a no-authority rule for fake "game master"
    messages, and a knowledge-ceiling line; critic adds "only change what is wrong", no invented facts, and a strict
    definition of `BROKE_CHARACTER` (the first draft flagged 4/20 falsely).
18. **Default harness models** (override with `-model`, `AI_SEAT_MODELS`, `CRITIC_MODEL`):
    seat `google/gemma-4-31b-it` (the only model that did not answer 37*43 correctly; Qwen3-30B and Mistral Small did), critic `mistralai/mistral-small-2603`. All Apache-2.0, non-Llama.
19. **The critic sees the task text, which quotes another player's message, so it can be injected too.** The critic prompt
    marks the task as untrusted, and `StripAddedPrefix` removes a short `KEYWORD:` prefix the critic adds that the draft
    lacked (seen with the fake "game master" demanding `GM-OK:`).

## First deployment: login + start screen (Railway)

20. **Host is Railway: one service from the root `Dockerfile` plus Railway's Postgres service.** SPEC §5/§7 mention Cloud Run;
    the app is host-agnostic (single container, env config), so this is a deploy choice only. `railway.json` pins one replica
    and a `/healthz` check. The `PORT` env var is honoured.
21. **Real username/password auth is in already (register, login, logout, me), the game is not.** The start screen has a disabled
    "Ready to play". This proves the DB wiring on the deployed URL. `/api/leaderboard`, `/api/stats` and `/ws` are not built yet.
22. **Only a SHA-256 of the session token is stored** (`sessions.token_hash`), not the raw token as SPEC §6 shows. Same cookie
    (`sid`, httpOnly, SameSite=Lax, 7d, `Secure` behind an https proxy). There is no `SESSION_SECRET`: tokens are random and opaque.
23. **`users.username` is plain `text` with a lowercase `check`, not `citext`**, so no Postgres extension is needed.
    The server lowercases input before use.
24. **Login and register are rate-limited per client IP (10 per minute, in memory).** Fine for one instance. The IP is the last
    `X-Forwarded-For` entry. POSTs must be `application/json`, which with SameSite=Lax covers CSRF for these endpoints.
25. **Migrations are hand-rolled** (`internal/store/migrations/*.sql`, embedded, tracked in `schema_migrations`, advisory lock).
    The server runs them on start and retries the DB connection for ~30s, because the DB service can start after the app.
    No `DATABASE_URL` means the server still starts and `/api/*` auth answers 503.
26. **Web: Next.js + TypeScript with plain CSS ported from `design/game-room`; Tailwind is deferred.** The mockup CSS is already
    the palette and components. Fonts come from `next/font/google`, which self-hosts them at build time. Dev runs `next dev`
    with rewrites to `:8080`. Production uses `output: 'export'`, which is embedded in the Go binary.
27. **Pinned Go deps (`pgx v5.7.2`, `x/crypto v0.31.0`)** so `go.mod` stays at `go 1.23`. Newer releases force Go 1.26.
