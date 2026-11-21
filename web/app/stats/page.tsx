"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, type BoardRow, type FoolGroup, type FoolRates, type Leaderboard, type Me, type SortKey } from "@/lib/api";
import { Disclosure } from "../components";

// Stats page (design/game-room/stats.html, SPEC §2.4). Usernames appear here and nowhere in the
// game screens (DECISIONS 13). Games with a bot_human seat are not counted by the server.

const METRICS: Record<SortKey, { label: string; value: (r: BoardRow) => string }> = {
  detection: { label: "Found the human", value: (r) => pct(r.detection) },
  recognizability: { label: "Was found", value: (r) => pct(r.recognizability) },
  points: { label: "Points per game", value: (r) => r.points_per_game.toFixed(2) },
};
const SORTS = Object.keys(METRICS) as SortKey[];
// Bar colours for models, from the 16-colour palette.
const MODEL_COLOURS = ["var(--red)", "var(--blue)", "var(--green)", "var(--purple)", "var(--teal)", "var(--pink)", "var(--gold)", "var(--sky)"];

function pct(x: number) {
  return `${Math.round(x * 100)}%`;
}

export default function StatsPage() {
  const router = useRouter();
  const [me, setMe] = useState<Me | null>(null);
  const [sort, setSort] = useState<SortKey>("detection");
  const [board, setBoard] = useState<Leaderboard | null>(null);
  const [fool, setFool] = useState<FoolRates | null>(null);
  const [error, setError] = useState<string | null>(null);

  const fail = (e: unknown) => {
    if (e instanceof ApiError && e.status === 401) router.replace("/login");
    else setError(e instanceof Error ? e.message : "Could not reach the server");
  };

  useEffect(() => {
    api.me().then(setMe).catch(fail);
    api.stats().then(setFool).catch(fail);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    api.leaderboard(sort).then(setBoard).catch(fail);
  }, [sort]); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <main className="wrap stats">
      <div className="topbar">
        <div className="px logo">Find the Human · Stats</div>
        <nav className="stats-nav">
          <a className="px" href="#me">Me</a>
          <a className="px" href="#board">Board</a>
          <a className="px" href="#fool">Fool rate</a>
          <button className="ghost-link" onClick={() => router.push("/")}>Lobby</button>
        </nav>
      </div>
      <Disclosure />
      {error && <div className="error" role="alert" style={{ marginTop: 18 }}>{error}</div>}
      {!error && (!board || !fool || !me) && <p className="muted">Loading…</p>}
      {board && me && <Standing board={board} me={me} sort={sort} onSort={setSort} />}
      {board && board.me.recent.length > 0 && <Recent board={board} />}
      {board && me && <Board board={board} me={me} sort={sort} />}
      {fool && <Fool fool={fool} />}
    </main>
  );
}

function Standing({ board, me, sort, onSort }: { board: Leaderboard; me: Me; sort: SortKey; onSort: (s: SortKey) => void }) {
  const m = board.me;
  const rank = m.ranks[sort];
  return (
    <section className="panel" id="me">
      <h2>Your standing</h2>
      <div className="metric-tabs" role="tablist" aria-label="Rank by">
        {SORTS.map((k) => (
          <button key={k} role="tab" aria-selected={k === sort} className={`px tab${k === sort ? " on" : ""}`} onClick={() => onSort(k)}>
            {METRICS[k].label}
          </button>
        ))}
      </div>
      <div className="standing">
        <div className="rank-badge">
          <span className="px">{rank ? `#${rank}` : "—"}</span>
          <small className="px">{rank ? `of ${board.ranked}` : "unranked"}</small>
        </div>
        <div>
          {rank ? (
            <p>
              <b>{me.username}</b>, you rank <b>#{rank}</b> of {board.ranked} on <b>{METRICS[sort].label.toLowerCase()}</b>.
              {board.ranked > 2 && rank / board.ranked <= 0.5 && <> That is the top {Math.max(1, Math.round((rank / board.ranked) * 100))}%.</>}
            </p>
          ) : (
            <p>
              <b>{me.username}</b>, you have {m.games === 0 ? "no finished games yet" : `${m.games} game${m.games === 1 ? "" : "s"}`}.
            </p>
          )}
          <p className="notice">
            {m.games_needed > 0
              ? `Play ${m.games_needed} more game${m.games_needed === 1 ? "" : "s"} to get ranked.`
              : "Ranks update after every finished game."}
          </p>
        </div>
      </div>
      <div className="kpis">
        <div className="kpi"><span className="px">Games</span><b className="px">{m.games}</b></div>
        <div className="kpi"><span className="px">Points</span><b className="px">{m.points}</b><small>{m.points_per_game.toFixed(2)} per game</small></div>
        <div className="kpi">
          <span className="px">Found the human</span><b className="px">{pct(m.detection)}</b>
          <small>{m.found} of {m.games} · baseline {pct(board.baseline)}</small>
        </div>
        <div className="kpi">
          <span className="px">Was found</span><b className="px">{pct(m.recognizability)}</b>
          <small>{m.was_found} of {m.games} · baseline {pct(board.baseline)}</small>
        </div>
      </div>
    </section>
  );
}

function Tick({ on, label = on ? "Yes" : "No", cls = on ? "yes" : "no" }: { on: boolean; label?: string; cls?: string }) {
  return <span className={`px tick ${cls}`}>{label}</span>;
}

