package accumulate

import (
	"math"
	"sort"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/oci"
	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// Sample is one normalized datapoint ready to become a bucket.
type Sample struct {
	ResourceID string
	Direction  persistence.Direction
	Hour       time.Time
	Bytes      int64
}

// BucketHour converts an aggregated datapoint timestamp into the hour the
// bucket covers. OCI timestamps an aggregated point at the end of its window,
// so an aligned timestamp belongs to the previous hour. An unaligned timestamp
// falls back to the hour that contains it.
func BucketHour(timestamp time.Time, resolution time.Duration) time.Time {
	if resolution <= 0 {
		resolution = time.Hour
	}
	utc := timestamp.UTC()
	truncated := utc.Truncate(resolution)
	if utc.Equal(truncated) {
		return truncated.Add(-resolution)
	}
	return truncated
}

// NormalizeSeries converts OCI series into samples, keeping only the hours in
// [start, end). Negative values are clamped to zero: a counter reset must not
// subtract traffic. Duplicate (resource, hour) pairs keep the last point.
func NormalizeSeries(series []oci.Series, direction persistence.Direction, start, end time.Time) []Sample {
	start = start.UTC().Truncate(time.Hour)
	end = end.UTC().Truncate(time.Hour)
	if !end.After(start) {
		return nil
	}

	byHour := make(map[string]map[time.Time]int64)
	for _, s := range series {
		if s.ResourceID == "" {
			continue
		}
		hours := byHour[s.ResourceID]
		if hours == nil {
			hours = make(map[time.Time]int64)
			byHour[s.ResourceID] = hours
		}
		for _, point := range s.Points {
			if math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
				continue
			}
			hour := BucketHour(point.Time, time.Hour)
			if hour.Before(start) || !hour.Before(end) {
				continue
			}
			value := math.Round(point.Value)
			if value < 0 {
				value = 0
			}
			hours[hour] = int64(value)
		}
	}

	var samples []Sample
	for resourceID, hours := range byHour {
		for hour, value := range hours {
			samples = append(samples, Sample{
				ResourceID: resourceID,
				Direction:  direction,
				Hour:       hour,
				Bytes:      value,
			})
		}
	}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].ResourceID != samples[j].ResourceID {
			return samples[i].ResourceID < samples[j].ResourceID
		}
		return samples[i].Hour.Before(samples[j].Hour)
	})
	return samples
}
