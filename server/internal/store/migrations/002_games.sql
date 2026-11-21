-- Games and everything that happens in them (SPEC §6). Rows are appended as events happen,
-- each with a server timestamp (CLAUDE.md invariant 4).

create table games (
  id             uuid primary key,
  mode           text not null default 'coop' check (mode in ('coop')),
  difficulty     text not null,          -- independent|directed; 'stub' for M2 canned AI seats
  config         jsonb not null,
  prompt_version text not null,
  status         text not null check (status in ('running', 'finished', 'aborted')),
  started_at     timestamptz not null,
  ended_at       timestamptz
);
create index games_started_idx on games (started_at);

create table seats (
  game_id     uuid not null references games(id) on delete cascade,
  seat        int  not null,
  alias       text not null,
  sprite_seed int  not null,
  kind        text not null check (kind in ('human', 'ai', 'bot_human')),
  user_id     uuid references users(id) on delete set null,  -- account delete anonymizes (SPEC §9)
  provider    text,
  model_id    text,
  persona     jsonb,
  primary key (game_id, seat),
  check (kind = 'human' or user_id is null)
);
create index seats_user_idx on seats (user_id);

create table messages (
  id                 uuid primary key,
  game_id            uuid not null references games(id) on delete cascade,
  round              int  not null,
  phase              text not null,
  kind               text not null check (kind in ('intro', 'question', 'answer')),
  seat               int  not null,
  question_id        uuid,                 -- answers: the question's message id
  text               text,
  skipped            boolean not null,
  phase_started_at   timestamptz not null,
  submitted_at       timestamptz,          -- null: nothing was sent before the deadline
  first_keystroke_ms int,                  -- client telemetry, humans only
  edit_count         int,
  broke_character    boolean not null default false,
  foreign key (game_id, seat) references seats (game_id, seat)
);
create index messages_game_idx on messages (game_id, round, phase);

create table votes (
  game_id     uuid not null references games(id) on delete cascade,
  voter_seat  int  not null,
  target_seat int  not null,
  cast_at     timestamptz not null,
  primary key (game_id, voter_seat)
);

create table note_events (
  id          bigserial primary key,
  game_id     uuid not null references games(id) on delete cascade,
  owner_seat  int  not null,
  target_seat int  not null,
  mark        text not null check (mark in ('none', '?', '!', 'human', 'ai')),
  note        text,
  round       int  not null,
  phase       text not null,
  ts          timestamptz not null
);
create index note_events_game_idx on note_events (game_id);

create table results (
  game_id      uuid not null references games(id) on delete cascade,
  seat         int  not null,
  points       int  not null,
  found_other  boolean not null,
  was_found    boolean not null,
  mutual       boolean not null,
  votes_stolen int,
  primary key (game_id, seat)
);

create table ai_calls (
  id             uuid primary key default gen_random_uuid(),
  game_id        uuid not null references games(id) on delete cascade,
  seat           int,
  purpose        text not null,  -- persona|director|intro|ask|answer|critic|vote|analysis
  provider       text,
  model_id       text,
  prompt_version text,
  request        jsonb,
  draft          text,
  final          text,
  latency_ms     int,
  input_tokens   int,
  output_tokens  int,
  error          text,
  created_at     timestamptz not null
);
create index ai_calls_game_idx on ai_calls (game_id);

-- Games that count for research, leaderboard and stats: finished, and no bot_human seat
-- (invariant 5). Export and every future aggregate must read from this view.
create view real_games as
  select g.* from games g
  where g.status = 'finished'
    and not exists (select 1 from seats s where s.game_id = g.id and s.kind = 'bot_human');
