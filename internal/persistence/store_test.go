package persistence

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store := New(t.TempDir())
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	return store
}

func utc(y int, m time.Month, d, h int) time.Time {
	return time.Date(y, m, d, h, 0, 0, 0, time.UTC)
}

func testBucket(resource string, direction Direction, hour time.Time, bytes int64, status Status) Bucket {
	return Bucket{
		ResourceID: resource,
		Direction:  direction,
		Hour:       hour,
		Bytes:      bytes,
		Status:     status,
		CapturedAt: utc(2026, 11, 7, 13),
	}
}

func testHistory(t *testing.T) *History {
	t.Helper()
	history := NewHistory("2026-11")
	history.Set(testBucket("ocid1.vnic.oc1.iad.a", DirectionOutbound, utc(2026, 11, 1, 0), 100, StatusComplete))
	history.Set(testBucket("ocid1.vnic.oc1.iad.a", DirectionOutbound, utc(2026, 11, 1, 1), 200, StatusComplete))
	history.Set(testBucket("ocid1.vnic.oc1.iad.a", DirectionInbound, utc(2026, 11, 1, 0), 50, StatusNoData))
	return history
}

func TestStateRoundTrip(t *testing.T) {
	store := newTestStore(t)
	state := NewState()
	lastRun := utc(2026, 11, 7, 13)
	success := utc(2026, 11, 7, 12)
	state.LastRun = &lastRun
	state.LastSuccessfulQuery = &success
	state.ConsecutiveFailures = 2
	state.MarkFinalized("2026-10")
	state.SetVNIC("ocid1.vnic.oc1.iad.a", VNICState{
		FirstVisible: timePtr(utc(2026, 11, 1, 0)),
		LastVisible:  timePtr(utc(2026, 11, 7, 12)),
		Visible:      true,
	})
	state.AddCorrections("2026-10", 3, -120, success)
	state.Pending = append(state.Pending, Notification{
		Kind:      NotificationMonthlyThreshold,
		Period:    "2026-10",
		Level:     90,
		CreatedAt: success,
		Text:      "alerta pendiente de prueba",
	})
	state.Alerts.MonthlyAlerts["2026-10"] = []int{50, 75}
	state.Alerts.CollectionFailure = &CollectionFailureState{
		FailureSince:   timePtr(utc(2026, 11, 6, 0)),
		LastNotifiedAt: timePtr(utc(2026, 11, 6, 12)),
	}
	telegramEnabled := false
	state.TelegramEnabled = &telegramEnabled

	if err := store.SaveState(state); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	info, err := os.Stat(store.StatePath())
	if err != nil {
		t.Fatalf("stat state: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("state permissions = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(store.Dir())
	if err != nil {
		t.Fatalf("stat state dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("state dir permissions = %o, want 700", perm)
	}

	loaded, err := store.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	wantJSON, _ := json.Marshal(state)
	gotJSON, _ := json.Marshal(loaded)
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("state round trip mismatch\nwant %s\ngot  %s", wantJSON, gotJSON)
	}
	if !store.HasState() {
		t.Error("HasState() = false, want true")
	}
}

func TestLoadStateMissingReturnsEmpty(t *testing.T) {
	store := New(t.TempDir())
	state, err := store.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if state.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", state.SchemaVersion, CurrentSchemaVersion)
	}
	if state.VNICs == nil || state.Months == nil {
		t.Error("maps not initialized")
	}
	if store.HasState() {
		t.Error("HasState() = true, want false")
	}
}

func TestCorruptStateQuarantined(t *testing.T) {
	store := newTestStore(t)
	const corrupt = "{ this is not json"
	if err := os.WriteFile(store.StatePath(), []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := store.LoadState()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("LoadState() error = %v, want ErrCorrupt", err)
	}
	var corruption *CorruptionError
	if !errors.As(err, &corruption) {
		t.Fatalf("error is not CorruptionError: %v", err)
	}
	if corruption.MovedTo == "" {
		t.Fatal("MovedTo is empty")
	}
	if _, statErr := os.Stat(store.StatePath()); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("original state still exists: %v", statErr)
	}
	moved, readErr := os.ReadFile(corruption.MovedTo)
	if readErr != nil {
		t.Fatalf("read quarantined file: %v", readErr)
	}
	if string(moved) != corrupt {
		t.Errorf("quarantined content = %q, want %q", moved, corrupt)
	}
}

