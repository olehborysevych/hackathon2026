"use client";

import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { Phase, PublicSeat, Question, ResultSeat, SubmitAck } from "@/lib/ws";
import { Sprite } from "../components";
import {
  aliasOf, answerOf, markToStop, PHASES, phaseSeconds, questionsFor, revealOf, slotKey, STOPS, stopToMark,
  type NoteState, type State, type View,
} from "./model";

// Table screen (design/game-room/game-screen.html). Desktop: a 900px stage scaled to the page,
// four players round the table, you at the bottom centre with your composer as your speech box.
// Narrow screens: the same elements reflow into a 2-column grid (CSS only, see game.css).

export type Telemetry = { first_keystroke_ms?: number; edit_count?: number };

export type TableProps = {
  s: State;
  view: View;
  live: boolean;
  viewIdx: number;
  viewCount: number;
  onView: (delta: number) => void;
  onLive: () => void;
  qIdx: number;
  onQ: (i: number) => void;
  drafts: Record<string, string>;
  onDraft: (key: string, text: string) => void;
  sent: Record<string, string>;
  onSubmit: (phase: Phase, round: number, kind: "intro" | "question" | "answer", text: string, qid: string | undefined, t: Telemetry) => void;
  onSkip: (phase: Phase, round: number, kind: "intro" | "question" | "answer", qid: string | undefined) => void;
  pick: number | null;
  onPick: (seat: number) => void;
  onVote: () => void;
  onNote: (target: number, n: NoteState) => void;
};

// Positions on the 900-wide stage: seat anchors are centres [x, y, scale]; speech boxes are
// anchored at their bottom centre [x, bottom, width] so long text grows upwards, away from the seat.
const SEAT_POS = [
  [118, 545, 1.0], // left
  [328, 300, 0.82], // back left
  [572, 300, 0.82], // back right
  [782, 545, 1.0], // right
] as const;
const BUB_POS = [
  [112, 425, 210],
  [318, 212, 262],
  [582, 212, 262],
  [788, 425, 210],
] as const;
const ME_POS = [450, 715, 1.12] as const;

export function Table(p: TableProps) {
  const { s, view } = p;
  const others = othersInOrder(s.seats, s.mySeat);
  const qs = view.phase === "ANSWER" || view.phase === "REVEAL" ? questionsFor(s, view.round) : [];
  const q: Question | undefined = qs[Math.min(p.qIdx, qs.length - 1)];
  const unmasked = view.phase === "RESULT" && s.result ? s.result.seats : null;

  return (
    <StageWrap>
      <Scene />
      <div className="table" data-phase={view.phase}>
        <Centre {...p} qs={qs} q={q} />
      </div>

      {others.map((seat, i) => {
        const [x, y, k] = SEAT_POS[i] ?? SEAT_POS[0];
        const [bx, by, bw] = BUB_POS[i] ?? BUB_POS[0];
        return (
          <div className="pgroup" key={seat.seat}>
            <div className="seat-at" style={{ left: x, top: y, ["--k" as string]: k }}>
              <Player {...p} seat={seat} unmasked={unmasked} />
            </div>
            <Bubble s={s} view={view} live={p.live} seat={seat.seat} q={q} style={{ left: bx, top: by, width: bw }} />
          </div>
        );
      })}

      <div className="me-group">
        <div className="seat-at" style={{ left: ME_POS[0], top: ME_POS[1], ["--k" as string]: ME_POS[2] }}>
          <Player {...p} seat={s.seats.find((x) => x.seat === s.mySeat)!} unmasked={unmasked} />
        </div>
        <div className={`bub me-box tail-t${p.live ? "" : " review"}`}>
          {q && qs.length > 1 && (
            // Phones: the table's ◀ ▶ is a long scroll away, so repeat it next to the composer.
            <div className="mq-nav">
              <button className="qbtn" onClick={() => p.onQ((qs.indexOf(q) - 1 + qs.length) % qs.length)} aria-label="Previous question">◀</button>
              <span className="px">Q {qs.indexOf(q) + 1} / {qs.length} · {aliasOf(s, q.seat)}</span>
              <button className="qbtn" onClick={() => p.onQ((qs.indexOf(q) + 1) % qs.length)} aria-label="Next question">▶</button>
            </div>
          )}
          <Composer {...p} q={q} />
        </div>
      </div>

      <PhaseNav {...p} />
    </StageWrap>
  );
}

