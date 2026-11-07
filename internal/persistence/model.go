// Package persistence stores the monitor state and the monthly history as JSON
// files. It provides atomic writes, strict schema validation, corruption
// quarantine and an exclusive lock.
package persistence

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// CurrentSchemaVersion is the version written to every state and history file.
// A file with a version this binary does not understand is rejected without
// being overwritten.
const CurrentSchemaVersion = 2

// OldestSupportedSchemaVersion is the oldest version this binary can read. A
// version 1 file (the Hito 2 schema) is migrated in memory and rewritten as
// version 2 on the next successful save; a version 1 finalized history is read
// as is and never rewritten.
const OldestSupportedSchemaVersion = 1

// MonthLayout formats month identifiers, which are also history file names.
const MonthLayout = "2006-01"

// Direction identifies the traffic direction of a bucket. Both directions are
// stored separately and are never added together.
type Direction string

const (
	DirectionOutbound Direction = "outbound"
	DirectionInbound  Direction = "inbound"
)

// Valid reports whether the direction is known.
func (d Direction) Valid() bool {
	return d == DirectionOutbound || d == DirectionInbound
}

// Status is the state of one hourly bucket.
type Status string

const (
	// StatusComplete is an hour whose data is final.
	StatusComplete Status = "complete"
	// StatusProvisional is an hour that ended inside publication_latency_hours
	// and will be queried again.
	StatusProvisional Status = "provisional"
	// StatusNoData is a successful query of an hour without datapoints for a
	// VNIC that has been visible at some point. It counts as zero.
	StatusNoData Status = "no_data"
	// StatusIncomplete is an hour that could not be collected reliably: failed
	// query, VNIC never visible, hour before the VNIC was first visible, or data
	// outside OCI retention.
	StatusIncomplete Status = "incomplete"
)

// Valid reports whether the status is known.
func (s Status) Valid() bool {
	switch s {
	case StatusComplete, StatusProvisional, StatusNoData, StatusIncomplete:
		return true
	}
	return false
}

// Bucket is the storage unit: the traffic of one resource and direction during
// one UTC hour. Hour is the start of the hour.
type Bucket struct {
	ResourceID string    `json:"resource_id"`
	Direction  Direction `json:"direction"`
	Hour       time.Time `json:"hour"`
	Bytes      int64     `json:"bytes"`
	Status     Status    `json:"status"`
	CapturedAt time.Time `json:"captured_at"`
}

// bucketKey identifies a bucket. It is the deduplication key.
type bucketKey struct {
	ResourceID string
	Direction  Direction
	Hour       time.Time
}

func keyFor(resourceID string, direction Direction, hour time.Time) bucketKey {
	return bucketKey{ResourceID: resourceID, Direction: direction, Hour: hour.UTC()}
}

// History holds the buckets of one month. A finalized history is immutable.
type History struct {
	SchemaVersion int        `json:"schema_version"`
	Month         string     `json:"month"`
	Finalized     bool       `json:"finalized"`
	FinalizedAt   *time.Time `json:"finalized_at,omitempty"`
	Buckets       []Bucket   `json:"buckets"`

	index map[bucketKey]int
}

// NewHistory returns an empty history for month ("YYYY-MM").
func NewHistory(month string) *History {
	return &History{
		SchemaVersion: CurrentSchemaVersion,
		Month:         month,
		Buckets:       []Bucket{},
		index:         make(map[bucketKey]int),
	}
}

// Len returns the number of stored buckets.
func (h *History) Len() int {
	return len(h.Buckets)
}

// Get returns the bucket for the key.
func (h *History) Get(resourceID string, direction Direction, hour time.Time) (Bucket, bool) {
	h.ensureIndex()
	i, ok := h.index[keyFor(resourceID, direction, hour)]
	if !ok {
		return Bucket{}, false
	}
	return h.Buckets[i], true
}

// Set inserts or overwrites the bucket identified by (resource_id, direction,
// hour). It returns the previous bucket and whether it existed. The hour is
// normalized to UTC and truncated to the hour.
func (h *History) Set(b Bucket) (Bucket, bool) {
	h.ensureIndex()
	b.Hour = b.Hour.UTC().Truncate(time.Hour)
	key := keyFor(b.ResourceID, b.Direction, b.Hour)
	if i, ok := h.index[key]; ok {
		previous := h.Buckets[i]
		h.Buckets[i] = b
		return previous, true
	}
	h.index[key] = len(h.Buckets)
	h.Buckets = append(h.Buckets, b)
	return Bucket{}, false
}

