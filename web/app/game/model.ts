// Client-side game state for the table screen. Everything here is derived from what the server
// sent to this player: game.snapshot, phase.start, phase.reveal, submit.ack, vote.ack, note.ack and
// game.result. Nothing here knows (or may know) which seats are AI before game.result (invariant 1).

import type {
  GameResult, Phase, PhaseReveal, PhaseStart, PublicConfig, PublicSeat, Question, RevealItem, Snapshot, SubmitAck,
} from "@/lib/ws";

export type Mark = "none" | "?" | "!" | "human" | "ai";
export type NoteState = { mark: Mark; note: string };

export type State = {
  loaded: boolean;
  gameId: string;
  mySeat: number;
  seats: PublicSeat[];
  config: PublicConfig | null;
  phase: Phase;
  round: number;
  deadline: number; // server clock, ms
  skew: number; // server clock − local clock, ms
  phaseStartedLocal: number;
  questions: Question[];
  reveals: PhaseReveal[];
  mine: SubmitAck[];
  myVote: number | null;
  notes: Record<number, NoteState>;
  result: GameResult | null;
};

export type Action =
  | { t: "snapshot"; p: Snapshot }
  | { t: "phase"; p: PhaseStart }
  | { t: "reveal"; p: PhaseReveal }
  | { t: "ack"; p: SubmitAck }
  | { t: "vote"; target: number }
  | { t: "note"; target: number; n: NoteState }
  | { t: "result"; p: GameResult };

export const initial: State = {
  loaded: false, gameId: "", mySeat: -1, seats: [], config: null, phase: "LOBBY", round: 0, deadline: 0, skew: 0,
  phaseStartedLocal: 0, questions: [], reveals: [], mine: [], myVote: null, notes: {}, result: null,
};

const MARKS: Mark[] = ["none", "?", "!", "human", "ai"];
export const asMark = (m: string): Mark => (MARKS.includes(m as Mark) ? (m as Mark) : "none");

export function reducer(s: State, a: Action): State {
  switch (a.t) {
    case "snapshot": {
      const p = a.p;
      const notes: Record<number, NoteState> = {};
      for (const n of p.notes ?? []) notes[n.target_seat] = { mark: asMark(n.mark), note: n.note ?? "" };
      return {
        loaded: true, gameId: p.game_id, mySeat: p.my_seat, seats: p.seats, config: p.config,
        phase: p.result ? "RESULT" : p.phase, round: p.round, deadline: p.deadline_ts, skew: p.server_ts - Date.now(),
        phaseStartedLocal: Date.now(), questions: p.questions ?? [], reveals: p.reveals ?? [], mine: p.mine ?? [],
        myVote: p.my_vote ?? null, notes, result: p.result ?? null,
      };
    }
    case "phase": {
      if (!s.loaded) return s; // the snapshot we asked for is on its way and is newer
      const p = a.p;
      return {
        ...s, phase: p.phase, round: p.round, deadline: p.deadline_ts, skew: p.server_ts - Date.now(),
        phaseStartedLocal: Date.now(), mine: [],
        questions: p.phase === "ANSWER" ? p.questions ?? [] : p.phase === "REVEAL" ? s.questions : [],
      };
    }
    case "reveal":
      if (!s.loaded || s.reveals.some((r) => r.phase === a.p.phase && r.round === a.p.round)) return s;
      return { ...s, reveals: [...s.reveals, a.p] };
    case "ack":
      if (!s.loaded || a.p.phase !== s.phase || a.p.round !== s.round) return s;
      if (s.mine.some((m) => (m.question_id ?? "") === (a.p.question_id ?? ""))) return s;
      return { ...s, mine: [...s.mine, a.p] };
    case "vote":
      return { ...s, myVote: a.target };
    case "note":
      return { ...s, notes: { ...s.notes, [a.target]: a.n } };
    case "result":
      if (!s.loaded) return s;
      return { ...s, result: a.p, phase: "RESULT", deadline: 0 };
  }
}

// ---- phases the player can step through with ▲ / ▼ (DECISIONS 6) ----

export type View = { phase: Phase; round: number };
export const sameView = (a: View, b: View) => a.phase === b.phase && a.round === b.round;

// views lists every phase this game has reached so far, in order. The last one is live.
export function views(s: State): View[] {
  if (!s.loaded || s.phase === "LOBBY") return [];
  const rounds = Math.max(1, s.config?.rounds ?? 1);
  const all: View[] = [{ phase: "INTRO", round: 0 }];
  for (let r = 1; r <= rounds; r++) all.push({ phase: "ASK", round: r }, { phase: "ANSWER", round: r }, { phase: "REVEAL", round: r });
  all.push({ phase: "VOTE", round: rounds }, { phase: "RESULT", round: rounds });
  const idx = all.findIndex((v) => v.phase === s.phase && (v.round === s.round || v.phase === "VOTE" || v.phase === "RESULT"));
  return idx < 0 ? all.slice(0, 1) : all.slice(0, idx + 1);
}

export function revealOf(s: State, phase: Phase, round: number): PhaseReveal | undefined {
  return s.reveals.find((r) => r.phase === phase && r.round === round);
}

// questionsFor returns the questions on the table in round r: from the ASK reveal (same ids as the
// questions in phase.start ANSWER), falling back to phase.start if the reveal is not there yet.
export function questionsFor(s: State, round: number): Question[] {
  const ask = revealOf(s, "ASK", round);
  if (ask) return ask.items.filter((i) => i.text !== null).map((i) => ({ id: i.id, seat: i.seat, text: i.text as string }));
  return round === s.round ? s.questions : [];
}

export function answerOf(s: State, round: number, qid: string, seat: number): RevealItem | undefined {
  return revealOf(s, "ANSWER", round)?.items.find((i) => i.question_id === qid && i.seat === seat);
}

export function aliasOf(s: State, seat: number) {
  return s.seats.find((x) => x.seat === seat)?.alias ?? `Seat ${seat + 1}`;
}

export const slotKey = (phase: Phase, round: number, qid?: string) => `${phase}-${round}-${qid ?? ""}`;

export function phaseSeconds(c: PublicConfig | null, p: Phase): number {
  if (!c) return 0;
  return ({ INTRO: c.t_intro, ASK: c.t_ask, ANSWER: c.t_answer, REVEAL: c.t_reveal, VOTE: c.t_vote } as Record<string, number>)[p] ?? 0;
}

export const PHASES: Record<Phase, { title: string; hint: string }> = {
  LOBBY: { title: "Sitting down", hint: "" },
  INTRO: { title: "Introductions", hint: "Say hi in 160 characters. Optional." },
  ASK: { title: "Ask the table", hint: "One question for everyone. 160 characters." },
  ANSWER: { title: "Answer the table", hint: "Answer each question. Skip any. 200 characters." },
  REVEAL: { title: "Answers are in", hint: "Everyone sees these at the same time." },
  VOTE: { title: "Who is the human?", hint: "Pick one seat. Locked once cast." },
  RESULT: { title: "Game over", hint: "Seats are revealed." },
};

// ---- suspicion slider ↔ SPEC §4 marks (DECISIONS 76) ----

export const STOPS = ["AI", "Leaning AI", "Unsure", "Leaning human", "Human"] as const;
const STOP_MARK: Mark[] = ["ai", "!", "none", "?", "human"];
export const markToStop = (m: Mark) => Math.max(0, STOP_MARK.indexOf(m));
export const stopToMark = (i: number): Mark => STOP_MARK[Math.max(0, Math.min(4, i))];
