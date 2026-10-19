#!/bin/sh
# End-to-end on Linux inside a privileged container. A "VPS" lives in netns srv (kernel WireGuard +
# a fake internet where 1.1.1.1 answers HTTP); mayvpnd runs in the main namespace.
set -eu
W=/work; SOCK=/run/mayvpn.sock
pass() { echo "PASS  $*"; }
fail() { echo "FAIL  $*"; exit 1; }
http() { python3 -c "import urllib.request,sys; urllib.request.urlopen('$1', timeout=3)" 2>/dev/null; }

# --- the "VPS" and its internet
ip netns add srv
ip link add veth-c type veth peer name veth-s
ip link set veth-s netns srv
ip addr add 10.200.0.1/24 dev veth-c && ip link set veth-c up
ip -n srv addr add 10.200.0.2/24 dev veth-s && ip -n srv link set veth-s up && ip -n srv link set lo up
ip -n srv addr add 1.1.1.1/32 dev lo
ip netns exec srv python3 -m http.server 80 --bind 1.1.1.1 >/dev/null 2>&1 &
ip netns exec srv python3 -m http.server 8080 --bind 10.200.0.2 >/dev/null 2>&1 &
ip route replace default via 10.200.0.2 dev veth-c
umask 077
wg genkey > s.key; wg pubkey < s.key > s.pub; wg genkey > c.key; wg pubkey < c.key > c.pub; wg genpsk > psk
ip -n srv link add wg-srv type wireguard
ip netns exec srv wg set wg-srv private-key s.key listen-port 51820 peer "$(cat c.pub)" preshared-key psk allowed-ips 10.66.66.3/32
ip -n srv addr add 10.66.66.1/24 dev wg-srv && ip -n srv link set wg-srv up
cat > client.conf <<CONF
[Interface]
PrivateKey = $(cat c.key)
Address = 10.66.66.3/32
DNS = 1.1.1.1
PostUp = touch /tmp/pwned

[Peer]
PublicKey = $(cat s.pub)
PresharedKey = $(cat psk)
Endpoint = 10.200.0.2:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
CONF
sleep 1
cp /etc/resolv.conf /tmp/resolv.orig
ROUTES_BEFORE=$(ip route; ip -6 route)

# --- helper
"$W/mayvpnd" serve --socket $SOCK --uid 0 > /tmp/daemon.out 2>&1 &
for i in $(seq 50); do [ -S $SOCK ] && break; sleep 0.1; done
[ -S $SOCK ] && pass "helper listening" || fail "no socket"

# --- connect with the kill switch
python3 client.py $SOCK connect client.conf 1 || fail "connect"
ip -d link show mayvpn0 | grep -q "wireguard" && pass "kernel WireGuard link mayvpn0" || fail "not a kernel wireguard link"
[ "$(wg show mayvpn0 latest-handshakes | awk '{print $2}')" -gt 0 ] && pass "handshake with the server" || fail "no handshake"
ip route | grep -q "^0.0.0.0/1 dev mayvpn0" && ip route | grep -q "^128.0.0.0/1 dev mayvpn0" && pass "split default routes" || fail "routes"
ip route | grep -q "^10.200.0.2 via 10.200.0.2 dev veth-c" && pass "server pinned outside the tunnel" || fail "server host route"
ip -6 route | grep -q "blackhole ::/1" && pass "IPv6 blackholed (tunnel has no v6)" || fail "v6 blackhole"
nft list table inet mayvpn >/dev/null && pass "nftables kill switch loaded" || fail "no nft table"
grep -q "nameserver 1.1.1.1" /etc/resolv.conf && pass "DNS → 1.1.1.1 (resolv.conf fallback, no resolved here)" || fail "dns"
http http://1.1.1.1/ && pass "traffic through the tunnel" || fail "no traffic through tunnel"
http http://10.200.0.2:8080/ && fail "kill switch let traffic out around the tunnel" || pass "kill switch blocks traffic outside the tunnel"
[ ! -e /tmp/pwned ] && pass "PostUp was not executed" || fail "PostUp ran!"

# --- disconnect restores everything
python3 client.py $SOCK disconnect || fail "disconnect"
ip link show mayvpn0 >/dev/null 2>&1 && fail "mayvpn0 left behind" || pass "link removed"
nft list table inet mayvpn >/dev/null 2>&1 && fail "nft table left behind" || pass "kill switch removed"
cmp -s /etc/resolv.conf /tmp/resolv.orig && pass "resolv.conf restored byte for byte" || fail "resolv.conf differs"
[ "$ROUTES_BEFORE" = "$(ip route; ip -6 route)" ] && pass "routing table exactly as before" || fail "routes differ"
http http://10.200.0.2:8080/ && pass "normal traffic flows again" || fail "still blocked"

# --- without the kill switch the outside path stays open while connected
python3 client.py $SOCK connect client.conf 0 >/dev/null || fail "connect 2"
http http://10.200.0.2:8080/ && pass "kill switch off: LAN-side host reachable" || fail "blocked without kill switch"
python3 client.py $SOCK disconnect >/dev/null

# --- no logs
kill -TERM %1 2>/dev/null || pkill -TERM mayvpnd; sleep 0.5
[ ! -s /tmp/daemon.out ] && pass "helper printed nothing" || fail "helper wrote output: $(cat /tmp/daemon.out)"
[ ! -e $SOCK ] && pass "socket removed on stop" || fail "socket left"
echo "ALL PASSED"
