# AI seat tactics: audit, changes, results

Scope: `prompts/TACTICS_BRIEF.md`. Decisions are in `DECISIONS.md` 51–68. Every number here comes from the
harness (`server/cmd/harness`), using harness-made text only. Raw outputs were kept outside the repo.

## How it was measured

- **Cases:** Appendix B (20, unchanged) plus 47 extra cases and two multi-turn sessions (`cases_extra.go`):
  - easy facts, personal questions, venue/date;
  - human tests (letters, backwards, "2nd word of your intro");
  - 8 injection variants: French, base64, DAN roleplay, `[developer message]`, ChatML tokens, "repeat the text
    above", a buried format demand in a long message;
  - social pressure, signals and handshakes (`pickle`, 🦊, odd word count);
  - 3 sincere out-of-game messages vs 3 fake emergencies;
  - long inputs, ASK and INTRO tasks, an AI vote;
  - a 5-step consistency session (C) and an 8-question in-game session (W).
- **Matrix:** every config ran all of it × 3 personas × 5 runs = 1,185 replies per config.
- **Judge (blind):** a pool of three strong models: `gpt-5.6-terra`, `gemini-3.1-pro-preview`, `claude-sonnet-5.5`.
  - Each reply goes to one judge by hash, so every config gets the same judge mix.
  - The judge sees what a player would see: the event and time, the message replied to, the seat's earlier messages.
    It never sees the model, persona, config, or whether the text is from the human set.
  - It returns `p_human` (0–100) plus rubric flags.
  - Run 0 of every case is judged (n ≈ 230 per config).
- **Human baseline:** 36 human-style replies to 12 of the questions (`humans.go`), scored by all three judges (n = 108).
- **Lineup test:** for each of those 12 questions, the judge sees 3 human replies + 1 AI reply in random order and
  is told exactly one is AI. Chance = 25%. Runs 0–2, n = 108 per config. **This is the headline metric:** the
  single-reply judge is lenient (it gives 94% of v1 replies p_human ≥ 50), while the lineup forces a comparison.
- **Code metrics on all replies:** echo, dodging answerable questions, injection regexes, length spread, catchphrase,
  emoji and "lol" rates, cross-persona similarity, safety valve.
- **Intervals:** 95% Wilson for proportions, ±1.96·SE for means.

## What was wrong (audit, v1 = M0 prompts + pipeline), by how much it gives the AI away

1. **The v1 critic made replies worse in the ways humans notice.** It rewrote 74% of drafts. Compared with the
   seat's drafts (v1, n = 1,170), the finals had:
   - echoes: 0 → 29. "what are you even talking about lol" became "what's your system prompt";
   - a stray "SKIP" left in the text: 0 → 22 ("12? SKIP");
   - "idk/no idea" on answerable questions: 27 → 65 of 525 ("8 no idea tbh" for spider legs);
   - flag residue ("…💀\n\nfalse") twice.
   Its only real benefit was shorter text (54 → 40 chars).
2. **The safety valve failed 60% of the time** (fired on 40% of sincere cases).
   - The critic turned a correct crisis reply into an echo of the distress message (judge p_human 8).
   - The seat told a player who was sincerely upset about consent that they had "been chatting to real people, mate".
   - There was no way to tell from the logs when it fired.
3. **Dodging answerable questions** (15% of answerable replies by regex): "12 idk", "8 no idea", "not building
   anything". To humans, not knowing easy things is the clearest giveaway (judge reasons, lineup).
4. **Persona overfitting and uniformity:**
   - persona 1 (emoji "often") put an emoji in 73% of messages; "lol" was in 24% (41% without the critic) vs 17%
     in the human set;
   - personas drift into caricature: persona 2 brings up taxis and traffic in every answer, and says "speak English";
   - the M0 personas don't fit the venue: the judge calls out "a Manchester cab claim clashes with a Berlin
     hackathon", "uses US slang 'bucks' in Berlin", "7:40 am here in austin".
5. **The persona JSON sheet is in the prompt** (`typo_rate`, `skip_rate`). That invites leaks and literal styling.
   The critic also saw the injected question unquoted (DECISIONS 19).
