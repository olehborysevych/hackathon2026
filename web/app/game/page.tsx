"use client";

import { Suspense, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import {
  socket, type ConnState, type GameResult, type Phase, type PhaseReveal, type PhaseStart, type PublicConfig,
  type PublicSeat, type Question, type Snapshot, type SubmitAck, type WsError,
} from "@/lib/ws";
import { Disclosure, Sprite } from "../components";

// Table screen (design/game-room/game-screen.html), with the vote and result screens.
// Static export ⇒ the game id comes from the query string (/game?id=…), not a route segment.
export default function GamePage() {
  return (
    <Suspense fallback={<main className="wrap"><p className="muted">Loading…</p></main>}>
      <Game />
    </Suspense>
  );
}

type State = {
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
  result: GameResult | null;
};

type Action =
  | { t: "snapshot"; p: Snapshot }
  | { t: "phase"; p: PhaseStart }
  | { t: "reveal"; p: PhaseReveal }
  | { t: "ack"; p: SubmitAck }
  | { t: "vote"; target: number }
  | { t: "result"; p: GameResult };

const initial: State = {
  loaded: false, gameId: "", mySeat: -1, seats: [], config: null, phase: "LOBBY", round: 0,
  deadline: 0, skew: 0, phaseStartedLocal: 0, questions: [], reveals: [], mine: [], myVote: null, result: null,
};

function reducer(s: State, a: Action): State {
  switch (a.t) {
    case "snapshot": {
      const p = a.p;
      return {
        loaded: true, gameId: p.game_id, mySeat: p.my_seat, seats: p.seats, config: p.config,
        phase: p.result ? "RESULT" : p.phase, round: p.round, deadline: p.deadline_ts, skew: p.server_ts - Date.now(),
        phaseStartedLocal: Date.now(), questions: p.questions ?? [], reveals: p.reveals ?? [], mine: p.mine ?? [],
        myVote: p.my_vote ?? null, result: p.result ?? null,
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
      return { ...s, mine: [...s.mine, a.p] };
    case "vote":
      return { ...s, myVote: a.target };
    case "result":
      if (!s.loaded) return s;
      return { ...s, result: a.p, phase: "RESULT", deadline: 0 };
  }
}

const PHASES: Record<Phase, { title: string; hint: string }> = {
  LOBBY: { title: "Sitting down", hint: "" },
  INTRO: { title: "Introductions", hint: "Say hi in 160 characters. Optional." },
  ASK: { title: "Ask the table", hint: "One question for everyone. 160 characters. Optional." },
  ANSWER: { title: "Answer the table", hint: "Answer each question. Skip any. 200 characters." },
  REVEAL: { title: "Answers are in", hint: "Everyone sees these at the same time." },
  VOTE: { title: "Who is the human?", hint: "Pick one seat. Locked once cast." },
  RESULT: { title: "Game over", hint: "Seats are revealed." },
};

function phaseSeconds(c: PublicConfig | null, p: Phase): number {
  if (!c) return 0;
  return ({ INTRO: c.t_intro, ASK: c.t_ask, ANSWER: c.t_answer, REVEAL: c.t_reveal, VOTE: c.t_vote } as Record<string, number>)[p] ?? 0;
}

function useNow(ms: number) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(id);
  }, [ms]);
  return now;
}

