package injection

import (
	"net/netip"
	"testing"
)

func TestPublicEgressRejectsNodeAndMetadataNetworks(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.0.1", "172.16.1.1", "192.168.1.1", "169.254.169.254", "100.100.100.200", "0.0.0.0", "224.0.0.1", "240.1.1.1", "fd00::1", "fe80::1", "198.18.0.1"} {
		if publicEgressAddress(netip.MustParseAddr(address)) {
			t.Errorf("allowed %s", address)
		}
	}
	for _, address := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !publicEgressAddress(netip.MustParseAddr(address)) {
			t.Errorf("rejected %s", address)
		}
	}
	if c, err := dialPublic(t.Context(), "tcp", "127.0.0.1:9190"); err == nil {
		c.Close()
		t.Fatal("dialed Node loopback")
	}
}
