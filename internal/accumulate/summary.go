package accumulate

import (
	"fmt"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// DirectionSummary aggregates one direction of one month.
type DirectionSummary struct {
	Direction   persistence.Direction
	Bytes       int64
	Buckets     int
	Complete    int
	Provisional int
	NoData      int
	Incomplete  int
	// FirstHour and LastHour are the oldest and newest bucket hour of the
	// direction, zero when there is no bucket. They are informational: the
	// daily report uses them to show the covered period. They are recomputed
	// from the history and are never persisted.
	FirstHour time.Time
	LastHour  time.Time
	// MaxNoDataStreak is the longest run of consecutive no_data hours of one
	// resource and direction.
	MaxNoDataStreak int
	// Status is complete, provisional or incomplete for this direction.
	Status persistence.Status
	// Reason explains an incomplete or provisional status in Spanish.
	Reason string
}

// Summary aggregates one month.
type Summary struct {
	Month       string
	Finalized   bool
	FinalizedAt *time.Time
	Outbound    DirectionSummary
	Inbound     DirectionSummary
	// Status follows the outbound accumulator, which is the one the roadmap
	// tracks. Inbound is always shown separately.
	Status persistence.Status
	// HasData is false when the month has no bucket at all.
	HasData bool
}

// Direction returns the summary of a direction.
func (s Summary) Direction(direction persistence.Direction) DirectionSummary {
	if direction == persistence.DirectionInbound {
		return s.Inbound
	}
	return s.Outbound
}

type vnicDirectionKey struct {
	resourceID string
	direction  persistence.Direction
}

// Summarize sums the buckets of the month. The monthly total is always computed
// from the buckets, never kept as an incremental counter.
func Summarize(history *persistence.History, opts Options) Summary {
	summary := Summary{
		Month:     history.Month,
		Finalized: history.Finalized,
		Outbound:  DirectionSummary{Direction: persistence.DirectionOutbound},
		Inbound:   DirectionSummary{Direction: persistence.DirectionInbound},
	}
	if history.FinalizedAt != nil {
		at := history.FinalizedAt.UTC()
		summary.FinalizedAt = &at
	}

	hours := make(map[vnicDirectionKey][]time.Time)
	for _, bucket := range history.Sorted() {
		direction := &summary.Outbound
		if bucket.Direction == persistence.DirectionInbound {
			direction = &summary.Inbound
		}
		direction.Bytes += bucket.Bytes
		direction.Buckets++
		hour := bucket.Hour.UTC()
		if direction.FirstHour.IsZero() || hour.Before(direction.FirstHour) {
			direction.FirstHour = hour
		}
		if direction.LastHour.IsZero() || hour.After(direction.LastHour) {
			direction.LastHour = hour
		}
		switch bucket.Status {
		case persistence.StatusComplete:
			direction.Complete++
		case persistence.StatusProvisional:
			direction.Provisional++
		case persistence.StatusNoData:
			direction.NoData++
		case persistence.StatusIncomplete:
			direction.Incomplete++
		}
		if bucket.Status == persistence.StatusNoData {
			key := vnicDirectionKey{resourceID: bucket.ResourceID, direction: bucket.Direction}
			hours[key] = append(hours[key], bucket.Hour)
		}
	}

	maxStreak := map[persistence.Direction]int{}
	for key, series := range hours {
		streak := 0
		var previous time.Time
		for i, hour := range series {
			utc := hour.UTC()
			if i > 0 && utc.Equal(previous.Add(time.Hour)) {
				streak++
			} else {
				streak = 1
			}
			if streak > maxStreak[key.direction] {
				maxStreak[key.direction] = streak
			}
			previous = utc
		}
	}
	summary.Outbound.MaxNoDataStreak = maxStreak[persistence.DirectionOutbound]
	summary.Inbound.MaxNoDataStreak = maxStreak[persistence.DirectionInbound]

	for _, direction := range []*DirectionSummary{&summary.Outbound, &summary.Inbound} {
		aggregateStatus(direction, opts)
	}

	summary.HasData = summary.Outbound.Buckets > 0 || summary.Inbound.Buckets > 0
	summary.Status = summary.Outbound.Status
	if summary.Outbound.Buckets == 0 && summary.Inbound.Buckets > 0 {
		summary.Status = summary.Inbound.Status
	}
	return summary
}

func aggregateStatus(direction *DirectionSummary, opts Options) {
	if direction.Buckets == 0 {
		direction.Status = persistence.StatusIncomplete
		direction.Reason = ""
		return
	}
	if direction.Incomplete > 0 {
		direction.Status = persistence.StatusIncomplete
		direction.Reason = bucketCount(direction.Incomplete, "incompleto", "incompletos")
		return
	}
	if opts.PublishZeros && direction.MaxNoDataStreak > opts.noDataStreakHours() {
		direction.Status = persistence.StatusIncomplete
		direction.Reason = fmt.Sprintf("racha de no_data de %d h mayor que %d h", direction.MaxNoDataStreak, opts.noDataStreakHours())
		return
	}
	if direction.Provisional > 0 {
		direction.Status = persistence.StatusProvisional
		direction.Reason = bucketCount(direction.Provisional, "provisional", "provisionales")
		return
	}
	direction.Status = persistence.StatusComplete
}

func bucketCount(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d bucket %s", n, singular)
	}
	return fmt.Sprintf("%d buckets %s", n, plural)
}
