package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// A relay wakes devices for servers that don't hold their app's push credentials.
// The credentials of an app belong to whoever publishes it, so a self-hosted
// server can't use them; the publisher runs a relay that can. The app gives the
// relay its push token and gets an opaque handle, which it hands to the server.
// To notify the device the server asks the relay to wake the handle, saying only
// which subscription and message it is for. The relay learns nothing about the
// topic or the content, and the server never sees the push token.

// RelayWakePath is where a relay takes wake requests.
const RelayWakePath = "/v1/wake"

// MaxWakeTTL is the longest a wake request may ask the push service to keep trying.
const MaxWakeTTL = 28 * 24 * time.Hour

// handleRE is what a handle looks like: opaque, URL-safe, and long enough to be
// unguessable.
var handleRE = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// ValidHandle reports whether s can be a relay handle.
func ValidHandle(s string) bool { return handleRE.MatchString(s) }

// WakeRequest is the body of a wake request.
type WakeRequest struct {
	Handle string `json:"handle"`
	// Sub and ID are the subscription and the message the device will be told
	// about. They are opaque to the relay.
	Sub string `json:"sub"`
	ID  string `json:"id"`
	// Priority is the message's, 1 to 5, so that the notification can be as
	// urgent as the message. It reveals nothing else.
	Priority int `json:"priority,omitempty"`
	// TTL is how many seconds the push service should keep trying to reach the
	// device.
	TTL int64 `json:"ttl,omitempty"`
}

// NormalizeRelayURL returns the canonical form of a relay's address, the one
// allowed-relays and registrations are compared in, or an error if it can't be one.
func NormalizeRelayURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("%q is not a relay address such as https://push.example.org", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%q must not contain credentials, a query or a fragment", raw)
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		return "", fmt.Errorf("%q must use https (plain http is accepted only on this machine)", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := parseAddr(host)
	return ip.IsValid() && ip.IsLoopback()
}

// RelaySender wakes devices through one relay.
type RelaySender struct {
	client *http.Client
	base   string
}

// NewRelaySender returns a sender for the relay at base, which NormalizeRelayURL
// made. Relays are chosen by the operator, not by users, so the client may use the
// environment's proxy.
func NewRelaySender(base string, client *http.Client) *RelaySender {
	return &RelaySender{client: client, base: base}
}

// MaxPayload implements Sender. A relay is never given content, so every
// notification through it is a poll request.
func (s *RelaySender) MaxPayload() int { return 0 }

// Send implements Sender.
func (s *RelaySender) Send(ctx context.Context, n Notification) error {
	body, err := json.Marshal(WakeRequest{
		Handle: n.Device.Target, Sub: n.SubscriptionID, ID: n.Message.ID,
		Priority: n.Message.Priority, TTL: int64(min(n.TTL(), MaxWakeTTL) / time.Second),
	})
	if err != nil {
		return Permanent(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+RelayWakePath, bytes.NewReader(body))
	if err != nil {
		return Permanent(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return roundTrip(s.client, req)
}
