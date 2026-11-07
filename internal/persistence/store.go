package persistence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Sentinel errors. Callers use errors.Is to decide the exit code.
var (
	// ErrLocked means another execution holds the state directory lock.
	ErrLocked = errors.New("state directory is locked by another execution")
	// ErrSchemaVersion means a file declares a schema version this binary does
	// not understand. The file is never overwritten.
	ErrSchemaVersion = errors.New("unsupported schema version")
	// ErrFinalizedHistory means a write was attempted on an immutable month.
	ErrFinalizedHistory = errors.New("history file is finalized")
	// ErrCorrupt means a file could not be decoded or validated.
	ErrCorrupt = errors.New("corrupt file")
)

// SchemaVersionError reports an unsupported schema version.
type SchemaVersionError struct {
	Path string
	Got  int
}

func (e *SchemaVersionError) Error() string {
	return fmt.Sprintf("%s: %v: got %d, want %d", e.Path, ErrSchemaVersion, e.Got, CurrentSchemaVersion)
}

func (e *SchemaVersionError) Unwrap() error { return ErrSchemaVersion }

// CorruptionError reports a corrupt file that was moved aside.
type CorruptionError struct {
	Path    string
	MovedTo string
	Err     error
}

func (e *CorruptionError) Error() string {
	if e.MovedTo == "" {
		return fmt.Sprintf("%s: %v: %v", e.Path, ErrCorrupt, e.Err)
	}
	return fmt.Sprintf("%s: %v: %v (moved to %s)", e.Path, ErrCorrupt, e.Err, e.MovedTo)
}

func (e *CorruptionError) Unwrap() error { return e.Err }

func (e *CorruptionError) Is(target error) bool { return target == ErrCorrupt }

// Store manages the state directory: state.json, lock and history/.
type Store struct {
	dir string
}

// New returns a Store rooted at dir. It does not touch the filesystem.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// Dir returns the state directory.
func (s *Store) Dir() string { return s.dir }

// StatePath returns the path of state.json.
func (s *Store) StatePath() string { return filepath.Join(s.dir, "state.json") }

// LockPath returns the path of the lock file.
func (s *Store) LockPath() string { return filepath.Join(s.dir, "lock") }

// HistoryDir returns the path of the history directory.
func (s *Store) HistoryDir() string { return filepath.Join(s.dir, "history") }

// HistoryPath returns the path of the history file of month.
func (s *Store) HistoryPath(month string) (string, error) {
	if _, err := MonthStart(month); err != nil {
		return "", err
	}
	return filepath.Join(s.HistoryDir(), month+".json"), nil
}

// Ensure creates the state directory and the history directory and enforces
// the 0700 permissions on both.
func (s *Store) Ensure() error {
	if s.dir == "" {
		return errors.New("state directory is empty")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create state directory %q: %w", s.dir, err)
	}
	if err := os.MkdirAll(s.HistoryDir(), 0o700); err != nil {
		return fmt.Errorf("create history directory %q: %w", s.HistoryDir(), err)
	}
	for _, dir := range []string{s.dir, s.HistoryDir()} {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("set permissions on %q: %w", dir, err)
		}
	}
	return nil
}

// ListMonths returns the months with a history file, oldest first.
func (s *Store) ListMonths() ([]string, error) {
	entries, err := os.ReadDir(s.HistoryDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list history directory: %w", err)
	}
	var months []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		month := strings.TrimSuffix(name, ".json")
		if _, err := MonthStart(month); err != nil {
			continue
		}
		months = append(months, month)
	}
	sort.Strings(months)
	return months, nil
}

// HasState reports whether state.json exists.
func (s *Store) HasState() bool {
	_, err := os.Stat(s.StatePath())
	return err == nil
}

// HasHistory reports whether the history file of month exists.
func (s *Store) HasHistory(month string) bool {
	path, err := s.HistoryPath(month)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// LoadOptions controls how a read treats a corrupt file.
type LoadOptions struct {
	// Quarantine moves a corrupt file to <name>.corrupt-<timestamp> before
	// returning the error. When false the file is left untouched, which is
	// what a dry run needs.
	Quarantine bool
}

// LoadState reads state.json with the default options: a corrupt file is moved
// aside. A missing file returns an empty state and no error. An unknown schema
// version is reported without moving or modifying the file.
func (s *Store) LoadState() (State, error) {
	return s.LoadStateWith(LoadOptions{Quarantine: true})
}

// LoadStateWith reads state.json using options.
func (s *Store) LoadStateWith(options LoadOptions) (State, error) {
	path := s.StatePath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewState(), nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read state %q: %w", path, err)
	}

	version, err := peekSchema(data)
	if err != nil {
		return State{}, s.corrupt(path, fmt.Errorf("decode state: %w", err), options)
	}
	if version != CurrentSchemaVersion {
		return State{}, &SchemaVersionError{Path: path, Got: version}
	}

	var state State
	if err := decodeStrict(data, &state); err != nil {
		return State{}, s.corrupt(path, fmt.Errorf("decode state: %w", err), options)
	}
	state.ensureMaps()
	if err := state.Validate(); err != nil {
		return State{}, s.corrupt(path, err, options)
	}
	return state, nil
}

