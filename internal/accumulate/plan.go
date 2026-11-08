package accumulate

import (
	"fmt"
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// Metric names of the OCI VNIC namespace.
const (
	MetricOutbound = "VnicToNetworkBytes"
	MetricInbound  = "VnicFromNetworkBytes"
)

// Job is one MQL query to run: one VNIC, one direction and one time window.
type Job struct {
	Month        string
	VNICID       string
	Direction    persistence.Direction
	Start        time.Time
	End          time.Time
	Finalization bool
}

// Metric returns the OCI metric of the job direction.
func (j Job) Metric() string {
	if j.Direction == persistence.DirectionInbound {
		return MetricInbound
	}
	return MetricOutbound
}

// MQL returns the aggregated query of the job: one hour of buckets summed into
// one datapoint.
func (j Job) MQL() string {
	return fmt.Sprintf("%s[1h]{resourceId = %q}.sum()", j.Metric(), j.VNICID)
}

// QueryEnd returns the endTime to send to OCI. Aggregated points are
// timestamped at the end of their window and the API excludes endTime, so the
// query must reach one resolution past the last bucket we want.
func (j Job) QueryEnd() time.Time {
	return j.End.Add(time.Hour)
}

// Hours returns the number of hourly buckets the window covers.
func (j Job) Hours() int {
	hours := int(j.End.UTC().Truncate(time.Hour).Sub(j.Start.UTC().Truncate(time.Hour)) / time.Hour)
	if hours < 0 {
		return 0
	}
	return hours
}

// Gap is a range of hours that can no longer be recovered and is stored as
// incomplete.
type Gap struct {
	Month     string
	VNICID    string
	Direction persistence.Direction
	Start     time.Time
	End       time.Time
	Reason    string
}

// Gap reasons.
const (
	GapOutOfRetention = "out_of_retention"
)

// MonthPlan is the work the next run has to do for one month.
type MonthPlan struct {
	Month string
	// Current is true for the month in progress.
	Current bool
	// Due is true when the month can be finalized once its jobs succeed.
	Due  bool
	Jobs []Job
	Gaps []Gap
}

// Plan is the full work of a run.
type Plan struct {
	Months []MonthPlan
}

// HasWork reports whether the plan contains jobs or gaps.
func (p Plan) HasWork() bool {
	for _, m := range p.Months {
		if len(m.Jobs) > 0 || len(m.Gaps) > 0 {
			return true
		}
	}
	return false
}

// PlanMonths decides the query windows, the finalization of closed months and
// the unrecoverable gaps. It never performs I/O.
func PlanMonths(now time.Time, opts Options, state persistence.State, histories map[string]*persistence.History) Plan {
	now = now.UTC()
	currentMonth := persistence.FormatMonth(now)
	retentionStart := now.Add(-opts.retention()).Truncate(time.Hour)

	historyMonths := make([]string, 0, len(histories))
	for month := range histories {
		historyMonths = append(historyMonths, month)
	}

	var plan Plan
	for _, month := range MonthsToConsider(now, state, opts, historyMonths) {
		monthStart, err := persistence.MonthStart(month)
		if err != nil {
			continue
		}
		monthEnd, err := persistence.MonthEnd(month)
		if err != nil {
			continue
		}
		history := histories[month]
		if history == nil {
			history = persistence.NewHistory(month)
		}
		if state.IsFinalized(month) || history.Finalized {
			continue
		}

		monthPlan := MonthPlan{Month: month, Current: month == currentMonth}

		switch {
		case monthPlan.Current:
			windowStart := clampStart(monthStart, retentionStart)
			windowEnd := now.Truncate(time.Hour)
			if retentionStart.After(monthStart) {
				monthPlan.Gaps = monthGaps(opts, history, month, monthStart, windowStart, GapOutOfRetention)
			}
			if windowEnd.After(windowStart) {
				monthPlan.Jobs = planJobs(opts, history, month, windowStart, windowEnd, now, false)
			}

		default:
			windowStart := clampStart(monthStart, retentionStart)
			if retentionStart.After(monthStart) {
				monthPlan.Gaps = monthGaps(opts, history, month, monthStart, windowStart, GapOutOfRetention)
			}
			if !now.Before(monthEnd.Add(opts.requeryWindow())) {
				// Finalization: query from the oldest hour that needs
				// attention through the end of the month. If nothing needs
				// attention, closing the month is enough.
				monthPlan.Due = true
				monthPlan.Jobs = planJobs(opts, history, month, windowStart, monthEnd, now, true)
			} else {
				// Still inside the grace period: keep correcting recent hours.
				graceStart := now.Add(-opts.requeryWindow()).Truncate(time.Hour)
				windowStart = clampStart(windowStart, graceStart)
				windowEnd := monthEnd
				if now.Before(monthEnd) {
					windowEnd = now.Truncate(time.Hour)
				}
				if windowEnd.After(windowStart) {
					monthPlan.Jobs = planJobs(opts, history, month, windowStart, windowEnd, now, false)
				}
			}
		}

		if len(monthPlan.Jobs) > 0 || len(monthPlan.Gaps) > 0 || monthPlan.Due {
			plan.Months = append(plan.Months, monthPlan)
		}
	}
	return plan
}

func clampStart(start, floor time.Time) time.Time {
	if floor.After(start) {
		return floor
	}
	return start
}

func planJobs(opts Options, history *persistence.History, month string, start, end, now time.Time, finalization bool) []Job {
	var jobs []Job
	requeryStart := now.Add(-opts.requeryWindow()).UTC().Truncate(time.Hour)
	for _, vnic := range opts.VNICIDs {
		for _, direction := range opts.Directions() {
			oldest, needsAttention := oldestAttention(history, vnic, direction, start, end)
			jobStart := start
			if finalization {
				if !needsAttention {
					continue
				}
				jobStart = oldest
			} else {
				jobStart = requeryStart
				if needsAttention && oldest.Before(jobStart) {
					jobStart = oldest
				}
				if jobStart.Before(start) {
					jobStart = start
				}
			}
			if !end.After(jobStart) {
				continue
			}
			jobs = append(jobs, Job{
				Month:        month,
				VNICID:       vnic,
				Direction:    direction,
				Start:        jobStart,
				End:          end,
				Finalization: finalization,
			})
		}
	}
	return jobs
}

// oldestAttention returns the oldest hour in [start, end) that is provisional,
// incomplete or missing.
func oldestAttention(history *persistence.History, vnic string, direction persistence.Direction, start, end time.Time) (time.Time, bool) {
	for hour := start.UTC().Truncate(time.Hour); hour.Before(end); hour = hour.Add(time.Hour) {
		bucket, ok := history.Get(vnic, direction, hour)
		if !ok {
			return hour, true
		}
		if bucket.Status == persistence.StatusProvisional || bucket.Status == persistence.StatusIncomplete {
			return hour, true
		}
	}
	return time.Time{}, false
}

// monthGaps groups the missing hours of one VNIC and direction in [start, end)
// into contiguous ranges.
func monthGaps(opts Options, history *persistence.History, month string, start, end time.Time, reason string) []Gap {
	start = start.UTC().Truncate(time.Hour)
	end = end.UTC().Truncate(time.Hour)
	if !end.After(start) {
		return nil
	}
	var gaps []Gap
	for _, vnic := range opts.VNICIDs {
		for _, direction := range opts.Directions() {
			var runStart time.Time
			for hour := start; hour.Before(end); hour = hour.Add(time.Hour) {
				_, ok := history.Get(vnic, direction, hour)
				if !ok {
					if runStart.IsZero() {
						runStart = hour
					}
					continue
				}
				if !runStart.IsZero() {
					gaps = append(gaps, Gap{Month: month, VNICID: vnic, Direction: direction, Start: runStart, End: hour, Reason: reason})
					runStart = time.Time{}
				}
			}
			if !runStart.IsZero() {
				gaps = append(gaps, Gap{Month: month, VNICID: vnic, Direction: direction, Start: runStart, End: end, Reason: reason})
			}
		}
	}
	return gaps
}

// ApplyGaps stores every missing hour of the gaps as incomplete with zero
// bytes. Existing buckets are never modified.
func ApplyGaps(history *persistence.History, gaps []Gap, now time.Time) int {
	if history.Finalized {
		return 0
	}
	filled := 0
	now = now.UTC()
	for _, gap := range gaps {
		for hour := gap.Start.UTC().Truncate(time.Hour); hour.Before(gap.End); hour = hour.Add(time.Hour) {
			if _, exists := history.Get(gap.VNICID, gap.Direction, hour); exists {
				continue
			}
			history.Set(persistence.Bucket{
				ResourceID: gap.VNICID,
				Direction:  gap.Direction,
				Hour:       hour,
				Bytes:      0,
				Status:     persistence.StatusIncomplete,
				CapturedAt: now,
			})
			filled++
		}
	}
	return filled
}

// MarkJobFailed stores as incomplete every hour of a failed query that has no
// confirmed data: missing hours and provisional hours. Complete and no_data
// buckets keep their previous value.
func MarkJobFailed(history *persistence.History, job Job, now time.Time) int {
	if history.Finalized {
		return 0
	}
	failed := 0
	now = now.UTC()
	for hour := job.Start.UTC().Truncate(time.Hour); hour.Before(job.End); hour = hour.Add(time.Hour) {
		bucket, exists := history.Get(job.VNICID, job.Direction, hour)
		if exists && bucket.Status != persistence.StatusProvisional {
			continue
		}
		history.Set(persistence.Bucket{
			ResourceID: job.VNICID,
			Direction:  job.Direction,
			Hour:       hour,
			Bytes:      0,
			Status:     persistence.StatusIncomplete,
			CapturedAt: now,
		})
		failed++
	}
	return failed
}
