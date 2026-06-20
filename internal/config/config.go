package config

import (
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"tg-channel-archive-api/internal/api"
	"tg-channel-archive-api/internal/telegram"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	LogLevel    slog.Level
	API         api.Config
	Telegram    telegram.Config
}

func Load() (Config, error) {
	apiID, err := intEnv("TELEGRAM_API_ID", 0)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddr:    stringEnv("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		LogLevel:    logLevelEnv("LOG_LEVEL", slog.LevelInfo),
		API: api.Config{
			RateLimitRPS:   floatEnv("RATE_LIMIT_RPS", 2),
			RateLimitBurst: intEnvDefault("RATE_LIMIT_BURST", 10),
			CORSOrigin:     stringEnv("CORS_ORIGIN", "*"),
			DefaultLimit:   intEnvDefault("DEFAULT_PAGE_LIMIT", 50),
			MaxLimit:       intEnvDefault("MAX_PAGE_LIMIT", 100),
			MediaBaseURL:   mediaBaseURL(os.Getenv("TELEGRAM_BOT_TOKEN")),
			TrustProxy:     boolEnv("TRUST_PROXY_HEADERS", false),
		},
		Telegram: telegram.Config{
			Enabled:      boolEnv("TELEGRAM_ENABLED", true),
			Source:       stringEnv("TELEGRAM_SOURCE", "bot"),
			APIID:        apiID,
			APIHash:      os.Getenv("TELEGRAM_API_HASH"),
			Phone:        os.Getenv("TELEGRAM_PHONE"),
			Password:     os.Getenv("TELEGRAM_PASSWORD"),
			BotToken:     os.Getenv("TELEGRAM_BOT_TOKEN"),
			BotMode:      stringEnv("TELEGRAM_BOT_RECEIVE_MODE", "longpoll"),
			BotSecret:    os.Getenv("TELEGRAM_BOT_SECRET_TOKEN"),
			BotWebhook:   stringEnv("TELEGRAM_BOT_WEBHOOK_PATH", "/telegram/webhook"),
			SessionFile:  stringEnv("TELEGRAM_SESSION_FILE", "telegram.session.json"),
			Channels:     csvEnv("TELEGRAM_CHANNELS"),
			PollInterval: durationEnv("TELEGRAM_POLL_INTERVAL", 5*time.Minute),
			PollTimeout:  durationEnv("TELEGRAM_BOT_POLL_TIMEOUT", 50*time.Second),
			BatchLimit:   intEnvDefault("TELEGRAM_BATCH_LIMIT", 100),
		},
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.API.RateLimitRPS <= 0 {
		return Config{}, errors.New("RATE_LIMIT_RPS must be positive")
	}
	if cfg.API.RateLimitBurst <= 0 {
		return Config{}, errors.New("RATE_LIMIT_BURST must be positive")
	}
	if cfg.API.DefaultLimit <= 0 || cfg.API.MaxLimit <= 0 || cfg.API.DefaultLimit > cfg.API.MaxLimit {
		return Config{}, errors.New("page limits must be positive and DEFAULT_PAGE_LIMIT must be <= MAX_PAGE_LIMIT")
	}
	if cfg.Telegram.Enabled {
		switch cfg.Telegram.Source {
		case "bot":
			if cfg.Telegram.BotToken == "" {
				return Config{}, errors.New("TELEGRAM_BOT_TOKEN is required when TELEGRAM_SOURCE=bot")
			}
			if cfg.Telegram.PollTimeout <= 0 {
				return Config{}, errors.New("TELEGRAM_BOT_POLL_TIMEOUT must be positive")
			}
			switch cfg.Telegram.BotMode {
			case "longpoll":
			case "webhook":
				if cfg.Telegram.BotSecret == "" {
					return Config{}, errors.New("TELEGRAM_BOT_SECRET_TOKEN is required when TELEGRAM_BOT_RECEIVE_MODE=webhook")
				}
				if !strings.HasPrefix(cfg.Telegram.BotWebhook, "/") {
					return Config{}, errors.New("TELEGRAM_BOT_WEBHOOK_PATH must start with /")
				}
			default:
				return Config{}, errors.New("TELEGRAM_BOT_RECEIVE_MODE must be longpoll or webhook")
			}
		case "mtproto":
			if cfg.Telegram.APIID == 0 || cfg.Telegram.APIHash == "" {
				return Config{}, errors.New("TELEGRAM_API_ID and TELEGRAM_API_HASH are required when TELEGRAM_SOURCE=mtproto")
			}
			if cfg.Telegram.Phone == "" {
				return Config{}, errors.New("TELEGRAM_PHONE is required when TELEGRAM_SOURCE=mtproto")
			}
			if len(cfg.Telegram.Channels) == 0 {
				return Config{}, errors.New("TELEGRAM_CHANNELS is required when TELEGRAM_SOURCE=mtproto")
			}
		default:
			return Config{}, errors.New("TELEGRAM_SOURCE must be bot or mtproto")
		}
	}

	return cfg, nil
}

func mediaBaseURL(botToken string) string {
	if strings.TrimSpace(botToken) == "" {
		return ""
	}
	return "https://api.telegram.org/file/bot" + botToken
}

func stringEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func csvEnv(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func boolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func intEnv(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func intEnvDefault(key string, fallback int) int {
	parsed, err := intEnv(key, fallback)
	if err != nil {
		return fallback
	}
	return parsed
}

func floatEnv(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func logLevelEnv(key string, fallback slog.Level) slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "info", "":
		return slog.LevelInfo
	default:
		return fallback
	}
}
