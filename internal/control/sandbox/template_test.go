package sandbox

import "testing"

func TestTemplateReference(t *testing.T) {
	identifier, tag, err := ParseTemplateReference("")
	if err != nil || identifier != "base" || tag != "default" {
		t.Fatalf("default reference: %q %q %v", identifier, tag, err)
	}
	for _, reference := range []string{"base", "t3-base", "linux-base", "abc:version-1", "12345678-1234-1234-1234-123456789abc:default"} {
		identifier, tag, err := ParseTemplateReference(reference)
		if err != nil || identifier == "" || tag == "" {
			t.Fatalf("reference %q: %q %q %v", reference, identifier, tag, err)
		}
	}
	for _, reference := range []string{"../base", "base:", ":tag", "base:a:b", "base/tag", " base", "base:" + string(make([]byte, 65))} {
		if _, _, err := ParseTemplateReference(reference); err == nil {
			t.Fatalf("accepted %q", reference)
		}
	}
}
