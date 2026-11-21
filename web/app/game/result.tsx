"use client";

import { useRouter } from "next/navigation";
import type { GameResult, ResultSeat } from "@/lib/ws";
import { Sprite } from "../components";
import type { State } from "./model";

// Result screen (design/game-room/result.html): verdict, did you guess right, were you found,
// points, the table unmasked (human / AI + model) and the vote arrows.
export function ResultView({ s, result, onTable }: { s: State; result: GameResult; onTable: () => void }) {
  const router = useRouter();
  const me = s.mySeat;
  const seat = (i: number) => result.seats.find((x) => x.seat === i);
  const name = (i: number) => seat(i)?.alias ?? `Seat ${i + 1}`;
  const isHuman = (x?: ResultSeat) => x?.kind === "human" || x?.kind === "bot_human";
  const other = result.seats.find((x) => x.seat !== me && isHuman(x));
  const mine = result.results.find((r) => r.seat === me);
  const myVote = result.votes.find((v) => v.voter === me)?.target ?? null;
  const foundBy = other && result.votes.some((v) => v.voter === other.seat && v.target === me) ? other.seat : null;
  const correct = mine?.found_other ?? (other !== undefined && myVote === other.seat);
  const found = mine?.was_found ?? foundBy !== null;
  const mutual = mine?.mutual ?? (correct && found);
  const points = mine?.points ?? 0;
  const o = other ? name(other.seat) : "The other human";

  const verdict = mutual
    ? { cls: "mutual", tag: "Mutual match", title: "You found each other ✓", sub: `You picked ${o}, and ${o} picked you.` }
    : correct
      ? { cls: "mutual", tag: "Correct guess", title: "You found the human", sub: `${o} did not find you back.` }
      : found
        ? {
            cls: "wrong", tag: "One-sided", title: "You missed, but you were found",
            sub: `${o} voted for you. ${myVote !== null ? `You voted for ${name(myVote)}, who is an AI.` : "You did not vote."}`,
          }
        : {
            cls: "missed", tag: "Missed", title: "You missed",
            sub: `${myVote !== null ? `You voted for ${name(myVote)}, an AI.` : "You did not vote."} ${o} was the other human and did not find you.`,
          };

  const votesIn = (i: number) => result.votes.filter((v) => v.target === i).length;
  const role = (x?: ResultSeat) =>
    x?.kind === "ai" ? <span className="role ai">AI · {x.model_id ?? "?"}</span>
      : x?.kind === "bot_human" ? <span className="role human">Human · bot</span>
        : <span className="role human">Human</span>;

  return (
    <div className="result">
      <section className={`verdict ${verdict.cls}`}>
        <span className="vtag px">{verdict.tag}</span>
        <h1 className="px">{verdict.title}</h1>
        <p>{verdict.sub}</p>
      </section>

      <div className="pair">
        <div className="card">
          <p className="q px">Did you guess right?</p>
          <span className={`answer px ${correct ? "yes" : "no"}`}>{correct ? "Yes" : "No"}</span>
          <p>
            {myVote === null ? "You did not vote." : <>You voted for <b>{name(myVote)}</b>. {correct ? "They are the other human." : "They are an AI."}</>}
          </p>
        </div>
        <div className="card">
          <p className="q px">Did the other human find you?</p>
          <span className={`answer px ${found ? "yes" : "no"}`}>{found ? "Yes" : "No"}</span>
          <p>{found && foundBy !== null ? <><b>{name(foundBy)}</b> voted for you.</> : `${o} did not vote for you.`}</p>
        </div>
      </div>

      <div className="points">
        <div>
          <div className="px plabel-s">Points this game</div>
          <div className="big px">+{points}</div>
        </div>
        <ul>
          <li>{correct ? <b>+1</b> : "+0"} you voted for the other human</li>
          <li>{found ? <b>+1</b> : "+0"} the other human voted for you</li>
          <li>{mutual ? <b>+1</b> : "+0"} mutual bonus</li>
        </ul>
      </div>

      <h2 className="sec px">The table, unmasked</h2>
      <div className="reveal">
        {[...result.seats].sort((a, b) => (a.seat === me ? -1 : b.seat === me ? 1 : a.seat - b.seat)).map((x) => (
          <div key={x.seat} className={`who${x.seat === me ? " me" : ""}`}>
            <div className="medal"><Sprite seed={x.sprite_seed} size={50} /></div>
            <div className="name px">{x.alias}{x.seat === me ? " · you" : ""}</div>
            {role(x)}
            <div className="got">{votesIn(x.seat)} vote{votesIn(x.seat) === 1 ? "" : "s"}</div>
          </div>
        ))}
      </div>

      <h2 className="sec px">Votes</h2>
      <table className="votes-t">
        <thead>
          <tr><th className="px">Voter</th><th className="px" aria-label="voted for" /><th className="px">Voted for</th><th className="px">Target is</th><th className="px">Counts</th></tr>
        </thead>
        <tbody>
          {result.votes.length === 0 && (
            <tr><td colSpan={5} className="empty">No votes were cast.</td></tr>
          )}
          {result.votes.map((v) => {
            const humanVoter = isHuman(seat(v.voter));
            const hit = humanVoter && isHuman(seat(v.target));
            return (
              <tr key={v.voter} className={v.voter === me ? "mine" : ""}>
                <td>{name(v.voter)}{v.voter === me ? " (you)" : ""}</td>
                <td className="arrow" aria-hidden="true">→</td>
                <td>{name(v.target)}{v.target === me ? " (you)" : ""}</td>
                <td>{isHuman(seat(v.target)) ? <span className="vpill h px">Human</span> : <span className="vpill a px">AI</span>}</td>
                <td>
                  {humanVoter ? <span className={`vpill px${hit ? " hit" : ""}`}>Human vote{hit ? " ✓" : ""}</span> : <span className="vpill px">AI vote · not scored</span>}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>

      <div className="actions">
        <button className="btn big" onClick={() => router.push("/stats")}>My stats</button>
        <button className="btn big ghost" onClick={onTable}>Review the table</button>
        <button className="btn big ghost" onClick={() => router.push("/")}>Play again</button>
      </div>
    </div>
  );
}
