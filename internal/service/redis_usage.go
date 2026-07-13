package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/quota"
	"cpa-usage-keeper/internal/repository/dto"
	"cpa-usage-keeper/internal/timeutil"
)

// DecodeRedisUsageMessage 将 redis_inboxes.raw_message 原样解码为 usage_events 入库实体。
func DecodeRedisUsageMessage(message string, fetchedAt time.Time) (entities.UsageEvent, json.RawMessage, error) {
	event, raw, _, err := DecodeRedisUsageMessageWithHeaders(message, fetchedAt)
	return event, raw, err
}

// DecodeRedisUsageMessageWithHeaders 解码 usage event，并在 OAuth 响应携带 headers 时抽取 quota cache 更新快照。
func DecodeRedisUsageMessageWithHeaders(message string, fetchedAt time.Time) (entities.UsageEvent, json.RawMessage, *quota.UsageHeaderSnapshot, error) {
	raw := json.RawMessage(message)
	var payload queuedUsageDetail
	if err := json.Unmarshal(raw, &payload); err != nil {
		return entities.UsageEvent{}, nil, nil, fmt.Errorf("decode redis usage message: %w", err)
	}
	if strings.TrimSpace(payload.RequestID) == "" {
		return entities.UsageEvent{}, raw, nil, fmt.Errorf("decode redis usage message: request_id is required")
	}
	event := payload.toUsageEvent(fetchedAt)
	return event, raw, payload.toUsageHeaderSnapshot(event), nil
}

// queuedUsageDetail 对应 CPA Redis 队列中的单条 usage JSON payload。
type queuedUsageDetail struct {
	Timestamp       time.Time       `json:"timestamp"`
	LatencyMS       int64           `json:"latency_ms"`
	TTFTMS          *int64          `json:"ttft_ms"`
	Source          string          `json:"source"`
	AuthIndex       string          `json:"auth_index"`
	Tokens          dto.TokenStats  `json:"tokens"`
	Failed          bool            `json:"failed"`
	Fail            queuedUsageFail `json:"fail"`
	Provider        string          `json:"provider"`
	Model           string          `json:"model"`
	Alias           *string         `json:"alias"`
	ReasoningEffort string          `json:"reasoning_effort"`
	ServiceTier     string          `json:"service_tier"`
	ExecutorType    string          `json:"executor_type"`
	Endpoint        string          `json:"endpoint"`
	AuthType        string          `json:"auth_type"`
	APIKey          string          `json:"api_key"`
	RequestID       string          `json:"request_id"`
	ResponseHeaders json.RawMessage `json:"response_headers"`
}

type queuedUsageFail struct {
	StatusCode int    `json:"status_code"`
	Body       string `json:"body"`
}

var (
	errorHTMLTagPattern = regexp.MustCompile(`<[^>]+>`)
	errorAPIKeyPattern  = regexp.MustCompile(`(?i)\b(sk-[a-z0-9_-]{8,}|bearer\s+[a-z0-9._~+/-]{8,})\b`)
)

func normalizeRedisAuthType(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "api_key" {
		return "apikey"
	}
	return trimmed
}

func trimRedisOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// toUsageEvent 保持 Redis payload 的 model/request_id 语义，缺失时间才用本地拉取时间兜底。
func (d queuedUsageDetail) toUsageEvent(fetchedAt time.Time) entities.UsageEvent {
	apiGroupKey := firstNonEmpty(d.APIKey, d.Provider, d.Endpoint, "unknown")
	model := firstNonEmpty(d.Model, "unknown")
	d.applyMissingUsageEstimate(model)
	statusCode, errorType, errorMessage := normalizeUsageFailure(d.Failed, d.Fail)
	timestamp := timeutil.NormalizeStorageTime(d.Timestamp)
	if timestamp.IsZero() {
		timestamp = timeutil.NormalizeStorageTime(fetchedAt)
	}
	source := strings.TrimSpace(d.Source)
	authIndex := strings.TrimSpace(d.AuthIndex)
	eventKey := strings.TrimSpace(d.RequestID)
	return entities.UsageEvent{
		EventKey:            eventKey,
		APIGroupKey:         apiGroupKey,
		Provider:            strings.TrimSpace(d.Provider),
		Endpoint:            strings.TrimSpace(d.Endpoint),
		AuthType:            normalizeRedisAuthType(d.AuthType),
		RequestID:           strings.TrimSpace(d.RequestID),
		Model:               model,
		ModelAlias:          trimRedisOptionalString(d.Alias),
		ReasoningEffort:     strings.TrimSpace(d.ReasoningEffort),
		ServiceTier:         strings.TrimSpace(d.ServiceTier),
		ExecutorType:        strings.TrimSpace(d.ExecutorType),
		Timestamp:           timestamp,
		Source:              source,
		AuthIndex:           authIndex,
		Failed:              d.Failed,
		StatusCode:          statusCode,
		ErrorType:           errorType,
		ErrorMessage:        errorMessage,
		LatencyMS:           max(d.LatencyMS, 0),
		TTFTMS:              d.TTFTMS,
		InputTokens:         d.Tokens.InputTokens,
		OutputTokens:        d.Tokens.OutputTokens,
		ReasoningTokens:     d.Tokens.ReasoningTokens,
		CachedTokens:        d.Tokens.CachedTokens,
		CacheReadTokens:     d.Tokens.CacheReadTokens,
		CacheCreationTokens: d.Tokens.CacheCreationTokens,
		TotalTokens:         d.Tokens.TotalTokens,
	}
}

func normalizeUsageFailure(failed bool, fail queuedUsageFail) (int, string, string) {
	if !failed {
		return http.StatusOK, "", ""
	}
	statusCode := fail.StatusCode
	if statusCode <= 0 {
		statusCode = http.StatusInternalServerError
	}
	message := usageFailureMessage(fail.Body)
	return statusCode, classifyUsageFailure(statusCode, message), message
}

func usageFailureMessage(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ""
	}
	var payload any
	if json.Unmarshal([]byte(trimmed), &payload) == nil {
		if message := findUsageFailureMessage(payload); message != "" {
			trimmed = message
		}
	}
	trimmed = html.UnescapeString(errorHTMLTagPattern.ReplaceAllString(trimmed, " "))
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	trimmed = errorAPIKeyPattern.ReplaceAllString(trimmed, "[redacted]")
	runes := []rune(trimmed)
	if len(runes) > 320 {
		trimmed = string(runes[:320]) + "..."
	}
	return trimmed
}

func findUsageFailureMessage(value any) string {
	switch item := value.(type) {
	case map[string]any:
		for _, key := range []string{"message", "detail", "error_description", "body"} {
			if text, ok := item[key].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
		for _, key := range []string{"error", "cause"} {
			if nested, ok := item[key]; ok {
				if message := findUsageFailureMessage(nested); message != "" {
					return message
				}
			}
		}
	case string:
		return strings.TrimSpace(item)
	}
	return ""
}

func classifyUsageFailure(statusCode int, message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "context canceled") || strings.Contains(lower, "client disconnected"):
		return "client_canceled"
	case strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout"):
		return "timeout"
	case strings.Contains(lower, "request entity too large") || statusCode == http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case strings.Contains(lower, "policy") || strings.Contains(lower, "cyber") || strings.Contains(lower, "safety"):
		return "policy_rejected"
	case strings.Contains(lower, "tool output") || strings.Contains(lower, "function call"):
		return "tool_call_error"
	case strings.Contains(lower, "model not found") || strings.Contains(lower, "no auth available"):
		return "model_unavailable"
	case strings.Contains(lower, "upstream request failed") || strings.Contains(lower, "bad gateway"):
		return "upstream_error"
	case statusCode == http.StatusBadRequest || statusCode == http.StatusUnprocessableEntity:
		return "invalid_request"
	case statusCode == http.StatusUnauthorized:
		return "authentication_error"
	case statusCode == http.StatusForbidden:
		return "permission_error"
	case statusCode == http.StatusNotFound:
		return "not_found"
	case statusCode == http.StatusTooManyRequests:
		return "rate_limit"
	case statusCode >= 500:
		return "server_error"
	default:
		return "unknown_error"
	}
}

