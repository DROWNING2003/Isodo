package templatebuilder

import "golang.org/x/sys/unix"

func requireSnapshotHost() error {
	var host unix.Utsname
	if err := unix.Uname(&host); err != nil {
		return err
	}
	return checkSnapshotKernel(unix.ByteSliceToString(host.Release[:]))
}