// SaveState writes state.json atomically with 0600. It refuses to overwrite a
// file whose schema version is unknown or that cannot be decoded.
func (s *Store) SaveState(state State) error {
	state.ensureMaps()
	if state.SchemaVersion != CurrentSchemaVersion {
		return &SchemaVersionError{Path: s.StatePath(), Got: state.SchemaVersion}
	}
	if err := state.Validate(); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	if err := s.Ensure(); err != nil {
		return err
	}
	if err := s.checkStateWritable(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	return writeFileAtomic(s.StatePath(), append(data, '\n'), 0o600)
}

// LoadHistory reads the history of month with the default options: a corrupt
// file is moved aside. It returns exists=false and an empty history when the
// file is missing.
func (s *Store) LoadHistory(month string) (*History, bool, error) {
	return s.LoadHistoryWith(month, LoadOptions{Quarantine: true})
}

// LoadHistoryWith reads the history of month using options.
func (s *Store) LoadHistoryWith(month string, options LoadOptions) (*History, bool, error) {
	path, err := s.HistoryPath(month)
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewHistory(month), false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read history %q: %w", path, err)
	}

	version, err := peekSchema(data)
	if err != nil {
		return nil, false, s.corrupt(path, fmt.Errorf("decode history %s: %w", month, err), options)
	}
	if version != CurrentSchemaVersion {
		return nil, false, &SchemaVersionError{Path: path, Got: version}
	}

	var history History
	if err := decodeStrict(data, &history); err != nil {
		return nil, false, s.corrupt(path, fmt.Errorf("decode history %s: %w", month, err), options)
	}
	if history.Month != month {
		return nil, false, s.corrupt(path, fmt.Errorf("history %s: file declares month %q", month, history.Month), options)
	}
	history.ensureIndex()
	if err := history.Validate(); err != nil {
		return nil, false, s.corrupt(path, err, options)
	}
	return &history, true, nil
}

// SaveHistory writes the history of its month atomically with 0600. It refuses
// to overwrite an existing finalized file or a file with an unknown schema.
func (s *Store) SaveHistory(history *History) error {
	if history == nil {
		return errors.New("history is nil")
	}
	if history.SchemaVersion != CurrentSchemaVersion {
		return &SchemaVersionError{Path: "history " + history.Month, Got: history.SchemaVersion}
	}
	if err := history.Validate(); err != nil {
		return fmt.Errorf("history: %w", err)
	}
	if err := s.Ensure(); err != nil {
		return err
	}
	path, err := s.HistoryPath(history.Month)
	if err != nil {
		return err
	}
	if err := s.checkHistoryWritable(path, history.Month); err != nil {
		return err
	}
	history.Sort()
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("encode history %s: %w", history.Month, err)
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}

// checkStateWritable refuses to overwrite a file this binary cannot safely
// replace.
func (s *Store) checkStateWritable() error {
	path := s.StatePath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read existing state: %w", err)
	}
	version, err := peekSchema(data)
	if err != nil {
		return &CorruptionError{Path: path, Err: fmt.Errorf("refusing to overwrite: %w", err)}
	}
	if version != CurrentSchemaVersion {
		return &SchemaVersionError{Path: path, Got: version}
	}
	return nil
}

// checkHistoryWritable refuses to overwrite a finalized or unknown file.
func (s *Store) checkHistoryWritable(path, month string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read existing history %s: %w", month, err)
	}
	var header struct {
		SchemaVersion int  `json:"schema_version"`
		Finalized     bool `json:"finalized"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return &CorruptionError{Path: path, Err: fmt.Errorf("refusing to overwrite: %w", err)}
	}
	if header.SchemaVersion != CurrentSchemaVersion {
		return &SchemaVersionError{Path: path, Got: header.SchemaVersion}
	}
	if header.Finalized {
		return fmt.Errorf("%s: %w", path, ErrFinalizedHistory)
	}
	return nil
}

// corrupt reports a corrupt file, moving it aside only when options ask for it.
func (s *Store) corrupt(path string, cause error, options LoadOptions) error {
	if !options.Quarantine {
		return &CorruptionError{Path: path, Err: cause}
	}
	return s.quarantine(path, cause)
}

// quarantine moves a corrupt file aside and returns a CorruptionError. It never
// deletes the file.
func (s *Store) quarantine(path string, cause error) error {
	target, err := nextCorruptName(path)
	if err != nil {
		return &CorruptionError{Path: path, Err: fmt.Errorf("%w (cannot build quarantine name: %v)", cause, err)}
	}
	if err := os.Rename(path, target); err != nil {
		return &CorruptionError{Path: path, Err: fmt.Errorf("%w (cannot move aside: %v)", cause, err)}
	}
	return &CorruptionError{Path: path, MovedTo: target, Err: cause}
}

func nextCorruptName(path string) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	base := path + ".corrupt-" + stamp
	candidate := base
	for i := 1; ; i++ {
		_, err := os.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

func peekSchema(data []byte) (int, error) {
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return 0, err
	}
	return header.SchemaVersion, nil
}

func decodeStrict(data []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("file contains more than one JSON value")
		}
		return err
	}
	return nil
}

// writeFileAtomic writes data to a temporary file in the same directory,
// fsyncs it, renames it over path and fsyncs the directory.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", path, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return fmt.Errorf("chmod temporary file for %q: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temporary file for %q: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("fsync temporary file for %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temporary file for %q: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename temporary file over %q: %w", path, err)
	}
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}
