package persistence

import (
	"fmt"
	"os"
)

// Lock is an exclusive advisory lock over the state directory.
type Lock struct {
	file *os.File
}

// Lock acquires a non-blocking exclusive flock on <state_dir>/lock. It returns
// ErrLocked when another execution holds it.
func (s *Store) Lock() (*Lock, error) {
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(s.LockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := tryLockFile(file); err != nil {
		file.Close()
		return nil, err
	}
	return &Lock{file: file}, nil
}

// Close releases the lock. It is safe to call more than once.
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := unlockFile(file)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("unlock state directory: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close lock file: %w", closeErr)
	}
	return nil
}
