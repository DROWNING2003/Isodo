package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
	"github.com/DROWNING2003/Isodo/internal/node/templatebuilder"
	"github.com/DROWNING2003/Isodo/internal/templateartifact"
	"github.com/google/uuid"
)

type t3ProbeResult struct {
	SandboxID        string   `json:"sandbox_id"`
	GuestEpoch       string   `json:"guest_epoch"`
	BootToReadyMS    int64    `json:"boot_to_ready_ms"`
	BootToT3MS       int64    `json:"boot_to_t3_ms"`
	T3HTTPStatus     int      `json:"t3_http_status"`
	T3HTMLOK         bool     `json:"t3_html_ok"`
	PairingOK        bool     `json:"pairing_ok"`
	CredentialIssued bool     `json:"credential_issued"`
	Scopes           []string `json:"scopes"`
	EgressHTTPStatus int      `json:"egress_http_status,omitempty"`
	EgressOK         bool     `json:"egress_ok,omitempty"`
	DNSHTTPStatus    int      `json:"dns_http_status,omitempty"`
	DNSOK            bool     `json:"dns_ok,omitempty"`
}

func probeT3(ctx context.Context, binary, directory, digest, tap, guestIP, gateway string) (result t3ProbeResult, err error) {
	artifact, err := templateartifact.VerifyArtifact(directory, digest)
	if err != nil {
		return result, err
	}
	parent, err := os.MkdirTemp(filepath.Dir(artifact.Directory()), ".t3-probe-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(parent)
	identity := &guestv1.RuntimeIdentity{SandboxId: uuid.NewString(), OwnershipGeneration: 1, BootId: uuid.NewString()}
	started := time.Now()
	instance, err := templatebuilder.StartRuntime(ctx, ctx, templatebuilder.RuntimeConfig{
		VMMBinary: binary, ArtifactDirectory: artifact.Directory(), ManifestDigest: digest,
		Verified: artifact, Directory: filepath.Join(parent, identity.SandboxId), Identity: identity,
		CPUs: 2, MemoryBytes: 2 << 30,
		Tap: tap, MAC: "52:54:00:12:34:56", GuestIP: guestIP, Gateway: gateway,
	})
	if err != nil {
		return result, err
	}
	defer func() {
		_ = instance.Delete()
	}()
	result.SandboxID = identity.SandboxId
	result.GuestEpoch = instance.Identity().GuestEpoch
	result.BootToReadyMS = time.Since(started).Milliseconds()

	connection, err := guesttransport.NewVsockClientConn(instance.VsockSocket())
	if err != nil {
		return result, err
	}
	defer connection.Close()
	client := guestv1.NewGuestServiceClient(connection)

	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		status, body, requestErr := t3HTTP(ctx, client, instance.Identity())
		if requestErr == nil && status == 200 && strings.Contains(body, `id="root"`) {
			result.T3HTTPStatus = status
			result.T3HTMLOK = true
			result.BootToT3MS = time.Since(started).Milliseconds()
			break
		}
		lastErr = requestErr
		if lastErr == nil {
			lastErr = fmt.Errorf("T3 returned HTTP %d without the web root", status)
		}
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("wait for T3 HTTP: %w (last error: %v)", ctx.Err(), lastErr)
		case <-deadline.C:
			diagnostics, diagnosticErr := t3GuestCommand(ctx, client, instance.Identity(), []string{
				"/bin/sh", "-c", "echo LOG:; tail -100 /workspace/.isodo/t3-server.log 2>&1; echo ---; ls -la /workspace/.isodo 2>&1; echo ---; cat /proc/net/tcp 2>&1 | head -20",
			})
			return result, fmt.Errorf("T3 did not serve its web root within 90s (last error: %v); diagnostics: %s; diagnostics error: %v", lastErr, diagnostics, diagnosticErr)
		case <-ticker.C:
		}
	}

	credential, scopes, err := t3Pair(ctx, client, instance.Identity())
	if err != nil {
		return result, err
	}
	result.PairingOK = true
	result.CredentialIssued = credential != ""
	result.Scopes = scopes
	if !containsScope(scopes, "terminal:operate") {
		return result, errors.New("T3 pairing credential does not grant terminal:operate")
	}
	if tap != "" {
		output, commandErr := t3GuestCommand(ctx, client, instance.Identity(), []string{
			"/bin/sh", "-c", "ip -br addr 2>&1; ip route 2>&1; cat /etc/resolv.conf 2>&1; code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 https://1.1.1.1 2>&1); echo EGRESS=$code; dns=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 https://www.cloudflare.com 2>&1); echo DNS=$dns; true",
		})
		if commandErr != nil {
			return result, fmt.Errorf("CHV egress probe failed: %w (%s)", commandErr, output)
		}
		marker := strings.LastIndex(output, "EGRESS=")
		if marker < 0 {
			return result, fmt.Errorf("CHV egress probe returned no status: %s", output)
		}
		statusText := strings.TrimSpace(output[marker+len("EGRESS="):])
		if newline := strings.IndexByte(statusText, '\n'); newline >= 0 {
			statusText = strings.TrimSpace(statusText[:newline])
		}
		status, parseErr := strconv.Atoi(statusText)
		if parseErr != nil || status < 100 || status > 599 {
			return result, fmt.Errorf("CHV egress returned %q: %s", output[marker:], output)
		}
		result.EgressHTTPStatus = status
		result.EgressOK = true
		dnsMarker := strings.LastIndex(output, "DNS=")
		if dnsMarker < 0 {
			return result, fmt.Errorf("CHV DNS probe returned no status: %s", output)
		}
		dnsText := strings.TrimSpace(output[dnsMarker+len("DNS="):])
		if newline := strings.IndexByte(dnsText, '\n'); newline >= 0 {
			dnsText = strings.TrimSpace(dnsText[:newline])
		}
		dnsStatus, dnsErr := strconv.Atoi(dnsText)
		if dnsErr != nil || dnsStatus < 100 || dnsStatus > 599 {
			return result, fmt.Errorf("CHV DNS egress returned %q: %s", output[dnsMarker:], output)
		}
		result.DNSHTTPStatus = dnsStatus
		result.DNSOK = true
	}
	if err = instance.Stop(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func t3HTTP(ctx context.Context, client guestv1.GuestServiceClient, identity *guestv1.RuntimeIdentity) (int, string, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	stream, err := client.PortForward(attemptCtx)
	if err != nil {
		return 0, "", err
	}
	if err := stream.Send(&guestv1.PortForwardFrame{Frame: &guestv1.PortForwardFrame_Open{Open: &guestv1.PortForwardOpen{
		Identity: identity, RequestId: "probe-t3-" + uuid.NewString(), Port: 8080, Service: "t3",
	}}}); err != nil {
		return 0, "", err
	}
	request := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	if err := stream.Send(&guestv1.PortForwardFrame{Frame: &guestv1.PortForwardFrame_Data{Data: &guestv1.PortForwardData{Data: []byte(request)}}}); err != nil {
		return 0, "", err
	}
	// Do not half-close before the response: Node's HTTP server closes the
	// connection itself after `Connection: close`, and an early CloseWrite can
	// make it discard the in-flight request.
	var response bytes.Buffer
	for response.Len() < 2<<20 {
		frame, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return 0, "", recvErr
		}
		if data := frame.GetData(); data != nil {
			_, _ = response.Write(data.GetData())
			continue
		}
		if frame.GetEnd() != nil {
			break
		}
		if forwardErr := frame.GetError(); forwardErr != nil {
			return 0, "", fmt.Errorf("%s: %s", forwardErr.GetCode(), forwardErr.GetMessage())
		}
	}
	text := response.String()
	status := 0
	if _, scanErr := fmt.Sscanf(strings.SplitN(text, "\r\n", 2)[0], "HTTP/1.1 %d", &status); scanErr != nil {
		return 0, text, fmt.Errorf("parse T3 HTTP status: %w", scanErr)
	}
	return status, text, nil
}

