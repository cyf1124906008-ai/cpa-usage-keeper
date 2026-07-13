package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type runtimeMetricsSnapshot struct {
	CurrentInflight int64 `json:"current_inflight"`
	PeakInflight    int64 `json:"peak_inflight"`
}

func (s *usageService) loadRuntimeMetrics(ctx context.Context) (runtimeMetricsSnapshot, bool) {
	if s == nil || s.httpClient == nil || strings.TrimSpace(s.runtimeMetricsURL) == "" {
		return runtimeMetricsSnapshot{}, false
	}
	req, err := http.NewRequestWithContext(usageServiceContext(ctx), http.MethodGet, s.runtimeMetricsURL, nil)
	if err != nil {
		return runtimeMetricsSnapshot{}, false
	}
	response, err := s.httpClient.Do(req)
	if err != nil {
		return runtimeMetricsSnapshot{}, false
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return runtimeMetricsSnapshot{}, false
	}
	var result runtimeMetricsSnapshot
	if json.NewDecoder(response.Body).Decode(&result) != nil {
		return runtimeMetricsSnapshot{}, false
	}
	if result.CurrentInflight < 0 {
		result.CurrentInflight = 0
	}
	if result.PeakInflight < result.CurrentInflight {
		result.PeakInflight = result.CurrentInflight
	}
	return result, true
}
