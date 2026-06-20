# Telegram Channel Archive API

Golang backend that periodically synchronizes Telegram channel history into Postgres and exposes rate-limited read APIs for a blog frontend.

## Features

- Periodically fetches full channel history with Telegram MTProto.
- Upserts channels and messages into Postgres.
- Reconciles missing message IDs as deleted during full sync.
- Exposes JSON APIs for recent messages and latest message.
- Applies per-client-IP rate limiting to public API endpoints.

## Setup

1. Create a bot with <https://t.me/BotFather>.
2. Add the bot as an administrator of each channel you want to archive.
3. Start Postgres:

```sh
docker compose up -d postgres
```

4. Configure environment:

```sh
cp .env.example .env
```

Fill `TELEGRAM_BOT_TOKEN`. The default `TELEGRAM_SOURCE=bot` uses Telegram Bot API long polling and receives new channel posts and edits.
Set `TELEGRAM_CHANNELS` to an optional comma-separated allowlist of channel usernames or chat IDs. If it is empty, the bot stores posts from every channel where it is an administrator.

5. Run the server:

```sh
set -a
. ./.env
set +a
go run ./cmd/server
```

Bot mode does not fetch historical messages. If you need historical backfill and full reconciliation, set `TELEGRAM_SOURCE=mtproto` and fill `TELEGRAM_API_ID`, `TELEGRAM_API_HASH`, `TELEGRAM_PHONE`, and `TELEGRAM_CHANNELS`; on first run, `gotd/td` may require interactive Telegram login in the terminal to create `telegram.session.json`.

Bot mode supports two receive modes:

- `TELEGRAM_BOT_RECEIVE_MODE=longpoll`: default; the service calls `getUpdates`.
- `TELEGRAM_BOT_RECEIVE_MODE=webhook`: exposes `POST /telegram/webhook` by default. Set `TELEGRAM_BOT_SECRET_TOKEN` and configure Telegram with the same secret token:

```sh
curl "https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/setWebhook" \
  -d "url=https://example.com/telegram/webhook" \
  -d "secret_token=$TELEGRAM_BOT_SECRET_TOKEN" \
  -d 'allowed_updates=["channel_post","edited_channel_post"]'
```

For an API-only local stack:

```sh
TELEGRAM_ENABLED=false docker compose up --build api
```

Database migrations are managed by `goose` and embedded into the server binary from `internal/store/migrations`. The server runs pending migrations automatically on startup.

## API

### `GET /healthz`

Returns service health. This endpoint pings Postgres and returns `503` if the database is unavailable.

### `GET /api/channels`

Returns archived channels with message counts and sync status metadata.

Example:

```sh
curl 'http://localhost:8080/api/channels'
```

### `GET /api/messages`

Query parameters:

- `channel`: optional channel username or peer ID.
- `q`: optional case-insensitive text search.
- `limit`: optional page size, capped by `MAX_PAGE_LIMIT`.
- `before`: optional RFC3339 timestamp for pagination.
- `after`: optional RFC3339 timestamp.

Example:

```sh
curl 'http://localhost:8080/api/messages?channel=some_public_channel&q=keyword&limit=20'
```

The response includes `has_more` and `next_before`. To load the next older page, call the same endpoint again with `before=<next_before>`.

### `GET /api/messages/latest`

Query parameters:

- `channel`: optional channel username or peer ID.

Example:

```sh
curl 'http://localhost:8080/api/messages/latest?channel=some_public_channel'
```

### `GET /api/media`

Proxies Telegram media through the backend so the blog frontend does not need the bot token.

Query parameters:

- `channel`: channel username or peer ID.
- `message_id`: Telegram message ID from `telegram_message_id`.

Example:

```sh
curl 'http://localhost:8080/api/media?channel=some_public_channel&message_id=123'
```

## Configuration

See `.env.example` for all settings.

Set `TELEGRAM_ENABLED=false` to run only the API against existing database rows.

Set `TRUST_PROXY_HEADERS=true` only when the service is behind a trusted reverse proxy that overwrites `X-Forwarded-For` or `X-Real-IP`; otherwise clients could spoof those headers to bypass rate limits.

JSON API endpoints send `Cache-Control: no-store`. Media proxy responses send `MEDIA_CACHE_CONTROL` when `MEDIA_CACHE_ENABLED=true`; set `MEDIA_CACHE_ENABLED=false` to force `Cache-Control: no-store` for `/api/media` too.
