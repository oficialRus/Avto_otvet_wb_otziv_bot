package metrics

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

var (
	// ActiveUsers tracks the number of active users (users with configured services)
	ActiveUsers = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "feedback_bot_active_users_total",
			Help: "Total number of active users with configured services",
		},
	)

	// ProcessedFeedbacks tracks the number of processed feedbacks
	ProcessedFeedbacks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "feedback_bot_processed_feedbacks_total",
			Help: "Total number of processed feedbacks",
		},
		[]string{"user_id", "status"}, // status: answered, skipped, failed
	)

	// RateLimitHits tracks rate limit violations
	RateLimitHits = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "feedback_bot_rate_limit_hits_total",
			Help: "Total number of rate limit violations",
		},
		[]string{"user_id"},
	)

	// DatabaseErrors tracks database errors
	DatabaseErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "feedback_bot_database_errors_total",
			Help: "Total number of database errors",
		},
		[]string{"operation"}, // operation: get_config, save_config, exists, save
	)

	// APIErrors tracks API errors
	APIErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "feedback_bot_api_errors_total",
			Help: "Total number of API errors",
		},
		[]string{"api", "operation"}, // api: wb, telegram; operation: fetch, answer, send_message
	)
)

func init() {
	// Register all metrics
	prometheus.MustRegister(ActiveUsers)
	prometheus.MustRegister(ProcessedFeedbacks)
	prometheus.MustRegister(RateLimitHits)
	prometheus.MustRegister(DatabaseErrors)
	prometheus.MustRegister(APIErrors)
}

// MustServe exposes Prometheus metrics on the given address (e.g., ":8080").
// It registers the default Prometheus handler and launches http.Server in a
// separate goroutine. Fatal‑logs on startup failure. Returns the server so the
// caller can gracefully shutdown.
//
// Example usage:
//
//	srv := metrics.MustServe(":8080", log)
//	// later: srv.Shutdown(ctx)
func MustServe(addr string, log *zap.SugaredLogger) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		log.Infow("metrics endpoint listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalw("metrics server failed", "err", err)
		}
	}()

	return srv
}

// Helper functions for updating metrics

// UpdateActiveUsers updates the active users metric
func UpdateActiveUsers(count int) {
	ActiveUsers.Set(float64(count))
}

// IncrementProcessedFeedback increments processed feedback counter
func IncrementProcessedFeedback(userID int64, status string) {
	ProcessedFeedbacks.WithLabelValues(strconv.FormatInt(userID, 10), status).Inc()
}

// IncrementRateLimitHit increments rate limit hit counter
func IncrementRateLimitHit(userID int64) {
	RateLimitHits.WithLabelValues(strconv.FormatInt(userID, 10)).Inc()
}

// IncrementDatabaseError increments database error counter
func IncrementDatabaseError(operation string) {
	DatabaseErrors.WithLabelValues(operation).Inc()
}

// IncrementAPIError increments API error counter
func IncrementAPIError(api, operation string) {
	APIErrors.WithLabelValues(api, operation).Inc()
}
