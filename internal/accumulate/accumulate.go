// Package accumulate implements the hourly bucket model: normalization of OCI
// datapoints, requery window planning, overwrite by key, monthly totals and
// month finalization. It performs no I/O.
package accumulate

import (
	"sort"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// DefaultRetention is the documented OCI Monitoring retention (90 days).
const DefaultRetention = 90 * 24 * time.Hour

// Options configures the accumulation logic. Durations are usually built from
// the configuration file by the app package.
type Options struct {
	// RequeryWindow is how far back each run queries and the grace period
	// before a month is finalized (48 h by default).
	RequeryWindow time.Duration
	// PublicationLatency is the margin used to mark recent hours provisional.
	PublicationLatency time.Duration
	// NoDataStreak is the run of consecutive no_data hours that makes the
	// monthly total incomplete when PublishZeros is true: if OCI publishes
	// explicit zeros, a visible VNIC should not be missing datapoints for many
	// hours, so the gap is treated as a collection problem. When OCI omits
	// zeros (PublishZeros=false) a long run is normal idle traffic and does not
	// invalidate the total.
	NoDataStreak time.Duration
	// Retention bounds how far back OCI data can be recovered.
	Retention time.Duration
	// PublishZeros is the pending empirical question: whether oci_vcn publishes
	// explicit zero datapoints for idle VNICs instead of omitting them. The
	// default (false) assumes omission so that idle VNICs do not produce false
	// incomplete totals; it will be set from evidence, not from configuration.
	PublishZeros bool
	// VNICIDs are the configured VNICs.
	VNICIDs []string
	// CollectInbound enables VnicFromNetworkBytes buckets.
	CollectInbound bool
}

func (o Options) requeryWindow() time.Duration {
	if o.RequeryWindow <= 0 {
		return 48 * time.Hour
	}
	return o.RequeryWindow
}

func (o Options) publicationLatency() time.Duration {
	if o.PublicationLatency <= 0 {
		return 2 * time.Hour
	}
	return o.PublicationLatency
}

func (o Options) noDataStreakHours() int {
	if o.NoDataStreak <= 0 {
		return 6
	}
	hours := int(o.NoDataStreak / time.Hour)
	if hours < 1 {
		return 1
	}
	return hours
}

func (o Options) retention() time.Duration {
	if o.Retention <= 0 {
		return DefaultRetention
	}
	return o.Retention
}

// Directions returns the directions enabled by the options, outbound first.
func (o Options) Directions() []persistence.Direction {
	dirs := []persistence.Direction{persistence.DirectionOutbound}
	if o.CollectInbound {
		dirs = append(dirs, persistence.DirectionInbound)
	}
	return dirs
}

// CorrectionStats counts the buckets whose bytes changed and the total byte
// difference.
type CorrectionStats struct {
	Buckets    int
	DeltaBytes int64
}

// Add sums another correction block.
func (c *CorrectionStats) Add(other CorrectionStats) {
	c.Buckets += other.Buckets
	c.DeltaBytes += other.DeltaBytes
}

// MonthsToConsider returns the months, oldest first and without duplicates,
// that the current run has to examine: from the oldest trace (last run or last
// successful query) or the retention horizon, whichever is later, through the
// current month, plus any month that already has a history file inside the
// retention window. A month with a history file is included even when the last
// run happened in a later month, so an unfinalized month is never silently
// abandoned. On a fresh install there is neither a trace nor a history and only
// the current month is examined: the app does not claim traffic from before it
// was installed.
func MonthsToConsider(now time.Time, state persistence.State, opts Options, historyMonths []string) []string {
	now = now.UTC()
	retention := opts.retention()
	current := persistence.FormatMonth(now)
	retentionFloor := persistence.FormatMonth(now.Add(-retention))

	latestTrace := ""
	for _, trace := range []*time.Time{state.LastRun, state.LastSuccessfulQuery} {
		if trace == nil {
			continue
		}
		if month := persistence.FormatMonth(*trace); month > latestTrace {
			latestTrace = month
		}
	}

	earliest := current
	if latestTrace != "" {
		earliest = retentionFloor
		if latestTrace > earliest {
			earliest = latestTrace
		}
	}
	if earliest > current {
		// A clock adjustment must not skip the month in progress.
		earliest = current
	}

	seen := make(map[string]struct{})
	for _, month := range historyMonths {
		if _, err := persistence.MonthStart(month); err != nil {
			continue
		}
		if month < retentionFloor || month > current {
			continue
		}
		seen[month] = struct{}{}
	}

	start, err := persistence.MonthStart(earliest)
	if err != nil {
		return []string{current}
	}
	var months []string
	for month := start; !month.After(now); month = month.AddDate(0, 1, 0) {
		months = append(months, persistence.FormatMonth(month))
	}

	known := make(map[string]struct{}, len(months))
	for _, month := range months {
		known[month] = struct{}{}
	}
	var extra []string
	for month := range seen {
		if _, ok := known[month]; ok {
			continue
		}
		extra = append(extra, month)
	}
	if len(extra) == 0 {
		return months
	}
	months = append(months, extra...)
	sort.Strings(months)
	return months
}