function Game() {
  const router = useRouter();
  const id = useSearchParams().get("id") ?? "";
  const [s, dispatch] = useReducer(reducer, initial);
  const [conn, setConn] = useState<ConnState>("connecting");
  const [notice, setNotice] = useState<string | null>(null);
  const [gone, setGone] = useState(false);

  useEffect(() => {
    if (!id) {
      setGone(true);
      return;
    }
    let alive = true;
    const ws = socket();
    const offs: (() => void)[] = [];
    api
      .me()
      .then(() => {
        if (!alive) return;
        offs.push(
          ws.onState(setConn),
          ws.on("game.snapshot", (p: Snapshot) => p.game_id === id && dispatch({ t: "snapshot", p })),
          ws.on("phase.start", (p: PhaseStart) => dispatch({ t: "phase", p })),
          ws.on("phase.reveal", (p: PhaseReveal) => dispatch({ t: "reveal", p })),
          ws.on("submit.ack", (p: SubmitAck) => dispatch({ t: "ack", p })),
          ws.on("vote.ack", (p: { target_seat: number }) => dispatch({ t: "vote", target: p.target_seat })),
          ws.on("game.result", (p: GameResult) => p.game_id === id && dispatch({ t: "result", p })),
          ws.on("error", (p: WsError) => {
            if (p.code === "game_not_found") setGone(true);
            else setNotice(p.message);
          }),
          ws.onOpen(() => ws.send("game.resync", { game_id: id })),
        );
        ws.connect();
      })
      .catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 401) router.replace("/login");
        else setNotice(e instanceof Error ? e.message : "Could not reach the server");
      });
    return () => {
      alive = false;
      offs.forEach((off) => off());
    };
  }, [id, router]);

  useEffect(() => {
    if (!notice) return;
    const t = setTimeout(() => setNotice(null), 4000);
    return () => clearTimeout(t);
  }, [notice]);

  if (gone) {
    return (
      <main className="wrap">
        <div className="px logo">Find the Human</div>
        <Disclosure />
        <section className="panel ready">
          <h2 style={{ marginBottom: 0 }}>Table closed</h2>
          <p>That game is over or does not exist.</p>
          <button className="big-btn" onClick={() => router.push("/")}>Back to the lobby</button>
        </section>
      </main>
    );
  }

  return (
    <main className="wrap game">
      <div className="topbar">
        <div className="px logo">Find the Human</div>
        {conn !== "open" && <span className="px conn">Reconnecting…</span>}
      </div>
      <Disclosure />
      {!s.loaded ? (
        <p className="muted">Joining the table…</p>
      ) : (
        <>
          <PhaseBanner s={s} />
          <SeatRow s={s} />
          {notice && <div className="error" role="alert">{notice}</div>}
          {s.phase === "RESULT" && s.result ? <ResultView s={s} result={s.result} /> : <Composer s={s} onNotice={setNotice} />}
          <Transcript s={s} />
        </>
      )}
    </main>
  );
}

function PhaseBanner({ s }: { s: State }) {
  const now = useNow(250);
  const left = Math.max(0, s.deadline - (now + s.skew));
  const total = phaseSeconds(s.config, s.phase) * 1000 || left || 1;
  const info = PHASES[s.phase];
  const roundLabel = s.phase === "ASK" || s.phase === "ANSWER" || s.phase === "REVEAL" ? ` · Round ${s.round} of ${s.config?.rounds ?? "?"}` : "";
  return (
    <section className="sign">
      <div className="px sign-title">{info.title}{roundLabel}</div>
      <p className="hint">{info.hint}</p>
      {s.phase !== "RESULT" && (
        <>
          <div className="px timer" aria-live="off">{Math.ceil(left / 1000)}s</div>
          <div className="bar"><i style={{ width: `${Math.min(100, (left / total) * 100)}%` }} /></div>
        </>
      )}
    </section>
  );
}

function SeatRow({ s }: { s: State }) {
  return (
    <div className="seats-row">
      {s.seats.map((seat) => (
        <div key={seat.seat} className={`seat${seat.seat === s.mySeat ? " me" : ""}`}>
          <Sprite seed={seat.sprite_seed} size={48} />
          <div className="alias">{seat.alias}</div>
          {seat.seat === s.mySeat && <span className="tag you">You</span>}
        </div>
      ))}
    </div>
  );
}

function aliasOf(s: State, seat: number) {
  return s.seats.find((x) => x.seat === seat)?.alias ?? `Seat ${seat + 1}`;
}

function send(type: string, payload: object, onNotice: (m: string) => void) {
  if (!socket().send(type, payload)) onNotice("Not connected. Reconnecting…");
}

