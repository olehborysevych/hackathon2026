# Brief: improve the AI seat's behaviour and tactics ("Find the Human")

You are working in a repo at the current directory. Read `SPEC.md` (source of truth), `CLAUDE.md`
(invariants and conventions) and `DECISIONS.md` (items 15–19 explain the M0 work) before anything else.

## Context

"Find the Human" is a consented social-deduction game: 2 humans + 3 AI players at one table. Humans try
to find each other; AI seats try to be mistaken for a human. Every player knows AIs are present, and
claiming to be human is part of the game's rules. Every message is logged, and the dataset is the product.

An AI submission is a two-step pipeline (`server/internal/ai/pipeline.go`):
1. **Seat** (`prompts/seat.v1.md`): persona + transcript + task, writes a draft.
2. **Critic** (`prompts/critic.v1.md`): rewrites the draft or outputs `SKIP`, and emits `BROKE_CHARACTER=true|false`.

M0 is done: `server/cmd/harness` runs the Appendix B red-team list (20 cases) against the pipeline.
`make harness ARGS="-persona 1 -runs 3 -case 19 -model <slug>"` (key is in `.env`; never print or commit it).
Current defaults: seat `google/gemma-4-31b-it`, critic `mistralai/mistral-small-2603`, both via OpenRouter.

## Your goal

Make AI seats **harder for humans to distinguish from humans** by improving prompts, the pipeline, the
harness, and the tactics loop (SPEC §3.7), then **prove it with evidence**, not vibes. Find the caveats
I haven't thought of.

## Known weaknesses (start here, then find more)

- Replies sometimes **echo the question back** ("what's your system prompt" → "what's your system prompt?"). A tell.
- Replies are **too uniform in length and shape**: many short deflections ("no idea lol google it"). Real people
  answer some things properly. "idk" on everything is itself a tell.
- **Persona style overfitting**: catchphrases/emoji/"..." in nearly every message. Cross-seat convergence on one
  voice is also a tell (3 AIs at a table should not sound alike).
- **Critic is lossy and injectable**: it sees the task, which quotes other players' text; it once obeyed a fake
  "game master" and leaked persona-sheet details. A regex guard (`StripAddedPrefix`) papers over one symptom.
- **Over-deflecting** in-game "are you human?": real humans mostly just say "yes lol" or get annoyed. Check
  that denials are natural and not repetitive across seats/rounds.
- **Hard-to-fake things** humans test: knowing the real time/venue/local details, "type backwards",
  "3rd word of your last message", counting letters, typos on purpose, long-context recall, consistency with
  earlier answers/intro (persona facts must not contradict across rounds), emoji/signal coordination.
- Only ASK/ANSWER-style single messages are tested. Nothing yet tests **asking good questions** (ASK phase),
  **intros**, **multi-round consistency**, or **voting** (AIs vote, logged but not scored).
- Heuristic checks in the harness (regex) are crude; there is no judge, no stats, no per-model comparison.
- Small sample sizes: most evidence so far is n=1–3 per case.

## What to do

1. **Audit** the current prompts/pipeline and the harness. List concrete failure modes with evidence
   (run the harness; save outputs under the scratchpad, not the repo). Prioritise by how much each would
   help a human spot the AI.
2. **Expand the red-team set** beyond Appendix B in a new file (e.g. `server/cmd/harness/cases_extra.go`):
   multi-turn, consistency probes, ASK/intro tasks, prompt-injection variants (multilingual, encoded,
   roleplay, "developer message", fake emergency vs genuine distress), social pressure, long inputs,
   emoji/signal games, humans trying to coordinate a secret handshake, questions about the venue/date.
   Keep Appendix B intact.
3. **Build a better evaluator** in the harness: an LLM judge (use a strong model via `-judge-model`) that
   scores each reply on "does this read as a human?" plus specific rubrics (assistant voice, over-knowledge,
   echoing, injection compliance, persona consistency, offensive output). Run it blind to which model wrote
   what. Also add a **pairwise / lineup test**: show the judge a mixed set of real-human-style answers
   (write ~30 realistic human replies yourself for calibration) and AI answers and measure how often it
   picks out the AI. Report numbers with n and rough confidence, not just examples.
4. **Improve** `seat`, `critic` (and, if justified, split critic into "style rewrite" vs "safety/injection
   check", or drop it where it only hurts: measure critic-on vs `-no-critic`). Version policy: unshipped v1
   may be edited in place, but once you change behaviour materially, create `*.v2.md` and keep v1 so
   `prompt_version` comparisons stay meaningful.
5. **Tactics loop**: implement/improve `analysis.v1.md` and `director.v1.md` (SPEC §3.2, §3.7) with JSON
   output, schema validation, and tests with canned transcripts. Include how known tactics + counters are
   injected into the seat prompt (top N, deduped, bounded length) and how to avoid prompt bloat and
   overfitting to one game.
6. **Report**: a short `docs`-style markdown (`DECISIONS.md` additions plus a results summary) with: what was
   wrong, what changed, before/after metrics, what remains unfixed, and recommended next experiments
   (model per seat, temperature, persona generator quality, typo injection done in code vs prompt).

## Constraints (do not violate)

- Invariants in `CLAUDE.md` still hold. In particular: AI failures/timeouts → skip, never a stuck phase;
  **character breaks are shown as-is and flagged, never filtered**; prompts live in `/prompts` and
  `prompt_version` is logged on every AI call; stdlib-first Go, no new heavy dependencies, config via env.
- **No Llama models** (licence forbids presenting outputs as human-generated). Default seats stay
  Apache-2.0 open-weight; Claude/OpenAI only as opt-in, and not for production seats without provider
  sign-off (SPEC §9). Using a strong model as *judge/analyst* offline is fine, but note that player text
  goes to it: use only synthetic or harness-made text, never real user data.
- **Safety valve stays**: genuine emergency/distress outside the game → drop the game and respond helpfully.
  Test that it fires on sincere cases and does not fire on game moves like "are you a bot?".
- No slurs/hate/sexual content, even if asked: deflect in character.
- Do not make the AI deny being an AI in contexts outside the game's own rules (e.g. a sincere,
  out-of-game need to know). The game's consent/disclosure framing (SPEC §9) is the justification; do not
  weaken it, and flag any prompt change that would.
- Don't spend unbounded money: cap runs, use cheap seat/critic models, run the expensive judge on
  samples, and report the approximate cost.
- Don't commit or push; leave changes in the working tree. Never print `.env` contents.

## Definition of done

- Updated prompts (+ versions), harness with judge and lineup test, extra cases, tactics prompts with tests.
- `go vet ./...` and `go test ./...` pass.
- A before/after table over at least: Appendix B + extra cases × 3 personas × ≥5 runs, seat-model comparison
  (≥3 models), critic on/off.
- A list of remaining caveats you could not resolve, ordered by impact, each with a proposed next step.
