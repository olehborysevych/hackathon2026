-- Users and sessions (SPEC §6). Usernames are stored lowercase; the check keeps it that way.
create table users (
  id            uuid primary key default gen_random_uuid(),
  username      text not null unique check (username ~ '^[a-z0-9_]{3,20}$'),
  password_hash text not null,
  consent_at    timestamptz not null,
  created_at    timestamptz not null default now()
);

-- Only a SHA-256 of the session token is stored, so a DB leak does not hand out live sessions.
create table sessions (
  token_hash text primary key,
  user_id    uuid not null references users(id) on delete cascade,
  created_at timestamptz not null default now(),
  expires_at timestamptz not null
);
create index sessions_user_idx on sessions (user_id);
create index sessions_expires_idx on sessions (expires_at);
