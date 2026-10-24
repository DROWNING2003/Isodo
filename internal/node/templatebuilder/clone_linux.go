package templatebuilder

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func tryClone(destination, source *os.File) (bool, error) {
	err := unix.IoctlFileClone(int(destination.Fd()), int(source.Fd()))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		return false, nil
	}
	return false, err
}