// othersInOrder seats the other players clockwise from your left, starting after your seat.
function othersInOrder(seats: PublicSeat[], me: number): PublicSeat[] {
  const sorted = [...seats].sort((a, b) => a.seat - b.seat);
  const i = sorted.findIndex((x) => x.seat === me);
  return [...sorted.slice(i + 1), ...sorted.slice(0, Math.max(0, i))];
}

// StageWrap scales the fixed-size stage to the page width and sets the wrapper's height to fit.
function StageWrap({ children }: { children: React.ReactNode }) {
  const wrap = useRef<HTMLDivElement>(null);
  const stage = useRef<HTMLElement>(null);
  useLayoutEffect(() => {
    const w = wrap.current, st = stage.current;
    if (!w || !st) return;
    const fit = () => {
      const desktop = window.matchMedia("(min-width: 720px)").matches;
      if (!desktop) {
        w.style.removeProperty("--s");
        w.style.height = "";
        return;
      }
      const sc = Math.min(1, w.clientWidth / 900);
      let bottom = 0;
      st.querySelectorAll<HTMLElement>(".me-box, .pnav").forEach((el) => (bottom = Math.max(bottom, el.offsetTop + el.offsetHeight)));
      w.style.setProperty("--s", String(sc));
      w.style.height = `${(bottom + 24) * sc}px`;
      st.style.height = `${bottom + 24}px`;
    };
    fit();
    const ro = new ResizeObserver(fit);
    ro.observe(w);
    st.querySelectorAll(".me-box, .pnav").forEach((el) => ro.observe(el));
    window.addEventListener("resize", fit);
    return () => {
      ro.disconnect();
      window.removeEventListener("resize", fit);
    };
  });
  return (
    <div className="stage-wrap" ref={wrap}>
      <section className="stage" ref={stage}>{children}</section>
    </div>
  );
}

function Scene() {
  return (
    <div className="scene" aria-hidden="true">
      <div className="back">
        {Array.from({ length: 15 }, (_, k) => <i key={k} style={{ left: `${(k / 15) * 100}%` }} />)}
        <div className="lantern" style={{ left: 50 }} />
        <div className="lantern" style={{ right: 50 }} />
      </div>
      <div className="floor">
        {Array.from({ length: 10 }, (_, k) => <i key={k} style={{ top: `${Math.pow((k + 1) / 11, 1.5) * 100}%` }} />)}
      </div>
    </div>
  );
}

// ---- countdown ----

export function useNow(ms: number) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(id);
  }, [ms]);
  return now;
}

export function Countdown({ s, bar }: { s: State; bar?: boolean }) {
  const now = useNow(250);
  if (!s.deadline || s.phase === "RESULT") return null;
  const left = Math.max(0, s.deadline - (now + s.skew));
  const total = phaseSeconds(s.config, s.phase) * 1000 || left || 1;
  const sec = Math.ceil(left / 1000);
  const txt = `${Math.floor(sec / 60)}:${String(sec % 60).padStart(2, "0")}`;
  return (
    <>
      <div className={`px timer${sec <= 5 ? " low" : ""}`} aria-label={`${sec} seconds left`}>{txt}</div>
      {bar && <div className="bar"><i style={{ width: `${Math.min(100, (left / total) * 100)}%` }} /></div>}
    </>
  );
}

// ---- table centre ----

