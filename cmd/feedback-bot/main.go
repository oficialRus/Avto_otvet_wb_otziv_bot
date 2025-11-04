package main

import (
	"context"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"feedback_bot/internal/config"
	"feedback_bot/internal/telegram"
	"feedback_bot/internal/storage"
	"feedback_bot/pkg/logger"
	"feedback_bot/pkg/metrics"
)

// maskDSN masks sensitive information in PostgreSQL DSN for logging
func maskDSN(dsn string) string {
	if strings.Contains(dsn, "password=") {
		parts := strings.Split(dsn, " ")
		for i, part := range parts {
			if strings.HasPrefix(part, "password=") {
				parts[i] = "password=***"
				break
			}
		}
		return strings.Join(parts, " ")
	}
	return dsn
}

func main() {
	// 1. Load configuration
	cfg := config.MustLoad()

	// 2. Init structured logger (zap based)
	log := logger.New(cfg.LogLevel)
	defer logger.Sync(log)

	log.Infow("starting feedback-bot", "version", cfg.Version)
	
	// Log admin user ID if configured
	if cfg.AdminUserID != 0 {
		log.Infow("admin user configured", "admin_user_id", cfg.AdminUserID)
	} else {
		log.Warnw("admin user not configured", "tip", "Set ADMIN_USER_ID environment variable to enable /admin command")
	}
	
	// Log channel subscription check configuration
	if cfg.RequiredChannelID != 0 || cfg.RequiredChannel != "" {
		if cfg.RequiredChannelID != 0 {
			log.Infow("channel subscription check enabled", "channel_id", cfg.RequiredChannelID, "channel", cfg.RequiredChannel)
		} else {
			log.Infow("channel subscription check enabled", "channel", cfg.RequiredChannel)
		}
	} else {
		log.Warnw("channel subscription check disabled", "tip", "Set REQUIRED_CHANNEL_ID or REQUIRED_CHANNEL environment variable to enable subscription check")
	}

	// 3. Root context with graceful shutdown on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 4. Expose Prometheus metrics endpoint (optional)
	metricsSrv := metrics.MustServe(cfg.MetricsAddr, log)

	// 5. Storage for processed feedback IDs and user configurations
	// Supports both SQLite (default) and PostgreSQL
	var store storage.Store
	var configStore storage.ConfigStore
	
	var err error
	if cfg.DBType == "postgres" {
		log.Infow("initializing PostgreSQL storage", "dsn", maskDSN(cfg.DBPath))
		store, configStore, err = storage.NewPostgreSQL(cfg.DBPath)
		if err != nil {
			log.Fatalw("init PostgreSQL storage failed", "err", err)
		}
	} else {
		log.Infow("initializing SQLite storage", "path", cfg.DBPath)
		store, configStore, err = storage.NewSQLite(cfg.DBPath)
		if err != nil {
			log.Fatalw("init SQLite storage failed", "err", err)
		}
	}
	defer store.Close()

	// 6. Initialize and start Telegram bot (required)
	// Bot will handle service initialization after user provides configuration
	tgBot, err := telegram.New(cfg.TelegramToken, configStore, store, log, ctx, cfg.RequiredChannel, cfg.RequiredChannelID, cfg.AdminUserID)
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

	// Shutdown bot (stops all schedulers)
	tgBot.Shutdown()
	
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		log.Warnw("metrics server shutdown error", "err", err)
	}

	log.Info("bye")
}
