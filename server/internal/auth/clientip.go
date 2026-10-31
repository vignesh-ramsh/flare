package auth

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ProxyTrust says which proxies' word is believed about who a client is. Behind a
// reverse proxy every connection comes from the proxy, and the real client's
// address is only in the X-Forwarded-For header, which anyone could write. So it
// is believed only when the connection comes from an address listed here.
type ProxyTrust struct{ prefixes []netip.Prefix }

// ParseProxies reads a list of addresses and CIDR ranges, such as "10.0.0.5" and
// "172.16.0.0/12".
func ParseProxies(list []string) (ProxyTrust, error) {
	var t ProxyTrust
	for _, s := range list {
		s = strings.TrimSpace(s)
		if p, err := netip.ParsePrefix(s); err == nil {
			t.prefixes = append(t.prefixes, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return ProxyTrust{}, fmt.Errorf("%q is not an IP address or a CIDR range such as 10.0.0.0/8", s)
		}
		a = a.Unmap()
		t.prefixes = append(t.prefixes, netip.PrefixFrom(a, a.BitLen()))
	}
	return t, nil
}

func (t ProxyTrust) trusts(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range t.prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ClientIP returns the address of whoever is really connecting, as best it can
// be told. Without trusted proxies that is the connection's own address. With
// them, it is the first address in X-Forwarded-For, reading from the right, that
// is not itself a trusted proxy: each proxy appends the address it saw, so what
// is to the right of that point was added by trusted hops, and what is to its
// left could have been made up by the client.
func (t ProxyTrust) ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	remote = remote.Unmap()
	if len(t.prefixes) == 0 || !t.trusts(remote) {
		return remote.String()
	}

	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	client := remote
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break // something that isn't an address: believe nothing further left
		}
		client = a.Unmap()
		if !t.trusts(client) {
			break
		}
	}
	return client.String()
}

// ThrottleKey is what failed attempts are counted against for an address. IPv6
// networks hand each customer a whole /64, so a client could otherwise rotate
// through billions of addresses; those are counted as one.
func ThrottleKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.Unmap()
	if a.Is6() {
		return netip.PrefixFrom(a, 64).Masked().String()
	}
	return a.String()
}
