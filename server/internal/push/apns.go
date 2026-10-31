package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// Apple Push Notification service, with token-based authentication: the server
// signs a short-lived JWT with the .p8 key from the Apple Developer account, and
// speaks HTTP/2 to Apple.

const (
	apnsProduction = "https://api.push.apple.com"
	apnsSandbox    = "https://api.sandbox.push.apple.com"
	// apnsMaxPayload is the part of the 4096 bytes Apple allows that is left for
	// the notification once the alert text, which repeats part of it, and the
	// fixed parts are counted.
	apnsMaxPayload = 1500
	// Apple wants a token renewed no more than every 20 minutes and no later than
	// every 60.
	apnsTokenLife = 40 * time.Minute

	apnsMaxTitle = 120 // characters of title shown in the alert
	apnsMaxBody  = 500 // bytes of body shown in the alert
)

// placeholderAlert is shown when the notification holds no message: the app's
// notification service extension replaces it with the real one. If it can't, this
// is better than nothing.
const placeholderAlert = "New message"

// APNsSender sends to iOS and macOS devices through APNs.
type APNsSender struct {
	client  *http.Client
	key     *ecdsa.PrivateKey
	keyID   string
	teamID  string
	topic   string // the app's bundle ID
	baseURL string
	now     func() time.Time

	mu       sync.Mutex
	jwt      string
	jwtMade  time.Time
	jwtStale bool // Apple rejected it as expired: make another
}

// NewAPNs returns a sender for the app whose bundle ID is topic. keyPEM is the
// .p8 file from the Apple Developer account, keyID its ID and teamID the team's.
// sandbox selects Apple's development servers, which only accept the tokens of
// apps built for development.
func NewAPNs(keyPEM []byte, keyID, teamID, topic string, sandbox bool, client *http.Client) (*APNsSender, error) {
	if keyID == "" || teamID == "" || topic == "" {
		return nil, errors.New("APNs needs a key ID, a team ID and the app's bundle ID")
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("the APNs key is not PEM: use the .p8 file from the Apple Developer account")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the APNs key can't be read: %w", err)
	}
	key, ok := k.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("the APNs key is not a P-256 key")
	}
	base := apnsProduction
	if sandbox {
		base = apnsSandbox
	}
	return &APNsSender{client: client, key: key, keyID: keyID, teamID: teamID, topic: topic, baseURL: base, now: time.Now}, nil
}

// NewHTTP2Client returns a client for the push services of Google and Apple, which
// are fixed addresses. Unlike EndpointClient it may use the environment's proxy,
// for servers that can only reach the internet through one. APNs requires HTTP/2.
func NewHTTP2Client() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = true
	tr.MaxIdleConnsPerHost = 4
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}
}

// MaxPayload implements Sender.
func (s *APNsSender) MaxPayload() int { return apnsMaxPayload }

// providerToken returns the JWT that signs requests, making a new one when the
// last is old or Apple said it had expired.
func (s *APNsSender) providerToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.jwt != "" && !s.jwtStale && now.Sub(s.jwtMade) < apnsTokenLife {
		return s.jwt, nil
	}
	jwt, err := signES256(s.key,
		map[string]string{"alg": "ES256", "kid": s.keyID},
		map[string]any{"iss": s.teamID, "iat": now.Unix()})
	if err != nil {
		return "", err
	}
	s.jwt, s.jwtMade, s.jwtStale = jwt, now, false
	return jwt, nil
}

func (s *APNsSender) expireToken(used string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jwt == used {
		s.jwtStale = true
	}
}

// Send implements Sender.
func (s *APNsSender) Send(ctx context.Context, n Notification) error {
	body, err := s.body(n)
	if err != nil {
		return Permanent(err)
	}
	endpoint := s.baseURL + "/3/device/" + url.PathEscape(n.Device.Target)

	for attempt := 0; ; attempt++ {
		token, err := s.providerToken()
		if err != nil {
			return Transient(err, 0)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return Permanent(err)
		}
		h := req.Header
		h.Set("Authorization", "bearer "+token)
		h.Set("Content-Type", "application/json")
		h.Set("apns-topic", s.topic)
		h.Set("apns-push-type", "alert")
		h.Set("apns-priority", apnsPriority(n.Message.Priority))
		h.Set("apns-expiration", strconv.FormatInt(n.Expires.Unix(), 10))

		resp, err := s.client.Do(req)
		if err != nil {
			return Transient(cleanError(err), 0)
		}
		reply, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseRead))
		resp.Body.Close()
		reason := apnsReason(reply)
		if resp.StatusCode == http.StatusForbidden && reason == "ExpiredProviderToken" && attempt == 0 {
			s.expireToken(token)
			continue
		}
		return apnsResult(resp.StatusCode, reason, resp.Header)
	}
}

func apnsPriority(priority int) string {
	if priority <= 2 {
		return "5" // power-considerate: Apple may hold it until the device is awake anyway
	}
	return "10"
}

// interruptionLevel maps a priority to how much the notification may disturb
// (section 5.5 of the architecture).
func interruptionLevel(priority int) string {
	switch {
	case priority <= 2:
		return "passive"
	case priority == 3:
		return "active"
	}
	return "time-sensitive"
}

// body builds the notification's JSON.
func (s *APNsSender) body(n Notification) ([]byte, error) {
	alert := map[string]string{"body": placeholderAlert}
	if !n.WakeUp {
		// The notification holds the message, so show it. The extension may replace
		// it, but where there is none (or it runs out of time) this is what appears.
		alert = map[string]string{}
		if n.Message.Title != "" {
			alert["title"] = truncateRunes(n.Message.Title, apnsMaxTitle)
		}
		text := n.Message.Message
		if text == "" && n.Message.Title == "" {
			text = placeholderAlert
		}
		if text != "" {
			alert["body"] = truncateBytes(text, apnsMaxBody)
		}
	}
	aps := map[string]any{
		"alert":              alert,
		"mutable-content":    1,
		"interruption-level": interruptionLevel(n.Message.Priority),
	}
	if n.Message.Priority >= 3 {
		aps["sound"] = "default"
	}
	return json.Marshal(map[string]any{"aps": aps, "flare": json.RawMessage(n.Payload)})
}

// truncateRunes cuts s to at most max characters.
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

// truncateBytes cuts s to at most max bytes without splitting a character.
func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	const ellipsis = "…"
	cut := max - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}

func apnsReason(body []byte) string {
	var r struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &r)
	return r.Reason
}

// apnsResult sorts out APNs's answer
// (https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns).
func apnsResult(status int, reason string, header http.Header) error {
	if status == http.StatusOK {
		return nil
	}
	err := fmt.Errorf("APNs answered %d %s", status, reason)
	switch {
	case status == http.StatusGone, reason == "Unregistered",
		reason == "BadDeviceToken", reason == "DeviceTokenNotForTopic":
		// The app was removed, or the token is not one APNs will take for this app
		// and environment. The app registers again when it next runs.
		return Gone(err)
	case status == http.StatusTooManyRequests, status >= 500:
		return Transient(err, retryAfter(header.Get("Retry-After"), time.Now()))
	case status == http.StatusForbidden:
		// Our key, team or topic is wrong, or the token expired twice: the operator
		// can fix it, after which the notification is still worth sending.
		return Transient(err, 0)
	}
	return Permanent(err)
}
