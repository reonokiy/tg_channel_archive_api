package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"tg-channel-archive-api/internal/api"
	"tg-channel-archive-api/internal/config"
	"tg-channel-archive-api/internal/store"
	"tg-channel-archive-api/internal/telegram"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		slog.Error("run migrations", "error", err)
		os.Exit(1)
	}

	var botReceiver *telegram.BotReceiver
	if cfg.Telegram.Enabled {
		switch cfg.Telegram.Source {
		case "bot":
			botReceiver = telegram.NewBotReceiver(db, cfg.Telegram)
			switch cfg.Telegram.BotMode {
			case "longpoll":
				go botReceiver.Run(ctx)
			case "webhook":
				slog.Info("telegram bot webhook enabled", "path", cfg.Telegram.BotWebhook)
			}
		case "mtproto":
			syncer := telegram.NewSyncer(db, cfg.Telegram)
			go syncer.Run(ctx)
		}
	} else {
		slog.Warn("telegram sync disabled; API will serve existing database rows only")
	}

	handler := api.NewServer(db, cfg.API)
	routes := chi.NewRouter()
	if botReceiver != nil && cfg.Telegram.BotMode == "webhook" {
		routes.Post(cfg.Telegram.BotWebhook, botReceiver.WebhookHandler)
	}
	routes.Mount("/", handler.Routes())

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           routes,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("http server listening", "addr", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown", "error", err)
	}
}
