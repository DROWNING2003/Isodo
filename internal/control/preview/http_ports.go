package preview

import (
	"errors"
	"fmt"
	"github.com/DROWNING2003/Isodo/internal/runtimeports"
	"net"
)

// Guest management and the existing T3 workbench must never be treated as
// ordinary, user-published HTTP ports. This is a policy guard, not an
// authorization check: callers must still validate ownership and VM identity.
const (
	t3WorkbenchPort uint16 = runtimeports.Workbench
)

// HTTPPortBaseURL computes the stable, user-facing address for a sandbox's
// application HTTP port: <port>-<sandboxID>.<domain>. It creates no listener or
// policy. The Gateway enforces the current persisted visibility and runtime;
// public ports are anonymous, while private ports require a short-lived grant.
//
// The existing T3 Publish() behavior is deliberately unchanged.
func (p *Publisher) HTTPPortBaseURL(sandboxID string, port uint16) (string, error) {
	if p == nil {
		return "", errors.New("preview is not configured")
	}
	if !uuidPattern.MatchString(sandboxID) || port == 0 {
		return "", errors.New("invalid sandbox HTTP port target")
	}
	if !ValidHTTPPort(port) {
		return "", fmt.Errorf("port %d is reserved for sandbox management", port)
	}
	publicHost := fmt.Sprintf("%d-%s.%s", port, sandboxID, p.domain)
	switch {
	case p.publicPort != "":
		publicHost = net.JoinHostPort(publicHost, p.publicPort)
	case p.domain == "localhost" && p.apiPort != "":
		publicHost = net.JoinHostPort(publicHost, p.apiPort)
	case p.scheme == "http" && p.apiPort != "":
		publicHost = net.JoinHostPort(publicHost, p.apiPort)
	}
	return p.scheme + "://" + publicHost + "/", nil
}