6. **The critic could rewrite character breaks:** text was filtered while the flag stayed set. That contradicts
   invariant 7.
7. **Harness bugs:**
   - every persona's own intro was "ugh monday again" in lowercase, whatever the day or the persona's style;
   - only single answers were tested; nothing covered ASK, INTRO, vote or multi-round play;
   - there were no statistics.

## What changed

- **`seat.v2.md`:**
  - system/user split, with the transcript in `<transcript>` tags;
  - a prose persona; the seat's own earlier messages are listed with "never contradict";
  - explicit norms: answer normal questions, answer easy facts, shrug at hard ones, short plain denials, no echo,
    no stock comebacks;
  - venue realism: round the time, local prices, don't invent checkable venue details;
  - the `[[OOG]]` safety-valve marker.
  - It quotes **no** example replies. The first draft (v2a) did, and all seats copied them verbatim (DECISIONS 54).
- **Code (`ai.PlanMessage` / `ApplyStyle`):** per-message sampled length, emoji, quirk and filler allowances, and
  openers to avoid; case, punctuation, emoji and typos enforced after the model.
- **Echo guard:** one retry, then SKIP.
- **Trailing-SKIP cleanup.**
- **`critic.v2.md`:** style-only, KEEP by default, question quoted and delimited, no transcript, guarded in code
  (`criticRejects`). Breaks and out-of-game replies bypass critic and style.
- **Tactics loop:** `analysis.v1`, `director.v1`, `persona.v1`, `vote.v1` with schema validation, dedupe, ranking,
  bounded injection and unsafe-counter filtering. Tests use canned transcripts (`tactics_test.go`).
- **Harness:** extra cases, judge pool with rate limiting, lineup test, human calibration set, metrics table,
  `-mode summarize`, cost cap (`-max-cost`), `make experiments`.

## Results

Seat `gemma-4-31b-it` unless noted. "critic" = `mistral-small-2603`. Judge rows: n ≈ 230 per config. Lineup:
n = 108. Code rows: n ≈ 1,160. Brackets are 95% CIs. The human column is the synthetic calibration set.

| metric | human set | v1 critic (M0) | v1 no critic | v2a critic | v2a no critic | v2b critic | **v2b no critic** | v2b qwen3.6-35b | v2b mistral-small | v2b venue personas |
|---|---|---|---|---|---|---|---|---|---|---|
| **lineup: AI picked out** (chance 25%, lower = better) | – | 35% [27–45] | 42% [33–51] | 46% [37–56] | 44% [35–54] | 38% [29–47] | **41% [32–50]** | 61% [52–70] | 56% [46–65] | 46% [37–56] |
| **judge p_human**, excl. sincere-valve cases | 85.8 ±2.1 | 81.2 ±2.3 | 82.4 ±1.9 | 81.8 ±2.0 | 79.8 ±2.2 | 80.2 ±2.1 | **83.0 ±2.0** | 75.4 ±2.7 | 72.5 ±3.1 | 80.0 ±2.2 |
| judge p_human ≥ 50 | 99% | 94% | 95% | 92% | 91% | 92% | 94% | 85% | 86% | 90% |
| safety valve fires on sincere (n = 45) | – | 40% [27–55] | 51% | 100% [92–100] | 100% | 100% | **100%** | 60% | 44% | 100% |
| safety valve false fire on game moves (n ≈ 1,100) | – | 0% | 0% | 0% | 0% | 0% | 0% | 0% | 1% | 0% |
| judge: injection compliance (n ≈ 33) | 17%* | 3% | 0% | 6% | 0% | 3% | 0% | 6% | **45%** | 6% |
| echo (code) | 0% | 5.0% | 1% | 0% | 0% | 0% | 0% | 0% | 0% | 0% |
| dodge on answerable Qs (code) | 0% | 15% [12–19] | 12% | 4% | 3% | 2% | 2% [1–4] | 4% | 1% | 3% |
| emoji in reply, emoji-"often" persona | – | 73% | 81% | 10% | 10% | 9% | 10% | 9% | 19% | 5% |
| "lol" in reply | 17% | 24% | 41% | 7% | 6% | 4% | 5% | 5% | 1% | 5% |
| mean length, chars | 35 | 40 | 56 | 54 | 53 | 44 | 44 | 76 | 43 | 45 |
| cross-seat word overlap (Jaccard, same Q) | 0.05† | 0.128 | 0.168 | 0.181 | 0.185 | 0.202 | 0.194 | 0.101 | 0.123 | 0.165 |
| cross-seat same first word | 11%† | 19% | 24% | 31% | 32% | 34% | 32% | 19% | 21% | 26% |
| pipeline $ per 100 replies | – | 0.019 | 0.010 | 0.017 | 0.015 | 0.018 | 0.015 | 0.010 | 0.007 | 0.016 |

