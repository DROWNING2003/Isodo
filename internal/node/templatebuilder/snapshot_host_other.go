//go:build !linux || !amd64

package templatebuilder

import "errors"

func requireSnapshotHost() error {
	return errors.New("CHV runtime snapshots currently require a Linux amd64 host with kernel 6.8 or newer")
}
