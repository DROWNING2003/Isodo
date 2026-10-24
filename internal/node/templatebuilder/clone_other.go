//go:build !linux

package templatebuilder

import "os"

func tryClone(*os.File, *os.File) (bool, error) { return false, nil }