func (d *queuedUsageDetail) applyMissingUsageEstimate(model string) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("ESTIMATE_MISSING_USAGE")), "true") ||
		!d.isMissingSuccessfulUsage() || d.Tokens.TotalTokens != 0 ||
		!strings.EqualFold(strings.TrimSpace(d.Provider), "codex") ||
		!strings.Contains(strings.ToLower(d.Endpoint), "/v1/responses") {
		return
	}

	nonCached := missingUsageEstimate("ESTIMATED_NON_CACHED_INPUT_TOKENS", 1582)
	cached := missingUsageEstimate("ESTIMATED_CACHED_INPUT_TOKENS", 109056)
	output := missingUsageEstimate("ESTIMATED_OUTPUT_TOKENS", 320)
	d.Tokens.InputTokens = nonCached + cached
	d.Tokens.CachedTokens = cached
	d.Tokens.CacheReadTokens = cached
	d.Tokens.OutputTokens = output
	d.Tokens.TotalTokens = nonCached + cached + output
	d.ReasoningEffort = firstNonEmpty(d.ReasoningEffort, "estimated-missing-usage")
	estimatedAlias := model + " (estimated)"
	d.Alias = &estimatedAlias
}

func (d *queuedUsageDetail) isMissingSuccessfulUsage() bool {
	if !d.Failed {
		return true
	}
	return d.Fail.StatusCode == http.StatusOK &&
		strings.Contains(strings.ToLower(d.Fail.Body), "context canceled")
}

func missingUsageEstimate(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func (d queuedUsageDetail) toUsageHeaderSnapshot(event entities.UsageEvent) *quota.UsageHeaderSnapshot {
	headers, ok := decodeRedisUsageResponseHeaders(d.ResponseHeaders)
	if !ok {
		return nil
	}
	snapshot, ok := quota.BuildUsageHeaderSnapshot(quota.UsageHeaderSnapshotInput{
		AuthType:   event.AuthType,
		AuthIndex:  event.AuthIndex,
		Provider:   event.Provider,
		ObservedAt: event.Timestamp,
		Headers:    headers,
	})
	if !ok {
		return nil
	}
	return snapshot
}

func decodeRedisUsageResponseHeaders(raw json.RawMessage) (http.Header, bool) {
	if rawJSONMessageIsEmptyOrNull(raw) {
		return nil, false
	}
	var headers http.Header
	if err := json.Unmarshal(raw, &headers); err == nil && len(headers) > 0 {
		return headers, true
	}
	var rawHeaders map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawHeaders); err != nil || len(rawHeaders) == 0 {
		return nil, false
	}
	headers = make(http.Header, len(rawHeaders))
	for key, rawValue := range rawHeaders {
		for _, value := range decodeRedisUsageHeaderValues(rawValue) {
			headers.Add(key, value)
		}
	}
	if len(headers) == 0 {
		return nil, false
	}
	return headers, true
}

func rawJSONMessageIsEmptyOrNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || (len(trimmed) == 4 && trimmed[0] == 'n' && trimmed[1] == 'u' && trimmed[2] == 'l' && trimmed[3] == 'l')
}

func decodeRedisUsageHeaderValues(raw json.RawMessage) []string {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return []string{value}
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		return values
	}
	var rawValues []json.RawMessage
	if err := json.Unmarshal(raw, &rawValues); err != nil {
		return nil
	}
	values = make([]string, 0, len(rawValues))
	for _, rawValue := range rawValues {
		if err := json.Unmarshal(rawValue, &value); err == nil {
			values = append(values, value)
		}
	}
	return values
}
