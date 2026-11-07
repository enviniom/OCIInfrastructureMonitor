package accumulate

import (
	"time"

	"github.com/enviniom/OCIInfrastructureMonitor/internal/persistence"
)

// Result reports what one reconciled query changed.
type Result struct {
	// Samples is the number of datapoints inside the job window.
	Samples int
	// NewBuckets is the number of hours that had no bucket before.
	NewBuckets int
	// Corrections counts buckets whose bytes changed (or appeared) and the
	// total byte difference.
	Corrections CorrectionStats
	// LateCorrections counts the changes discarded because the month was
	// already finalized.
	LateCorrections CorrectionStats
}

// Reconcile writes every hour of the job window into the history, overwriting
// by key. Hours without a datapoint become provisional while they are recent,
// no_data for a VNIC already seen and incomplete for an hour before the VNIC
// was first visible. A finalized history is never modified: its late
// corrections are only counted so the caller can log them.
//
// Visibility is inferred from datapoints; vnic must already include the
// observations of the current run (see UpdateVNIC).
func Reconcile(h *persistence.History, job Job, samples []Sample, now time.Time, opts Options, vnic persistence.VNICState) Result {
	var result Result
	start := job.Start.UTC().Truncate(time.Hour)
	end := job.End.UTC().Truncate(time.Hour)
	if !end.After(start) {
		return result
	}

	byHour := make(map[time.Time]int64, len(samples))
	var firstSample *time.Time
	for _, s := range samples {
		if s.ResourceID != job.VNICID || s.Direction != job.Direction {
			continue
		}
		hour := s.Hour.UTC().Truncate(time.Hour)
		if hour.Before(start) || !hour.Before(end) {
			continue
		}
		byHour[hour] = s.Bytes
		result.Samples++
		if firstSample == nil || hour.Before(*firstSample) {
			h := hour
			firstSample = &h
		}
	}

	firstVisible := vnic.FirstVisible
	if firstSample != nil && (firstVisible == nil || firstSample.Before(*firstVisible)) {
		firstVisible = firstSample
	}

	if h.Finalized {
		// The month is immutable. Count what a late correction would have
		// changed, but never touch the file.
		for hour, bytes := range byHour {
			stored, ok := h.Get(job.VNICID, job.Direction, hour)
			switch {
			case !ok:
				result.LateCorrections.Buckets++
				result.LateCorrections.DeltaBytes += bytes
			case stored.Bytes != bytes:
				result.LateCorrections.Buckets++
				result.LateCorrections.DeltaBytes += bytes - stored.Bytes
			}
		}
		return result
	}

	now = now.UTC()
	provisionalAfter := now.Add(-opts.publicationLatency())
	for hour := start; hour.Before(end); hour = hour.Add(time.Hour) {
		recent := hour.Add(time.Hour).After(provisionalAfter)
		point, hasPoint := byHour[hour]

		var bytes int64
		var status persistence.Status
		switch {
		case hasPoint:
			// A received datapoint always overwrites the key, so late OCI
			// corrections are absorbed.
			bytes = point
			if recent {
				status = persistence.StatusProvisional
			} else {
				status = persistence.StatusComplete
			}
		default:
			previous, existed := h.Get(job.VNICID, job.Direction, hour)
			switch {
			case existed && (previous.Status == persistence.StatusComplete || previous.Status == persistence.StatusNoData):
				// Confirmed hours are not attention, and a query that omits
				// them must not erase data already observed.
				continue
			case existed && previous.Status == persistence.StatusProvisional && previous.Bytes > 0:
				// The point was observed before even if this response omits it.
				bytes = previous.Bytes
				if recent {
					status = persistence.StatusProvisional
				} else {
					status = persistence.StatusComplete
				}
			default:
				// Missing, failed or provisional without data: classify again.
				if recent {
					status = persistence.StatusProvisional
				} else if firstVisible != nil && !hour.Before(*firstVisible) {
					status = persistence.StatusNoData
				} else {
					status = persistence.StatusIncomplete
				}
			}
		}

		bucket := persistence.Bucket{
			ResourceID: job.VNICID,
			Direction:  job.Direction,
			Hour:       hour,
			Bytes:      bytes,
			Status:     status,
			CapturedAt: now,
		}
		previous, existed := h.Set(bucket)
		if !existed {
			result.NewBuckets++
			continue
		}
		if previous.Bytes != bucket.Bytes {
			result.Corrections.Buckets++
			result.Corrections.DeltaBytes += bucket.Bytes - previous.Bytes
		}
	}
	return result
}

// UpdateVNIC applies the datapoints observed during a run to the persisted
// observation of a VNIC. It returns the new state and whether a
// "stopped appearing" warning was raised by this transition.
//
// The warning is raised when a VNIC that had datapoints before is absent during
// a successful run. A VNIC that has never produced datapoints does not warn:
// its hours are incomplete instead.
func UpdateVNIC(vnic persistence.VNICState, dataHours []time.Time, now time.Time) (persistence.VNICState, bool) {
	now = now.UTC()
	if len(dataHours) > 0 {
		first := dataHours[0].UTC().Truncate(time.Hour)
		last := first
		for _, hour := range dataHours[1:] {
			hour = hour.UTC().Truncate(time.Hour)
			if hour.Before(first) {
				first = hour
			}
			if hour.After(last) {
				last = hour
			}
		}
		if vnic.FirstVisible == nil || first.Before(*vnic.FirstVisible) {
			v := first
			vnic.FirstVisible = &v
		}
		if vnic.LastVisible == nil || last.After(*vnic.LastVisible) {
			v := last
			vnic.LastVisible = &v
		}
		at := now
		vnic.LastDataAt = &at
		vnic.Visible = true
		vnic.WarningSince = nil
		return vnic, false
	}

	if vnic.FirstVisible == nil && vnic.LastVisible == nil {
		vnic.Visible = false
		return vnic, false
	}

	vnic.Visible = false
	if vnic.WarningSince == nil {
		at := now
		vnic.WarningSince = &at
		return vnic, true
	}
	return vnic, false
}