func t3Pair(ctx context.Context, client guestv1.GuestServiceClient, identity *guestv1.RuntimeIdentity) (string, []string, error) {
	process, err := client.StartProcess(ctx, &guestv1.StartProcessRequest{
		Identity:  identity,
		RequestId: "probe-t3-pair-" + uuid.NewString(),
		Command: &guestv1.Command{Argv: []string{
			"/usr/local/bin/node", "/opt/isodo/t3/bin.mjs", "auth", "pairing", "create",
			"--base-dir", "/workspace/.t3", "--ttl", "5m", "--json",
		}},
		Env: map[string]string{
			"HOME":           "/workspace/.home",
			"T3CODE_HOME":    "/workspace/.t3",
			"CODEX_HOME":     "/workspace/.codex",
			"T3CODE_SANDBOX": "1",
			"PATH":           "/usr/local/bin:/usr/bin:/bin",
		},
	})
	if err != nil {
		return "", nil, err
	}
	stream, err := client.SubscribeProcess(ctx, &guestv1.SubscribeProcessRequest{Ref: process.GetRef()})
	if err != nil {
		return "", nil, err
	}
	var stdout strings.Builder
	for {
		event, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return "", nil, errors.New("T3 pairing stream ended without an exit event")
		}
		if recvErr != nil {
			return "", nil, recvErr
		}
		if output := event.GetOutput(); output != nil && output.GetChannel() == guestv1.OutputChannel_OUTPUT_CHANNEL_STDOUT {
			_, _ = stdout.Write(output.GetData())
		}
		if exit := event.GetExit(); exit != nil {
			if exit.GetExitCode() != 0 {
				return "", nil, fmt.Errorf("T3 pairing exited with status %d", exit.GetExitCode())
			}
			break
		}
	}
	raw := stdout.String()
	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return "", nil, fmt.Errorf("T3 pairing returned no JSON: %q", raw)
	}
	var issued struct {
		Credential string   `json:"credential"`
		Scopes     []string `json:"scopes"`
	}
	if err := json.Unmarshal([]byte(raw[start:]), &issued); err != nil {
		return "", nil, fmt.Errorf("decode T3 pairing JSON: %w", err)
	}
	if issued.Credential == "" {
		return "", nil, errors.New("T3 pairing returned no credential")
	}
	return issued.Credential, issued.Scopes, nil
}

