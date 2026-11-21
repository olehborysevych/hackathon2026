"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, type Me } from "@/lib/api";
import type { PublicConfig } from "@/lib/ws";
import { Disclosure } from "./components";

const DEFAULT_CONFIG: PublicConfig = {
  seats: 5, humans: 2, rounds: 1, max_intro: 160, max_ask: 160, max_answer: 200,
  t_intro: 60, t_ask: 45, t_answer: 90, t_reveal: 30, t_vote: 45,
};

// Start screen (design/game-room/start.html). "Ready to play" goes to the matching screen.
export default function StartPage() {
  const router = useRouter();
  const [me, setMe] = useState<Me | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [cfg, setCfg] = useState<PublicConfig>(DEFAULT_CONFIG);

  useEffect(() => {
    api.config().then(setCfg).catch(() => {});
  }, []);

  useEffect(() => {
    api
      .me()
      .then(setMe)
      .catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 401) router.replace("/login");
        else setError(e instanceof Error ? e.message : "Could not reach the server");
      });
  }, [router]);

  async function logout() {
    try {
      await api.logout();
    } finally {
      router.replace("/login");
    }
  }

  if (error) {
    return (
      <main className="wrap">
        <div className="px logo">Find the Human</div>
        <div className="error" role="alert" style={{ marginTop: 18 }}>{error}</div>
      </main>
    );
  }
  if (!me) {
    return (
      <main className="wrap">
        <div className="px logo">Find the Human</div>
        <p className="muted">Loading…</p>
      </main>
    );
  }

  return (
    <main className="wrap">
      <div className="topbar">
        <div className="px logo">Find the Human</div>
        <div style={{ display: "flex", gap: 14 }}>
          <button className="ghost-link" onClick={() => router.push("/stats")}>Stats</button>
          <button className="ghost-link" onClick={logout}>Log out</button>
        </div>
      </div>
      <Disclosure />

      <section className="hero">
        <h1>Two humans. Three AIs. One table.</h1>
        <p>Find the other human. Try not to get fooled by the machines pretending to be people.</p>
      </section>

      <section className="panel">
        <h2>How a game works</h2>
        <ol className="steps">
          <li><span className="n">1</span><span>Intro: say hi, 160 characters, optional</span><span className="t">{cfg.t_intro} SEC</span></li>
          <li><span className="n">2</span><span>Ask: one question for the table</span><span className="t">{cfg.t_ask} SEC</span></li>
          <li><span className="n">3</span><span>Answer: reply to each question, skip any</span><span className="t">{cfg.t_answer} SEC</span></li>
          <li><span className="n">4</span><span>Reveal: all answers appear at once</span><span className="t">{cfg.t_reveal} SEC</span></li>
          <li><span className="n">5</span><span>Vote: pick the seat you think is the other human</span><span className="t">{cfg.t_vote} SEC</span></li>
          <li><span className="n">6</span><span>Result: see who was who and who found whom</span><span className="t">—</span></li>
        </ol>
        <p style={{ margin: "12px 0 0", fontSize: 20 }}>
          {cfg.rounds === 1 ? "One round" : `${cfg.rounds} rounds`} of ask, answer and reveal. A full game takes up to about{" "}
          {Math.ceil((cfg.t_intro + cfg.rounds * (cfg.t_ask + cfg.t_answer + cfg.t_reveal) + cfg.t_vote) / 60)} minutes.
        </p>
      </section>

      <section className="panel">
        <h2>Scoring</h2>
        <table className="score">
          <tbody>
            <tr><td>You voted for the other human</td><td className="pts">+1</td></tr>
            <tr><td>The other human voted for you</td><td className="pts">+1</td></tr>
            <tr><td><b>Mutual match</b> (both of the above)</td><td className="pts">+3 each</td></tr>
            <tr><td>Miss</td><td className="pts">0</td></tr>
          </tbody>
        </table>
        <ul className="rules">
          <li>Everyone has a made-up alias for this game. Usernames never appear at the table.</li>
          <li>Answers stay sealed until the phase ends. You can&apos;t see who has already answered.</li>
          <li>Mark suspicion on any other seat. Only you can see it.</li>
        </ul>
      </section>

      <section className="panel ready">
        <h2 style={{ marginBottom: 0 }}>Ready?</h2>
        <p>Signed in as <b>{me.username}</b></p>
        <button className="big-btn" onClick={() => router.push("/match")}>Ready to play</button>
        <p>You&apos;ll sit down with the next human in the queue.</p>
        <button className="btn ghost" onClick={() => router.push("/stats")}>Leaderboard &amp; stats</button>
      </section>
    </main>
  );
}
