package templatebuilder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/node/vmm"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"github.com/google/uuid"
)

type WarmCloneResult struct {
	SandboxID                string `json:"sandbox_id"`
	GuestEpoch               string `json:"guest_epoch"`
	DiskCloneMilliseconds    int64  `json:"disk_clone_ms"`
	RestoreMilliseconds      int64  `json:"chv_restore_ms"`
	ResumeMilliseconds       int64  `json:"chv_resume_call_ms"`
	GuestResumeMilliseconds  int64  `json:"guest_resume_ms"`
	InstanceInitMilliseconds int64  `json:"instance_init_ms"`
	FirstExecMilliseconds    int64  `json:"first_exec_ms"`
	TotalMilliseconds        int64  `json:"clone_to_first_exec_ms"`
	CoWMappingConfirmed      bool   `json:"cow_mapping_confirmed"`
	Random                   string `json:"random_sample"`
}
type WarmProbeResult struct {
	Mode                            vmm.MemoryRestoreMode `json:"requested_memory_mode"`
	TemplatePreparationMilliseconds int64                 `json:"template_preparation_ms"`
	Clones                          []WarmCloneResult     `json:"clones"`
	IsolationVerified               bool                  `json:"disk_markers_isolated"`
}

// ProbeWarm is an operator-only local experiment, not the public Create path.
// It freezes a clean template, keeps its seed disks/memory immutable, and gives
// each restore private QCOW2 overlays plus a freshly rewritten configuration.
func ProbeWarm(ctx context.Context, binary, directory, digest string, mode vmm.MemoryRestoreMode) (result WarmProbeResult, err error) {
	if runtime.GOOS != "linux" {
		return result, errors.New("snapshot experiments require Linux/KVM")
	}
	if mode != vmm.MemoryRestoreCopy && mode != vmm.MemoryRestoreOnDemand && mode != vmm.MemoryRestoreCopyOnWrite {
		return result, errors.New("unsupported memory restore mode")
	}
	result.Mode = mode
	beginning := time.Now()
	directory, err = filepath.Abs(directory)
	if err != nil {
		return result, err
	}
	m, err := templateartifact.Verify(directory, digest)
	if err != nil {
		return result, err
	}
	if m.KernelConfig == nil || m.KernelVersion == "" {
		return result, errors.New("snapshot experiment requires a hashed Guest kernel config and version")
	}
	configuration, err := os.ReadFile(filepath.Join(directory, m.KernelConfig.Name))
	if err != nil {
		return result, err
	}
	if !strings.Contains(string(configuration), "CONFIG_VMGENID=y\n") && !strings.Contains(string(configuration), "CONFIG_VMGENID=m\n") {
		return result, errors.New("Guest kernel does not enable VMGENID")
	}
	work, err := os.MkdirTemp("", "isodo-warm-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(work)
	sourceWork := filepath.Join(work, "source")
	if err = os.Mkdir(sourceWork, 0700); err != nil {
		return result, err
	}
	config, err := prepareVM(ctx, directory, sourceWork, m)
	if err != nil {
		return result, err
	}
	source, err := boot(ctx, binary, sourceWork, config)
	if err != nil {
		return result, err
	}
	defer source.kill()
	readyCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	ready, err := guesttransport.WaitTemplateReady(readyCtx, config.Vsock.Socket)
	cancel()
	if err != nil {
		return result, withLogTail(err, config.Serial.File)
	}
	if err = verifyReady(ready, m); err != nil {
		return result, err
	}
	serial, err := os.ReadFile(config.Serial.File)
	if err != nil || !strings.Contains(string(serial), "VM Generation ID driver bound") {
		return result, errors.New("Guest init did not confirm VMGENID driver binding")
	}
	if err = source.client.Pause(ctx); err != nil {
		return result, err
	}
	snapshot := filepath.Join(work, "snapshot")
	if err = os.Mkdir(snapshot, 0700); err != nil {
		return result, err
	}
	if err = source.client.Snapshot(ctx, snapshot); err != nil {
		return result, err
	}
	source.kill() // No writer may modify the source disks after this point.
	for _, name := range []string{"config.json", "state.json", "memory-ranges"} {
		info, e := os.Lstat(filepath.Join(snapshot, name))
		if e != nil || !info.Mode().IsRegular() {
			return result, fmt.Errorf("snapshot is missing regular %s", name)
		}
		if err = os.Chmod(filepath.Join(snapshot, name), 0400); err != nil {
			return result, err
		}
	}
	// The disks were private to the now-reaped template VM; freeze them in place.
	for _, disk := range config.Disks {
		f, e := os.OpenFile(disk.Path, os.O_RDWR, 0)
		if e != nil {
			return result, e
		}
		e = errors.Join(f.Sync(), f.Close())
		if e != nil {
			return result, e
		}
		if err = os.Chmod(disk.Path, 0400); err != nil {
			return result, err
		}
	}
	result.TemplatePreparationMilliseconds = time.Since(beginning).Milliseconds()
	var clones []*vmSession
	var identities []*guestv1.RuntimeIdentity
	var sockets []string
	defer func() {
		for _, clone := range clones {
			clone.kill()
		}
	}()
	for i := range 2 {
		started := time.Now()
		identity := &guestv1.RuntimeIdentity{SandboxId: uuid.NewString(), OwnershipGeneration: 1, BootId: uuid.NewString()}
		cloneWork := filepath.Join(work, fmt.Sprintf("clone-%d", i))
		if err = os.Mkdir(cloneWork, 0700); err != nil {
			return result, err
		}
		root := filepath.Join(cloneWork, "root.qcow2")
		workspace := filepath.Join(cloneWork, "workspace.qcow2")
		diskStart := time.Now()
		for j, path := range []string{root, workspace} {
			output, e := exec.CommandContext(ctx, "qemu-img", "create", "-q", "-f", "qcow2", "-F", "raw", "-b", config.Disks[j].Path, path).CombinedOutput()
			if e != nil {
				return result, fmt.Errorf("create trusted QCOW2 overlay: %w: %s", e, output)
			}
		}
		row := WarmCloneResult{SandboxID: identity.SandboxId, DiskCloneMilliseconds: time.Since(diskStart).Milliseconds()}
		cloneSnapshot := filepath.Join(cloneWork, "snapshot")
		if err = os.Mkdir(cloneSnapshot, 0700); err != nil {
			return result, err
		}
		for _, name := range []string{"state.json", "memory-ranges"} {
			if err = os.Link(filepath.Join(snapshot, name), filepath.Join(cloneSnapshot, name)); err != nil {
				return result, err
			}
		}
		socket := filepath.Join(cloneWork, "vsock")
		if err = rewriteSnapshotConfig(filepath.Join(snapshot, "config.json"), filepath.Join(cloneSnapshot, "config.json"), root, workspace, socket, filepath.Join(cloneWork, "serial.log")); err != nil {
			return result, err
		}
		clone, e := startVMM(ctx, ctx, binary, cloneWork)
		if e != nil {
			return result, e
		}
		clones = append(clones, clone)
		restoreStart := time.Now()
		if err = clone.client.Restore(ctx, cloneSnapshot, mode); err != nil {
			return result, withLogTail(err, filepath.Join(cloneWork, "vmm.log"))
		}
		row.RestoreMilliseconds = time.Since(restoreStart).Milliseconds()
		if mode == vmm.MemoryRestoreCopyOnWrite {
			row.CoWMappingConfirmed, err = hasPrivateSnapshotMapping(clone.command.Process.Pid, config.Memory.Size)
			if err != nil {
				return result, err
			}
			if !row.CoWMappingConfirmed {
				return result, errors.New("requested CoW restore did not retain the expected private snapshot memory mapping")
			}
		}
		resumeStart := time.Now()
		if err = clone.client.Resume(ctx); err != nil {
			return result, err
		}
		row.ResumeMilliseconds = time.Since(resumeStart).Milliseconds()
		initCtx, stop := context.WithTimeout(ctx, 30*time.Second)
		restored, e := guesttransport.WaitTemplateReady(initCtx, socket)
		if e == nil {
			e = verifyReady(restored, m)
		}
		row.GuestResumeMilliseconds = time.Since(resumeStart).Milliseconds()
		initStart := time.Now()
		if e == nil {
			restored, e = guesttransport.BindRuntime(initCtx, socket, identity)
		}
		if e == nil {
			restored, e = guesttransport.WaitReady(initCtx, socket, restored.Identity)
		}
		stop()
		if e != nil {
			return result, e
		}
		identities = append(identities, restored.Identity)
		sockets = append(sockets, socket)
		row.GuestEpoch = restored.Identity.GuestEpoch
		row.InstanceInitMilliseconds = time.Since(initStart).Milliseconds()
		commandStart := time.Now()
		row.Random, err = warmExec(ctx, socket, restored.Identity, true)
		if err != nil {
			return result, err
		}
		row.FirstExecMilliseconds = time.Since(commandStart).Milliseconds()
		row.TotalMilliseconds = time.Since(started).Milliseconds()
		result.Clones = append(result.Clones, row)
	}
	if result.Clones[0].GuestEpoch == result.Clones[1].GuestEpoch || result.Clones[0].Random == result.Clones[1].Random {
		return result, errors.New("restored Guests reused random values")
	}
	for i, socket := range sockets {
		if _, err = warmExec(ctx, socket, identities[i], false); err != nil {
			return result, err
		}
	}
	for _, clone := range clones {
		if err = clone.stop(ctx); err != nil {
			return result, err
		}
	}
	result.IsolationVerified = true
	return result, nil
}

func rewriteSnapshotConfig(source, destination, root, workspace, socket, serial string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		return err
	}
	disks, ok := config["disks"].([]any)
	if !ok || len(disks) != 2 {
		return errors.New("snapshot has an unexpected disk layout")
	}
	for i, path := range []string{root, workspace} {
		disk, ok := disks[i].(map[string]any)
		if !ok {
			return errors.New("snapshot disk configuration is invalid")
		}
		disk["path"] = path
		disk["image_type"] = "Qcow2"
		disk["backing_files"] = true
		disk["readonly"] = false
	}
	vsock, ok := config["vsock"].(map[string]any)
	if !ok {
		return errors.New("snapshot has no vsock configuration")
	}
	vsock["socket"] = socket
	config["serial"] = map[string]any{"mode": "File", "file": serial}
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, encoded, 0400)
}

