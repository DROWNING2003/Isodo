package orchestrator

import (
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/node/injection"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"path/filepath"
	"strings"
	"testing"
)

func TestCHVInjectionRPCFencesRevisionAndMasksSecrets(t *testing.T) {
	manager, err := injection.NewManager(injection.ManagerConfig{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	journal, err := openLimaJournal(filepath.Join(t.TempDir(), "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if err := journal.saveSandbox(sandboxRecord{SandboxID: id, Generation: 2, Revision: 7, State: "running"}); err != nil {
		t.Fatal(err)
	}
	server := &CHVServer{journal: journal, injections: manager, busy: map[string]bool{}}
	request := &nodev1.SetInjectionsRequest{Target: &nodev1.SandboxTarget{SandboxId: id, OwnershipGeneration: 2, Revision: 7}, Rules: []*nodev1.InjectionMaterial{{Key: "one-off:model", Name: "model", Kind: "one-off", Spec: &nodev1.InjectionSpec{Type: "openai", BaseUrl: "https://api.example.test/v1", IfHeaders: map[string]string{"Authorization": "Bearer placeholder"}}, Secret: &nodev1.InjectionSecret{ApiKey: "sensitive-model-api-key"}}}}
	response, err := server.SetInjections(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := protojson.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Rules) != 1 || strings.Contains(string(encoded), "sensitive-model-api-key") || !response.Enabled {
		t.Fatal("response exposed material or lost rule")
	}
	request.Target.Revision = 6
	if _, err := server.SetInjections(t.Context(), request); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale revision=%v", err)
	}
	request.Target.Revision = 7
	request.Target.OwnershipGeneration = 1
	if _, err := server.GetInjections(t.Context(), request.Target); status.Code(err) != codes.NotFound {
		t.Fatalf("stale generation=%v", err)
	}
	request.Target.OwnershipGeneration = 2
	server.busy[id] = true
	if _, err := server.SetInjections(t.Context(), request); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("busy runtime=%v", err)
	}
}

func TestCHVRevocationRemovesOnlyTheSelectedCredential(t *testing.T) {
	manager, err := injection.NewManager(injection.ManagerConfig{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	first, second := uuid.NewString(), uuid.NewString()
	_, err = manager.SetRules("sandbox", []injection.Rule{
		{Key: "persistent:" + first, PersistentID: first, Kind: injection.KindPersistent, Name: "first", Spec: injection.Spec{Type: "openai"}, Secret: injection.Secret{APIKey: "first-model-key"}},
		{Key: "persistent:" + second, PersistentID: second, Kind: injection.KindPersistent, Name: "second", Spec: injection.Spec{Type: "anthropic"}, Secret: injection.Secret{APIKey: "second-model-key"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &CHVServer{injections: manager}
	if _, err := server.RevokeCredential(t.Context(), &nodev1.RevokeCredentialRequest{PersistentId: first}); err != nil {
		t.Fatal(err)
	}
	rules := manager.Get("sandbox").Rules
	if len(rules) != 1 || rules[0].PersistentID != second {
		t.Fatal("revocation affected another credential")
	}
}