func containsScope(scopes []string, wanted string) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

func t3GuestCommand(ctx context.Context, client guestv1.GuestServiceClient, identity *guestv1.RuntimeIdentity, argv []string) (string, error) {
	process, err := client.StartProcess(ctx, &guestv1.StartProcessRequest{
		Identity: identity, RequestId: "probe-t3-diag-" + uuid.NewString(),
		Command: &guestv1.Command{Argv: argv},
	})
	if err != nil {
		return "", err
	}
	stream, err := client.SubscribeProcess(ctx, &guestv1.SubscribeProcessRequest{Ref: process.GetRef()})
	if err != nil {
		return "", err
	}
	var output strings.Builder
	for {
		event, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return output.String(), errors.New("diagnostic stream ended without an exit event")
		}
		if recvErr != nil {
			return output.String(), recvErr
		}
		if chunk := event.GetOutput(); chunk != nil && chunk.GetChannel() == guestv1.OutputChannel_OUTPUT_CHANNEL_STDOUT {
			_, _ = output.Write(chunk.GetData())
		}
		if exit := event.GetExit(); exit != nil {
			if exit.GetExitCode() != 0 {
				return output.String(), fmt.Errorf("diagnostic command exited with status %d", exit.GetExitCode())
			}
			return output.String(), nil
		}
	}
}
