package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadAPIOnlyConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TELEGRAM_ENABLED", "false")
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("RATE_LIMIT_RPS", "3.5")
	t.Setenv("RATE_LIMIT_BURST", "7")
	t.Setenv("DEFAULT_PAGE_LIMIT", "25")
	t.Setenv("MAX_PAGE_LIMIT", "75")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("TRUST_PROXY_HEADERS", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.API.RateLimitRPS != 3.5 || cfg.API.RateLimitBurst != 7 {
		t.Fatalf("rate limit = %+v", cfg.API)
	}
	if cfg.API.DefaultLimit != 25 || cfg.API.MaxLimit != 75 {
		t.Fatalf("page limits = %+v", cfg.API)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("LogLevel = %v", cfg.LogLevel)
	}
	if !cfg.API.TrustProxy {
		t.Fatal("expected trusted proxy headers")
	}
	if cfg.Telegram.Enabled {
		t.Fatal("telegram should be disabled")
	}
}

func TestLoadBotTelegramConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TELEGRAM_ENABLED", "true")
	t.Setenv("TELEGRAM_SOURCE", "bot")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_BOT_POLL_TIMEOUT", "30s")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram.Source != "bot" || cfg.Telegram.BotToken != "123:abc" {
		t.Fatalf("bot config = %+v", cfg.Telegram)
	}
	if cfg.Telegram.BotMode != "longpoll" || cfg.Telegram.BotWebhook != "/telegram/webhook" {
		t.Fatalf("bot receive config = %+v", cfg.Telegram)
	}
	if cfg.Telegram.PollTimeout != 30*time.Second {
		t.Fatalf("poll timeout = %v", cfg.Telegram.PollTimeout)
	}
}

func TestLoadBotWebhookConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TELEGRAM_ENABLED", "true")
	t.Setenv("TELEGRAM_SOURCE", "bot")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_BOT_RECEIVE_MODE", "webhook")
	t.Setenv("TELEGRAM_BOT_SECRET_TOKEN", "secret")
	t.Setenv("TELEGRAM_BOT_WEBHOOK_PATH", "/custom/webhook")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram.BotMode != "webhook" || cfg.Telegram.BotSecret != "secret" || cfg.Telegram.BotWebhook != "/custom/webhook" {
		t.Fatalf("bot webhook config = %+v", cfg.Telegram)
	}
}

func TestLoadMTProtoTelegramConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TELEGRAM_ENABLED", "true")
	t.Setenv("TELEGRAM_SOURCE", "mtproto")
	t.Setenv("TELEGRAM_API_ID", "123")
	t.Setenv("TELEGRAM_API_HASH", "hash")
	t.Setenv("TELEGRAM_PHONE", "+15551234567")
	t.Setenv("TELEGRAM_CHANNELS", " one, @two ,,")
	t.Setenv("TELEGRAM_POLL_INTERVAL", "30s")
	t.Setenv("TELEGRAM_BATCH_LIMIT", "42")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram.APIID != 123 || cfg.Telegram.APIHash != "hash" {
		t.Fatalf("telegram credentials = %+v", cfg.Telegram)
	}
	if cfg.Telegram.Phone != "+15551234567" {
		t.Fatalf("phone = %q", cfg.Telegram.Phone)
	}
	if got, want := cfg.Telegram.Channels, []string{"one", "@two"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("channels = %#v", got)
	}
	if cfg.Telegram.PollInterval != 30*time.Second || cfg.Telegram.BatchLimit != 42 {
		t.Fatalf("telegram sync settings = %+v", cfg.Telegram)
	}
}

func TestLoadRequiresBotTokenWhenBotEnabled(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TELEGRAM_ENABLED", "true")
	t.Setenv("TELEGRAM_SOURCE", "bot")

	if _, err := Load(); err == nil {
		t.Fatal("expected missing bot token error")
	}
}

func TestLoadRequiresBotWebhookSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TELEGRAM_ENABLED", "true")
	t.Setenv("TELEGRAM_SOURCE", "bot")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_BOT_RECEIVE_MODE", "webhook")

	if _, err := Load(); err == nil {
		t.Fatal("expected missing webhook secret error")
	}
}

func TestLoadRequiresTelegramPhoneWhenMTProtoEnabled(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TELEGRAM_ENABLED", "true")
	t.Setenv("TELEGRAM_SOURCE", "mtproto")
	t.Setenv("TELEGRAM_API_ID", "123")
	t.Setenv("TELEGRAM_API_HASH", "hash")
	t.Setenv("TELEGRAM_CHANNELS", "one")

	if _, err := Load(); err == nil {
		t.Fatal("expected missing phone error")
	}
}

func TestLoadRejectsInvalidPageLimits(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TELEGRAM_ENABLED", "false")
	t.Setenv("DEFAULT_PAGE_LIMIT", "100")
	t.Setenv("MAX_PAGE_LIMIT", "50")

	if _, err := Load(); err == nil {
		t.Fatal("expected page limit error")
	}
}
