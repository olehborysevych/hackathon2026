"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, type Me } from "@/lib/api";
import { Disclosure } from "./components";

// Start screen (design/game-room/start.html). Matchmaking is not built yet, so the
// "Ready to play" button is disabled for now.
export default function StartPage() {
  const router = useRouter();
  const [me, setMe] = useState<Me | null>(null);
  const [error, setError] = useState<string | null>(null);

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
        <button className="ghost-link" onClick={logout}>Log out</button>
      </div>
      <Disclosure />

      <section className="hero">
        <h1>Two humans. Three AIs. One table.</h1>
        <p>Find the other human. Try not to get fooled by the machines pretending to be people.</p>
      </section>

      <section className="panel">
        <h2>How a game works</h2>
        <ol className="steps">
          <li><span className="n">1</span><span>Intro: say hi, 160 characters, optional</span><span className="t">60 SEC</span></li>
          <li><span className="n">2</span><span>Ask: one question for the table</span><span className="t">45 SEC</span></li>
          <li><span className="n">3</span><span>Answer: reply to each question, skip any</span><span className="t">90 SEC</span></li>
          <li><span className="n">4</span><span>Reveal: all answers appear at once</span><span className="t">30 SEC</span></li>
          <li><span className="n">5</span><span>Vote: pick the seat you think is the other human</span><span className="t">45 SEC</span></li>
          <li><span className="n">6</span><span>Result: see who was who and who found whom</span><span className="t">—</span></li>
        </ol>
        <p style={{ margin: "12px 0 0", fontSize: 20 }}>
          Two rounds of ask, answer and reveal. A full game takes about 7 to 8 minutes.
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
        <button className="big-btn" disabled>Ready to play</button>
        <p>Tables open soon. You&apos;re all set.</p>
      </section>
    </main>
  );
}
