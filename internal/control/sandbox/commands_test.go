package sandbox_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/control/auth"
	"github.com/DROWNING2003/Isodo/internal/control/sandbox"
	"github.com/DROWNING2003/Isodo/internal/httpapi"
)

type testCommandRuntime struct{}

func (testCommandRuntime) NodeID() string { return "node-a" }
func (testCommandRuntime) GetInjections(context.Context, *nodev1.SandboxTarget) (*nodev1.SandboxInjections, error) {
	return &nodev1.SandboxInjections{}, nil
}
func (testCommandRuntime) SetInjections(context.Context, *nodev1.SetInjectionsRequest) (*nodev1.SandboxInjections, error) {
	return &nodev1.SandboxInjections{}, nil
}

type recordingCommandRunner struct {
	calls int
	input sandbox.CommandRequest
	target *nodev1.SandboxTarget
}

func (f *recordingCommandRunner) RunCommand(_ context.Context, target *nodev1.SandboxTarget, input sandbox.CommandRequest) (sandbox.CommandResult, error) {
	f.calls++
	f.target, f.input = target, input
	code := int32(0)
	return sandbox.CommandResult{ExecutionID: "exec-1", Stdout: "hello\n", Stderr: "", ExitCode: &code, Reason: "EXIT_REASON_COMPLETED"}, nil
}

type commandAuth struct{}

func (commandAuth) Authenticate(ctx context.Context, header string) (auth.Identity, error) {
	if header == "Bearer executor" {
		return auth.Identity{TenantID: "tenant-a", Credential: "api_key", Scopes: []string{"sandboxes:execute"}}, nil
	}
	if header == "Bearer writer" {
		return auth.Identity{TenantID: "tenant-a", Credential: "api_key", Scopes: []string{"sandboxes:write"}}, nil
	}
	return testAuthenticator{}.Authenticate(ctx, header)
}

func TestSandboxRunCommandAuthorizationAndValidation(t *testing.T) {
	store := newMemoryStore()
	item := store.items[otherSandboxID]
	item.ObservedState = "running"
	item.DesiredState = "running"
	item.Revision = "1"
	item.NodeID = "node-a"
	item.OwnershipGeneration = 7
	store.items[otherSandboxID] = item

	runner := &recordingCommandRunner{}
	handler := sandbox.NewHandler(store)
	handler.SetRuntime(testCommandRuntime{})
	handler.SetCommandRunner(runner)
	mux := httpapi.NewHandler(nil, nil, handler, &auth.Access{Bearer: commandAuth{}})
	url := "/v1/sandboxes/" + otherSandboxID + "/commands/run"

	cases := []struct {
		name, token, body string
		want int
	}{
		{"owner", "Bearer user-a", `{"argv":["/bin/echo","hello"]}`, 200},
		{"explicit execution key", "Bearer executor", `{"argv":["/bin/echo","hello"]}`, 200},
		{"write key denied", "Bearer writer", `{"argv":["/bin/echo","hello"]}`, 403},
		{"another tenant", "Bearer user-b", `{"argv":["/bin/echo","hello"]}`, 404},
		{"no authorization", "", `{"argv":["/bin/echo","hello"]}`, 401},
		{"missing argv", "Bearer user-a", `{"argv":[]}`, 400},
		{"excessive timeout", "Bearer user-a", `{"argv":["/bin/echo"],"timeout_seconds":301}`, 400},
		{"unknown field", "Bearer user-a", `{"argv":["/bin/echo"],"host_path":"/etc"}`, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, url, strings.NewReader(tc.body))
			if tc.token != "" {
				request.Header.Set("Authorization", tc.token)
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want %d, body=%s", response.Code, tc.want, response.Body.String())
			}
			if tc.want == 200 && !strings.Contains(response.Body.String(), `"stdout":"hello\n"`) {
				t.Fatalf("missing result output: %s", response.Body.String())
			}
		})
	}
	if runner.calls != 2 {
		t.Fatalf("runner called %d times, want 2", runner.calls)
	}
	if runner.target == nil || runner.target.GetOwnershipGeneration() != 7 || runner.input.TimeoutSeconds != 60 {
		t.Fatalf("command was not fenced or default timeout missing: target=%v input=%+v", runner.target, runner.input)
	}

	item.ObservedState = "stopped"
	store.items[otherSandboxID] = item
	request := httptest.NewRequest(http.MethodPost, url, strings.NewReader(`{"argv":["/bin/echo"]}`))
	request.Header.Set("Authorization", "Bearer user-a")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != 409 || runner.calls != 2 {
		t.Fatalf("stopped sandbox command: status=%d calls=%d", response.Code, runner.calls)
	}
}
