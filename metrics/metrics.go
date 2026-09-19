// Package metrics collects download statistics and exposes them through
// a Prometheus HTTP endpoint.
package metrics

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// DownloadBytesTotal counts all bytes written to disk by the downloader.
	DownloadBytesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lux_download_bytes_total",
		Help: "Total number of bytes downloaded.",
	})
	// DownloadSpeedBps is the download speed of the most recent file in bytes/second.
	DownloadSpeedBps = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lux_download_speed_bytes_per_second",
		Help: "Download speed of the most recent download in bytes per second.",
	})
	// DownloadsTotal counts finished downloads by status (success/failure).
	DownloadsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "lux_downloads_total",
		Help: "Total number of downloads by status.",
	}, []string{"status"})
	// RetriesTotal counts download/HTTP retry attempts.
	RetriesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lux_download_retries_total",
		Help: "Total number of retry attempts during downloads.",
	})
)

// RecordDownload records the result of one download task.
func RecordDownload(size int64, duration time.Duration, err error) {
	if err != nil {
		DownloadsTotal.WithLabelValues("failure").Inc()
		return
	}
	DownloadsTotal.WithLabelValues("success").Inc()
	if duration > 0 && size > 0 {
		DownloadSpeedBps.Set(float64(size) / duration.Seconds())
	}
}

// StartServer starts an HTTP server exposing the /metrics endpoint on the
// given port. It runs in the background and returns immediately.
func StartServer(port int) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		slog.Info("metrics server started", slog.String("addr", server.Addr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics server error", slog.String("error", err.Error()))
		}
	}()
}
