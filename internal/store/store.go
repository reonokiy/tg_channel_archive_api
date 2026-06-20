package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type DB struct {
	db *sqlx.DB
}

type JSONRaw json.RawMessage

func (r JSONRaw) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return r, nil
}

func (r *JSONRaw) Scan(src any) error {
	if src == nil {
		*r = nil
		return nil
	}
	switch value := src.(type) {
	case []byte:
		*r = append((*r)[:0], value...)
	case string:
		*r = append((*r)[:0], value...)
	default:
		return nil
	}
	return nil
}

func (r JSONRaw) Value() (driver.Value, error) {
	if len(r) == 0 {
		return nil, nil
	}
	return []byte(r), nil
}

type Channel struct {
	ID               int64      `db:"id" json:"id"`
	PeerID           int64      `db:"peer_id" json:"peer_id"`
	Username         string     `db:"username" json:"username"`
	Title            string     `db:"title" json:"title"`
	MessageCount     int        `db:"message_count" json:"message_count"`
	LatestMessageAt  *time.Time `db:"latest_message_at" json:"latest_message_at,omitempty"`
	LastSyncStarted  *time.Time `db:"last_started_at" json:"last_sync_started_at,omitempty"`
	LastSyncFinished *time.Time `db:"last_finished_at" json:"last_sync_finished_at,omitempty"`
	LastSyncSuccess  *time.Time `db:"last_success_at" json:"last_sync_success_at,omitempty"`
	LastSyncError    *string    `db:"last_error" json:"last_sync_error,omitempty"`
	Created          time.Time  `db:"created_at" json:"created_at"`
	Updated          time.Time  `db:"updated_at" json:"updated_at"`
}

type Message struct {
	ID              int64      `db:"id" json:"id"`
	ChannelID       int64      `db:"channel_id" json:"channel_id"`
	TelegramMsgID   int        `db:"telegram_message_id" json:"telegram_message_id"`
	Text            string     `db:"text" json:"text"`
	MediaType       string     `db:"media_type" json:"media_type,omitempty"`
	MediaFileID     string     `db:"media_file_id" json:"media_file_id,omitempty"`
	MediaFilePath   string     `db:"media_file_path" json:"media_file_path,omitempty"`
	RawPayload      JSONRaw    `db:"raw_payload" json:"raw_payload,omitempty"`
	Views           *int       `db:"views" json:"views,omitempty"`
	Forwards        *int       `db:"forwards" json:"forwards,omitempty"`
	ReplyCount      *int       `db:"reply_count" json:"reply_count,omitempty"`
	TelegramDate    time.Time  `db:"telegram_date" json:"telegram_date"`
	EditedAt        *time.Time `db:"edited_at" json:"edited_at,omitempty"`
	DeletedAt       *time.Time `db:"deleted_at" json:"deleted_at,omitempty"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at" json:"updated_at"`
	ChannelUsername string     `db:"channel_username" json:"channel_username,omitempty"`
	ChannelTitle    string     `db:"channel_title" json:"channel_title,omitempty"`
}

type UpsertMessageParams struct {
	ChannelID     int64
	TelegramMsgID int
	Text          string
	MediaType     string
	MediaFileID   string
	MediaFilePath string
	RawPayload    JSONRaw
	Views         *int
	Forwards      *int
	ReplyCount    *int
	TelegramDate  time.Time
	EditedAt      *time.Time
}

type ListMessagesParams struct {
	Channel string
	Query   string
	Before  *time.Time
	After   *time.Time
	Limit   int
}

type MessageMedia struct {
	MediaType     string `db:"media_type"`
	MediaFileID   string `db:"media_file_id"`
	MediaFilePath string `db:"media_file_path"`
}

type SyncStatusParams struct {
	ChannelID    int64
	StartedAt    time.Time
	FinishedAt   time.Time
	Success      bool
	Error        string
	MessageCount int
}

