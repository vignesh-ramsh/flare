package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func req(remote string, xff ...string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func proxies(t *testing.T, list ...string) ProxyTrust {
	t.Helper()
	p, err := ParseProxies(list)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestClientIPWithoutTrustedProxiesIsTheConnection(t *testing.T) {
	none := proxies(t)
	for name, tt := range map[string]struct {
		r    *http.Request
		want string
	}{
		"ipv4":                             {req("203.0.113.7:5555"), "203.0.113.7"},
		"ipv6":                             {req("[2001:db8::1]:5555"), "2001:db8::1"},
		"ipv4 inside ipv6":                 {req("[::ffff:203.0.113.7]:5555"), "203.0.113.7"},
		"no port":                          {req("203.0.113.7"), "203.0.113.7"},
		"a spoofed header is ignored":      {req("203.0.113.7:1", "1.2.3.4"), "203.0.113.7"},
		"many spoofed headers are ignored": {req("203.0.113.7:1", "1.2.3.4, 5.6.7.8", "9.9.9.9"), "203.0.113.7"},
	} {
		if got := none.ClientIP(tt.r); got != tt.want {
			t.Errorf("%s: ClientIP = %q, want %q", name, got, tt.want)
		}
	}
}

func TestClientIPBehindATrustedProxy(t *testing.T) {
	trust := proxies(t, "10.0.0.0/8", "192.168.1.5")
	tests := map[string]struct {
		r    *http.Request
		want string
	}{
		"the proxy reports the client":     {req("10.0.0.2:1", "203.0.113.7"), "203.0.113.7"},
		"a chain of two trusted proxies":   {req("10.0.0.2:1", "203.0.113.7, 10.0.0.9"), "203.0.113.7"},
		"a bare trusted address":           {req("192.168.1.5:1", "203.0.113.7"), "203.0.113.7"},
		"the client made up the left part": {req("10.0.0.2:1", "6.6.6.6, 203.0.113.7"), "203.0.113.7"},
		"made up and a proxy hop":          {req("10.0.0.2:1", "6.6.6.6, 203.0.113.7, 10.0.0.9"), "203.0.113.7"},
		"several header lines":             {req("10.0.0.2:1", "6.6.6.6", "203.0.113.7, 10.0.0.9"), "203.0.113.7"},
		"no header: the proxy itself":      {req("10.0.0.2:1"), "10.0.0.2"},
		"everything trusted: the leftmost": {req("10.0.0.2:1", "10.1.1.1, 10.2.2.2"), "10.1.1.1"},
		// 10.0.0.9 is a trusted hop; the garbage to its left can't be believed, so
		// the search stops at the furthest address that could be verified.
		"garbage stops the search":               {req("10.0.0.2:1", "203.0.113.7, not-an-ip, 10.0.0.9"), "10.0.0.9"},
		"empty entries":                          {req("10.0.0.2:1", ", ,"), "10.0.0.2"},
		"ipv6 client":                            {req("10.0.0.2:1", "2001:db8::7"), "2001:db8::7"},
		"an untrusted connection ignores header": {req("203.0.113.50:1", "1.1.1.1"), "203.0.113.50"},
		"just outside the trusted range":         {req("11.0.0.2:1", "1.1.1.1"), "11.0.0.2"},
	}
	for name, tt := range tests {
		if got := trust.ClientIP(tt.r); got != tt.want {
			t.Errorf("%s: ClientIP = %q, want %q", name, got, tt.want)
		}
	}
}

func TestParseProxies(t *testing.T) {
	if _, err := ParseProxies([]string{"10.0.0.0/8", " 192.168.1.5 ", "::1", "2001:db8::/32", "::ffff:10.0.0.1"}); err != nil {
		t.Errorf("valid proxies refused: %v", err)
	}
	for _, bad := range []string{"", "example.com", "10.0.0.0/33", "10.0.0", "300.1.1.1", "*"} {
		if _, err := ParseProxies([]string{bad}); err == nil {
			t.Errorf("ParseProxies(%q) succeeded", bad)
		}
	}
	// An IPv4-mapped address in the list trusts the plain IPv4 address.
	p := proxies(t, "::ffff:10.0.0.1")
	if got := p.ClientIP(req("10.0.0.1:1", "203.0.113.7")); got != "203.0.113.7" {
		t.Errorf("ClientIP = %q", got)
	}
}

func TestThrottleKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":          "203.0.113.7",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::1": "2001:db8:1:2::/64", // the same /64: one client as far as counting goes
		"::ffff:203.0.113.7":   "203.0.113.7",
		"not an ip":            "not an ip",
	} {
		if got := ThrottleKey(in); got != want {
			t.Errorf("ThrottleKey(%q) = %q, want %q", in, got, want)
		}
	}
}
