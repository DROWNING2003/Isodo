package agent

import (
	"golang.org/x/sys/unix"
	"os"
)

func renameWorkspace(source *os.Root, from string, destination *os.Root, to string, noReplace bool) error {
	sf, err := source.Open(".")
	if err != nil {
		return err
	}
	defer sf.Close()
	df, err := destination.Open(".")
	if err != nil {
		return err
	}
	defer df.Close()
	flags := uint(0)
	if noReplace {
		flags = unix.RENAME_NOREPLACE
	}
	return unix.Renameat2(int(sf.Fd()), from, int(df.Fd()), to, flags)
}
