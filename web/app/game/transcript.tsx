"use client";

import type { State } from "./model";
import { questionsFor, revealOf } from "./model";

// Full transcript of everything revealed so far (SPEC §8), folded away under the table. The table
// itself shows one phase and one question at a time (DECISIONS 5); this is the whole log in one place.
export function Transcript({ s, alias }: { s: State; alias: (seat: number) => string }) {
  const intro = revealOf(s, "INTRO", 0);
  if (!intro) return null;
  const rounds: number[] = [];
  for (let r = 1; r <= (s.config?.rounds ?? 0); r++) if (revealOf(s, "ASK", r)) rounds.push(r);

  return (
    <details className="panel transcript">
      <summary className="px">Full transcript</summary>
      <div className="tagline px">Intros</div>
      {intro.items.map((it) => (
        <div key={it.id} className={`ent intro${it.text === null ? " skip" : ""}`}>
          <span className="who">{alias(it.seat)}</span>
          <span className="say">{it.text ?? "—"}</span>
        </div>
      ))}
      {rounds.map((r) => {
        const answers = revealOf(s, "ANSWER", r);
        const qs = questionsFor(s, r);
        return (
          <div key={r}>
            <div className="tagline px">Round {r}</div>
            {qs.length === 0 && <div className="ent skip"><span className="say">— nobody asked a question</span></div>}
            {qs.map((q) => (
              <div key={q.id}>
                <div className="ent q">
                  <span className="who">{alias(q.seat)} asks</span>
                  <span className="say">{q.text}</span>
                </div>
                {answers ? (
                  answers.items.filter((a) => a.question_id === q.id).map((a) => (
                    <div key={a.id} className={`ent a${a.text === null ? " skip" : ""}`}>
                      <span className="who">{alias(a.seat)}</span>
                      <span className="say">{a.text ?? "—"}</span>
                    </div>
                  ))
                ) : (
                  <div className="ent a skip"><span className="say">answers are sealed until the phase ends</span></div>
                )}
              </div>
            ))}
          </div>
        );
      })}
    </details>
  );
}
