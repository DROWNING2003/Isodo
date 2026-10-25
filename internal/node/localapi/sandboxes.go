package localapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// SandboxEntry is one sandbox this Node created for the desktop client. The
// Node owns the lifecycle; this file only remembers what to ask it about, so a
// restarted desktop still shows the sandboxes it made.
type SandboxEntry struct {
	ID                  string          `json:"id"`
	Name                string          `json:"name"`
	OperationID         string          `json:"operation_id,omitempty"`
	Action              string          `json:"action,omitempty"`
	DeletionRequestedAt *time.Time      `json:"deletion_requested_at,omitempty"`
	Revision            uint64          `json:"revision"`
	Metadata            json.RawMessage `json:"metadata,omitempty"`
	TemplateID          string          `json:"template_id,omitempty"`
	TemplateBuildID     string          `json:"template_build_id,omitempty"`
	DesiredState        string          `json:"desired_state,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// SandboxIndex persists the desktop-created sandboxes next to the Node journal.
type SandboxIndex struct {
	mu      sync.Mutex
	path    string
	entries []SandboxEntry
}

// NewSandboxIndex loads the index. An empty path keeps it in memory only.
func NewSandboxIndex(path string) (*SandboxIndex, error) {
	index := &SandboxIndex{path: path}
	if path == "" {
		return index, nil
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return index, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read local sandbox index: %w", err)
	}
	if err := json.Unmarshal(content, &index.entries); err != nil {
		return nil, fmt.Errorf("parse local sandbox index: %w", err)
	}
	return index, nil
}

func (i *SandboxIndex) save() error {
	if i.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(i.path), 0o700); err != nil {
		return err
	}
	content, err := json.MarshalIndent(i.entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(i.path, content, 0o600)
}

// Add records a newly created sandbox.
func (i *SandboxIndex) Add(id, name string) error {
	return i.AddDetails(id, name, "base", "", nil)
}

func (i *SandboxIndex) AddDetails(id, name, template, build string, metadata json.RawMessage) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	position := slices.IndexFunc(i.entries, func(entry SandboxEntry) bool { return entry.ID == id })
	now := time.Now().UTC()
	if template == "" {
		template = "base"
	}
	entry := SandboxEntry{ID: id, Name: name, Revision: 1, Metadata: metadata, TemplateID: template, TemplateBuildID: build, DesiredState: "running", CreatedAt: now, UpdatedAt: now}
	if position >= 0 {
		entry.Revision = i.entries[position].Revision
		i.entries[position] = entry
	} else {
		i.entries = append([]SandboxEntry{entry}, i.entries...)
	}
	return i.save()
}

// Next returns the entry and the revision a lifecycle change must carry.
func (i *SandboxIndex) Next(id string) (SandboxEntry, uint64, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	position := slices.IndexFunc(i.entries, func(entry SandboxEntry) bool { return entry.ID == id })
	if position < 0 {
		return SandboxEntry{}, 0, false
	}
	entry := i.entries[position]
	return entry, entry.Revision + 1, true
}

// Commit stores the revision bump after the Node accepted the change.
func (i *SandboxIndex) Commit(id string, revision uint64) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	position := slices.IndexFunc(i.entries, func(entry SandboxEntry) bool { return entry.ID == id })
	if position >= 0 {
		i.entries[position].Revision = revision
	}
	return i.save()
}

// Remove forgets a sandbox the Node no longer owns.
func (i *SandboxIndex) Remove(id string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.entries = slices.DeleteFunc(i.entries, func(entry SandboxEntry) bool { return entry.ID == id })
	return i.save()
}

// Entries returns a copy of every recorded sandbox.
func (i *SandboxIndex) Entries() []SandboxEntry {
	i.mu.Lock()
	defer i.mu.Unlock()
	return slices.Clone(i.entries)
}

func (i *SandboxIndex) CommitDesired(id string, revision uint64, desired string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	position := slices.IndexFunc(i.entries, func(entry SandboxEntry) bool { return entry.ID == id })
	if position >= 0 {
		i.entries[position].Revision = revision
		i.entries[position].DesiredState = desired
		i.entries[position].UpdatedAt = time.Now().UTC()
	}
	return i.save()
}

func (i *SandboxIndex) SetAction(id, operationID, action string, deletion *time.Time) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	for n := range i.entries {
		if i.entries[n].ID == id {
			i.entries[n].OperationID = operationID
			i.entries[n].Action = action
			i.entries[n].DeletionRequestedAt = deletion
			i.entries[n].UpdatedAt = time.Now().UTC()
			break
		}
	}
	return i.save()
}
func (i *SandboxIndex) UpdateProperties(id, name string, metadata json.RawMessage) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	for n := range i.entries {
		if i.entries[n].ID == id {
			i.entries[n].Name = name
			i.entries[n].Metadata = metadata
			i.entries[n].UpdatedAt = time.Now().UTC()
			break
		}
	}
	return i.save()
}