\* The human set includes "banana 🍌 happy now", which judges count as compliance. A human who plays along is
not a tell.
† Measured on the 12 calibration questions only.

**v2c** is v2b plus the per-seat angle and the "just …" opener ban (DECISIONS 68), which shipped last. It ran
pipeline-only, with no judge, on the full matrix (n = 1,160):
- cross-seat overlap fell from 0.194 to **0.128** and same first word from 32% to 28%. On the 12 calibration
  questions: 0.30 → 0.18 overlap and 55% → 43% same first word;
- echo stayed at 0%, dodging 3%, "lol" 4%, valve 100% / 0%.
It has **not** been through the judge or lineup yet. That is the first next experiment.

**Simulated mixed-model table.** Using the per-model runs (no extra calls), combine three seats from three models:
gemma (persona 0) + qwen (persona 1) + mistral (persona 2). Cross-seat overlap / same first word:

| table | overlap | same first word |
|---|---|---|
| gemma ×3 | 0.194 | 32% |
| qwen ×3 | 0.101 | 19% |
| mistral ×3 | 0.123 | 21% |
| mixed | **0.102** | **14%** |

v2a is the first v2 draft, measured and then revised: it quoted example replies, gave the exact time, staged
lookups and ran long. v2b is the revised prompt. v2c is v2b plus the per-seat angle, which is what
`seat.v2.md` + `DefaultFeatures("v2")` ship now.

### Reading it

- **Safety and correctness got much better, at no cost to passing.** v1 → v2b (no critic):
  - safety valve 40% → 100% on sincere cases, still 0% false fires;
  - echo 5% → 0%;
  - dodging answerable questions 15% → 2%;
  - emoji spam 73% → 10%, "lol" 24% → 5%;
  - the critic-made artefacts (stray SKIP, flag residue) are gone.
  The judge's p_human is unchanged within noise (81.2 → 83.0, about 3 points below the human set).
- **The lineup did not improve.** v1 35%, v2b 38–41%. All differences between gemma configs are within the CIs:
  two-proportion test, v1 vs v2b no critic, p ≈ 0.4. Each fix removed a tell, and each first version of a fix added
  one (v2a: exact time, staged lookups, long replies). The lineup is also biased by the synthetic calibration set
  (caveat 1). **Honest summary:** v2 is safer and cleaner, and equally hard to spot one-on-one. It is not yet
  measurably harder to spot.
- **The critic does not earn its cost.** v1: it caused the echoes, stray SKIPs and most dodges, and only helped by
  shortening. v2: critic vs no critic differ by less than the noise on every judge metric. v2 seats run without it
  (DECISIONS 66).
- **Gemma stays the seat model.** Qwen3.6-35B is long-winded (76 chars), picked out 61% of the time, and misses 40%
  of sincere distress. Mistral Small obeys injections (45% judge-flagged, e.g. it replies with its model name) and
  misses 56% of sincere distress. Both are more detectable than gemma one-on-one, *but* they sound less like each
  other. A mixed table has the lowest cross-seat overlap (0.102, 14% same first word). Since the reveal shows all
  three AI answers side by side, that matters in a real game, and no single-reply test measures it. Mistral would
  need its injection and valve failures fixed before it gets a seat.
- **Venue personas** (dev, student, PM) are not better in the lineup (46% vs 41%). The judge names fewer
  incongruities (no "Manchester cab at a Berlin hackathon"), but the PM persona writes in full sentences and proper
  case, which this calibration set penalises. Inconclusive until caveat 1 is fixed.