func TestLoadStateReadOnlyDoesNotQuarantine(t *testing.T) {
	store := newTestStore(t)
	const content = "{ not json"
	if err := os.WriteFile(store.StatePath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := store.LoadStateWith(LoadOptions{Quarantine: false})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("LoadStateWith() error = %v, want ErrCorrupt", err)
	}
	var corruption *CorruptionError
	if !errors.As(err, &corruption) {
		t.Fatalf("error is not CorruptionError: %v", err)
	}
	if corruption.MovedTo != "" {
		t.Errorf("MovedTo = %q, want empty", corruption.MovedTo)
	}
	got, readErr := os.ReadFile(store.StatePath())
	if readErr != nil {
		t.Fatalf("corrupt state was moved: %v", readErr)
	}
	if string(got) != content {
		t.Errorf("state content changed to %q", got)
	}
}

func TestLoadHistoryReadOnlyDoesNotQuarantine(t *testing.T) {
	store := newTestStore(t)
	path, _ := store.HistoryPath("2026-11")
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := store.LoadHistoryWith("2026-11", LoadOptions{Quarantine: false})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("LoadHistoryWith() error = %v, want ErrCorrupt", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("corrupt history was moved: %v", statErr)
	}
}

func TestUnknownSchemaStateIsPreserved(t *testing.T) {
	store := newTestStore(t)
	const content = `{"schema_version": 4, "future": true}`
	if err := os.WriteFile(store.StatePath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := store.LoadState()
	if !errors.Is(err, ErrSchemaVersion) {
		t.Fatalf("LoadState() error = %v, want ErrSchemaVersion", err)
	}
	got, readErr := os.ReadFile(store.StatePath())
	if readErr != nil {
		t.Fatalf("state file was moved: %v", readErr)
	}
	if string(got) != content {
		t.Errorf("state file content changed to %q", got)
	}

	if saveErr := store.SaveState(NewState()); !errors.Is(saveErr, ErrSchemaVersion) {
		t.Fatalf("SaveState() error = %v, want ErrSchemaVersion", saveErr)
	}
	got, _ = os.ReadFile(store.StatePath())
	if string(got) != content {
		t.Errorf("SaveState overwrote an unknown schema: %q", got)
	}
}

func TestHistoryRoundTripAndOrder(t *testing.T) {
	store := newTestStore(t)
	history := NewHistory("2026-11")
	history.Set(testBucket("ocid1.vnic.oc1.iad.b", DirectionOutbound, utc(2026, 11, 1, 2), 3, StatusComplete))
	history.Set(testBucket("ocid1.vnic.oc1.iad.a", DirectionOutbound, utc(2026, 11, 1, 1), 2, StatusProvisional))
	history.Set(testBucket("ocid1.vnic.oc1.iad.a", DirectionOutbound, utc(2026, 11, 1, 0), 1, StatusComplete))

	if err := store.SaveHistory(history); err != nil {
		t.Fatalf("SaveHistory() error = %v", err)
	}

	loaded, exists, err := store.LoadHistory("2026-11")
	if err != nil {
		t.Fatalf("LoadHistory() error = %v", err)
	}
	if !exists {
		t.Fatal("exists = false, want true")
	}
	if loaded.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", loaded.Len())
	}
	hours := []time.Time{utc(2026, 11, 1, 0), utc(2026, 11, 1, 1), utc(2026, 11, 1, 2)}
	for i, hour := range hours {
		if !loaded.Buckets[i].Hour.Equal(hour) {
			t.Errorf("bucket %d hour = %s, want %s", i, loaded.Buckets[i].Hour, hour)
		}
	}
	got, ok := loaded.Get("ocid1.vnic.oc1.iad.a", DirectionOutbound, utc(2026, 11, 1, 1))
	if !ok || got.Bytes != 2 || got.Status != StatusProvisional {
		t.Errorf("Get() = %+v, %v", got, ok)
	}
}

