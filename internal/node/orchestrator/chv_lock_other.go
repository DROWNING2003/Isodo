//go:build !linux && !darwin

package orchestrator

import (
	"errors"
	"os"
)

func lockCHVDirectory(string) (*os.File, error) {
	return nil, errors.New("CHV Node state locking requires Linux or macOS")
}
