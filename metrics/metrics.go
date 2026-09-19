package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const metricsPath = "/metrics"

var (
	registry = prometheus.NewRegistry()

	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lux_http_requests_total",
			Help: "Total number of completed HTTP requests.",
		},
		[]string{"method", "host", "status"},
	)
	httpRequestErrorsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "lux_http_request_errors_total",
			Help: "Total number of HTTP requests that failed with a transport error.",
		},
	)
	httpRequestRetriesTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "lux_http_request_retries_total",
			Help: "Total number of HTTP request retries.",
		},
	)

	downloadsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lux_downloads_total",
			Help: "Total number of video downloads by result.",
		},
		[]string{"result"},
	)
	downloadsInFlight = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "lux_downloads_in_flight",
			Help: "Number of video downloads currently in progress.",
		},
	)
	downloadRetriesTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "lux_download_retries_total",
			Help: "Total number of retried chunk downloads.",
		},
	)
	downloadBytesTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "lux_download_bytes_total",
			Help: "Total number of bytes downloaded.",
		},
	)
	downloadSpeedBytesPerSecond = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "lux_download_speed_bytes_per_second",
			Help: "Average download speed in bytes per second of the most recent completed download.",
		},
	)
	downloadSuccessRatio = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "lux_download_success_ratio",
			Help: "Cumulative download success ratio, from 0 to 1.",
		},
	)
	downloadDurationSeconds = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "lux_download_duration_seconds",
			Help:    "Time spent downloading a single video.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 16),
		},
	)

	successfulDownloads uint64
	failedDownloads     uint64

	startOnce sync.Once
	server    *http.Server
)

func init() {
	registry.MustRegister(
		httpRequestsTotal,
		httpRequestErrorsTotal,
		httpRequestRetriesTotal,
		downloadsTotal,
		downloadsInFlight,
		downloadRetriesTotal,
		downloadBytesTotal,
		downloadSpeedBytesPerSecond,
		downloadSuccessRatio,
		downloadDurationSeconds,
	)
}

// IncHTTPRequest records a completed HTTP request by status category.
// statusClass is the response status code, or 0 for transport errors.
func IncHTTPRequest(method, host string, statusClass int) {
	if statusClass == 0 {
		httpRequestErrorsTotal.Inc()
		return
	}
	httpRequestsTotal.WithLabelValues(method, host, statusLabel(statusClass)).Inc()
}

// IncHTTPRetry records one HTTP-level retry attempt.
func IncHTTPRetry() {
	httpRequestRetriesTotal.Inc()
}

// IncDownloadRetry records one retry of a download chunk.
func IncDownloadRetry() {
	downloadRetriesTotal.Inc()
}

// IncDownloadInFlight marks the start of a video download.
func IncDownloadInFlight() {
	downloadsInFlight.Inc()
}

// DecDownloadInFlight marks the end of a video download.
func DecDownloadInFlight() {
	downloadsInFlight.Dec()
}

// ObserveDownload records the result, bytes transferred and wall-clock duration
// of a single video download.
func ObserveDownload(success bool, bytes int64, duration time.Duration) {
	result := "success"
	if success {
		total := atomic.AddUint64(&successfulDownloads, 1) + atomic.LoadUint64(&failedDownloads)
		downloadSuccessRatio.Set(float64(atomic.LoadUint64(&successfulDownloads)) / float64(total))
	} else {
		result = "failure"
		total := atomic.LoadUint64(&successfulDownloads) + atomic.AddUint64(&failedDownloads, 1)
		if total > 0 {
			downloadSuccessRatio.Set(float64(atomic.LoadUint64(&successfulDownloads)) / float64(total))
		}
	}
	downloadsTotal.WithLabelValues(result).Inc()
	downloadDurationSeconds.Observe(duration.Seconds())
	if bytes > 0 {
		downloadBytesTotal.Add(float64(bytes))
	}
	if duration > 0 {
		downloadSpeedBytesPerSecond.Set(float64(bytes) / duration.Seconds())
	}
}

func statusLabel(statusCode int) string {
	switch {
	case statusCode >= 500:
		return "5xx"
	case statusCode >= 400:
		return "4xx"
	case statusCode >= 300:
		return "3xx"
	case statusCode >= 200:
		return "2xx"
	default:
		return "1xx"
	}
}

// Start serves Prometheus metrics on the given port at /metrics.
// A non-positive port disables the metrics endpoint.
func Start(port int) {
	if port <= 0 {
		return
	}
	startOnce.Do(func() {
		mux := http.NewServeMux()
		mux.Handle(metricsPath, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
		server = &http.Server{
			Addr:              ":" + strconv.Itoa(port),
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			slog.Info("metrics endpoint listening", "addr", "http://localhost"+server.Addr+metricsPath)
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("metrics server error", "error", err)
			}
		}()
	})
}

// Shutdown gracefully stops the metrics endpoint.
func Shutdown(ctx context.Context) {
	if server == nil {
		return
	}
	if err := server.Shutdown(ctx); err != nil {
		slog.Error("metrics server shutdown error", "error", err)
	}
}