func Open(ctx context.Context, databaseURL string) (*DB, error) {
	db, err := sqlx.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

func (db *DB) Close() {
	db.db.Close()
}

func (db *DB) Ping(ctx context.Context) error {
	return db.db.PingContext(ctx)
}

func (db *DB) Migrate(ctx context.Context) error {
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	goose.SetBaseFS(migrationFS)
	return goose.UpContext(ctx, db.db.DB, "migrations")
}

func (db *DB) UpsertChannel(ctx context.Context, peerID int64, username, title string) (int64, error) {
	const query = `
insert into channels (peer_id, username, title)
values ($1, nullif($2, ''), $3)
on conflict (peer_id) do update set
  username = excluded.username,
  title = excluded.title,
  updated_at = now()
returning id`
	var id int64
	err := db.db.GetContext(ctx, &id, query, peerID, username, title)
	return id, err
}

func (db *DB) UpsertMessage(ctx context.Context, params UpsertMessageParams) error {
	const query = `
insert into messages (
  channel_id, telegram_message_id, text, media_type, media_file_id, media_file_path, raw_payload, views, forwards, reply_count, telegram_date, edited_at, deleted_at
) values ($1, $2, $3, nullif($4, ''), nullif($5, ''), nullif($6, ''), $7, $8, $9, $10, $11, $12, null)
on conflict (channel_id, telegram_message_id) do update set
  text = excluded.text,
  media_type = excluded.media_type,
  media_file_id = excluded.media_file_id,
  media_file_path = excluded.media_file_path,
  raw_payload = excluded.raw_payload,
  views = excluded.views,
  forwards = excluded.forwards,
  reply_count = excluded.reply_count,
  telegram_date = excluded.telegram_date,
  edited_at = excluded.edited_at,
  deleted_at = null,
  updated_at = now()`
	_, err := db.db.ExecContext(ctx, query,
		params.ChannelID,
		params.TelegramMsgID,
		params.Text,
		params.MediaType,
		params.MediaFileID,
		params.MediaFilePath,
		params.RawPayload,
		params.Views,
		params.Forwards,
		params.ReplyCount,
		params.TelegramDate,
		params.EditedAt,
	)
	return err
}

func (db *DB) RecordSyncStatus(ctx context.Context, params SyncStatusParams) error {
	const query = `
insert into sync_status (
  channel_id, last_started_at, last_finished_at, last_success_at, last_error, last_message_count
) values (
  $1, $2::timestamptz, $3::timestamptz, case when $4 then $3::timestamptz else null end, nullif($5, ''), $6
)
on conflict (channel_id) do update set
  last_started_at = excluded.last_started_at,
  last_finished_at = excluded.last_finished_at,
  last_success_at = case when $4 then excluded.last_success_at else sync_status.last_success_at end,
  last_error = excluded.last_error,
  last_message_count = excluded.last_message_count,
  updated_at = now()`
	_, err := db.db.ExecContext(ctx, query,
		params.ChannelID,
		params.StartedAt,
		params.FinishedAt,
		params.Success,
		params.Error,
		params.MessageCount,
	)
	return err
}

func (db *DB) ListChannels(ctx context.Context) ([]Channel, error) {
	const query = `
select
  c.id,
  c.peer_id,
  coalesce(c.username, '') as username,
  c.title,
  count(m.id)::int as message_count,
  max(m.telegram_date) as latest_message_at,
  s.last_started_at,
  s.last_finished_at,
  s.last_success_at,
  s.last_error,
  c.created_at,
  c.updated_at
from channels c
left join messages m on m.channel_id = c.id and m.deleted_at is null
left join sync_status s on s.channel_id = c.id
group by c.id, s.channel_id
order by coalesce(c.username, c.peer_id::text)`
	channels := make([]Channel, 0)
	err := db.db.SelectContext(ctx, &channels, query)
	return channels, err
}

func (db *DB) MarkMissingDeleted(ctx context.Context, channelID int64, existingIDs []int) error {
	if len(existingIDs) == 0 {
		const query = `
update messages
set deleted_at = now(), updated_at = now()
where channel_id = $1
  and deleted_at is null`
		_, err := db.db.ExecContext(ctx, query, channelID)
		return err
	}

	const query = `
update messages
set deleted_at = now(), updated_at = now()
where channel_id = ?
  and deleted_at is null
  and telegram_message_id not in (?)`
	expanded, args, err := sqlx.In(query, channelID, existingIDs)
	if err != nil {
		return err
	}
	expanded = db.db.Rebind(expanded)
	_, err = db.db.ExecContext(ctx, expanded, args...)
	return err
}

func (db *DB) ListMessages(ctx context.Context, params ListMessagesParams) ([]Message, error) {
	const query = `
select
  m.id,
  m.channel_id,
  m.telegram_message_id,
  m.text,
  coalesce(m.media_type, '') as media_type,
  coalesce(m.media_file_id, '') as media_file_id,
  coalesce(m.media_file_path, '') as media_file_path,
  m.raw_payload,
  m.views,
  m.forwards,
  m.reply_count,
  m.telegram_date,
  m.edited_at,
  m.deleted_at,
  m.created_at,
  m.updated_at,
  coalesce(c.username, '') as channel_username,
  c.title as channel_title
from messages m
join channels c on c.id = m.channel_id
where m.deleted_at is null
  and ($1 = '' or c.username = $1 or c.peer_id::text = $1)
  and ($2::timestamptz is null or m.telegram_date < $2)
  and ($3::timestamptz is null or m.telegram_date > $3)
  and ($4 = '' or m.text ilike '%' || $4 || '%')
order by m.telegram_date desc, m.telegram_message_id desc
limit $5`
	messages := make([]Message, 0)
	err := db.db.SelectContext(ctx, &messages, query, params.Channel, params.Before, params.After, params.Query, params.Limit)
	return messages, err
}

func (db *DB) GetMessageMedia(ctx context.Context, channel string, telegramMsgID int) (*MessageMedia, error) {
	const query = `
select
  coalesce(m.media_type, '') as media_type,
  coalesce(m.media_file_id, '') as media_file_id,
  coalesce(m.media_file_path, '') as media_file_path
from messages m
join channels c on c.id = m.channel_id
where m.deleted_at is null
  and m.telegram_message_id = $2
  and ($1 = '' or c.username = $1 or c.peer_id::text = $1)
limit 1`
	var media MessageMedia
	err := db.db.GetContext(ctx, &media, query, channel, telegramMsgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &media, nil
}

func (db *DB) LatestMessage(ctx context.Context, channel string) (*Message, error) {
	messages, err := db.ListMessages(ctx, ListMessagesParams{Channel: channel, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, nil
	}
	return &messages[0], nil
}
