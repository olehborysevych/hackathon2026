// Thin client for the REST API (SPEC §7). Same origin, cookie auth (httpOnly `sid`).

import type { PublicConfig } from "./ws";

export type Me = { username: string };

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
};
