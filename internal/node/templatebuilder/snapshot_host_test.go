package templatebuilder

import "testing"

func TestSnapshotHostKernelRequirement(t *testing.T) {
	for _, release := range []string{"5.15.0-46-generic", "5.16.0", "6.7.12", "unknown", "6", "-1.9.0"} {
		if err := checkSnapshotKernel(release); err == nil {
			t.Errorf("accepted unsupported kernel %q", release)
		}
	}
	for _, release := range []string{"6.8.0-138-generic", "6.12.10", "7.0.0"} {
		if err := checkSnapshotKernel(release); err != nil {
			t.Errorf("rejected supported kernel %q: %v", release, err)
		}
	}
}