function Centre(p: TableProps & { qs: Question[]; q?: Question }) {
  const { s, view, live, qs, q } = p;
  const info = PHASES[view.phase];
  if (view.phase === "ANSWER" || view.phase === "REVEAL") {
    if (!q) {
      return (
        <>
          <div className="px">{info.title}</div>
          <p className="qtext">Nobody asked a question this round.</p>
          {live && <Countdown s={s} />}
        </>
      );
    }
    const i = qs.indexOf(q);
    return (
      <>
        <div className="px asked">Asked by · {aliasOf(s, q.seat)}{q.seat === s.mySeat ? " (you)" : ""}</div>
        <p className="qtext">“{q.text}”</p>
        <div className="qnav">
          <button className="qbtn" onClick={() => p.onQ((i - 1 + qs.length) % qs.length)} disabled={qs.length < 2} aria-label="Previous question">◀</button>
          <span className="px">Q {i + 1} / {qs.length}</span>
          <button className="qbtn" onClick={() => p.onQ((i + 1) % qs.length)} disabled={qs.length < 2} aria-label="Next question">▶</button>
        </div>
        {live && <Countdown s={s} />}
      </>
    );
  }
  return (
    <>
      <div className="px">{info.title}</div>
      <p className="qtext">{live ? info.hint : "Looking back. This phase is over."}</p>
      {live && <Countdown s={s} bar />}
    </>
  );
}

// ---- players ----

function Player(p: TableProps & { seat: PublicSeat; unmasked: ResultSeat[] | null }) {
  const { s, seat, view, live } = p;
  const me = seat.seat === s.mySeat;
  const rv = p.unmasked?.find((x) => x.seat === seat.seat);
  const votable = live && view.phase === "VOTE" && !me && s.myVote === null;
  const picked = view.phase === "VOTE" && (s.myVote ?? p.pick) === seat.seat;
  const cls = ["pl", me ? "me" : "", votable ? "votable" : "", picked ? "picked" : ""].filter(Boolean).join(" ");
  const medal = (
    <div className="medal">
      <Sprite seed={seat.sprite_seed} size={58} />
    </div>
  );
  return (
    <div className={cls}>
      {votable ? (
        <button className="medal-btn" onClick={() => p.onPick(seat.seat)} aria-pressed={picked} aria-label={`Vote for ${seat.alias}`}>
          {medal}
        </button>
      ) : medal}
      <div className="name">{seat.alias}</div>
      {rv ? (
        <span className={`pill ${rv.kind === "ai" ? "ai" : "human"}`}>
          {rv.kind === "ai" ? `AI · ${rv.model_id ?? "?"}` : rv.kind === "bot_human" ? "BOT HUMAN" : "HUMAN"}
        </span>
      ) : me ? (
        <span className="pill you">You</span>
      ) : picked ? (
        <span className="pill vote">{s.myVote !== null ? "Your vote" : "Picked"}</span>
      ) : null}
      {!me && !rv && s.phase !== "RESULT" && (
        <Slider alias={seat.alias} n={s.notes[seat.seat] ?? { mark: "none", note: "" }} onChange={(n) => p.onNote(seat.seat, n)} max={60} />
      )}
    </div>
  );
}

