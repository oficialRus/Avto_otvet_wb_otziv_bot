package main

import (
	"context"
	"os/signal"
	"syscall"
	"time"

	"feedback_bot/internal/config"
	"feedback_bot/internal/telegram"
	"feedback_bot/internal/storage"
	"feedback_bot/pkg/logger"
	"feedback_bot/pkg/metrics"
)

func main() {
	// 1. Load configuration
	cfg := config.MustLoad()

	// 2. Init structured logger (zap based)
	log := logger.New(cfg.LogLevel)
	defer logger.Sync(log)

	log.Infow("starting feedback-bot", "version", cfg.Version)

	// 3. Root context with graceful shutdown on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 4. Expose Prometheus metrics endpoint (optional)
	metricsSrv := metrics.MustServe(cfg.MetricsAddr, log)

	// 5. Storage for processed feedback IDs and user configurations (SQLite)
	store, configStore, err := storage.NewSQLite(cfg.DBPath)
	if err != nil {
		log.Fatalw("init storage failed", "err", err)
	}
	defer store.Close()

	// 6. Initialize and start Telegram bot (required)
	// Bot will handle service initialization after user provides configuration
	tgBot, err := telegram.New(cfg.TelegramToken, configStore, store, log, ctx)
	if err != nil {
		log.Fatalw("failed to initialize telegram bot", "err", err)
	}

	// 7. Start Telegram bot (main interface)
	go tgBot.Run(ctx)
	log.Info("telegram bot started - waiting for user configuration")

	// 8. Wait for termination signal
	<-ctx.Done()
	log.Info("shutdown signal received, shutting down ...")

	// 9. Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		log.Warnw("metrics server shutdown error", "err", err)
	}

	log.Info("bye")
}
