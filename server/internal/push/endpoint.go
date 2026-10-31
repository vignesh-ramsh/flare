package push

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// MaxEndpointLength is the longest push endpoint accepted.
const MaxEndpointLength = 2048

// CheckEndpoint validates the URL of a Web Push or UnifiedPush endpoint. The
// server will send requests to it, and whoever registers a device chooses it, so
// it must not lead into the server's own network: unless allowPrivate is set,
// only https to a public address is accepted. A name is resolved when the
// connection is made, and checked again then (see EndpointClient).
func CheckEndpoint(raw string, allowPrivate bool) (*url.URL, error) {
	if len(raw) > MaxEndpointLength {
		return nil, fmt.Errorf("the endpoint is longer than %d characters", MaxEndpointLength)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("the endpoint must be a full URL such as https://push.example.com/abc")
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && allowPrivate:
	case u.Scheme == "http":
		return nil, errors.New("the endpoint must use https")
	default:
		return nil, errors.New("the endpoint must be an https URL")
	}
	if u.User != nil {
		return nil, errors.New("the endpoint must not contain credentials")
	}
	if u.Fragment != "" {
		return nil, errors.New("the endpoint must not contain a fragment")
	}
	if allowPrivate {
		return u, nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return nil, errors.New("the endpoint must not be on this machine")
	}
	if ip, err := netip.ParseAddr(host); err == nil && !PublicIP(ip) {
		return nil, errors.New("the endpoint must be at a public address")
	}
	return u, nil
}

// parseAddr parses an IP address, or returns the zero Addr.
func parseAddr(s string) netip.Addr {
	a, _ := netip.ParseAddr(s)
	return a
}

// Address ranges that are not on the public internet and are not covered by the
// helpers on netip.Addr.
var nonPublic = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",       // "this network"
		"100.64.0.0/10",   // carrier-grade NAT, also used inside some clouds
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // documentation
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // documentation
		"203.0.113.0/24",  // documentation
		"240.0.0.0/4",     // reserved, and the broadcast address
		"100::/64",        // discard-only
		"2001:db8::/32",   // documentation
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// PublicIP reports whether ip is an address on the public internet: not loopback,
// private, link-local (which includes the metadata services of the big clouds,
// 169.254.169.254), multicast, unspecified, or reserved. Addresses that carry an
// IPv4 address inside them are judged by that address.
func PublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if v4, ok := embeddedIPv4(ip); ok {
		return PublicIP(v4)
	}
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// embeddedIPv4 finds the IPv4 address in the IPv6 forms that carry one: NAT64
// (64:ff9b::/96) and 6to4 (2002::/16).
func embeddedIPv4(ip netip.Addr) (netip.Addr, bool) {
	if !ip.Is6() {
		return netip.Addr{}, false
	}
	b := ip.As16()
	switch {
	case netip.MustParsePrefix("64:ff9b::/96").Contains(ip):
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	case b[0] == 0x20 && b[1] == 0x02:
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	}
	return netip.Addr{}, false
}

// EndpointClient returns an HTTP client for sending to endpoints that users
// chose. It refuses to connect to a non-public address unless allowPrivate is
// set, checking the address actually being connected to, so a name that resolves
// to a private address (or changes to one between lookups) gets nowhere. It does
// not follow redirects and does not use a proxy: either would reach places this
// check can't see.
func EndpointClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if !allowPrivate {
		dialer.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !PublicIP(ip) {
				return errors.New("refusing to connect to a non-public address")
			}
			return nil
		}
	}
	return &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            dialer.DialContext,
			TLSHandshakeTimeout:    5 * time.Second,
			ResponseHeaderTimeout:  15 * time.Second,
			MaxIdleConnsPerHost:    2,
			IdleConnTimeout:        30 * time.Second,
			MaxResponseHeaderBytes: 16 << 10,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