// Slider is the 5-stop suspicion control (DECISIONS 4); each stop is one SPEC §4 mark (DECISIONS 76).
function Slider({ alias, n, onChange, max }: { alias: string; n: NoteState; onChange: (n: NoteState) => void; max: number }) {
  const v = markToStop(n.mark);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(n.note);
  const step = (d: number) => {
    const nv = Math.max(0, Math.min(4, v + d));
    if (nv !== v) onChange({ mark: stopToMark(nv), note: n.note });
  };
  const save = () => {
    const t = draft.trim().slice(0, max);
    if (t !== n.note) onChange({ mark: n.mark, note: t });
    setEditing(false);
  };
  return (
    <div className="sl-holder">
      <div className="sl">
        <button className="sl-btn" onClick={() => step(-1)} disabled={v === 0} aria-label={`Move ${alias} toward AI`}>◀</button>
        <div className="track" role="meter" aria-valuemin={0} aria-valuemax={4} aria-valuenow={v} aria-valuetext={STOPS[v]} aria-label={`Your read on ${alias}`}>
          {[0, 25, 50, 75, 100].map((x) => <i key={x} style={{ left: `${x}%` }} />)}
          <span className="knob" style={{ left: `${v * 25}%` }} />
        </div>
        <button className="sl-btn" onClick={() => step(1)} disabled={v === 4} aria-label={`Move ${alias} toward human`}>▶</button>
      </div>
      <div className="sl-scale px"><span>AI</span><span>Human</span></div>
      <div className="sl-state">{STOPS[v]}</div>
      {editing ? (
        <div className="note-edit">
          <input
            className="note-in" value={draft} maxLength={max} autoFocus placeholder="private note…" aria-label={`Private note on ${alias}`}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") save();
              if (e.key === "Escape") { setDraft(n.note); setEditing(false); }
            }}
            onBlur={save}
          />
          <span className="px note-count">{[...draft].length}/{max}</span>
        </div>
      ) : (
        <button className="note-btn" onClick={() => { setDraft(n.note); setEditing(true); }} aria-label={`Edit private note on ${alias}`}>
          {n.note ? `“${n.note}”` : "+ note"}
        </button>
      )}
    </div>
  );
}

// ---- speech boxes ----

type BubbleContent = { cls: string; label: string; text: string };

function bubbleFor(s: State, view: View, live: boolean, seat: number, q?: Question): BubbleContent {
  const alias = aliasOf(s, seat);
  const sealed = { cls: "empty", label: alias, text: "sealed" };
  const blank = { cls: "empty", label: alias, text: "" };
  const fromItem = (text: string | null | undefined, label: string) =>
    text ? { cls: "", label: `${alias} · ${label}`, text } : { cls: "skip", label: `${alias} · ${label}`, text: "—" };
  const intro = () => {
    const r = revealOf(s, "INTRO", 0);
    return r ? fromItem(r.items.find((i) => i.seat === seat)?.text, "intro") : blank;
  };
  switch (view.phase) {
    case "INTRO":
      return live ? sealed : intro();
    case "ASK": {
      if (live) return intro();
      const r = revealOf(s, "ASK", view.round);
      return r ? fromItem(r.items.find((i) => i.seat === seat)?.text, "asks") : blank;
    }
    case "ANSWER":
    case "REVEAL": {
      if (!q) return blank;
      if (q.seat === seat) return { cls: "q asking", label: `${alias} · asks the table`, text: q.text };
      if (view.phase === "ANSWER") return live ? sealed : blank;
      const a = answerOf(s, view.round, q.id, seat);
      return a ? fromItem(a.text, "answers") : blank;
    }
    default:
      return intro();
  }
}

function Bubble({ s, view, live, seat, q, style }: { s: State; view: View; live: boolean; seat: number; q?: Question; style: React.CSSProperties }) {
  const b = bubbleFor(s, view, live, seat, q);
  return (
    <div className={`bub tail-b ${b.cls}`} style={style}>
      <span className="who">{b.label}</span>
      {b.text && <span className="say">{b.text}</span>}
    </div>
  );
}

// ---- composer: your own speech box ----

