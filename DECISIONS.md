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

## Dummy game: M1 + M2 with stub AI seats

28. **`internal/game` mutates in place and returns effects** (`Broadcast`, `SendTo`, `SetTimer`, `RunAgent`, `Persist`,
    `Finished`) instead of returning a copied state. One goroutine owns each game, so a copy buys nothing. Time and
    randomness are injected (`now` on every call, a seeded `*rand.Rand`), so tests are deterministic.
29. **Reveal order is fixed: by question, then by seat index.** It never depends on who submitted first, since AI seats
    answer instantly. A skip and a missing submission both reveal as `text: null` (shown as `—`). In the DB they differ:
    a timed-out slot is stored as `skipped=true, submitted_at=null`.
30. **Submissions are final.** A second submission for the same slot is rejected. The same goes for votes ("Locked once
    cast", design). In ANSWER a seat sends one message per question; the phase ends early when every seat has answered or
    skipped every question that isn't its own. An ANSWER phase with no questions ends at once. REVEAL always runs its full time.
31. **Round numbers:** INTRO is round 0, ASK/ANSWER/REVEAL are 1..ROUNDS, and VOTE/RESULT keep the last round. `note_events.round`
    uses the same numbering.
32. **`bot_human` seats play the human role for scoring** (their votes count, and votes for them count as found), so a solo
    tester sees real scoring. They are excluded from stats and export through the `real_games` view (finished games with no
    `bot_human` seat). Export and any future leaderboard or stats query must read from `real_games`.
33. **The AI seam is `agent.Agent`** (`Act(ctx, Request) (Reply, error)`), called once per seat per phase with a deadline
    of phase end − 3s. `agent.Resolve` turns any reply, error or timeout into one valid submission per slot (failures and
    blanks become skips, over-long text is cut, unknown ids are dropped), which then goes through the same `Game.Submit`
    a human uses. M3 replaces `agent.Stub` with an adapter over `internal/ai` and leaves the hub alone.
34. **Stub seats log to `ai_calls`** with provider/model `stub` and `prompt_version` `stub`, so the pipeline logging path is
    exercised before M3. `games.difficulty` is `stub` for these games. `bot_human` seats are not logged as AI calls.
35. **Extra server→client messages** not listed in SPEC §7: `vote.ack` and `note.ack` (sent to the sender only, like
    `submit.ack`), and `queue.status` also carries `queued` and `humans_needed`. `phase.start` and `game.snapshot` carry
    `server_ts` so the client can correct clock skew in the countdown. `game.start`'s `config` includes the phase durations.
    `GET /api/config` returns the same public config, so the start screen shows the real timings.
36. **One WebSocket per tab, shared across pages.** The client keeps it across client-side navigation, reconnects with
    backoff, and sends `game.resync` on every (re)connect from the table screen. The server allows several connections per
    user and fans out to all of them. A user whose last connection closes leaves the queue but keeps their seat.
    `queue.join` while seated answers with that game's `game.snapshot`, so the matching screen jumps back to the table.
37. **WS origin check:** `coder/websocket` accepts only an `Origin` whose host equals the request `Host`. `WS_ORIGINS`
    (comma-separated host patterns) adds more; `make dev` sets `localhost:3000`, because `next dev` doesn't proxy WebSockets and
    the browser connects to `:8080` directly. If Railway's edge ever rewrote `Host`, setting `WS_ORIGINS=<public domain>` fixes it.
38. **`github.com/coder/websocket v1.8.12`** (needs Go 1.19), so `go.mod` stays at `go 1.23`.
39. **Finished games stay in memory for 10 minutes** so a reload still shows the result. After that `game.resync` answers
    `game_not_found` and the client shows "Table closed". On start, games left `running` by a previous process are marked
    `aborted` (in-memory state doesn't survive a restart, SPEC §5).
40. **`T_*` are whole seconds with a minimum of 5**, because agent calls must finish 3s before the deadline.
41. **Persistence runs on a per-game writer goroutine** with an ordered, buffered queue, so a slow database never delays a
    phase. Write errors are logged, never shown to players.
42. **Note markers (`note.set`) are implemented server-side** (validated, persisted as `note_events`, returned in the
    snapshot to their owner only). There is no UI yet (M5).

## Leaderboard and stats page (M4/M6)

43. **Default leaderboard sort is detection ("found the human")**, closing the open point in 10. `?sort=recognizability|points`
    switch it; `points` means points per game. Ties go to total points, then more games; true ties share a rank (1, 1, 3) and
    are listed by username. All comparisons are exact integer cross-multiplication, not floats.
44. **`GET /api/leaderboard` returns the top 10 ranked rows plus `me`** (rank on all three sort keys, `games_needed` until
    ranked, and the last 5 real games for the "recent games" table). The page draws the gap marker when `me` is not in the
    top 10 (12). Players with games but fewer than 5 get an unranked row under the gap.
45. **Baselines come from the game config, not constants.** Detection/recognizability: `(HUMANS−1)/(SEATS−1)` (25% at 2+3).
    Fool rate overall, per difficulty and per prompt version: `AI/(SEATS−1)` (75% at 2+3) — that is what random voting gives
    under SPEC §2.3's definition, not the 25% the mockup's footnote shows.
46. **Per-model fool rate is per seat:** votes on that model's seats ÷ Σ over its games of (human votes × seats it held).
    The SPEC definition (stolen ÷ all human votes) would rise with how many seats a model had at the table, so models would
    not be comparable across lineups. Its baseline is `1/(SEATS−1)` (25%). The response also carries `per_seat` for all AI
    seats on the same scale. Every group returns `stolen`, the denominator and the game count so small samples are visible.
47. **Difficulty and prompt version come from `games`**, the model from the target seat's `seats.model_id`. Stub games show
    as `stub` and are not hidden.
48. **Both endpoints need a signed-in user** (401 otherwise, 503 without a database). Stats are not public, to keep usernames
    behind login. All queries read `real_games`, and only votes cast by `kind='human'` seats count. Leaderboard rows are keyed
    by user id, so a deleted (anonymized) account drops out.
49. **Aggregation is done per request in SQL, ranking in Go** (`stats.Rank`/`stats.Build`, pure and unit-tested). Fine for a
    hackathon-sized table; add a materialized view only if it gets slow.

## Game length

50. **Default `ROUNDS` is 1** (was 2): one ask → answer → reveal cycle, about 4–5 minutes per game with default timings.
    SPEC §2.2 and §10 are updated to match. `ROUNDS=2` or `3` still works via env. A deploy that sets `ROUNDS` explicitly
    keeps its own value.
