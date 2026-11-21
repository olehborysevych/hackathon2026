"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import { socket, type ConnState, type GameStart, type QueueStatus, type Snapshot, type WsError } from "@/lib/ws";
import { Disclosure } from "../components";

// Matching screen (design/game-room/matching.html): FIFO queue until enough humans are waiting.
export default function MatchPage() {
  const router = useRouter();
  const [status, setStatus] = useState<QueueStatus | null>(null);
  const [conn, setConn] = useState<ConnState>("connecting");
  const [error, setError] = useState<string | null>(null);
  const seated = useRef(false);

  useEffect(() => {
    let alive = true;
    const s = socket();
    const offs: (() => void)[] = [];
    const go = (id: string) => {
      seated.current = true;
      router.push(`/game?id=${encodeURIComponent(id)}`);
    };

    api
      .me()
      .then(() => {
        if (!alive) return;
        offs.push(
          s.onState(setConn),
          s.on("queue.status", (p: QueueStatus) => setStatus(p)),
          s.on("game.start", (p: GameStart) => go(p.game_id)),
          s.on("game.snapshot", (p: Snapshot) => go(p.game_id)), // already seated at a table
          s.on("error", (p: WsError) => setError(p.message)),
          s.onOpen(() => s.send("queue.join")),
        );
        s.connect();
      })
      .catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 401) router.replace("/login");
        else setError(e instanceof Error ? e.message : "Could not reach the server");
      });

    return () => {
      alive = false;
      offs.forEach((off) => off());
      if (!seated.current) s.send("queue.leave");
    };
  }, [router]);

  const needed = status?.humans_needed ?? 2;
  const waiting = status?.queued ? status.humans_waiting : 0;
  const msg =
    conn !== "open" ? "Connecting to the tavern" : waiting >= needed ? "Human found! Filling the seats" : waiting > 0 ? "Waiting for another human" : "Joining the queue";

  return (
    <main className="wrap center">
      <div className="px logo" style={{ textAlign: "left" }}>Find the Human</div>
      <Disclosure />
      <section className="stage">
        <h1 className="px title">Finding a human</h1>
        <div className="slots">
          {Array.from({ length: needed }, (_, i) => (
            <div key={i} className={`slot${i < waiting ? " filled" : " blink"}`}>{i < waiting ? "✓" : "?"}</div>
          ))}
        </div>
        <p className="status" role="status">{msg}</p>
        <div className="dots" aria-hidden="true"><i>.</i><i>.</i><i>.</i></div>
        <p className="counts">Humans waiting: {Math.min(waiting, needed)} of {needed}</p>
        {error && <div className="error" role="alert">{error}</div>}
        <div className="actions">
          <button className="btn ghost" onClick={() => router.push("/")}>Cancel</button>
        </div>
      </section>
    </main>
  );
}
