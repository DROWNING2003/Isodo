package templatebuilder

import (
	"context"
	"errors"
	"fmt"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/node/vmm"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type frozenTemplate struct {
	work, snapshot string
	manifest       templateartifact.Manifest
	config         vmm.VMConfig
}

func (f *frozenTemplate) close() { _ = os.RemoveAll(f.work) }
func freezeTemplate(ctx context.Context, binary, directory, digest, parent string) (_ *frozenTemplate, err error) {
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	m, err := templateartifact.Verify(directory, digest)
	if err != nil {
		return nil, err
	}
	if m.RuntimeSnapshot != nil || m.MinimumMemoryMiB > 4096 {
		return nil, errors.New("warm construction requires a supported cold disk template")
	}
	if m.KernelConfig == nil || m.KernelVersion == "" {
		return nil, errors.New("snapshot experiment requires a hashed Guest kernel config and version")
	}
	configuration, err := os.ReadFile(filepath.Join(directory, m.KernelConfig.Name))
	if err != nil {
		return nil, err
	}
	if !strings.Contains(string(configuration), "CONFIG_VMGENID=y\n") && !strings.Contains(string(configuration), "CONFIG_VMGENID=m\n") {
		return nil, errors.New("Guest kernel does not enable VMGENID")
	}
	work, err := os.MkdirTemp(parent, "isodo-warm-")
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(work)
		}
	}()
	sourceWork := filepath.Join(work, "source")
	if err = os.Mkdir(sourceWork, 0700); err != nil {
		return nil, err
	}
	config, err := prepareVM(ctx, directory, sourceWork, m)
	if err != nil {
		return nil, err
	}
	source, err := boot(ctx, binary, sourceWork, config)
	if err != nil {
		return nil, err
	}
	defer source.kill()
	readyCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	ready, err := guesttransport.WaitTemplateReady(readyCtx, config.Vsock.Socket)
	cancel()
	if err != nil {
		return nil, withLogTail(err, config.Serial.File)
	}
	if err = verifyReady(ready, m); err != nil {
		return nil, err
	}
	serial, err := os.ReadFile(config.Serial.File)
	if err != nil || !strings.Contains(string(serial), "VM Generation ID driver bound") {
		return nil, errors.New("Guest init did not confirm VMGENID driver binding")
	}
	if err = source.client.Pause(ctx); err != nil {
		return nil, err
	}
	snapshot := filepath.Join(work, "snapshot")
	if err = os.Mkdir(snapshot, 0700); err != nil {
		return nil, err
	}
	if err = source.client.Snapshot(ctx, snapshot); err != nil {
		return nil, err
	}
	source.kill() // No writer may modify the source disks after this point.
	for _, name := range []string{"config.json", "state.json", "memory-ranges"} {
		info, e := os.Lstat(filepath.Join(snapshot, name))
		if e != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("snapshot is missing regular %s", name)
		}
		if err = os.Chmod(filepath.Join(snapshot, name), 0400); err != nil {
			return nil, err
		}
	}
	// The disks were private to the now-reaped template VM; freeze them in place.
	for _, disk := range config.Disks {
		f, e := os.OpenFile(disk.Path, os.O_RDWR, 0)
		if e != nil {
			return nil, e
		}
		e = errors.Join(f.Sync(), f.Close())
		if e != nil {
			return nil, e
		}
		if err = os.Chmod(disk.Path, 0400); err != nil {
			return nil, err
		}
	}
	success = true
	return &frozenTemplate{work: work, snapshot: snapshot, manifest: m, config: config}, nil
}