// Sorted returns a copy of the buckets ordered by hour, resource and direction.
// The returned slice is safe to modify.
func (h *History) Sorted() []Bucket {
	out := make([]Bucket, len(h.Buckets))
	copy(out, h.Buckets)
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Hour.Equal(out[j].Hour) {
			return out[i].Hour.Before(out[j].Hour)
		}
		if out[i].ResourceID != out[j].ResourceID {
			return out[i].ResourceID < out[j].ResourceID
		}
		return out[i].Direction < out[j].Direction
	})
	return out
}

// Sort orders the buckets in place so the JSON file is deterministic.
func (h *History) Sort() {
	sorted := h.Sorted()
	h.Buckets = sorted
	h.reindex()
}

// Finalize marks the history as immutable. It returns false if it was already
// finalized.
func (h *History) Finalize(at time.Time) bool {
	if h.Finalized {
		return false
	}
	utc := at.UTC()
	h.Finalized = true
	h.FinalizedAt = &utc
	return true
}

// HasData reports whether the history contains at least one bucket.
func (h *History) HasData() bool {
	return len(h.Buckets) > 0
}

func (h *History) ensureIndex() {
	if h.index != nil {
		return
	}
	h.reindex()
}

func (h *History) reindex() {
	h.index = make(map[bucketKey]int, len(h.Buckets))
	for i, b := range h.Buckets {
		h.index[keyFor(b.ResourceID, b.Direction, b.Hour)] = i
	}
}

// Validate checks the history invariants. It also rebuilds the in-memory index
// and rejects duplicate keys.
func (h *History) Validate() error {
	if h.SchemaVersion != CurrentSchemaVersion {
		return &SchemaVersionError{Path: "history " + h.Month, Got: h.SchemaVersion}
	}
	if _, err := MonthStart(h.Month); err != nil {
		return fmt.Errorf("history: invalid month %q: %w", h.Month, err)
	}
	if h.Finalized && h.FinalizedAt == nil {
		return fmt.Errorf("history %s: finalized without finalized_at", h.Month)
	}
	if !h.Finalized && h.FinalizedAt != nil {
		return fmt.Errorf("history %s: finalized_at set on an open month", h.Month)
	}

	monthStart, _ := MonthStart(h.Month)
	monthEnd, _ := MonthEnd(h.Month)

	seen := make(map[bucketKey]struct{}, len(h.Buckets))
	for i, b := range h.Buckets {
		if b.ResourceID == "" {
			return fmt.Errorf("history %s: bucket %d has no resource_id", h.Month, i)
		}
		if !b.Direction.Valid() {
			return fmt.Errorf("history %s: bucket %d has invalid direction %q", h.Month, i, b.Direction)
		}
		if !b.Status.Valid() {
			return fmt.Errorf("history %s: bucket %d has invalid status %q", h.Month, i, b.Status)
		}
		if !b.Hour.Equal(b.Hour.UTC().Truncate(time.Hour)) {
			return fmt.Errorf("history %s: bucket %d hour %s is not a UTC hour", h.Month, i, b.Hour)
		}
		if b.Hour.Before(monthStart) || !b.Hour.Before(monthEnd) {
			return fmt.Errorf("history %s: bucket %d hour %s is outside the month", h.Month, i, b.Hour)
		}
		if b.Bytes < 0 {
			return fmt.Errorf("history %s: bucket %d has negative bytes", h.Month, i)
		}
		if b.CapturedAt.IsZero() {
			return fmt.Errorf("history %s: bucket %d has no captured_at", h.Month, i)
		}
		key := keyFor(b.ResourceID, b.Direction, b.Hour)
		if _, dup := seen[key]; dup {
			return fmt.Errorf("history %s: duplicate bucket for %s %s %s", h.Month, b.ResourceID, b.Direction, b.Hour.Format(time.RFC3339))
		}
		seen[key] = struct{}{}
	}
	h.reindex()
	return nil
}

