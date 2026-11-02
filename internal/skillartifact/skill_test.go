package skillartifact

import (
	"strings"
	"testing"
)

func TestSkillMetadataAndLimits(t *testing.T) {
	v := Skill{Name: "review-code", Description: "说明\n---\nname: injected", Body: "Read and review."}
	if v.Validate() != nil {
		t.Fatal("valid skill rejected")
	}
	text := v.Markdown()
	if strings.Count(text, "\n---\n") != 1 || !strings.Contains(text, `\n---\nname: injected`) {
		t.Fatal("frontmatter escaped incorrectly")
	}
	for _, name := range []string{"../escape", "a/b", "$(id)", "A", "a--b", strings.Repeat("a", 65)} {
		bad := v
		bad.Name = name
		if bad.Validate() == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, body := range []string{" ", "a\x00b", strings.Repeat("a", MaxBody+1)} {
		bad := v
		bad.Body = body
		if bad.Validate() == nil {
			t.Fatal("accepted invalid body")
		}
	}
}
