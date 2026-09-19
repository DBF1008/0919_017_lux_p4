package metrics

import (
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

func counterValue(t *testing.T, c interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	return m.GetCounter().GetValue()
}

func gaugeValue(t *testing.T, g interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := g.Write(m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	return m.GetGauge().GetValue()
}

func TestStatusLabel(t *testing.T) {
	testCases := []struct {
		code int
		want string
	}{
		{100, "1xx"}, {200, "2xx"}, {301, "3xx"}, {404, "4xx"}, {500, "5xx"},
	}
	for _, tc := range testCases {
		if got := statusLabel(tc.code); got != tc.want {
			t.Errorf("statusLabel(%d) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

func TestIncHTTPRequest(t *testing.T) {
	before := counterValue(t, httpRequestErrorsTotal)
	IncHTTPRequest("GET", "example.com", 0)
	if got := counterValue(t, httpRequestErrorsTotal); got != before+1 {
		t.Fatalf("httpRequestErrorsTotal = %v, want %v", got, before+1)
	}
}

func TestIncDownloadRetry(t *testing.T) {
	before := counterValue(t, downloadRetriesTotal)
	IncDownloadRetry()
	IncDownloadRetry()
	if got := counterValue(t, downloadRetriesTotal); got != before+2 {
		t.Fatalf("downloadRetriesTotal = %v, want %v", got, before+2)
	}
}

func TestDownloadInFlight(t *testing.T) {
	IncDownloadInFlight()
	if got := gaugeValue(t, downloadsInFlight); got < 1 {
		t.Fatalf("downloadsInFlight = %v, want >= 1", got)
	}
	DecDownloadInFlight()
}

func TestObserveDownloadSuccess(t *testing.T) {
	ObserveDownload(true, 2000, 2*time.Second)
	if got := gaugeValue(t, downloadSuccessRatio); got != 1 {
		t.Fatalf("downloadSuccessRatio = %v, want 1 after a successful download", got)
	}
	if got := gaugeValue(t, downloadSpeedBytesPerSecond); got != 1000 {
		t.Fatalf("downloadSpeedBytesPerSecond = %v, want 1000", got)
	}
	if got := counterValue(t, downloadBytesTotal); got < 2000 {
		t.Fatalf("downloadBytesTotal = %v, want >= 2000", got)
	}
}

func TestObserveDownloadFailure(t *testing.T) {
	ObserveDownload(true, 0, time.Second)
	ObserveDownload(false, 0, time.Second)
	// one failure out of (successes + 1 failure); ratio must drop below 1
	if got := gaugeValue(t, downloadSuccessRatio); got >= 1 {
		t.Fatalf("downloadSuccessRatio = %v, want < 1 after a failed download", got)
	}
}
