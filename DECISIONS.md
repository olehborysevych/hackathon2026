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
