package httptransport

import (
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests.",
		},
		[]string{"path", "method", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path", "method"},
	)
)

// MetricsMiddleware records Prometheus metrics for each HTTP request.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		path := r.URL.Path // fallback; chi pattern matching happens per-route.

		defer func() {
			httpRequestsTotal.WithLabelValues(path, r.Method, http.StatusText(ww.Status())).Inc()
			httpRequestDuration.WithLabelValues(path, r.Method).Observe(time.Since(start).Seconds())
		}()

		next.ServeHTTP(ww, r)
	})
}

// sensitiveFieldRegex matches sensitive keys in JSON body for redaction.
var sensitiveFieldRegex = regexp.MustCompile(`(?i)("(?:password|token|authorization)"\s*:\s*)"[^"]*"`)

// RedactSensitiveFields replaces sensitive field values in log output.
func RedactSensitiveFields(s string) string {
	return sensitiveFieldRegex.ReplaceAllString(s, `${1}"[REDACTED]"`)
}
