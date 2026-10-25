#!/bin/sh
# Prepare an isolated CHV egress bridge on a Linux/KVM Node host.
# Usage: setup-chv-network.sh [bridge] [cidr]
set -eu
if [ "$(id -u)" != 0 ]; then
    echo 'setup-chv-network.sh must run as root' >&2
    exit 1
fi
bridge=${1:-${ISODO_CHV_NETWORK_BRIDGE:-isodo0}}
cidr=${2:-10.77.0.0/16}
gateway=${cidr%/*}
if [ "$gateway" = "$cidr" ]; then
    echo 'cidr must include a prefix, for example 10.77.0.0/16' >&2
    exit 1
fi
gateway=$(python3 - "$cidr" <<'PY'
import ipaddress, sys
network = ipaddress.ip_network(sys.argv[1], strict=False)
print(next(network.hosts()))
PY
)
ip link show "$bridge" >/dev/null 2>&1 || ip link add "$bridge" type bridge
ip addr replace "$gateway/${cidr#*/}" dev "$bridge"
ip link set "$bridge" up
sysctl -q -w net.ipv4.ip_forward=1

# Dedicated chain so the shared host's other FORWARD rules are untouched.
iptables -N ISODO-CHV 2>/dev/null || true
iptables -F ISODO-CHV
# Deny host/private/link-local/metadata. Public destinations are allowed.
for destination in 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 100.64.0.0/10; do
    iptables -A ISODO-CHV -d "$destination" -j DROP
done
iptables -A ISODO-CHV -j ACCEPT
iptables -C FORWARD -i "$bridge" -j ISODO-CHV 2>/dev/null || iptables -I FORWARD 1 -i "$bridge" -j ISODO-CHV

iptables -t nat -C POSTROUTING -s "$cidr" ! -o "$bridge" -j MASQUERADE 2>/dev/null || \
    iptables -t nat -A POSTROUTING -s "$cidr" ! -o "$bridge" -j MASQUERADE
echo "CHV network ready: bridge=$bridge gateway=$gateway cidr=$cidr"