func TestOverwriteByKeyKeepsOneBucket(t *testing.T) {
	history := NewHistory("2026-11")
	b := testBucket("ocid1.vnic.oc1.iad.a", DirectionOutbound, utc(2026, 11, 1, 0), 10, StatusComplete)
	history.Set(b)
	b.Bytes = 25
	previous, existed := history.Set(b)
	if !existed {
		t.Fatal("second Set() did not report an existing bucket")
	}
	if previous.Bytes != 10 {
		t.Errorf("previous bytes = %d, want 10", previous.Bytes)
	}
	if history.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", history.Len())
	}
}

func TestSetNormalizesHourToUTC(t *testing.T) {
	history := NewHistory("2026-11")
	loc := time.FixedZone("UTC-5", -5*3600)
	hour := time.Date(2026, 10, 31, 20, 0, 0, 0, loc) // 2026-11-01 01:00 UTC
	history.Set(Bucket{
		ResourceID: "ocid1.vnic.oc1.iad.a",
		Direction:  DirectionOutbound,
		Hour:       hour,
		Status:     StatusComplete,
		CapturedAt: utc(2026, 11, 7, 13),
	})
	if !history.Buckets[0].Hour.Equal(utc(2026, 11, 1, 1)) {
		t.Errorf("hour = %s, want 2026-11-01 01:00 UTC", history.Buckets[0].Hour)
	}
}

func TestLoadHistoryMissingReturnsEmpty(t *testing.T) {
	store := New(t.TempDir())
	history, exists, err := store.LoadHistory("2026-11")
	if err != nil {
		t.Fatalf("LoadHistory() error = %v", err)
	}
	if exists {
		t.Error("exists = true, want false")
	}
	if history.Len() != 0 || history.Month != "2026-11" {
		t.Errorf("history = %+v", history)
	}
}

func TestCorruptHistoryQuarantined(t *testing.T) {
	store := newTestStore(t)
	path, err := store.HistoryPath("2026-11")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, loadErr := store.LoadHistory("2026-11")
	if !errors.Is(loadErr, ErrCorrupt) {
		t.Fatalf("LoadHistory() error = %v, want ErrCorrupt", loadErr)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("corrupt history still in place: %v", statErr)
	}
}

