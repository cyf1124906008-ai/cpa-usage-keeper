package repository

import (
	"math"
	"testing"
	"time"

	"cpa-usage-keeper/internal/entities"
)

func TestApplyUsageOverviewRealtimeConcurrencyTracksAverageAndPeak(t *testing.T) {
	start := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	span := 30 * time.Second
	buckets := newUsageOverviewRealtimeBuckets(start, span, 1)

	applyUsageOverviewRealtimeConcurrency(buckets, entities.UsageEvent{
		Timestamp: start,
		LatencyMS: 20_000,
	}, start, span, start.Add(span))
	applyUsageOverviewRealtimeConcurrency(buckets, entities.UsageEvent{
		Timestamp: start.Add(10 * time.Second),
		LatencyMS: 20_000,
	}, start, span, start.Add(span))

	average := float64(buckets[0].concurrencyMS) / float64(span.Milliseconds())
	if math.Abs(average-(4.0/3.0)) > 0.0001 {
		t.Fatalf("average concurrency = %f, want %f", average, 4.0/3.0)
	}
	if peak := usageOverviewRealtimePeakConcurrency(buckets[0].intervals); peak != 2 {
		t.Fatalf("peak concurrency = %d, want 2", peak)
	}
}
