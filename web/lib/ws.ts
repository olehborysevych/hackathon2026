// WebSocket client for /ws (SPEC §7). One shared connection per tab that survives client-side
// navigation between the matching and table screens, and reconnects with backoff. Pages
// re-send what they need (queue.join, game.resync) from onOpen after every reconnect.

export type PublicSeat = { seat: number; alias: string; sprite_seed: number };
export type PublicConfig = {
  seats: number; humans: number; rounds: number;
  max_intro: number; max_ask: number; max_answer: number;
  t_intro: number; t_ask: number; t_answer: number; t_reveal: number; t_vote: number;
};
export type Question = { id: string; seat: number; text: string };
export type Phase = "LOBBY" | "INTRO" | "ASK" | "ANSWER" | "REVEAL" | "VOTE" | "RESULT";
export type MsgKind = "intro" | "question" | "answer";
export type PhaseStart = { phase: Phase; round: number; deadline_ts: number; server_ts: number; questions?: Question[] };
export type RevealItem = { id: string; seat: number; kind: MsgKind; question_id?: string; text: string | null };
export type PhaseReveal = { phase: Phase; round: number; items: RevealItem[] };
export type SubmitAck = { phase: Phase; round: number; kind: MsgKind; question_id?: string; skipped: boolean };
export type ResultSeat = { seat: number; alias: string; sprite_seed: number; kind: "human" | "ai" | "bot_human"; model_id?: string };
export type SeatResult = { seat: number; points: number; found_other: boolean; was_found: boolean; mutual: boolean; votes_stolen?: number };
export type GameResult = { game_id: string; seats: ResultSeat[]; votes: { voter: number; target: number }[]; results: SeatResult[] };
export type GameStart = { game_id: string; my_seat: number; seats: PublicSeat[]; config: PublicConfig };
export type Snapshot = {
  game_id: string; my_seat: number; seats: PublicSeat[]; config: PublicConfig;
  phase: Phase; round: number; deadline_ts: number; server_ts: number;
  questions?: Question[]; reveals: PhaseReveal[]; mine: SubmitAck[]; my_vote?: number;
  notes: { target_seat: number; mark: string; note?: string }[]; result?: GameResult;
};
export type QueueStatus = { queued: boolean; humans_waiting: number; humans_needed: number };
export type WsError = { code: string; message: string };

export type ConnState = "connecting" | "open" | "closed";

type Handler = (payload: any) => void; // eslint-disable-line @typescript-eslint/no-explicit-any

class GameSocket {
  private ws: WebSocket | null = null;
  private handlers = new Map<string, Set<Handler>>();
  private openHandlers = new Set<() => void>();
  private stateHandlers = new Set<(s: ConnState) => void>();
  private retry = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  state: ConnState = "closed";

  private url(): string {
    const { protocol, hostname, port, host } = window.location;
    const scheme = protocol === "https:" ? "wss" : "ws";
    // `next dev` runs on :3000 and does not proxy WebSockets; talk to Go on :8080 directly.
    if (port === "3000") return `${scheme}://${hostname}:8080/ws`;
    return `${scheme}://${host}/ws`;
  }

  private setState(s: ConnState) {
    this.state = s;
    this.stateHandlers.forEach((h) => h(s));
  }

  connect() {
    if (typeof window === "undefined") return;
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) return;
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
    }
    this.setState("connecting");
    const ws = new WebSocket(this.url());
    this.ws = ws;
    ws.onopen = () => {
      this.retry = 0;
      this.setState("open");
      this.openHandlers.forEach((h) => h());
    };
    ws.onmessage = (ev) => {
      let env: { type: string; payload: unknown };
      try {
        env = JSON.parse(ev.data);
      } catch {
        return;
      }
      this.handlers.get(env.type)?.forEach((h) => h(env.payload ?? {}));
    };
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.ws = null;
      this.setState("closed");
      const delay = Math.min(5000, 500 * 2 ** this.retry++);
      this.timer = setTimeout(() => this.connect(), delay);
    };
  }

  send(type: string, payload: object = {}): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false;
    this.ws.send(JSON.stringify({ type, payload }));
    return true;
  }

  on(type: string, h: Handler): () => void {
    if (!this.handlers.has(type)) this.handlers.set(type, new Set());
    this.handlers.get(type)!.add(h);
    return () => this.handlers.get(type)?.delete(h);
  }

  // onOpen runs now if the socket is already open, and after every (re)connect.
  onOpen(h: () => void): () => void {
    this.openHandlers.add(h);
    if (this.state === "open") h();
    return () => this.openHandlers.delete(h);
  }

  onState(h: (s: ConnState) => void): () => void {
    this.stateHandlers.add(h);
    h(this.state);
    return () => this.stateHandlers.delete(h);
  }
}

let instance: GameSocket | null = null;

export function socket(): GameSocket {
  if (!instance) instance = new GameSocket();
  return instance;
}
