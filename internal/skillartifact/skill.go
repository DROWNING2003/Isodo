// Package skillartifact defines the bounded, instruction-only Skill format.
package skillartifact

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const MaxSkills = 32
const MaxBody = 32768

// Keep a full sync below the Linux per-argument bound used by the Guest installer.
const MaxSyncBytes = 96 << 10

var slug = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

func (s Skill) Validate() error {
	if !slug.MatchString(s.Name) || len(s.Name) > 64 || len(s.Description) == 0 || len(s.Description) > 1024 || strings.TrimSpace(s.Body) == "" || len(s.Body) > MaxBody {
		return errors.New("invalid skill fields")
	}
	for _, v := range []string{s.Description, s.Body} {
		if !utf8.ValidString(v) || strings.ContainsAny(v, "\x00\r") {
			return errors.New("invalid skill text")
		}
	}
	return nil
}

// JSON strings are valid YAML scalars; metadata cannot inject frontmatter keys.
func (s Skill) Markdown() string {
	name, _ := json.Marshal(s.Name)
	description, _ := json.Marshal(s.Description)
	return "---\nname: " + string(name) + "\ndescription: " + string(description) + "\n---\n\n" + s.Body + "\n"
}

// InstalledDocument is the data-only payload for the fixed Guest installer.
type InstalledDocument struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

func Encode(skills []Skill) ([]byte, error) {
	documents := make([]InstalledDocument, 0, len(skills))
	for _, s := range skills {
		documents = append(documents, InstalledDocument{ID: s.ID, Name: s.Name, Content: s.Markdown()})
	}
	data, err := json.Marshal(documents)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSyncBytes {
		return nil, errors.New("combined Skill content exceeds 96 KiB")
	}
	return data, nil
}
