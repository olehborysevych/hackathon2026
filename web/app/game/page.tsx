"use client";

import { Suspense, useCallback, useEffect, useReducer, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import {
  socket, type ConnState, type GameResult, type Phase, type PhaseReveal, type PhaseStart, type Snapshot, type SubmitAck, type WsError,
} from "@/lib/ws";
import { Disclosure } from "../components";
import { aliasOf, asMark, initial, PHASES, questionsFor, reducer, sameView, slotKey, views, type NoteState, type View } from "./model";
import { ResultView } from "./result";
import { Countdown, Table, type Telemetry } from "./table";
import { Transcript } from "./transcript";
import "./game.css";

// Table, vote and result screens. Static export ⇒ the game id comes from the query string
// (/game?id=…), not a route segment.
export default function GamePage() {
  return (
    <Suspense fallback={<main className="wrap"><p className="muted">Loading…</p></main>}>
      <Game />
    </Suspense>
  );
}

function Game() {
  const router = useRouter();
  const id = useSearchParams().get("id") ?? "";
  const [s, dispatch] = useReducer(reducer, initial);
  const [conn, setConn] = useState<ConnState>("connecting");
  const [notice, setNotice] = useState<string | null>(null);
  const [gone, setGone] = useState(false);

  // UI state: which phase is on screen (null = live), the question on the table, drafts, vote pick.
  const [reviewIdx, setReviewIdx] = useState<number | null>(null);
  const [qIdx, setQIdx] = useState(0);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [sent, setSent] = useState<Record<string, string>>({});
  const [pick, setPick] = useState<number | null>(null);
  const [showTable, setShowTable] = useState(false);
  const pending = useRef<Record<string, string>>({});

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
          ws.on("submit.ack", (p: SubmitAck) => {
            const k = slotKey(p.phase, p.round, p.question_id);
            if (!p.skipped && pending.current[k] !== undefined) setSent((x) => ({ ...x, [k]: pending.current[k] }));
            dispatch({ t: "ack", p });
          }),
          ws.on("vote.ack", (p: { target_seat: number }) => dispatch({ t: "vote", target: p.target_seat })),
          ws.on("note.ack", (p: { target_seat: number; mark: string; note?: string }) =>
            dispatch({ t: "note", target: p.target_seat, n: { mark: asMark(p.mark), note: p.note ?? "" } })),
          ws.on("game.result", (p: GameResult) => p.game_id === id && dispatch({ t: "result", p })),
          ws.on("error", (p: WsError) => {
            if (p.code === "game_not_found") setGone(true);
            else setNotice(p.message);
          }),
          // Reconnect / reload: ask for the full state and render from game.snapshot (SPEC §5).
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

  // A new phase always brings you back to the live view. In ANSWER, start on the first question
  // that isn't yours.
  const phaseKey = `${s.phase}-${s.round}`;
  useEffect(() => {
    setReviewIdx(null);
    setPick(null);
    if (s.phase === "ANSWER" || s.phase === "REVEAL") {
      const qs = questionsFor(s, s.round);
      const first = qs.findIndex((q) => q.seat !== s.mySeat);
      setQIdx(Math.max(0, first));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [phaseKey, s.loaded]);

  const send = useCallback((type: string, payload: object) => {
    if (!socket().send(type, payload)) setNotice("Not connected. Reconnecting…");
  }, []);

  const vs = views(s);
  const liveIdx = vs.length - 1;
  const idx = reviewIdx === null ? liveIdx : Math.min(reviewIdx, liveIdx);
  const view: View = vs[idx] ?? { phase: s.phase, round: s.round };
  const live = idx === liveIdx;

  const onView = (d: number) => {
    const n = Math.max(0, Math.min(liveIdx, idx + d));
    setReviewIdx(n === liveIdx ? null : n);
    const v = vs[n];
    if (v && (v.phase === "ANSWER" || v.phase === "REVEAL")) {
      const cur = vs[idx];
      if (!cur || cur.round !== v.round) setQIdx(0);
    }
  };

  const onSubmit = (phase: Phase, round: number, kind: string, text: string, qid: string | undefined, t: Telemetry) => {
    pending.current[slotKey(phase, round, qid)] = text;
    send("game.submit", { game_id: s.gameId, phase, round, kind, question_id: qid, text, ...t });
    nextQuestion(phase, round, qid);
  };
  // After answering or skipping, move to the next question you haven't answered yet.
  const nextQuestion = (phase: Phase, round: number, qid: string | undefined) => {
    if (phase === "ANSWER" && qid) {
      const qs = questionsFor(s, round);
      const done = new Set(s.mine.map((m) => m.question_id).concat(qid));
      const next = qs.findIndex((q, i) => i > qIdx && q.seat !== s.mySeat && !done.has(q.id));
      const wrap = next >= 0 ? next : qs.findIndex((q) => q.seat !== s.mySeat && !done.has(q.id));
      if (wrap >= 0) setTimeout(() => setQIdx(wrap), 900);
    }
  };
  const onSkip = (phase: Phase, round: number, kind: string, qid: string | undefined) => {
    send("game.skip", { game_id: s.gameId, phase, round, kind, question_id: qid });
    nextQuestion(phase, round, qid);
  };
  const onNote = (target: number, n: NoteState) => {
    dispatch({ t: "note", target, n }); // optimistic; note.ack confirms
    send("note.set", { game_id: s.gameId, target_seat: target, mark: n.mark, note: n.note || undefined });
  };
  const onVote = () => pick !== null && send("vote.cast", { game_id: s.gameId, target_seat: pick });

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

  const info = PHASES[view.phase];
  const roundLabel = s.config && s.round > 0 ? `Round ${s.round} / ${s.config.rounds} · ` : "";
  const resultScreen = s.phase === "RESULT" && s.result && !showTable;

  return (
    <main className="room">
      <header className="head">
        <div className="px logo">Find the Human</div>
        <div className="px head-r">
          {conn !== "open" && <span className="conn">Reconnecting…</span>}
          {s.loaded && <span>{roundLabel}{s.phase}</span>}
        </div>
      </header>

      {!s.loaded ? (
        <>
          <Disclosure />
          <p className="muted">Joining the table…</p>
        </>
      ) : resultScreen ? (
        <>
          <Disclosure />
          <ResultView s={s} result={s.result!} onTable={() => setShowTable(true)} />
        </>
      ) : (
        <>
          <div className="sign-wrap">
            <div className="ropes" aria-hidden="true"><i /><i /></div>
            <div className={`sign${live ? "" : " review"}`}>
              <div className="px">{live ? info.title : `Looking back · ${info.title}`}</div>
              <p className="hint">{live ? info.hint : "Read-only. Press ▼ or “Back to live” to return."}</p>
              {s.phase !== "RESULT" && <Countdown s={s} />}
            </div>
          </div>
          <Disclosure />
          {notice && <div className="error toast" role="alert">{notice}</div>}
          {s.phase === "RESULT" && (
            <div className="actions" style={{ marginTop: 12 }}>
              <button className="btn" onClick={() => setShowTable(false)}>Back to the result</button>
            </div>
          )}
          <Table
            s={s} view={view} live={live} viewIdx={idx} viewCount={vs.length} onView={onView} onLive={() => setReviewIdx(null)}
            qIdx={qIdx} onQ={setQIdx} drafts={drafts} onDraft={(k, t) => setDrafts((d) => ({ ...d, [k]: t }))} sent={sent}
            onSubmit={onSubmit} onSkip={onSkip} pick={pick} onPick={setPick} onVote={onVote} onNote={onNote}
          />
          <Transcript s={s} alias={(i) => aliasOf(s, i)} />
        </>
      )}
    </main>
  );
}
