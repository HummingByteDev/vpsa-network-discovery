-- Audit: append-only event log. No service role gets UPDATE/DELETE here
-- (role separation is applied when per-service roles land in Phase 6).
create schema if not exists audit;

create table audit.event (
  id         bigint primary key generated always as identity,
  category   text not null,
  actor      text not null,
  action     text not null,
  subject    text,
  detail     jsonb not null default '{}',
  created_at timestamptz not null default now()
);
create index event_category_idx on audit.event (category, created_at);