function Composer({ s, onNotice }: { s: State; onNotice: (m: string) => void }) {
  const c = s.config;
  const ackFor = (qid?: string) => s.mine.find((m) => (m.question_id ?? "") === (qid ?? ""));
  const key = `${s.phase}-${s.round}`;

  if (s.phase === "INTRO" || s.phase === "ASK") {
    const kind = s.phase === "INTRO" ? "intro" : "question";
    return (
      <section className="panel composer">
        <div className="kick">{s.phase === "INTRO" ? "Your intro" : "Your question to the table"}</div>
        <TextSlot
          key={key} max={s.phase === "INTRO" ? c?.max_intro ?? 160 : c?.max_ask ?? 160} ack={ackFor()} startedAt={s.phaseStartedLocal}
          placeholder={s.phase === "INTRO" ? "say hi…" : "ask something only a human would answer well…"}
          onSend={(text, t) => send("game.submit", { game_id: s.gameId, phase: s.phase, round: s.round, kind, text, ...t }, onNotice)}
          onSkip={() => send("game.skip", { game_id: s.gameId, phase: s.phase, round: s.round, kind }, onNotice)}
        />
      </section>
    );
  }

  if (s.phase === "ANSWER") {
    const qs = s.questions.filter((q) => q.seat !== s.mySeat);
    return (
      <section className="panel composer">
        <div className="kick">Answer the table</div>
        {qs.length === 0 && <p className="hint">No questions for you this round. Wait for the reveal.</p>}
        {qs.map((q) => (
          <div key={q.id} className="answer-slot">
            <div className="qq"><b>{aliasOf(s, q.seat)} asks:</b> {q.text}</div>
            <TextSlot
              key={`${key}-${q.id}`} max={c?.max_answer ?? 200} ack={ackFor(q.id)} startedAt={s.phaseStartedLocal} placeholder="your answer…"
              onSend={(text, t) => send("game.submit", { game_id: s.gameId, phase: s.phase, round: s.round, kind: "answer", question_id: q.id, text, ...t }, onNotice)}
              onSkip={() => send("game.skip", { game_id: s.gameId, phase: s.phase, round: s.round, kind: "answer", question_id: q.id }, onNotice)}
            />
          </div>
        ))}
      </section>
    );
  }

  if (s.phase === "REVEAL") {
    return (
      <section className="panel composer">
        <div className="kick">Round {s.round} answers are on the table</div>
        <p className="hint">Read them below. {s.round < (c?.rounds ?? 0) ? "The next round starts soon." : "Voting starts soon."}</p>
      </section>
    );
  }

  if (s.phase === "VOTE") return <VotePanel s={s} onNotice={onNotice} />;
  return null;
}

type Telemetry = { first_keystroke_ms?: number; edit_count?: number };

function TextSlot(props: {
  max: number; ack?: SubmitAck; startedAt: number; placeholder: string;
  onSend: (text: string, t: Telemetry) => void; onSkip: () => void;
}) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const first = useRef<number | undefined>(undefined);
  const edits = useRef(0);
  const len = [...text.trim()].length;

  useEffect(() => {
    if (busy) {
      const t = setTimeout(() => setBusy(false), 3000); // re-enable if no ack arrives (e.g. rejected)
      return () => clearTimeout(t);
    }
  }, [busy]);

  if (props.ack) {
    return <p className="sent">{props.ack.skipped ? "Skipped." : "Sent ✓ Sealed until the phase ends."}</p>;
  }
  return (
    <div>
      <textarea
        className="ta" value={text} maxLength={props.max * 2} placeholder={props.placeholder} rows={3}
        onChange={(e) => {
          const v = e.target.value;
          if (first.current === undefined) first.current = Math.max(0, Date.now() - props.startedAt);
          if (v.length < text.length) edits.current++;
          setText(v);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            if (len > 0 && len <= props.max && !busy) {
              setBusy(true);
              props.onSend(text.trim(), { first_keystroke_ms: first.current, edit_count: edits.current });
            }
          }
        }}
      />
      <div className="row">
        <span className={`count${len > props.max ? " over" : ""}`}>{len} / {props.max}</span>
        <span className="grow" />
        <button className="btn ghost" disabled={busy} onClick={() => { setBusy(true); props.onSkip(); }}>Skip</button>
        <button
          className="btn" disabled={busy || len === 0 || len > props.max}
          onClick={() => { setBusy(true); props.onSend(text.trim(), { first_keystroke_ms: first.current, edit_count: edits.current }); }}
        >
          Send
        </button>
      </div>
    </div>
  );
}

function VotePanel({ s, onNotice }: { s: State; onNotice: (m: string) => void }) {
  const [pick, setPick] = useState<number | null>(null);
  if (s.myVote !== null) {
    return (
      <section className="panel composer">
        <div className="kick">Vote locked</div>
        <p className="hint">You picked <b>{aliasOf(s, s.myVote)}</b>. Waiting for the result.</p>
      </section>
    );
  }
  return (
    <section className="panel composer">
      <div className="kick">Who is the other human?</div>
      <div className="vote-grid">
        {s.seats.filter((x) => x.seat !== s.mySeat).map((x) => (
          <button key={x.seat} className={`seat selectable${pick === x.seat ? " picked" : ""}`} onClick={() => setPick(x.seat)} aria-pressed={pick === x.seat}>
            <Sprite seed={x.sprite_seed} size={48} />
            <div className="alias">{x.alias}</div>
          </button>
        ))}
      </div>
      <div className="row">
        <span className="grow" />
        <button className="btn" disabled={pick === null} onClick={() => pick !== null && send("vote.cast", { game_id: s.gameId, target_seat: pick }, onNotice)}>
          {pick === null ? "Pick a seat" : `Vote for ${aliasOf(s, pick)}`}
        </button>
      </div>
    </section>
  );
}