function Recent({ board }: { board: Leaderboard }) {
  return (
    <section className="panel">
      <h2>Your recent games</h2>
      <table className="stats-table">
        <thead>
          <tr><th className="px">Ended</th><th className="px">You found</th><th className="px">Found you</th><th className="px">Mutual</th><th className="px num">Pts</th></tr>
        </thead>
        <tbody>
          {board.me.recent.map((g) => (
            <tr key={g.game_id}>
              <td>{new Date(g.ended_at).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}</td>
              <td><Tick on={g.found} /></td>
              <td><Tick on={g.was_found} /></td>
              <td>{g.mutual ? <Tick on label="Mutual" cls="mutual" /> : "—"}</td>
              <td className="num">{g.points}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

function RateBar({ value, baseline, colour }: { value: number; baseline: number; colour?: string }) {
  return (
    <div className="rate-bar" title={`baseline ${pct(baseline)}`}>
      <i style={{ width: `${Math.min(100, value * 100)}%`, background: colour }} />
      <b style={{ left: `${baseline * 100}%` }} aria-hidden="true" />
    </div>
  );
}

function BoardLine({ r, baseline }: { r: BoardRow; baseline: number }) {
  return (
    <tr className={r.me ? "me" : ""}>
      <td className="px rank">{r.rank ?? "—"}</td>
      <td className="name">{r.me ? <><b>{r.username}</b> (you)</> : r.username}</td>
      <td className="num">{r.games}</td>
      <td className="num hide-s">{r.points}</td>
      <td><div className="cell"><RateBar value={r.detection} baseline={baseline} /> {pct(r.detection)}</div></td>
      <td className="hide-s"><div className="cell"><RateBar value={r.recognizability} baseline={baseline} /> {pct(r.recognizability)}</div></td>
      <td className="num hide-s">{r.points_per_game.toFixed(2)}</td>
    </tr>
  );
}

function Board({ board, me, sort }: { board: Leaderboard; me: Me; sort: SortKey }) {
  const inTop = board.top.some((r) => r.me);
  const own = board.me.games > 0 && !inTop ? { ...board.me, username: board.me.username || me.username } : null;
  return (
    <section className="panel" id="board">
      <h2>Global leaderboard</h2>
      <p className="sub">
        Ranked after {board.min_games} games. Ordered by <b>{METRICS[sort].label.toLowerCase()}</b>, ties by total points.
      </p>
      <table className="stats-table board">
        <thead>
          <tr>
            <th className="px">#</th><th className="px">Player</th><th className="px num">Games</th><th className="px num hide-s">Pts</th>
            <th className="px">Found</th><th className="px hide-s">Was found</th><th className="px num hide-s">Pts/game</th>
          </tr>
        </thead>
        <tbody>
          {board.top.length === 0 && (
            <tr><td colSpan={7} className="empty">Nobody has played {board.min_games} games yet.</td></tr>
          )}
          {board.top.map((r) => <BoardLine key={r.username} r={r} baseline={board.baseline} />)}
          <tr className="baseline">
            <td />
            <td colSpan={6}>Random baseline: {pct(board.baseline)} (red tick on the bars)</td>
          </tr>
          {own && (
            <>
              {board.top.length > 0 && <tr className="gap"><td colSpan={7}>…</td></tr>}
              <BoardLine r={own} baseline={board.baseline} />
            </>
          )}
        </tbody>
      </table>
    </section>
  );
}

function FoolRow({ g, colour }: { g: FoolGroup; colour?: string }) {
  return (
    <div className="fool-row">
      <span className="key" title={g.key}>{g.key}</span>
      <RateBar value={g.rate} baseline={g.baseline} colour={colour} />
      <span className="px val">{g.votes ? pct(g.rate) : "—"}</span>
      <small className="n">{g.stolen} of {g.votes} · {g.games} game{g.games === 1 ? "" : "s"}</small>
    </div>
  );
}

function Fool({ fool }: { fool: FoolRates }) {
  const o = fool.overall;
  return (
    <section className="panel" id="fool">
      <h2>AI fool rate</h2>
      <p className="sub">
        Share of human votes that landed on an AI seat. Overall:{" "}
        <span className="px big">{o.votes ? pct(o.rate) : "—"}</span>{" "}
        <span className="muted-ink">({o.stolen} of {o.votes} votes, {o.games} game{o.games === 1 ? "" : "s"}; random guessing gives {pct(o.baseline)})</span>
      </p>
      {o.votes === 0 && <p className="notice">No human votes in finished games yet. Practice games with a bot partner don&apos;t count.</p>}
      <div className="groups">
        <div className="group">
          <h3 className="px">By model, per seat</h3>
          <p className="hint">Votes a seat of this model got, out of every human vote it could have got. Random: {pct(fool.per_seat.baseline)}.</p>
          {fool.by_model.length === 0 && <p className="hint">No data yet.</p>}
          {fool.by_model.map((g, i) => <FoolRow key={g.key} g={g} colour={MODEL_COLOURS[i % MODEL_COLOURS.length]} />)}
        </div>
        <div className="group">
          <h3 className="px">By difficulty</h3>
          {fool.by_difficulty.length === 0 && <p className="hint">No data yet.</p>}
          {fool.by_difficulty.map((g) => <FoolRow key={g.key} g={g} />)}
          <h3 className="px" style={{ marginTop: 14 }}>By prompt version</h3>
          {fool.by_prompt_version.length === 0 && <p className="hint">No data yet.</p>}
          {fool.by_prompt_version.map((g) => <FoolRow key={g.key} g={g} />)}
        </div>
      </div>
      <p className="flag">
        The red tick on each bar is what random guessing would give. Above it, the AIs are fooling people. Small samples
        swing a lot: check the vote counts.
      </p>
    </section>
  );
}
