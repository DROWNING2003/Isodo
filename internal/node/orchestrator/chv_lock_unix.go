//go:build linux || darwin

package orchestrator

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lockCHVDirectory(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another Node owns the CHV state directory: %w", err)
	}
	return file, nil
}