function ResultView({ s, result }: { s: State; result: GameResult }) {
  const router = useRouter();
  const me = result.results.find((r) => r.seat === s.mySeat);
  const kindOf = (seat: number) => result.seats.find((x) => x.seat === seat)?.kind;
  const verdict = !me
    ? { cls: "wrong", title: "Game over", text: "" }
    : me.mutual
      ? { cls: "mutual", title: "You found each other ✓", text: "Mutual match. Best possible outcome." }
      : me.found_other
        ? { cls: "wrong", title: "You found the human", text: "They didn't pick you, though." }
        : me.was_found
          ? { cls: "wrong", title: "You were found", text: "The other human picked you, but you missed them." }
          : { cls: "missed", title: "Fooled!", text: "Neither of you found the other." };
  const label = (k?: string) => (k === "ai" ? "AI" : k === "bot_human" ? "Bot human" : "Human");
  return (
    <>
      <section className={`verdict ${verdict.cls}`}>
        <h1 className="px">{verdict.title}</h1>
        {verdict.text && <p>{verdict.text}</p>}
        <div className="px points">{me?.points ?? 0} <small>pts</small></div>
      </section>
      <h2 className="px sec">The table, unmasked</h2>
      <div className="seats-row">
        {result.seats.map((x) => {
          const r = result.results.find((y) => y.seat === x.seat);
          return (
            <div key={x.seat} className={`seat${x.seat === s.mySeat ? " me" : ""}`}>
              <Sprite seed={x.sprite_seed} size={48} />
              <div className="alias">{x.alias}</div>
              <span className={`tag ${x.kind === "ai" ? "ai" : "human"}`}>{label(x.kind)}</span>
              {x.model_id && <div className="model">{x.model_id}</div>}
              {r?.votes_stolen ? <div className="model">stole {r.votes_stolen} vote{r.votes_stolen > 1 ? "s" : ""}</div> : null}
            </div>
          );
        })}
      </div>
      <section className="panel">
        <h2>Votes</h2>
        {result.votes.length === 0 && <p className="hint">No votes were cast.</p>}
        <ul className="votes">
          {result.votes.map((v) => (
            <li key={v.voter}>
              <b>{aliasOf(s, v.voter)}</b> → <b>{aliasOf(s, v.target)}</b>
              <span className="muted-ink"> ({label(kindOf(v.voter))} → {label(kindOf(v.target))})</span>
            </li>
          ))}
        </ul>
      </section>
      <div className="actions">
        <button className="btn ghost" onClick={() => router.push("/")}>Lobby</button>
        <button className="btn" onClick={() => router.push("/match")}>Play again</button>
      </div>
    </>
  );
}

function Transcript({ s }: { s: State }) {
  const intro = s.reveals.find((r) => r.phase === "INTRO");
  const rounds = useMemo(() => {
    const out: { round: number; asks?: PhaseReveal; answers?: PhaseReveal }[] = [];
    for (let r = 1; r <= (s.config?.rounds ?? 0); r++) {
      const asks = s.reveals.find((x) => x.phase === "ASK" && x.round === r);
      if (!asks) continue;
      out.push({ round: r, asks, answers: s.reveals.find((x) => x.phase === "ANSWER" && x.round === r) });
    }
    return out;
  }, [s.reveals, s.config]);

  if (!intro) return null;
  return (
    <section className="panel transcript">
      <h2>Transcript</h2>
      <div className="tagline px">Intros</div>
      {intro.items.map((it) => (
        <div key={it.id} className={`ent intro${it.text === null ? " skip" : ""}`}>
          <span className="who">{aliasOf(s, it.seat)}</span>
          <span className="say">{it.text ?? "—"}</span>
        </div>
      ))}
      {rounds.map(({ round, asks, answers }) => (
        <div key={round}>
          <div className="tagline px">Round {round}</div>
          {asks!.items.map((q) =>
            q.text === null ? (
              <div key={q.id} className="ent skip"><span className="who">{aliasOf(s, q.seat)}</span><span className="say">— no question</span></div>
            ) : (
              <div key={q.id}>
                <div className="ent q">
                  <span className="who">{aliasOf(s, q.seat)} asks</span>
                  <span className="say">{q.text}</span>
                </div>
                {answers ? (
                  answers.items.filter((a) => a.question_id === q.id).map((a) => (
                    <div key={a.id} className={`ent a${a.text === null ? " skip" : ""}`}>
                      <span className="who">{aliasOf(s, a.seat)}</span>
                      <span className="say">{a.text ?? "—"}</span>
                    </div>
                  ))
                ) : (
                  <div className="ent a skip"><span className="say">answers sealed until the phase ends</span></div>
                )}
              </div>
            ),
          )}
        </div>
      ))}
    </section>
  );
}
