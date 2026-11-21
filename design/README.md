# Design — Find the Human (MVP)

This folder holds the approved UI design for the browser-in-desktop MVP. It is the visual and flow reference for the
implementation in `/web` (Next.js, static export, see `SPEC.md` §8). The HTML files are mockups with mock data.
They are not production code. Rules and API are in `SPEC.md`. Design choices are in `../DECISIONS.md`.

## What to build

Five screens, in this order. Each one has a matching mockup in `game-room/`.

| # | Screen | Mockup | Spec |
|---|---|---|---|
| 1 | Start: disclosure, how a game works, scoring, **Ready to play** | `game-room/start.html` | §2, §9 |
| 2 | Matching: FIFO queue, seats fill, opens the table | `game-room/matching.html` | §1, §2.1 |
| 3 | Table: round table, speech boxes, phases, slider, composer | `game-room/game-screen.html` | §2.2, §4, §8 |
| 4 | Result: verdict, did you guess right, were you found, points, unmasked seats, votes | `game-room/result.html` | §2.3, §7 `game.result` |
| 5 | Stats: your standing, global leaderboard, fool rate | `game-room/stats.html` | §2.4, §7 `/api/stats`, `/api/leaderboard` |

Start with the table (screen 3): it carries most of the design. The rest is simpler.

## The visual direction

- **Tavern look**, chosen over the synthwave variant (`game-room/game-screen-cyber.html`, kept for reference only).
- **16-colour palette** and pixel fonts, as in `SPEC.md` §8. Tokens are in `game-room/shared.css`.
- **Round medallions** for players, not square cards. **Speech boxes** for intros, questions and answers.
- **Side-on perspective:** four other players around a table, you at the bottom centre.
- The disclosure banner stays on start, matching and table screens (`SPEC.md` §9).

## Screen behaviour that must hold

These are the rules the mockups show. Implementation must keep them.

1. **Sealed content.** Each speech box shows only what the current phase allows (see `game-room/shared.js`
   `transcriptEntries` and `game-screen.html` `bubblesFor`). Nothing shows who has submitted or is typing.
2. **Aliases only** on game screens. Usernames appear only on the stats pages (`SPEC.md` invariant 3).
3. **Suspicion slider** per other player, five stops: AI · Leaning AI · Unsure · Leaning human · Human.
   Only the owner sees it. It maps onto the `SPEC.md` §4 marks. **The mapping is still open** (see below).
4. **One question at a time** in ANSWER and REVEAL, with the asker named and highlighted. ◀ ▶ moves between questions.
5. **Phase review** (▲ / ▼ on the table) lets a player step back through intros, questions and answers.
6. **Result** always states two things clearly: whether you guessed right, and whether another player found you.
   Points follow `SPEC.md` §2.3 (+1 correct guess, +1 found, +1 mutual bonus; mutual = 3).
7. **Stats** rank only players with at least 5 games (`SPEC.md` §2.4). The 25% random baseline is always shown.

## Mapping to the API (`SPEC.md` §7)

| Screen element | Source |
|---|---|
| Seats, aliases, sprite seeds | `game.start` |
| Phase, timer, questions in ANSWER | `phase.start` |
| Answers and intros in REVEAL | `phase.reveal` |
| Your submit / skip | `game.submit`, `game.skip` (ack: `submit.ack`) |
| Suspicion slider | `note.set` (logged with timestamps, invariant 4) |
| Vote | `vote.cast` |
| Result: votes, kinds, models, points | `game.result` |
| Standing and leaderboard | `GET /api/leaderboard`, `GET /api/stats` |

The mock data shapes in `game-room/shared.js` (`SEATS`, `INTROS`, `QUESTIONS`, `ANSWERS`, `REVEAL_SEATS`) follow
these messages. They are the first thing to replace with real data.

## Known open items

- **Slider stops vs. marks.** Map `AI · Leaning AI · Unsure · Leaning human · Human` onto `none | ? | ! | human | ai`.
- **Read-only review.** Past phases in the table are reviewable, but the composer still looks live. Make it read-only.
- **Leaderboard sort key.** Default is detection rate, ties by points. Confirm it.
- **Phone layout** is not designed yet. Table and speech boxes are tuned for desktop.
- **Test text.** `game-room/game-screen.html` replaces every intro, question and answer with 160-character
  placeholders at load. Remove that block when wiring real data.

## How to view

```bash
cd design/game-room && python3 -m http.server 8765
```

Open `http://127.0.0.1:8765/start.html`. Every page has a flow bar to move between screens.
`game-room/README.md` lists every file and what it is.
