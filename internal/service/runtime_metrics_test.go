package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoadRuntimeMetricsNormalizesValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"current_inflight":7,"peak_inflight":3}`))
	}))
	defer server.Close()

	provider := NewUsageServiceWithOptions(nil, UsageServiceOptions{RuntimeMetricsURL: server.URL})
	service, ok := provider.(*usageService)
	if !ok {
		t.Fatal("usage service has unexpected concrete type")
	}
	metrics, available := service.loadRuntimeMetrics(context.Background())
	if !available {
		t.Fatal("runtime metrics should be available")
	}
	if metrics.CurrentInflight != 7 || metrics.PeakInflight != 7 {
		t.Fatalf("metrics = %+v, want current=7 peak=7", metrics)
	}
}

func TestLoadRuntimeMetricsRejectsNonSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	provider := NewUsageServiceWithOptions(nil, UsageServiceOptions{RuntimeMetricsURL: server.URL})
	service := provider.(*usageService)
	if _, available := service.loadRuntimeMetrics(context.Background()); available {
		t.Fatal("runtime metrics should be unavailable")
	}
}