func hasPrivateSnapshotMapping(pid int, minimum uint64) (bool, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", pid))
	if err != nil {
		return false, err
	}
	return snapshotMappingBytes(string(data)) >= minimum, nil
}
func snapshotMappingBytes(maps string) uint64 {
	var total uint64
	for line := range strings.SplitSeq(maps, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 6 || parts[1] != "rw-p" || !strings.HasSuffix(parts[len(parts)-1], "/memory-ranges") {
			continue
		}
		start, end, ok := strings.Cut(parts[0], "-")
		if !ok {
			continue
		}
		a, e1 := strconv.ParseUint(start, 16, 64)
		b, e2 := strconv.ParseUint(end, 16, 64)
		if e1 == nil && e2 == nil && b >= a {
			total += b - a
		}
	}
	return total
}

// warmExec executes only a fixed private validation script. It does not advertise
// the incomplete public exec/files/PTY capability or accept user commands.
func warmExec(ctx context.Context, socket string, identity *guestv1.RuntimeIdentity, create bool) (string, error) {
	script := `import json,os,pathlib,sys,time
paths=[pathlib.Path('/workspace/isodo-clone-marker'),pathlib.Path('/home/isodo/isodo-clone-marker')]
if sys.argv[2]=='create':
 for p in paths:
  assert not p.exists(), 'inherited marker'
  p.write_text(sys.argv[1])
else:
 for p in paths: assert p.read_text()==sys.argv[1], 'cross-clone write'
print(json.dumps({'id':sys.argv[1],'random':os.urandom(32).hex(),'realtime_ns':time.time_ns(),'clocksource':pathlib.Path('/sys/devices/system/clocksource/clocksource0/current_clocksource').read_text().strip()}))`
	action := "check"
	if create {
		action = "create"
	}
	connection, err := guesttransport.NewVsockClientConn(socket)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := guestv1.NewGuestServiceClient(connection)
	process, err := client.StartProcess(bounded, &guestv1.StartProcessRequest{Identity: identity, RequestId: uuid.NewString(), Command: &guestv1.Command{Argv: []string{"/usr/bin/python3", "-c", script, identity.SandboxId, action}}, CommandTimeoutSeconds: new(uint32(10))})
	if err != nil {
		return "", err
	}
	stream, err := client.SubscribeProcess(bounded, &guestv1.SubscribeProcessRequest{Ref: process.Ref})
	if err != nil {
		return "", err
	}
	var output strings.Builder
	for {
		event, e := stream.Recv()
		if e != nil {
			if errors.Is(e, io.EOF) {
				return "", errors.New("command stream closed without exit")
			}
			return "", e
		}
		if chunk := event.GetOutput(); chunk != nil {
			if output.Len()+len(chunk.Data) > 8192 {
				return "", errors.New("validation output exceeded limit")
			}
			output.Write(chunk.Data)
		}
		if event.GetGap() != nil {
			return "", errors.New("validation output was lost")
		}
		if exit := event.GetExit(); exit != nil {
			if exit.ExitCode == nil || *exit.ExitCode != 0 || exit.OutputIncomplete {
				return "", fmt.Errorf("validation command failed: %s", output.String())
			}
			var value struct {
				ID, Random  string
				RealtimeNS  int64  `json:"realtime_ns"`
				Clocksource string `json:"clocksource"`
			}
			if err = json.Unmarshal([]byte(output.String()), &value); err != nil {
				return "", err
			}
			if value.ID != identity.SandboxId || len(value.Random) != 64 {
				return "", errors.New("validation output did not match assigned Sandbox")
			}
			drift := time.Now().UnixNano() - value.RealtimeNS
			if drift > int64(5*time.Second) || drift < -int64(5*time.Second) {
				return "", fmt.Errorf("restored Guest clock drift %d ms (clocksource=%s)", drift/int64(time.Millisecond), value.Clocksource)
			}
			return value.Random, nil
		}
	}
}
