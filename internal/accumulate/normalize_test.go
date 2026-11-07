package accumulate

import (
	"math"
	"testing"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/oci"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

func utcHour(y int, m time.Month, d, h int) time.Time {
	return time.Date(y, m, d, h, 0, 0, 0, time.UTC)
}

func testOptions() Options {
	return Options{
		RequeryWindow:      48 * time.Hour,
		PublicationLatency: 2 * time.Hour,
		NoDataStreak:       6 * time.Hour,
		Retention:          90 * 24 * time.Hour,
		VNICIDs:            []string{"vnic-a"},
	}
}

func TestBucketHour(t *testing.T) {
	aligned := utcHour(2026, 11, 7, 12)
	if got := BucketHour(aligned, time.Hour); !got.Equal(utcHour(2026, 11, 7, 11)) {
		t.Errorf("BucketHour(aligned) = %s, want 11:00", got)
	}
	unaligned := utcHour(2026, 11, 7, 12).Add(34 * time.Minute)
	if got := BucketHour(unaligned, time.Hour); !got.Equal(utcHour(2026, 11, 7, 12)) {
		t.Errorf("BucketHour(unaligned) = %s, want 12:00", got)
	}
}

func TestNormalizeSeries(t *testing.T) {
	start := utcHour(2026, 11, 7, 10)
	end := utcHour(2026, 11, 7, 12)

	series := []oci.Series{
		{
			ResourceID: "vnic-a",
			Points: []oci.Point{
				{Time: utcHour(2026, 11, 7, 11), Value: 100}, // hour 10
				{Time: utcHour(2026, 11, 7, 12), Value: 200}, // hour 11
				{Time: utcHour(2026, 11, 7, 13), Value: 300}, // hour 12, outside
				{Time: utcHour(2026, 11, 7, 10), Value: 400}, // hour 09, outside
			},
		},
		{
			ResourceID: "vnic-b",
			Points: []oci.Point{
				{Time: utcHour(2026, 11, 7, 10).Add(30 * time.Minute), Value: -50}, // hour 10, clamped
			},
		},
		{
			ResourceID: "vnic-c",
			Points: []oci.Point{
				{Time: utcHour(2026, 11, 7, 12), Value: math.NaN()},
			},
		},
	}

	samples := NormalizeSeries(series, persistence.DirectionOutbound, start, end)
	if len(samples) != 3 {
		t.Fatalf("samples = %d, want 3: %+v", len(samples), samples)
	}

	want := map[string]map[time.Time]int64{
		"vnic-a": {
			utcHour(2026, 11, 7, 10): 100,
			utcHour(2026, 11, 7, 11): 200,
		},
		"vnic-b": {
			utcHour(2026, 11, 7, 10): 0,
		},
	}
	count := map[string]int{}
	for _, sample := range samples {
		count[sample.ResourceID]++
		if sample.Direction != persistence.DirectionOutbound {
			t.Errorf("direction = %q", sample.Direction)
		}
		expected, ok := want[sample.ResourceID][sample.Hour]
		if !ok {
			t.Errorf("unexpected sample %s at %s", sample.ResourceID, sample.Hour)
			continue
		}
		if sample.Bytes != expected {
			t.Errorf("%s at %s bytes = %d, want %d", sample.ResourceID, sample.Hour, sample.Bytes, expected)
		}
	}
	for resourceID, hours := range want {
		if count[resourceID] != len(hours) {
			t.Errorf("%s has %d samples, want %d", resourceID, count[resourceID], len(hours))
		}
	}
}

func TestNormalizeSeriesEmptyWindow(t *testing.T) {
	start := utcHour(2026, 11, 7, 12)
	if samples := NormalizeSeries(nil, persistence.DirectionOutbound, start, start); samples != nil {
		t.Errorf("samples = %+v, want nil", samples)
	}
}

func TestNormalizeSeriesDuplicateHourKeepsLast(t *testing.T) {
	start := utcHour(2026, 11, 7, 10)
	end := utcHour(2026, 11, 7, 11)
	series := []oci.Series{
		{ResourceID: "vnic-a", Points: []oci.Point{{Time: utcHour(2026, 11, 7, 11), Value: 10}}},
		{ResourceID: "vnic-a", Points: []oci.Point{{Time: utcHour(2026, 11, 7, 11), Value: 20}}},
	}
	samples := NormalizeSeries(series, persistence.DirectionOutbound, start, end)
	if len(samples) != 1 || samples[0].Bytes != 20 {
		t.Fatalf("samples = %+v, want one sample with 20 bytes", samples)
	}
}
