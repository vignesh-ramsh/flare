package push

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPublicIP(t *testing.T) {
	tests := []struct {
		ip     string
		public bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"93.184.216.34", true},
		{"2606:4700:4700::1111", true},
		{"2a00:1450:4001:81b::200e", true},

		{"127.0.0.1", false},
		{"127.1.2.3", false},
		{"::1", false},
		{"0.0.0.0", false},
		{"0.1.2.3", false},
		{"::", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"172.31.255.255", false},
		{"192.168.1.1", false},
		{"fd00::1", false},
		{"fc00::1", false},
		{"169.254.169.254", false}, // the metadata service of AWS, GCP and Azure
		{"169.254.0.1", false},
		{"fe80::1", false},
		{"100.64.0.1", false},
		{"100.127.255.255", false},
		{"192.0.0.1", false},
		{"192.0.2.1", false},
		{"198.18.0.1", false},
		{"198.51.100.1", false},
		{"203.0.113.1", false},
		{"224.0.0.1", false},
		{"ff02::1", false},
		{"240.0.0.1", false},
		{"255.255.255.255", false},
		{"2001:db8::1", false},

		// An IPv4 address inside an IPv6 one is judged as the IPv4 address.
		{"::ffff:127.0.0.1", false},
		{"::ffff:10.0.0.1", false},
		{"::ffff:169.254.169.254", false},
		{"::ffff:8.8.8.8", true},
		{"::ffff:100.64.0.1", false}, // ranges that only our own list covers, mapped
		{"::ffff:192.0.2.1", false},
		{"::ffff:240.0.0.1", false},
		{"::ffff:0.1.2.3", false},
		{"64:ff9b::7f00:1", false},    // NAT64 to 127.0.0.1
		{"64:ff9b::a9fe:a9fe", false}, // NAT64 to 169.254.169.254
		{"64:ff9b::808:808", true},    // NAT64 to 8.8.8.8
		{"2002:7f00:1::1", false},     // 6to4 of 127.0.0.1
		{"2002:0808:0808::1", true},   // 6to4 of 8.8.8.8
	}
	for _, tt := range tests {
		ip := netip.MustParseAddr(tt.ip)
		if got := PublicIP(ip); got != tt.public {
			t.Errorf("PublicIP(%s) = %v, want %v", tt.ip, got, tt.public)
		}
	}
	if PublicIP(netip.Addr{}) {
		t.Error("the zero address is public")
	}
}

func TestCheckEndpoint(t *testing.T) {
	tests := []struct {
		url          string
		allowPrivate bool
		mention      string // "" means accepted
	}{
		{"https://push.example.com/abc", false, ""},
		{"https://push.example.com:8443/up/abc?x=1", false, ""},
		{"https://8.8.8.8/abc", false, ""},
		{"https://[2606:4700:4700::1111]/abc", false, ""},
		{"http://push.example.com/abc", false, "https"},
		{"ftp://push.example.com/abc", false, "https"},
		{"push.example.com/abc", false, "full URL"},
		{"https://", false, "full URL"},
		{"", false, "full URL"},
		{"https://user:pass@push.example.com/abc", false, "credentials"},
		{"https://push.example.com/abc#frag", false, "fragment"},
		{"https://localhost/abc", false, "this machine"},
		{"https://LOCALHOST:8080/abc", false, "this machine"},
		{"https://app.localhost/abc", false, "this machine"},
		{"https://127.0.0.1/abc", false, "public"},
		{"https://127.0.0.1:9000/abc", false, "public"},
		{"https://[::1]/abc", false, "public"},
		{"https://10.1.2.3/abc", false, "public"},
		{"https://192.168.0.5/abc", false, "public"},
		{"https://169.254.169.254/latest/meta-data/", false, "public"},
		{"https://[::ffff:127.0.0.1]/abc", false, "public"},
		{"https://[fd00::1]/abc", false, "public"},
		{"https://" + strings.Repeat("a", 3000) + ".example.com/", false, "longer than"},
		// For a distributor on the same network:
		{"http://192.168.1.10:8080/up/abc", true, ""},
		{"https://localhost:9000/abc", true, ""},
		{"http://ntfy.lan/abc", true, ""},
		{"http://user:pass@ntfy.lan/abc", true, "credentials"}, // still refused
		{"ftp://ntfy.lan/abc", true, "https"},
	}
	for _, tt := range tests {
		_, err := CheckEndpoint(tt.url, tt.allowPrivate)
		switch {
		case tt.mention == "" && err != nil:
			t.Errorf("CheckEndpoint(%.60q, %v) = %v, want accepted", tt.url, tt.allowPrivate, err)
		case tt.mention != "" && (err == nil || !strings.Contains(err.Error(), tt.mention)):
			t.Errorf("CheckEndpoint(%.60q, %v) = %v, want an error mentioning %q", tt.url, tt.allowPrivate, err, tt.mention)
		}
	}
}

func get(t *testing.T, c *http.Client, url string) (*http.Response, error) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	return c.Do(req)
}

func TestTheEndpointClientRefusesNonPublicAddressesWhenItConnects(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// The URL is syntactically fine; it is the connection that must be refused.
	// A name that resolves to a private address gets no further.
	if resp, err := get(t, EndpointClient(false), srv.URL); err == nil {
		resp.Body.Close()
		t.Fatal("connected to a loopback address")
	} else if !strings.Contains(err.Error(), "non-public") {
		t.Errorf("err = %v, want the refusal to be named", err)
	}
	// The same goes for a name: localhost resolves to loopback.
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if resp, err := get(t, EndpointClient(false), "http://localhost:"+port); err == nil {
		resp.Body.Close()
		t.Fatal("connected to localhost")
	}
	if hits.Load() != 0 {
		t.Errorf("the server was reached %d times", hits.Load())
	}

	resp, err := get(t, EndpointClient(true), srv.URL)
	if err != nil {
		t.Fatalf("with private addresses allowed: %v", err)
	}
	resp.Body.Close()
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", hits.Load())
	}
}

func TestTheEndpointClientDoesNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetHits.Add(1) }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	resp, err := get(t, EndpointClient(true), redirector.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || targetHits.Load() != 0 {
		t.Errorf("status %d, target hit %d times: a redirect was followed", resp.StatusCode, targetHits.Load())
	}
}

// A proxy would connect to the endpoint on the client's behalf, so the check on
// the address being connected to would see the proxy and not the endpoint. This
// can't be tested by sending a request: Go never proxies localhost or loopback
// addresses, which are the only ones a test can reach. So the setting itself is
// checked.
func TestTheEndpointClientUsesNoProxy(t *testing.T) {
	for _, allowPrivate := range []bool{false, true} {
		tr, ok := EndpointClient(allowPrivate).Transport.(*http.Transport)
		if !ok {
			t.Fatal("the client does not use an *http.Transport")
		}
		if tr.Proxy != nil {
			t.Errorf("allowPrivate=%v: the client would use a proxy from the environment", allowPrivate)
		}
	}
}
