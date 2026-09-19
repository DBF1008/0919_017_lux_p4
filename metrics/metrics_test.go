package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestRecordDownload(t *testing.T) {
	RecordDownload(1024, time.Second, nil)
	RecordDownload(0, 0, errTest)
	DownloadBytesTotal.Add(100)
	RetriesTotal.Inc()

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lux_download_bytes_total",
		"lux_download_speed_bytes_per_second",
		"lux_downloads_total",
		"lux_download_retries_total",
	} {
		if !names[want] {
			t.Errorf("metric %s not found", want)
		}
	}
}

var errTest = errString("test error")

type errString string

func (e errString) Error() string { return string(e) }
