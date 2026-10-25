package auth

import "testing"

func TestLoopbackLocalhostSubdomains(t *testing.T) {
	for _, host := range []string{"localhost", "app.isodo.localhost", "127.0.0.1", "::1"} {
		if !isLoopbackHost(host) {
			t.Errorf("loopback host rejected: %s", host)
		}
	}
	for _, host := range []string{"localhost.example.com", "evillocalhost", "app.localhost.evil"} {
		if isLoopbackHost(host) {
			t.Errorf("non-loopback host accepted: %s", host)
		}
	}
}