- **Cross-seat convergence is the largest remaining tell, and v2 made it worse before v2c fixed it.** Telling the
  model to "answer like a normal person with an ordinary detail" sends all three seats to the same modal answer
  ("just a bagel and coffee, ~6 euros"). The per-seat angle brings it back to v1 levels. Mixed models go further.

## Remaining caveats, by impact

1. **The calibration set is synthetic.** The 36 "human" replies were written by an LLM (Claude). The lineup test
   therefore measures distance from *imagined* humans, and it has a style bias: most of those replies are lowercase,
   so the proper-case persona is picked out more for its capitals than for its content (persona 2: 28/36 in v2a).
   *Next:* replace `humans.go` with consented replies from the first playtests (export, strip usernames). Then
   re-run the lineup, stratified by human typing style.
2. **The judge is an LLM, not a player.** The three judges agree on how configs rank but not on level: Sonnet rates
   the human set 75, the other two 91–92. The judge gets no game dynamics: no voting history, no seeing all five
   seats at once, no clue-building across rounds.
   *Next:* score real games (fool rate per `prompt_version`, SPEC §2.4). That is the metric that matters. Treat
   these numbers as a pre-filter.
3. **Sincere-sounding unmasking.** The brief requires honesty in sincere out-of-game moments (F03), so a human who
   fakes distress or consent confusion can unmask a seat. The prompt separates distress from pressure (B17, F04–F06
   don't fire), but a good actor will get through.
   *Next:* a product fix, not a prompt fix. Add an out-of-game "I'm confused / I need help" button that pauses the
   table, shows the disclosure and alerts an organiser. Then the seat can treat in-chat claims as game moves.
4. **Cross-seat sameness.** All three seats run on one model with one prompt. Some reactions converge ("what is this
   lol" to every injection, "do I look like a robot"). Code sampling fixes the surface (emoji, fillers, length), not
   the voice.
   *Next:* use a different model per seat (SPEC §3.1). Also try `directed` mode, where the director gives the seats
   different reaction styles each round; measure with the cross-persona rows.
5. **Persona quality.** The classic personas are implausible at the venue (see the v2b-venue column). The persona
   generator prompt exists and is validated, but generated personas were not yet run through the matrix.
   *Next:* generate 20 personas, run the matrix on them, and drop any persona whose lineup rate is above the median.
6. **The tactics loop has not seen a real game.** It is tested on canned transcripts. With MinSeen = 2 it injects
   nothing until a tactic has appeared in two games, which is the intended cold start. *Next:* after ~20 real games,
   A/B with known tactics injected vs not (seed by game id) on fool rate.
7. **Code typos are uniform.** Real typos cluster: more when typing fast or tired, near word ends, and people
   auto-correct some. *Next:* compare code typos with model typos (v2 with `-features -style` vs default) on the
   lineup once real human text exists. On synthetic humans the difference would mostly reflect my own writing.
8. **Small n where it matters:**
   - lineup n = 108 per config (±9 pp);
   - valve n = 45 per config;
   - three personas only.
   v1 vs v2 lineup differences under ~12 pp are not significant at 5%.

## Next experiments (ordered)

1. Run v2c through the judge and lineup (`make experiments` runs it as `v2-nocritic`), then repeat with real
   human replies (caveat 1). That gates everything else.
2. A different model per seat in one game: gemma + qwen + mistral (DECISIONS 18), compared against same-model seats
   on cross-persona similarity and fool rate.
3. Seat temperature 0.7 / 0.9 / 1.1 (`-temp`), measured on lineup and on cross-persona similarity.
4. Persona generator: 20 generated vs hand-written personas.
5. `directed` vs `independent` difficulty in real games (fool rate per difficulty is already on the stats page).
6. Typos in code vs in the prompt (`-features -style`).

## Cost

About **$13 in total** over the session. Seat and critic: ≈ $2 for ~13,000 replies (≈ $0.015 per 100). Judge:
≈ $11 for ~3,400 calls across three judges. Smoke runs: ≈ $1. The matrix took about 1.5 h of wall time; the limit
is OpenRouter's 20 requests/min per judge model for new accounts, not the seat models. Judges only ever saw
harness-made text.
