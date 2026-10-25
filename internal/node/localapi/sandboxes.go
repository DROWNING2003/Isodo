package localapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// SandboxEntry is one sandbox this Node created for the desktop client. The
// Node owns the lifecycle; this file only remembers what to ask it about, so a
// restarted desktop still shows the sandboxes it made.
type SandboxEntry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision uint64 `json:"revision"`
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
	i.mu.Lock()
	defer i.mu.Unlock()
	position := slices.IndexFunc(i.entries, func(entry SandboxEntry) bool { return entry.ID == id })
	entry := SandboxEntry{ID: id, Name: name, Revision: 1}
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
