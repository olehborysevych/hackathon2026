"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { api } from "@/lib/api";
import { Disclosure } from "../components";

type Mode = "login" | "register";

// Username + password only (SPEC §1: no email, no OAuth). Register requires consent (SPEC §9).
export default function LoginPage() {
  const router = useRouter();
  const [mode, setMode] = useState<Mode>("login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [consent, setConsent] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const name = username.trim().toLowerCase();
      if (mode === "login") await api.login(name, password);
      else await api.register(name, password, consent);
      router.replace("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong");
      setBusy(false);
    }
  }

  const isRegister = mode === "register";

  return (
    <main className="wrap">
      <div className="px logo">Find the Human</div>
      <Disclosure />

      <section className="hero">
        <h1>Two humans. Three AIs. One table.</h1>
        <p>Sign in to find a seat.</p>
      </section>

      <section className="panel">
        <div className="tabs" role="tablist">
          <button role="tab" aria-selected={!isRegister} onClick={() => { setMode("login"); setError(null); }}>
            Sign in
          </button>
          <button role="tab" aria-selected={isRegister} onClick={() => { setMode("register"); setError(null); }}>
            Create account
          </button>
        </div>

        <form onSubmit={submit}>
          {error && <div className="error" role="alert">{error}</div>}

          <div className="field">
            <label htmlFor="username">Username</label>
            <input
              id="username" type="text" autoComplete="username" autoCapitalize="none" spellCheck={false}
              required minLength={3} maxLength={20} value={username} onChange={(e) => setUsername(e.target.value)}
            />
            {isRegister && <p className="hint" style={{ margin: 0 }}>3–20 characters: a–z, 0–9, underscore.</p>}
          </div>

          <div className="field">
            <label htmlFor="password">Password</label>
            <input
              id="password" type="password" autoComplete={isRegister ? "new-password" : "current-password"}
              required minLength={isRegister ? 8 : 1} value={password} onChange={(e) => setPassword(e.target.value)}
            />
            {isRegister && <p className="hint" style={{ margin: 0 }}>At least 8 characters. No email and no password reset yet, so keep it safe.</p>}
          </div>

          {isRegister && (
            <label className="check">
              <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} required />
              <span>
                I agree that my game messages, votes and notes are stored and used for research to improve AI players.
              </span>
            </label>
          )}

          <button className="big-btn" type="submit" disabled={busy || (isRegister && !consent)}>
            {busy ? "Please wait…" : isRegister ? "Create account" : "Sign in"}
          </button>
        </form>
      </section>
    </main>
  );
}