function Composer(p: TableProps & { q?: Question }) {
  const { s, view, live, q } = p;
  const c = s.config;
  if (!live) return <ReviewBox {...p} />;

  const ack = (qid?: string) => s.mine.find((m) => (m.question_id ?? "") === (qid ?? ""));
  if (view.phase === "INTRO" || view.phase === "ASK") {
    const intro = view.phase === "INTRO";
    return (
      <TextSlot
        {...p} key={slotKey(view.phase, view.round)}
        label={intro ? "Your intro" : "Your question for the table"}
        placeholder={intro ? "say hi…" : "ask something only a human would answer well…"}
        max={intro ? c?.max_intro ?? 160 : c?.max_ask ?? 160}
        kind={intro ? "intro" : "question"} ack={ack()}
      />
    );
  }
  if (view.phase === "ANSWER") {
    if (!q) return <Info title="No questions" hint="Nobody asked anything this round. Wait for the reveal." />;
    if (q.seat === s.mySeat) {
      return <Info title="Your question" quote={q.text} hint="The others are answering it. Use ◀ ▶ on the table to answer theirs." />;
    }
    return (
      <TextSlot
        {...p} key={slotKey(view.phase, view.round, q.id)} label={`Answering ${aliasOf(s, q.seat)}`} quote={q.text}
        placeholder="your answer…" max={c?.max_answer ?? 200} kind="answer" qid={q.id} ack={ack(q.id)}
      />
    );
  }
  if (view.phase === "REVEAL") return <MyAnswerInfo {...p} />;
  if (view.phase === "VOTE") {
    if (s.myVote !== null) {
      return <Info title="Vote locked" hint={`You picked ${aliasOf(s, s.myVote)}. Seats are revealed when voting ends.`} />;
    }
    return (
      <>
        <div className="kick">Pick the human</div>
        <p className="hint">{p.pick === null ? "Click a seat at the table. Locked once cast." : `You picked ${aliasOf(s, p.pick)}. Locked once cast.`}</p>
        <div className="row">
          <span className="grow" />
          <button className="btn" disabled={p.pick === null} onClick={p.onVote}>
            {p.pick === null ? "Confirm vote" : `Confirm: ${aliasOf(s, p.pick)}`}
          </button>
        </div>
      </>
    );
  }
  return <Info title="Game over" hint="Seats are revealed. Step back with ▲ to read the game again." />;
}

function Info({ title, hint, quote, kick }: { title: string; hint?: string; quote?: string; kick?: string }) {
  return (
    <>
      {kick && <div className="px review-tag">{kick}</div>}
      <div className="kick">{title}</div>
      {quote !== undefined && <blockquote className={`qq${quote === "—" ? " skip" : ""}`}>{quote === "—" ? "—" : `“${quote}”`}</blockquote>}
      {hint && <p className="hint">{hint}</p>}
    </>
  );
}

function MyAnswerInfo(p: TableProps & { q?: Question }) {
  const { s, view, q } = p;
  if (!q) return <Info title="No questions" hint="Nobody asked anything this round." />;
  if (q.seat === s.mySeat) return <Info title="Your question" quote={q.text} hint="Read what the others said in their speech boxes." />;
  const a = answerOf(s, view.round, q.id, s.mySeat);
  return <Info title={`Your answer to ${aliasOf(s, q.seat)}`} quote={a?.text ?? "—"} hint={a?.text ? undefined : "You skipped this one."} />;
}

// ReviewBox is the composer while you look back at a past phase: read-only, clearly not live (DECISIONS 6, 77).
function ReviewBox(p: TableProps & { q?: Question }) {
  const { s, view, q } = p;
  const kick = `Looking back · ${view.phase} · read-only`;
  const mine = (phase: Phase, round: number) => revealOf(s, phase, round)?.items.find((i) => i.seat === s.mySeat)?.text ?? "—";
  let body: React.ReactNode;
  switch (view.phase) {
    case "INTRO":
      body = <Info kick={kick} title="Your intro" quote={mine("INTRO", 0)} />;
      break;
    case "ASK":
      body = <Info kick={kick} title="Your question" quote={mine("ASK", view.round)} />;
      break;
    case "ANSWER":
    case "REVEAL":
      if (!q) body = <Info kick={kick} title="No questions" hint="Nobody asked anything this round." />;
      else if (q.seat === s.mySeat) body = <Info kick={kick} title="Your question" quote={q.text} />;
      else body = <Info kick={kick} title={`Your answer to ${aliasOf(s, q.seat)}`} quote={answerOf(s, view.round, q.id, s.mySeat)?.text ?? "—"} />;
      break;
    case "VOTE":
      body = <Info kick={kick} title="Your vote" hint={s.myVote !== null ? aliasOf(s, s.myVote) : "You did not vote."} />;
      break;
    default:
      body = <Info kick={kick} title="Game over" />;
  }
  return (
    <>
      {body}
      <div className="row">
        <span className="grow" />
        <button className="btn" onClick={p.onLive}>Back to {s.phase === "RESULT" ? "the end" : `live · ${s.phase}`} ▼</button>
      </div>
    </>
  );
}