// VNICState is the persisted observation of one VNIC. Visibility is inferred
// from datapoints, not from ListMetrics.
type VNICState struct {
	FirstVisible *time.Time `json:"first_visible,omitempty"`
	LastVisible  *time.Time `json:"last_visible,omitempty"`
	LastDataAt   *time.Time `json:"last_data_at,omitempty"`
	Visible      bool       `json:"visible"`
	WarningSince *time.Time `json:"warning_since,omitempty"`
}

// Notification is one queued Telegram message. The queue logic belongs to the
// Hito 4; the Hito 2 only persists the field so the schema is stable.
type Notification struct {
	Kind        string     `json:"kind"`
	Period      string     `json:"period,omitempty"`
	Level       int        `json:"level,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	Attempts    int        `json:"attempts"`
	LastAttempt *time.Time `json:"last_attempt,omitempty"`
}

// Notification kinds. Only monthly_threshold is produced from the Hito 3 on;
// the rest are documented so the field has a closed vocabulary.
const (
	NotificationMonthlyThreshold   = "monthly_threshold"
	NotificationAnomaly6H          = "anomaly_6h"
	NotificationCollectionFailure  = "collection_failure"
	NotificationCollectionRecovery = "collection_recovery"
)

// MonthCorrections accumulates the corrections applied to a month while it is
// inside the requery grace period.
type MonthCorrections struct {
	BucketsCorrected int       `json:"buckets_corrected"`
	DeltaBytes       int64     `json:"delta_bytes"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// AlertState holds the alert bookkeeping.
// MonthlyAlerts maps a month ("YYYY-MM") to the thresholds already notified.
type AlertState struct {
	MonthlyAlerts map[string][]int `json:"monthly_alerts,omitempty"`
	Anomaly6H     *Anomaly6HState  `json:"anomaly_6h,omitempty"`
}

// Anomaly6HState is the bookkeeping of the abnormal-consumption alert over the
// last six complete hours. LastNotifiedAt is set only after a successful
// delivery, so the cooldown is measured from an alert that actually left the
// system.
type Anomaly6HState struct {
	LastNotifiedAt *time.Time `json:"last_notified_at,omitempty"`
}

// State is the content of state.json.
type State struct {
	SchemaVersion       int                         `json:"schema_version"`
	LastRun             *time.Time                  `json:"last_run,omitempty"`
	LastSuccessfulQuery *time.Time                  `json:"last_successful_query,omitempty"`
	ConsecutiveFailures int                         `json:"consecutive_failures"`
	LastFailure         *time.Time                  `json:"last_failure,omitempty"`
	FinalizedMonths     []string                    `json:"finalized_months"`
	Alerts              AlertState                  `json:"alerts"`
	Pending             []Notification              `json:"pending_notifications"`
	VNICs               map[string]VNICState        `json:"vnics"`
	Months              map[string]MonthCorrections `json:"months"`
}

// NewState returns an empty state for the current schema.
func NewState() State {
	state := State{SchemaVersion: CurrentSchemaVersion}
	state.ensureMaps()
	return state
}

func (s *State) ensureMaps() {
	if s.FinalizedMonths == nil {
		s.FinalizedMonths = []string{}
	}
	if s.Pending == nil {
		s.Pending = []Notification{}
	}
	if s.VNICs == nil {
		s.VNICs = map[string]VNICState{}
	}
	if s.Months == nil {
		s.Months = map[string]MonthCorrections{}
	}
	if s.Alerts.MonthlyAlerts == nil {
		s.Alerts.MonthlyAlerts = map[string][]int{}
	}
}

// IsFinalized reports whether month is recorded as finalized.
func (s *State) IsFinalized(month string) bool {
	for _, m := range s.FinalizedMonths {
		if m == month {
			return true
		}
	}
	return false
}

// MarkFinalized records month as finalized once.
func (s *State) MarkFinalized(month string) {
	if s.IsFinalized(month) {
		return
	}
	s.FinalizedMonths = append(s.FinalizedMonths, month)
	sort.Strings(s.FinalizedMonths)
}

// VNIC returns the persisted observation of a VNIC, zero if unknown.
func (s *State) VNIC(id string) VNICState {
	s.ensureMaps()
	return s.VNICs[id]
}

// SetVNIC stores the persisted observation of a VNIC.
func (s *State) SetVNIC(id string, v VNICState) {
	s.ensureMaps()
	s.VNICs[id] = v
}

// AddCorrections accumulates the corrections of one month.
func (s *State) AddCorrections(month string, buckets int, delta int64, at time.Time) {
	s.ensureMaps()
	c := s.Months[month]
	c.BucketsCorrected += buckets
	c.DeltaBytes += delta
	c.UpdatedAt = at.UTC()
	s.Months[month] = c
}

// Corrections returns the accumulated corrections of a month.
func (s *State) Corrections(month string) MonthCorrections {
	s.ensureMaps()
	return s.Months[month]
}

// Validate checks the state invariants.
func (s State) Validate() error {
	if s.SchemaVersion != CurrentSchemaVersion {
		return &SchemaVersionError{Path: "state", Got: s.SchemaVersion}
	}
	if s.ConsecutiveFailures < 0 {
		return errors.New("state: consecutive_failures must not be negative")
	}
	seen := make(map[string]struct{}, len(s.FinalizedMonths))
	for _, month := range s.FinalizedMonths {
		if _, err := MonthStart(month); err != nil {
			return fmt.Errorf("state: invalid finalized month %q", month)
		}
		if _, dup := seen[month]; dup {
			return fmt.Errorf("state: duplicate finalized month %q", month)
		}
		seen[month] = struct{}{}
	}
	for month := range s.Months {
		if _, err := MonthStart(month); err != nil {
			return fmt.Errorf("state: invalid month %q in months", month)
		}
	}
	for month, levels := range s.Alerts.MonthlyAlerts {
		if _, err := MonthStart(month); err != nil {
			return fmt.Errorf("state: invalid month %q in alerts", month)
		}
		for _, level := range levels {
			if level <= 0 || level > 100 {
				return fmt.Errorf("state: invalid alerted percentage %d for %s", level, month)
			}
		}
	}
	if s.Alerts.Anomaly6H != nil && s.Alerts.Anomaly6H.LastNotifiedAt != nil && s.Alerts.Anomaly6H.LastNotifiedAt.IsZero() {
		return errors.New("state: anomaly_6h last_notified_at is the zero time")
	}
	for id, v := range s.VNICs {
		if id == "" {
			return errors.New("state: empty VNIC OCID")
		}
		if v.WarningSince != nil && v.FirstVisible == nil && v.LastVisible == nil {
			return fmt.Errorf("state: vnic %s has a warning but was never visible", id)
		}
	}
	for i, n := range s.Pending {
		if n.Kind == "" {
			return fmt.Errorf("state: pending notification %d has no kind", i)
		}
		if n.Period != "" {
			if _, err := MonthStart(n.Period); err != nil {
				return fmt.Errorf("state: pending notification %d has invalid period %q", i, n.Period)
			}
		}
		if n.CreatedAt.IsZero() {
			return fmt.Errorf("state: pending notification %d has no created_at", i)
		}
		if n.Attempts < 0 {
			return fmt.Errorf("state: pending notification %d has negative attempts", i)
		}
	}
	return nil
}

// FormatMonth returns the "YYYY-MM" identifier of t in UTC.
func FormatMonth(t time.Time) string {
	return t.UTC().Format(MonthLayout)
}

// MonthStart parses "YYYY-MM" and returns the first instant of the month in UTC.
func MonthStart(month string) (time.Time, error) {
	t, err := time.ParseInLocation(MonthLayout, month, time.UTC)
	if err != nil || t.Format(MonthLayout) != month {
		return time.Time{}, fmt.Errorf("invalid month %q, want YYYY-MM", month)
	}
	return t.UTC(), nil
}

// MonthEnd parses "YYYY-MM" and returns the first instant of the next month in
// UTC, which is the exclusive end of the month.
func MonthEnd(month string) (time.Time, error) {
	start, err := MonthStart(month)
	if err != nil {
		return time.Time{}, err
	}
	return start.AddDate(0, 1, 0), nil
}

// PreviousMonth returns the "YYYY-MM" immediately before month.
func PreviousMonth(month string) (string, error) {
	start, err := MonthStart(month)
	if err != nil {
		return "", err
	}
	return FormatMonth(start.AddDate(0, 0, -1)), nil
}
