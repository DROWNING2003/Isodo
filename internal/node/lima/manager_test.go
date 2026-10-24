package lima

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
)

func TestLimaManagerGuestAgentLifecycle(t *testing.T) {
	if os.Getenv("ISODO_LIMA_INTEGRATION") != "true" {
		t.Skip("set ISODO_LIMA_INTEGRATION=true to create a real Lima VM")
	}
	binary := os.Getenv("ISODO_GUEST_AGENT_BINARY")
	if binary == "" {
		t.Fatal("ISODO_GUEST_AGENT_BINARY is required for Lima integration")
	}

	manager, err := NewManager(Config{
		GuestAgentBinary: binary,
		InstancePrefix:   "isodo-test-",
		VMType:           "vz",
		CPUs:             1,
		MemoryGiB:        1,
		DiskGiB:          5,
	})
	if err != nil {
		t.Fatal(err)
	}
	sandboxID := "lima-guest-agent-integration-20261024"
	templateID := "lima-guest-agent-integration"
	buildID := "build-20261024"
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	if _, err := manager.Inspect(ctx, sandboxID); err == nil {
		t.Fatalf("refusing to reuse existing Lima instance for %q", sandboxID)
	} else if !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("inspect integration instance: %v", err)
	}
	templateName, err := manager.TemplateInstanceName(templateID, buildID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.inspectName(ctx, templateName); err == nil {
		t.Fatalf("refusing to reuse existing Lima template build %q", templateName)
	} else if !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("inspect integration template build: %v", err)
	}
	_, err = manager.BuildTemplate(ctx, templateID, buildID, [][]string{{
		"sudo", "apt-get", "update",
	}, {
		"sudo", "apt-get", "install", "-y", "ca-certificates", "curl", "xz-utils",
	}, {
		"curl", "-fsSL", "-o", "/tmp/node.tar.xz", "https://nodejs.org/dist/v24.13.1/node-v24.13.1-linux-arm64.tar.xz",
	}, {
		"sudo", "tar", "-xJf", "/tmp/node.tar.xz", "-C", "/usr/local", "--strip-components=1",
	}, {
		"sudo", "install", "-d", "-o", "drowning", "-g", "drowning", "/opt/isodo-tools",
	}, {
		"npm", "install", "--global", "--prefix", "/opt/isodo-tools", "@openai/codex@0.130.0", "@anthropic-ai/claude-code@2.1.310",
	}, {
		"sudo", "ln", "-sfn", "/opt/isodo-tools/bin/codex", "/usr/local/bin/codex",
	}, {
		"sudo", "ln", "-sfn", "/opt/isodo-tools/bin/claude", "/usr/local/bin/claude",
	}, {
		"/bin/sh", "-c", "printf template-build-ok | sudo tee /etc/isodo-template-marker >/dev/null",
	}})
	if err != nil {
		t.Fatalf("build Lima template from Isodo Base: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		if err := manager.DeleteTemplate(cleanupCtx, templateID, buildID); err != nil {
			t.Errorf("delete integration Lima template build %s: %v", templateName, err)
		}
	})
	instance, err := manager.CreateFromTemplate(ctx, sandboxID, templateID, buildID)
	if err != nil {
		t.Fatalf("create Lima Guest from immutable template build: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		if err := manager.Delete(cleanupCtx, sandboxID); err != nil {
			t.Errorf("delete integration Lima Guest %s: %v", instance.Name, err)
		}
	})

	bootID, err := manager.BootID(ctx, sandboxID)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := manager.GuestEndpoint(sandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guesttransport.WaitTemplateReadyTCP(ctx, endpoint); err != nil {
		t.Fatalf("wait for template Guest Agent: %v", err)
	}
	identity := &guestv1.RuntimeIdentity{SandboxId: sandboxID, OwnershipGeneration: 1, BootId: bootID}
	ready, err := guesttransport.BindRuntimeTCP(ctx, endpoint, identity)
	if err != nil {
		t.Fatalf("bind Guest Agent: %v", err)
	}
	if !ready.GetRuntimeBound() || ready.GetIdentity().GetGuestEpoch() == "" {
		t.Fatalf("Guest Agent returned an incomplete bound identity: %+v", ready)
	}
	if _, err := guesttransport.WaitReadyTCP(ctx, endpoint, ready.GetIdentity()); err != nil {
		t.Fatalf("verify bound Guest Agent identity: %v", err)
	}
	connection, err := guesttransport.NewTCPClientConn(endpoint)
	if err != nil {
		t.Fatalf("connect Lima Guest Agent gRPC: %v", err)
	}
	defer connection.Close()
	client := guestv1.NewGuestServiceClient(connection)
	if _, err := client.MakeDirectory(ctx, &guestv1.MakeDirectoryRequest{
		Identity: ready.GetIdentity(), RequestId: "lima-mkdir-1", Path: "/workspace/lima-integration",
	}); err != nil {
		t.Fatalf("create Lima Guest workspace directory: %v", err)
	}
	contents := []byte("Lima Guest Files RPC integration\x00\xff")
	digest := sha256.Sum256(contents)
	write, err := client.WriteFile(ctx)
	if err != nil {
		t.Fatalf("open Lima Guest WriteFile stream: %v", err)
	}
	if err := write.Send(&guestv1.WriteFileFrame{Frame: &guestv1.WriteFileFrame_Header{Header: &guestv1.WriteFileHeader{
		Identity: ready.GetIdentity(), RequestId: "lima-write-1", Path: "/workspace/lima-integration/result.bin",
		SizeBytes: uint64(len(contents)), Sha256: digest[:],
	}}}); err != nil {
		t.Fatalf("send Lima Guest WriteFile header: %v", err)
	}
	if err := write.Send(&guestv1.WriteFileFrame{Frame: &guestv1.WriteFileFrame_Chunk{Chunk: &guestv1.FileChunk{Data: contents}}}); err != nil {
		t.Fatalf("send Lima Guest WriteFile chunk: %v", err)
	}
	if _, err := write.CloseAndRecv(); err != nil {
		t.Fatalf("commit Lima Guest WriteFile: %v", err)
	}
	read, err := client.ReadFile(ctx, &guestv1.ReadFileRequest{Identity: ready.GetIdentity(), Path: "/workspace/lima-integration/result.bin"})
	if err != nil {
		t.Fatalf("open Lima Guest ReadFile stream: %v", err)
	}
	var received []byte
	var readHash []byte
	for {
		frame, err := read.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read from Lima Guest: %v", err)
		}
		if chunk := frame.GetChunk(); chunk != nil {
			if chunk.GetOffset() != uint64(len(received)) {
				t.Fatalf("unexpected Lima Guest file offset %d", chunk.GetOffset())
			}
			received = append(received, chunk.GetData()...)
		}
		if end := frame.GetEnd(); end != nil {
			readHash = end.GetSha256()
		}
	}
	if string(received) != string(contents) || !bytes.Equal(readHash, digest[:]) {
		t.Fatalf("Lima Guest file round trip mismatch: bytes=%q hash=%x", received, readHash)
	}
	process, err := client.StartProcess(ctx, &guestv1.StartProcessRequest{
		Identity:  ready.GetIdentity(),
		RequestId: "lima-exec-1",
		Command:   &guestv1.Command{Argv: []string{"/bin/sh", "-c", "printf lima-exec-ok"}},
	})
	if err != nil {
		t.Fatalf("start process in Lima Guest: %v", err)
	}
	outputStream, err := client.SubscribeProcess(ctx, &guestv1.SubscribeProcessRequest{Ref: process.GetRef()})
	if err != nil {
		t.Fatalf("subscribe to Lima Guest process: %v", err)
	}
	var processOutput []byte
	var processExited bool
	for {
		event, err := outputStream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read Lima Guest process event: %v", err)
		}
		if output := event.GetOutput(); output != nil {
			processOutput = append(processOutput, output.GetData()...)
		}
		if event.GetExit() != nil {
			processExited = event.GetExit().GetExitCode() == 0
		}
	}
	if string(processOutput) != "lima-exec-ok" || !processExited {
		t.Fatalf("Lima Guest process output=%q exited=%t", processOutput, processExited)
	}
	templateProcess, err := client.StartProcess(ctx, &guestv1.StartProcessRequest{
		Identity:  ready.GetIdentity(),
		RequestId: "lima-template-inheritance-1",
		Command:   &guestv1.Command{Argv: []string{"/bin/cat", "/etc/isodo-template-marker"}},
	})
	if err != nil {
		t.Fatalf("start template inheritance check in Lima Guest: %v", err)
	}
	templateOutputStream, err := client.SubscribeProcess(ctx, &guestv1.SubscribeProcessRequest{Ref: templateProcess.GetRef()})
	if err != nil {
		t.Fatalf("subscribe to template inheritance check: %v", err)
	}
	var templateOutput []byte
	var templateExited bool
	for {
		event, err := templateOutputStream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read template inheritance event: %v", err)
		}
		if output := event.GetOutput(); output != nil {
			templateOutput = append(templateOutput, output.GetData()...)
		}
		if event.GetExit() != nil {
			templateExited = event.GetExit().GetExitCode() == 0
		}
	}
	if string(templateOutput) != "template-build-ok" || !templateExited {
		t.Fatalf("Sandbox did not inherit template build: output=%q exited=%t", templateOutput, templateExited)
	}
	for _, tool := range []struct {
		name      string
		requestID string
		argv      []string
		want      string
	}{
		{name: "Codex", requestID: "lima-tool-version-codex", argv: []string{"/usr/local/bin/codex", "--version"}, want: "0.130.0"},
		{name: "Claude Code", requestID: "lima-tool-version-claude-code", argv: []string{"env", "DISABLE_AUTOUPDATER=1", "/usr/local/bin/claude", "--version"}, want: "2.1.310"},
	} {
		output, exitCode, err := runGuestCommand(ctx, client, ready.GetIdentity(), tool.requestID, tool.argv)
		if err != nil {
			t.Fatalf("run %s in Lima Sandbox: %v", tool.name, err)
		}
		if exitCode != 0 || !strings.Contains(output, tool.want) {
			t.Fatalf("%s version output=%q exit_code=%d, want version %q", tool.name, output, exitCode, tool.want)
		}
	}
	if bundle := os.Getenv("ISODO_T3_SERVER_BUNDLE"); bundle != "" {
		bundlePath := "/workspace/lima-integration/t3-server-dist.bundle"
		if err := writeGuestFileFromHost(ctx, client, ready.GetIdentity(), "lima-t3-bundle-write", bundlePath, bundle); err != nil {
			t.Fatalf("upload T3 server bundle through Lima Guest Files: %v", err)
		}
		if output, exitCode, err := runGuestCommand(ctx, client, ready.GetIdentity(), "lima-t3-bundle-extract", []string{
			"/bin/sh", "-c", "mkdir -p /workspace/lima-integration/t3 && tar -xzf /workspace/lima-integration/t3-server-dist.bundle --strip-components=1 -C /workspace/lima-integration/t3",
		}); err != nil || exitCode != 0 {
			t.Fatalf("extract T3 server bundle in Lima Guest: output=%q exit_code=%d err=%v", output, exitCode, err)
		}
		server, err := client.StartProcess(ctx, &guestv1.StartProcessRequest{
			Identity:  ready.GetIdentity(),
			RequestId: "lima-t3-server-start",
			Command: &guestv1.Command{Argv: []string{
				"/bin/sh", "-c",
				"mkdir -p /workspace/lima-integration/home/.t3 /workspace/lima-integration/home/.codex && cd /workspace/lima-integration/t3 && export HOME=/workspace/lima-integration/home T3CODE_HOME=/workspace/lima-integration/home/.t3 CODEX_HOME=/workspace/lima-integration/home/.codex T3CODE_SANDBOX=1 && exec node bin.mjs serve --port 8080 --host 127.0.0.1 --base-dir /workspace/lima-integration/home/.t3 --mode web >/tmp/lima-t3-server.log 2>&1",
			}},
		})
		if err != nil {
			t.Fatalf("start T3 server process in Lima Guest: %v", err)
		}
		var httpResponse string
		for attempt := 0; attempt < 120; attempt++ {
			output, exitCode, checkErr := runGuestCommand(ctx, client, ready.GetIdentity(), fmt.Sprintf("lima-t3-http-check-%03d", attempt), []string{
				"node", "-e", "fetch('http://127.0.0.1:8080/').then(async r => { const html = await r.text(); console.log(`${r.status} ${html.includes('id=\\\"root\\\"')}`); }).catch(e => { console.error(e.message); process.exitCode = 1; })",
			})
			if checkErr == nil && exitCode == 0 {
				httpResponse = strings.TrimSpace(output)
				break
			}
			time.Sleep(time.Second)
		}
		if httpResponse == "" {
			serverLog, _, logErr := runGuestCommand(ctx, client, ready.GetIdentity(), "lima-t3-server-log", []string{"/bin/cat", "/tmp/lima-t3-server.log"})
			httpResponse = fmt.Sprintf("no response; process=%s log=%q log_error=%v", server.GetRef().GetExecutionId(), serverLog, logErr)
		}
		appURL, err := manager.SandboxAppURL(sandboxID)
		if err != nil {
			t.Fatalf("resolve host T3 app URL: %v", err)
		}
		appResponse, err := (&http.Client{Timeout: 5 * time.Second}).Get(appURL)
		if err != nil {
			t.Fatalf("open T3 app through Lima host port forward at %s: %v", appURL, err)
		}
		appHTML, readErr := io.ReadAll(appResponse.Body)
		closeErr := appResponse.Body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read host T3 app response: read=%v close=%v", readErr, closeErr)
		}
		if appResponse.StatusCode != http.StatusOK || !strings.Contains(string(appHTML), `id="root"`) {
			t.Fatalf("host T3 app endpoint returned status=%d body_has_root=%t", appResponse.StatusCode, strings.Contains(string(appHTML), `id="root"`))
		}
		if _, err := client.SignalProcess(ctx, &guestv1.SignalProcessRequest{
			Ref: server.GetRef(), Signal: guestv1.ProcessSignal_PROCESS_SIGNAL_TERMINATE,
		}); err != nil {
			t.Errorf("terminate T3 server in Lima Guest: %v", err)
		} else {
			deadline := time.Now().Add(5 * time.Second)
			serverStopped := false
			for time.Now().Before(deadline) {
				processInfo, err := client.GetProcess(ctx, server.GetRef())
				if err != nil {
					t.Errorf("inspect T3 server process in Lima Guest: %v", err)
					break
				}
				if processInfo.GetState() == guestv1.ProcessState_PROCESS_STATE_EXITED {
					serverStopped = true
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if !serverStopped {
				t.Errorf("T3 server process did not exit after termination signal")
			}
		}
		if httpResponse != "200 true" {
			t.Fatalf("T3 web application was not served from the Lima Guest: %q", httpResponse)
		}
		t.Logf("T3 web application is served from Lima Guest and host-accessible at %s: %s", appURL, httpResponse)
	}
	if os.Getenv("ISODO_T3_SERVER_BUNDLE") == "" {
		if _, err := client.Remove(ctx, &guestv1.RemoveRequest{
			Identity: ready.GetIdentity(), RequestId: "lima-remove-1", Path: "/workspace/lima-integration", Recursive: true,
		}); err != nil {
			t.Fatalf("remove Lima Guest integration files: %v", err)
		}
	}
}

