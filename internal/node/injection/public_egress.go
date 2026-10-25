package injection

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"
)

func publicEgressAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("240.0.0.0/4"),
	} {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// Resolve once, validate, then dial the literal address: DNS rebinding must not
// turn model egress into access to Node-local services or cloud metadata.
func dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("egress destination has no addresses")
	}
	for _, ip := range ips {
		if !publicEgressAddress(ip) {
			return nil, errors.New("private or reserved egress destination is forbidden")
		}
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	var lastErr error
	for _, ip := range ips {
		c, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
