package sandbox

import (
	"errors"
	"regexp"
	"strings"
)

// LocalLimaTemplateReference marks the development-only Lima base. It is an
// internal routing value and is never accepted as a public template name.
const LocalLimaTemplateReference = "@local-lima-base"

var ErrTemplateUnavailable = errors.New("template not found or not ready")
var templateIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
var templateTag = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

func ParseTemplateReference(reference string) (identifier, tag string, err error) {
	if reference == "" {
		reference = "linux-base"
	}
	identifier, tag, found := strings.Cut(reference, ":")
	if !found {
		tag = "default"
	}
	if !templateIdentifier.MatchString(identifier) || !templateTag.MatchString(tag) {
		return "", "", errors.New("template must be a catalog name or template UUID, optionally followed by :tag")
	}
	return identifier, tag, nil
}
