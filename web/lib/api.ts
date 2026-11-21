// Thin client for the REST API (SPEC §7). Same origin, cookie auth (httpOnly `sid`).

import type { PublicConfig } from "./ws";

export type Me = { username: string };

export type SortKey = "detection" | "recognizability" | "points";
export type BoardRow = {
  rank?: number; username: string; games: number; points: number; points_per_game: number;
  found: number; was_found: number; mutual: number; detection: number; recognizability: number; me?: boolean;
};
export type RecentGame = { game_id: string; ended_at: string; points: number; found: boolean; was_found: boolean; mutual: boolean };
export type Leaderboard = {
  sort: SortKey; min_games: number; baseline: number; ranked: number; top: BoardRow[];
  me: BoardRow & { ranks: Partial<Record<SortKey, number>>; games_needed: number; recent: RecentGame[] };
};
export type FoolGroup = { key: string; rate: number; stolen: number; votes: number; games: number; baseline: number };
export type FoolRates = {
  overall: FoolGroup; per_seat: FoolGroup; by_model: FoolGroup[]; by_difficulty: FoolGroup[]; by_prompt_version: FoolGroup[];
};

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: "same-origin",
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    let message = `Request failed (${res.status})`;
    try {
      const body = await res.json();
      if (body?.error) message = body.error;
    } catch {
      /* non-JSON error body */
    }
    throw new ApiError(res.status, message);
  }
  return (await res.json()) as T;
}

export const api = {
  me: () => call<Me>("/api/me"),
  login: (username: string, password: string) =>
    call<Me>("/api/login", { method: "POST", body: JSON.stringify({ username, password }) }),
  register: (username: string, password: string, consent: boolean) =>
    call<Me>("/api/register", { method: "POST", body: JSON.stringify({ username, password, consent }) }),
  logout: () => call<{ ok: boolean }>("/api/logout", { method: "POST" }),
  config: () => call<PublicConfig>("/api/config"),
  leaderboard: (sort: SortKey) => call<Leaderboard>(`/api/leaderboard?sort=${sort}`),
  stats: () => call<FoolRates>("/api/stats"),
};