func TestUnknownSchemaHistoryIsPreserved(t *testing.T) {
	store := newTestStore(t)
	path, _ := store.HistoryPath("2026-11")
	const content = `{"schema_version": 9, "month": "2026-11", "buckets": []}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := store.LoadHistory("2026-11")
	if !errors.Is(err, ErrSchemaVersion) {
		t.Fatalf("LoadHistory() error = %v, want ErrSchemaVersion", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("history was moved: %v", readErr)
	}
	if string(got) != content {
		t.Errorf("history content changed to %q", got)
	}
}

func TestSaveHistoryRefusesFinalized(t *testing.T) {
	store := newTestStore(t)
	history := testHistory(t)
	history.Finalize(utc(2026, 11, 3, 0))
	if err := store.SaveHistory(history); err != nil {
		t.Fatalf("SaveHistory(finalized) error = %v", err)
	}
	history.Set(testBucket("ocid1.vnic.oc1.iad.a", DirectionOutbound, utc(2026, 11, 2, 0), 999, StatusComplete))
	if err := store.SaveHistory(history); !errors.Is(err, ErrFinalizedHistory) {
		t.Fatalf("SaveHistory() error = %v, want ErrFinalizedHistory", err)
	}
}

func TestSaveHistoryRefusesUnknownSchemaOnDisk(t *testing.T) {
	store := newTestStore(t)
	path, _ := store.HistoryPath("2026-11")
	const content = `{"schema_version": 4, "month": "2026-11", "buckets": []}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory(testHistory(t)); !errors.Is(err, ErrSchemaVersion) {
		t.Fatalf("SaveHistory() error = %v, want ErrSchemaVersion", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("history file moved: %v", err)
	}
	if string(got) != content {
		t.Errorf("history overwritten: %q", got)
	}
}

func TestHistoryValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*History)
		wantErr string
	}{
		{"bad status", func(h *History) { h.Buckets[0].Status = "bogus" }, "invalid status"},
		{"bad direction", func(h *History) { h.Buckets[0].Direction = "sideways" }, "invalid direction"},
		{"empty resource", func(h *History) { h.Buckets[0].ResourceID = "" }, "no resource_id"},
		{"negative bytes", func(h *History) { h.Buckets[0].Bytes = -1 }, "negative bytes"},
		{"missing captured_at", func(h *History) { h.Buckets[0].CapturedAt = time.Time{} }, "no captured_at"},
		{"unaligned hour", func(h *History) { h.Buckets[0].Hour = h.Buckets[0].Hour.Add(30 * time.Minute) }, "not a UTC hour"},
		{"outside month", func(h *History) { h.Buckets[0].Hour = utc(2026, 12, 1, 0) }, "outside the month"},
		{"finalized without date", func(h *History) { h.Finalized = true }, "finalized without finalized_at"},
		{"date without finalized", func(h *History) { h.FinalizedAt = timePtr(utc(2026, 11, 3, 0)) }, "finalized_at set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			history := testHistory(t)
			tc.mutate(history)
			err := history.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestHistoryDuplicateKeyRejected(t *testing.T) {
	history := testHistory(t)
	duplicate := history.Buckets[0]
	history.Buckets = append(history.Buckets, duplicate)
	err := history.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate bucket") {
		t.Fatalf("Validate() error = %v, want duplicate bucket", err)
	}
}

func TestHistoryMonthMismatchIsCorrupt(t *testing.T) {
	store := newTestStore(t)
	path, _ := store.HistoryPath("2026-11")
	content := `{"schema_version":1,"month":"2026-10","finalized":false,"buckets":[]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := store.LoadHistory("2026-11")
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("LoadHistory() error = %v, want ErrCorrupt", err)
	}
}

func TestStateValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*State)
		wantErr string
	}{
		{"bad finalized month", func(s *State) { s.FinalizedMonths = []string{"2026-13"} }, "invalid finalized month"},
		{"duplicate finalized", func(s *State) { s.FinalizedMonths = []string{"2026-10", "2026-10"} }, "duplicate finalized"},
		{"negative failures", func(s *State) { s.ConsecutiveFailures = -1 }, "consecutive_failures"},
		{"bad month key", func(s *State) { s.Months["nope"] = MonthCorrections{} }, "invalid month"},
		{"warning without visibility", func(s *State) {
			s.VNICs["ocid1.vnic.oc1.iad.a"] = VNICState{WarningSince: timePtr(utc(2026, 11, 7, 13))}
		}, "never visible"},
		{"pending without kind", func(s *State) {
			s.Pending = append(s.Pending, Notification{CreatedAt: utc(2026, 11, 7, 13)})
		}, "no kind"},
		{"pending bad period", func(s *State) {
			s.Pending = append(s.Pending, Notification{Kind: NotificationAnomaly6H, Period: "x", CreatedAt: utc(2026, 11, 7, 13)})
		}, "invalid period"},
		{"pending unknown kind", func(s *State) {
			s.Pending = append(s.Pending, Notification{Kind: "future", CreatedAt: utc(2026, 11, 7, 13)})
		}, "unknown kind"},
		{"pending monthly without level", func(s *State) {
			s.Pending = append(s.Pending, Notification{Kind: NotificationMonthlyThreshold, Period: "2026-11", CreatedAt: utc(2026, 11, 7, 13)})
		}, "no level"},
		{"pending text too long", func(s *State) {
			s.Pending = append(s.Pending, Notification{
				Kind: NotificationAnomaly6H, Period: "2026-11", CreatedAt: utc(2026, 11, 7, 13),
				Text: strings.Repeat("x", MaxNotificationTextRunes+1),
			})
		}, "exceeds"},
		{"collection failure zero time", func(s *State) {
			zero := time.Time{}
			s.Alerts.CollectionFailure = &CollectionFailureState{FailureSince: &zero}
		}, "zero time"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := NewState()
			tc.mutate(&state)
			err := state.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestStateHelpers(t *testing.T) {
	state := NewState()
	if state.IsFinalized("2026-10") {
		t.Error("IsFinalized() = true on a new state")
	}
	state.MarkFinalized("2026-11")
	state.MarkFinalized("2026-09")
	state.MarkFinalized("2026-11")
	if !state.IsFinalized("2026-11") || !state.IsFinalized("2026-09") {
		t.Error("MarkFinalized() did not record the months")
	}
	if len(state.FinalizedMonths) != 2 {
		t.Errorf("FinalizedMonths = %v, want 2 entries", state.FinalizedMonths)
	}
	state.AddCorrections("2026-10", 1, 100, utc(2026, 11, 1, 0))
	state.AddCorrections("2026-10", 2, -30, utc(2026, 11, 1, 0))
	c := state.Corrections("2026-10")
	if c.BucketsCorrected != 3 || c.DeltaBytes != 70 {
		t.Errorf("Corrections() = %+v, want 3 buckets and 70 bytes", c)
	}
}

func TestListMonthsAndHasHistory(t *testing.T) {
	store := newTestStore(t)
	history := testHistory(t)
	if err := store.SaveHistory(history); err != nil {
		t.Fatal(err)
	}
	if !store.HasHistory("2026-11") {
		t.Error("HasHistory(2026-11) = false")
	}
	if store.HasHistory("2026-10") {
		t.Error("HasHistory(2026-10) = true")
	}
	if _, err := store.HistoryPath("2026-13"); err == nil {
		t.Error("HistoryPath() accepted an invalid month")
	}
	months, err := store.ListMonths()
	if err != nil {
		t.Fatalf("ListMonths() error = %v", err)
	}
	if len(months) != 1 || months[0] != "2026-11" {
		t.Errorf("ListMonths() = %v, want [2026-11]", months)
	}
}

func TestMonthHelpers(t *testing.T) {
	start, err := MonthStart("2026-11")
	if err != nil {
		t.Fatalf("MonthStart() error = %v", err)
	}
	if !start.Equal(utc(2026, 11, 1, 0)) {
		t.Errorf("MonthStart() = %s", start)
	}
	end, err := MonthEnd("2026-11")
	if err != nil {
		t.Fatalf("MonthEnd() error = %v", err)
	}
	if !end.Equal(utc(2026, 12, 1, 0)) {
		t.Errorf("MonthEnd() = %s", end)
	}
	previous, err := PreviousMonth("2026-01")
	if err != nil {
		t.Fatalf("PreviousMonth() error = %v", err)
	}
	if previous != "2025-12" {
		t.Errorf("PreviousMonth() = %q, want 2025-12", previous)
	}
	if _, err := MonthStart("2026-1"); err == nil {
		t.Error("MonthStart() accepted a malformed month")
	}
	if got := FormatMonth(utc(2026, 11, 30, 23)); got != "2026-11" {
		t.Errorf("FormatMonth() = %q", got)
	}
}

func TestSaveLeavesNoTemporaryFiles(t *testing.T) {
	store := newTestStore(t)
	if err := store.SaveState(NewState()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHistory(testHistory(t)); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Errorf("temporary file left behind: %s", entry.Name())
		}
	}
	historyEntries, err := os.ReadDir(store.HistoryDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range historyEntries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Errorf("temporary history file left behind: %s", entry.Name())
		}
	}
}

func TestFinalizeIsIdempotent(t *testing.T) {
	history := NewHistory("2026-11")
	if !history.Finalize(utc(2026, 11, 3, 0)) {
		t.Fatal("first Finalize() = false")
	}
	if history.Finalize(utc(2026, 11, 4, 0)) {
		t.Fatal("second Finalize() = true")
	}
	if !history.FinalizedAt.Equal(utc(2026, 11, 3, 0)) {
		t.Errorf("FinalizedAt = %s", history.FinalizedAt)
	}
}

func timePtr(t time.Time) *time.Time { return &t }

func TestConcurrentAtomicWritesRemainValid(t *testing.T) {
	store := newTestStore(t)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(value int) {
			defer wg.Done()
			state := NewState()
			state.ConsecutiveFailures = value % 3
			_ = store.SaveState(state)
		}(i)
	}
	wg.Wait()

	state, err := store.LoadState()
	if err != nil {
		t.Fatalf("LoadState() after concurrent writes = %v", err)
	}
	if state.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d", state.SchemaVersion)
	}
}

func TestStateVersion1IsMigratedInMemoryAndOnSave(t *testing.T) {
	store := newTestStore(t)
	const content = `{"schema_version":1,"alerts":{"monthly_alerts":{"2026-10":[50,75]}}}`
	if err := os.WriteFile(store.StatePath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := store.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if state.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", state.SchemaVersion, CurrentSchemaVersion)
	}
	if got := state.Alerts.MonthlyAlerts["2026-10"]; len(got) != 2 || got[0] != 50 || got[1] != 75 {
		t.Errorf("migrated monthly alerts = %v, want [50 75]", got)
	}
	if state.Alerts.Anomaly6H != nil {
		t.Errorf("migrated anomaly state = %+v, want nil", state.Alerts.Anomaly6H)
	}

	if err := store.SaveState(state); err != nil {
		t.Fatalf("SaveState() after migration error = %v", err)
	}
	raw, err := os.ReadFile(store.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		t.Fatal(err)
	}
	if header.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("saved schema_version = %d, want %d", header.SchemaVersion, CurrentSchemaVersion)
	}
}

func TestStateVersion2IsMigratedToVersion3(t *testing.T) {
	store := newTestStore(t)
	const content = `{"schema_version":2,"alerts":{"monthly_alerts":{"2026-10":[50,75]},"anomaly_6h":{"last_notified_at":"2026-10-15T00:00:00Z"}}}`
	if err := os.WriteFile(store.StatePath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := store.LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if state.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", state.SchemaVersion, CurrentSchemaVersion)
	}
	if got := state.Alerts.MonthlyAlerts["2026-10"]; len(got) != 2 || got[1] != 75 {
		t.Errorf("monthly alerts = %v, want [50 75]", got)
	}
	if state.Alerts.Anomaly6H == nil || state.Alerts.Anomaly6H.LastNotifiedAt == nil {
		t.Errorf("anomaly state = %+v, want last_notified_at kept", state.Alerts.Anomaly6H)
	}
	if state.Alerts.CollectionFailure != nil {
		t.Errorf("collection_failure = %+v, want nil after migration", state.Alerts.CollectionFailure)
	}
	if state.TelegramEnabled != nil {
		t.Errorf("telegram_enabled = %v, want nil after migration", state.TelegramEnabled)
	}

	if err := store.SaveState(state); err != nil {
		t.Fatalf("SaveState() after migration error = %v", err)
	}
	raw, err := os.ReadFile(store.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		t.Fatal(err)
	}
	if header.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("saved schema_version = %d, want %d", header.SchemaVersion, CurrentSchemaVersion)
	}
}

func TestHistoryVersion1IsMigratedInMemoryAndOnSave(t *testing.T) {
	store := newTestStore(t)
	path, err := store.HistoryPath("2026-11")
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"month":          "2026-11",
		"finalized":      false,
		"buckets": []map[string]any{{
			"resource_id": "ocid1.vnic.oc1.iad.a",
			"direction":   "outbound",
			"hour":        "2026-11-01T00:00:00Z",
			"bytes":       100,
			"status":      "complete",
			"captured_at": "2026-11-07T13:00:00Z",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	history, exists, err := store.LoadHistory("2026-11")
	if err != nil {
		t.Fatalf("LoadHistory() error = %v", err)
	}
	if !exists {
		t.Fatal("LoadHistory() exists = false")
	}
	if history.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", history.SchemaVersion, CurrentSchemaVersion)
	}
	if history.Len() != 1 {
		t.Errorf("buckets = %d, want 1", history.Len())
	}

	if err := store.SaveHistory(history); err != nil {
		t.Fatalf("SaveHistory() after migration error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		t.Fatal(err)
	}
	if header.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("saved schema_version = %d, want %d", header.SchemaVersion, CurrentSchemaVersion)
	}
}

func TestFinalizedVersion1HistoryIsReadButNotReopened(t *testing.T) {
	store := newTestStore(t)
	path, err := store.HistoryPath("2026-10")
	if err != nil {
		t.Fatal(err)
	}
	content := `{"schema_version":1,"month":"2026-10","finalized":true,"finalized_at":"2026-11-03T00:00:00Z","buckets":[]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	history, exists, err := store.LoadHistory("2026-10")
	if err != nil {
		t.Fatalf("LoadHistory() error = %v", err)
	}
	if !exists || !history.Finalized {
		t.Fatalf("history = %+v, exists = %v", history, exists)
	}
	if history.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", history.SchemaVersion, CurrentSchemaVersion)
	}
	if err := store.SaveHistory(history); !errors.Is(err, ErrFinalizedHistory) {
		t.Fatalf("SaveHistory() error = %v, want ErrFinalizedHistory", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != content {
		t.Errorf("finalized version 1 history was rewritten: %q", raw)
	}
}
