package config

import (
	"fmt"
	"os"
	"time"
)

// Env variable names (documented for reference)
const (
	envVersion      = "APP_VERSION"
	envLogLevel     = "LOG_LEVEL"
	envWBToken      = "WB_TOKEN"
	envWBBaseURL    = "WB_BASE_URL"
	envPollInterval = "POLL_INTERVAL" // Go duration string, e.g. "10m", "30s"
	envDBPath       = "DB_PATH"
	envTemplateBad  = "TPL_BAD"
	envTemplateGood = "TPL_GOOD"
	envMetricsAddr  = "METRICS_ADDR"
)

// Config aggregates all runtime settings required by the application.
// All fields are immutable after MustLoad().
//
// Defaults are chosen to let the service start locally with minimal env-vars,
// while sensitive/mandatory settings (e.g. WB_TOKEN) must be supplied.
//
// NOTE: To keep the MVP lightweight, we avoid external deps like envconfig/viper.
// Parsing relies solely on the standard library.
//
// Example:
//
//	WB_TOKEN=xxxxx LOG_LEVEL=debug go run ./cmd/feedback-bot
//
// Critical errors in configuration cause a panic via MustLoad().
// In production, build systems can allow overriding defaults with ldflags.
//
//	go build -ldflags "-X github.com/yourorg/feedback-bot/internal/config.defaultVersion=$(git rev-parse --short HEAD)" ...
//
// Time‑zone: Europe/Helsinki (2025‑08‑01). All absolute times should respect that TZ,
// but durations like PollInterval are time‑zone agnostic.
//
// Changes to this struct ripple through the entire project, so keep it minimal.
// Long‑term we might migrate to a more robust config layer with per‑env YAML + env‑override.
//
// (in future, to enable DI)
//
//go:generate go run github.com/google/wire/cmd/wire
type Config struct {
	Version      string        // app semantic version or git SHA
	LogLevel     string        // debug, info, warn, error, fatal (zap levels)
	WBToken      string        // Bearer token with Feedback scope bit 7
	WBBaseURL    string        // https://feedbacks-api.wildberries.ru or sandbox URL
	PollInterval time.Duration // polling interval, default 10m
	DBPath       string        // path to SQLite file (or DSN for other drivers)
	TemplateBad  string        // reply text for 1–3★ reviews
	TemplateGood string        // reply text for 4–5★ reviews
	MetricsAddr  string        // listen address for Prometheus endpoint, default :8080
}

var (
	defaultVersion      = "dev"
	defaultLogLevel     = "info"
	defaultWBBaseURL    = "https://feedbacks-api.wildberries.ru"
	defaultPollInterval = 10 * time.Minute
	defaultDBPath       = "data/feedbacks.db"
	defaultTemplateBad  = "Здравствуйте! Благодарим за ваш отзыв. Сожалеем, что товар не оправдал ожиданий. Мы уже анализируем проблему и постараемся улучшить качество."
	defaultTemplateGood = "Спасибо за ваш отзыв! Нам приятно, что товар вам понравился. Хорошего дня и удачных покупок!"
	defaultMetricsAddr  = ":8080"
)

// MustLoad is a convenience wrapper around Load() that panics on error.
// Preferable in main() early startup where configuration problems are fatal.
func MustLoad() Config {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}

// Load reads environment variables, applies defaults, validates the result
// and returns a ready-to-use Config instance.
func Load() (Config, error) {
	var cfg Config

	cfg.Version = getEnv(envVersion, defaultVersion)
	cfg.LogLevel = getEnv(envLogLevel, defaultLogLevel)
	cfg.WBToken = os.Getenv(envWBToken) // required, no default
	cfg.WBBaseURL = getEnv(envWBBaseURL, defaultWBBaseURL)

	// PollInterval parsing
	if s := os.Getenv(envPollInterval); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return Config{}, fmt.Errorf("invalid %s: %w", envPollInterval, err)
		}
		cfg.PollInterval = d
	} else {
		cfg.PollInterval = defaultPollInterval
	}

	cfg.DBPath = getEnv(envDBPath, defaultDBPath)
	cfg.TemplateBad = getEnv(envTemplateBad, defaultTemplateBad)
	cfg.TemplateGood = getEnv(envTemplateGood, defaultTemplateGood)
	cfg.MetricsAddr = getEnv(envMetricsAddr, defaultMetricsAddr)

	// Validation
	if cfg.WBToken == "" {
		return Config{}, fmt.Errorf("%s is required", envWBToken)
	}
	if cfg.PollInterval < time.Minute {
		return Config{}, fmt.Errorf("poll interval too small (>=1m)")
	}
	return cfg, nil
}

// getEnv returns the value of the environment variable if set, otherwise def.
func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
