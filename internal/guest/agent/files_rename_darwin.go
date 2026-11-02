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
	flags := uint32(0)
	if noReplace {
		flags = unix.RENAME_EXCL
	}
	return unix.RenameatxNp(int(sf.Fd()), from, int(df.Fd()), to, flags)
}
