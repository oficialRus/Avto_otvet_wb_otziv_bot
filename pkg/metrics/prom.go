package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

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
