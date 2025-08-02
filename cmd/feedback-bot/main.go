package main

import (
	"context"
	"os/signal"
	"syscall"
	"time"

	"feedback_bot/internal/config"
	"feedback_bot/internal/scheduler"

	"feedback_bot/internal/service"

	"feedback_bot/internal/storage"

	"feedback_bot/internal/wbapi"

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

	// 4. Expose Prometheus metrics endpoint
	metricsSrv := metrics.MustServe(cfg.MetricsAddr, log)

	// 5. Build WB API client with optional rate limit (3 rps, burst 6)
	wbClient := wbapi.New(
		cfg.WBToken,
		wbapi.WithBaseURL(cfg.WBBaseURL),
		wbapi.WithRateLimit(3, 6),
		wbapi.WithLogger(log),
	)

	// 6. Storage for processed feedback IDs (SQLite)
	store, err := storage.NewSQLite(cfg.DBPath)
	if err != nil {
		log.Fatalw("init storage failed", "err", err)
	}
	defer store.Close()

	// 7. Compose service (business logic)
	const maxTake = 5000
	svc := service.New(
		wbClient,
		store,
		cfg.TemplateBad,
		cfg.TemplateGood,
		log,
		maxTake,
	)

	// 8. Start scheduler
	poller := scheduler.New(cfg.PollInterval, svc.HandleCycle, log)
	go poller.Run(ctx)

	// 9. Wait for termination signal
	<-ctx.Done()
	log.Info("shutdown signal received, shutting down ...")

	// 10. Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poller.Shutdown()

	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		log.Warnw("metrics server shutdown error", "err", err)
	}

	log.Info("bye")
}
