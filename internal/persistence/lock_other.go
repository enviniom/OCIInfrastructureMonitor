//go:build !unix

package persistence

import (
	"errors"
	"os"
)

func tryLockFile(*os.File) error {
	return errors.New("exclusive locking is not supported on this platform")
}

func unlockFile(*os.File) error {
	return nil
}
