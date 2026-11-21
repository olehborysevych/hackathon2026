-- Real AI seats (M3): the safety-valve flag per message and the cost of each AI call.
-- messages.broke_character, seats.persona and ai_calls.request/draft/tokens already exist (002).
alter table messages add column out_of_game boolean not null default false;
alter table ai_calls add column cost_usd numeric(12, 8);
