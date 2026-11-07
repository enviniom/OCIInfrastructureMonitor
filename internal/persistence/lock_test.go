package persistence

import (
	"errors"
	"os"
	"testing"
)

func TestLockIsExclusive(t *testing.T) {
	store := New(t.TempDir())

	first, err := store.Lock()
	if err != nil {
		t.Fatalf("first Lock() error = %v", err)
	}

	second, err := store.Lock()
	if !errors.Is(err, ErrLocked) {
		if second != nil {
			second.Close()
		}
		t.Fatalf("second Lock() error = %v, want ErrLocked", err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	// Closing twice is safe.
	if err := first.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	third, err := store.Lock()
	if err != nil {
		t.Fatalf("Lock() after release error = %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestLockFilePermissions(t *testing.T) {
	store := New(t.TempDir())
	lock, err := store.Lock()
	if err != nil {
		t.Fatalf("Lock() error = %v", err)
	}
	defer lock.Close()

	info, err := os.Stat(store.LockPath())
	if err != nil {
		t.Fatalf("stat lock: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("lock permissions = %o, want 600", perm)
	}
}
