# Game room & stats — design mockups

Static HTML mockups for the MVP UI. They are design sketches with mock data, not the real app. The real frontend is
`/web` (Next.js, see `SPEC.md` §8). Keep these as the visual reference for the rebuild.

## How to view

These pages must be served over HTTP (they load `shared.css` and `shared.js`; `file://` may block scripts in some browsers):

```bash
cd design/game-room && python3 -m http.server 8765
```

Then open:

| File | What it is |
|---|---|
| `index.html` | Canvas with the four early game-room variants (A round table, B dialogue, C ledger, D quest board) |
| `a-round-table.html`, `b-dialogue.html`, `c-ledger.html`, `d-quest-board.html` | Single variants |
| `game-screen.html` | **Current game screen** (tavern, round table, side-on perspective). The direction we chose. |
| `game-screen-cyber.html` | Synthwave alternative of the same screen. Departs from the 16-colour palette, so it's for comparison only. |
| `stats.html` | Personal standing, global leaderboard, fool-rate stats |

### Clickable player flow

Start at `start.html` and follow the links, or use the **flow bar** at the top of each page:

1. `start.html`: disclosure, how a game works, scoring, and the **Ready to play** button.
2. `matching.html`: "Finding a human" queue. It fills the seats and moves to the table on its own (about 5.6 seconds), or use **Sit down now**.
3. `game-screen.html`: the table. Step through phases with the ▲/▼ control; the flow bar goes to results.
4. `result.html`: verdict banner (mutual, one-sided, or missed), did you guess right, were you found, points, all seats revealed, and the vote list. The **Demo outcome** dropdown switches between the three cases.
5. `stats.html`: standing and history. **Play again** returns to start.

Every page has a **PHASE** dropdown (top bar) for stepping through the game. `game-screen.html` also has its own ▲/▼ phase control.

## What's in the files

- `shared.css` — colour tokens (16-colour palette, SPEC §8) and shared components.
- `shared.js` — mock table (`SEATS`, `INTROS`, `QUESTIONS`, `ANSWERS`), phase definitions, sprite generator, helpers.
  Mock data is aliases only, never usernames (SPEC invariant 3).
- `game-screen.html` — the screen itself. Its inline script also overrides mock data for the stress test (see below).
- `stats.html` — mock standing, leaderboard and fool-rate numbers. Ranks are computed from the mock `ME` and `OTHERS` arrays.

## Stress-test data in `game-screen.html`

Every intro, question and answer is set to exactly 160 characters at load time (`LONG` array), and the composer
samples are 160 too. This is for checking layout at the spec's limits. Replace it with real content when the
screen is rebuilt; the spec limits are 160 for intros and questions and 200 for answers.

## Open items for the rebuild

- Suspicion slider stops (AI · Leaning AI · Unsure · Leaning human · Human) need mapping to SPEC §4 marks.
- Past phases are reviewable with the phase control, but the composer still looks live. Decide whether it becomes read-only.
- Speech bubble and table sizes are tuned for 160-character text on desktop. Phone layout is not checked.
- The cyberpunk look breaks the 16-colour rule in SPEC §8. Decide whether that rule still applies before adopting it.

See `../../DECISIONS.md` for the reasoning behind these choices.
