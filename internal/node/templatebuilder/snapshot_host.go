package templatebuilder

import (
	"fmt"
	"strconv"
	"strings"
)

func checkSnapshotKernel(release string) error {
	majorText, rest, ok := strings.Cut(release, ".")
	if !ok {
		return fmt.Errorf("cannot determine snapshot host kernel version: %q", release)
	}
	minorText, _, _ := strings.Cut(rest, ".")
	major, majorErr := strconv.Atoi(majorText)
	minor, minorErr := strconv.Atoi(minorText)
	if majorErr != nil || minorErr != nil || major < 0 || minor < 0 {
		return fmt.Errorf("cannot determine snapshot host kernel version: %q", release)
	}
	if major < 6 || major == 6 && minor < 8 {
		return fmt.Errorf("CHV runtime snapshots require host Linux 6.8 or newer; running %s (Cloud Hypervisor issue #9101)", release)
	}
	return nil
}