function TextSlot(p: TableProps & {
  label: string; quote?: string; placeholder: string; max: number; kind: "intro" | "question" | "answer"; qid?: string; ack?: SubmitAck;
}) {
  const { s, view } = p;
  const key = slotKey(view.phase, view.round, p.qid);
  const text = p.drafts[key] ?? "";
  const [busy, setBusy] = useState(false);
  const first = useRef<number | undefined>(undefined);
  const edits = useRef(0);
  const len = [...text.trim()].length;

  useEffect(() => {
    if (!busy) return;
    const t = setTimeout(() => setBusy(false), 3000); // re-enable if no ack arrives (e.g. rejected)
    return () => clearTimeout(t);
  }, [busy]);

  const head = (
    <>
      <div className="kick">{p.label}</div>
      {p.quote && <blockquote className="qq">“{p.quote}”</blockquote>}
    </>
  );
  if (p.ack) {
    const said = p.sent[key];
    return (
      <>
        {head}
        {p.ack.skipped ? <p className="sent skip">Skipped. Shown as “—” at the reveal.</p> : (
          <>
            {said && <p className="said">{said}</p>}
            <p className="sent">Sent ✓ Sealed until the phase ends.</p>
          </>
        )}
      </>
    );
  }
  // An empty Send is a skip (no separate Skip button). Enter only sends text, so a stray
  // keypress in an empty box never skips.
  const send = () => {
    if (len > p.max || busy) return;
    setBusy(true);
    if (len === 0) {
      p.onSkip(view.phase, view.round, p.kind, p.qid);
      return;
    }
    p.onSubmit(view.phase, view.round, p.kind, text.trim(), p.qid, { first_keystroke_ms: first.current, edit_count: edits.current });
  };
  return (
    <>
      {head}
      <textarea
        className="ta" value={text} maxLength={p.max} placeholder={p.placeholder} rows={3} aria-label={p.label}
        onChange={(e) => {
          const v = e.target.value;
          if (first.current === undefined) first.current = Math.max(0, Date.now() - s.phaseStartedLocal);
          if (v.length < text.length) edits.current++;
          p.onDraft(key, v);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            if (len > 0) send();
          }
        }}
      />
      <div className="row">
        <span className={`count${len > p.max ? " over" : len > p.max - 20 ? " near" : ""}`}>{len}/{p.max}</span>
        <span className="grow" />
        <button className="btn" disabled={busy || len > p.max} onClick={send}>Send</button>
      </div>
    </>
  );
}

// ---- ▲ / ▼ phase review ----

function PhaseNav(p: TableProps) {
  const { view, viewIdx, viewCount, live } = p;
  return (
    <div className={`pnav${live ? "" : " review"}`}>
      <button className="pbtn" onClick={() => p.onView(-1)} disabled={viewIdx === 0} aria-label="Previous phase">▲</button>
      <div className="plabel">
        <span className="px">{live ? "Phase" : "Looking back"}</span>
        <b className="px">{view.phase}</b>
        <span className="px">{viewIdx + 1} / {viewCount}</span>
      </div>
      <button className="pbtn" onClick={() => p.onView(1)} disabled={viewIdx >= viewCount - 1} aria-label="Next phase">▼</button>
    </div>
  );
}
