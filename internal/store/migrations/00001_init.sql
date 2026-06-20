-- +goose Up
create table if not exists channels (
  id bigserial primary key,
  peer_id bigint not null unique,
  username text unique,
  title text not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists messages (
  id bigserial primary key,
  channel_id bigint not null references channels(id) on delete cascade,
  telegram_message_id integer not null,
  text text not null,
  views integer,
  forwards integer,
  reply_count integer,
  telegram_date timestamptz not null,
  edited_at timestamptz,
  media_type text,
  media_file_id text,
  media_file_path text,
  raw_payload jsonb,
  deleted_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (channel_id, telegram_message_id)
);

alter table messages add column if not exists media_type text;
alter table messages add column if not exists media_file_id text;
alter table messages add column if not exists media_file_path text;
alter table messages add column if not exists raw_payload jsonb;

create extension if not exists pg_trgm;

create index if not exists messages_channel_date_idx
  on messages (channel_id, telegram_date desc, telegram_message_id desc)
  where deleted_at is null;

create index if not exists messages_date_idx
  on messages (telegram_date desc, telegram_message_id desc)
  where deleted_at is null;

create index if not exists messages_text_trgm_idx
  on messages using gin (text gin_trgm_ops)
  where deleted_at is null;

create table if not exists sync_status (
  channel_id bigint primary key references channels(id) on delete cascade,
  last_started_at timestamptz,
  last_finished_at timestamptz,
  last_success_at timestamptz,
  last_error text,
  last_message_count integer not null default 0,
  updated_at timestamptz not null default now()
);

-- +goose Down
drop table if exists sync_status;
drop table if exists messages;
drop table if exists channels;
drop extension if exists pg_trgm;