func writeGuestFileFromHost(ctx context.Context, client guestv1.GuestServiceClient, identity *guestv1.RuntimeIdentity, requestID, guestPath, hostPath string) error {
	file, err := os.Open(hostPath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	stream, err := client.WriteFile(ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(&guestv1.WriteFileFrame{Frame: &guestv1.WriteFileFrame_Header{Header: &guestv1.WriteFileHeader{
		Identity: identity, RequestId: requestID, Path: guestPath, SizeBytes: uint64(info.Size()), Sha256: digest.Sum(nil),
	}}}); err != nil {
		return err
	}
	buffer := make([]byte, 64<<10)
	var offset uint64
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			if err := stream.Send(&guestv1.WriteFileFrame{Frame: &guestv1.WriteFileFrame_Chunk{Chunk: &guestv1.FileChunk{
				Data: bytes.Clone(buffer[:n]), Offset: offset,
			}}}); err != nil {
				return err
			}
			offset += uint64(n)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	_, err = stream.CloseAndRecv()
	return err
}

func runGuestCommand(ctx context.Context, client guestv1.GuestServiceClient, identity *guestv1.RuntimeIdentity, requestID string, argv []string) (string, int32, error) {
	process, err := client.StartProcess(ctx, &guestv1.StartProcessRequest{
		Identity: identity, RequestId: requestID, Command: &guestv1.Command{Argv: argv},
	})
	if err != nil {
		return "", -1, err
	}
	stream, err := client.SubscribeProcess(ctx, &guestv1.SubscribeProcessRequest{Ref: process.GetRef()})
	if err != nil {
		return "", -1, err
	}
	var output []byte
	exitCode := int32(-1)
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return string(output), exitCode, nil
		}
		if err != nil {
			return string(output), exitCode, err
		}
		if chunk := event.GetOutput(); chunk != nil {
			output = append(output, chunk.GetData()...)
		}
		if exit := event.GetExit(); exit != nil {
			exitCode = exit.GetExitCode()
		}
	}
}

func TestTemplateInstanceNameIsStableAndBuildScoped(t *testing.T) {
	manager, err := NewManager(Config{InstancePrefix: "isodo-test-", VMType: "qemu"})
	if err != nil {
		t.Fatal(err)
	}

	first, err := manager.TemplateInstanceName("template-1", "build-1")
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != mustTemplateInstanceName(t, manager, "template-1", "build-1") {
		t.Fatalf("template instance name is not deterministic: %q", first)
	}
	if first == mustTemplateInstanceName(t, manager, "template-1", "build-2") {
		t.Fatal("different immutable build IDs must produce different Lima instances")
	}
}

func mustTemplateInstanceName(t *testing.T, manager *Manager, templateID, buildID string) string {
	t.Helper()
	name, err := manager.TemplateInstanceName(templateID, buildID)
	if err != nil {
		t.Fatal(err)
	}
	return name
}
