package push

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// apple stands in for APNs. It speaks HTTP/2 over TLS, as the real one requires.
type apple struct {
	*httptest.Server
	pub *ecdsa.PublicKey

	mu       sync.Mutex
	calls    []apnsCall
	replies  []appleReply // consumed in order; the last repeats
	rejected map[string]bool
}

type appleReply struct {
	status  int
	reason  string
	headers map[string]string
}

type apnsCall struct {
	Proto   int
	Path    string
	Header  http.Header
	Body    map[string]any
	RawSize int
}

func newApple(t *testing.T, key *ecdsa.PrivateKey) *apple {
	t.Helper()
	a := &apple{pub: &key.PublicKey}
	a.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		call := apnsCall{Proto: r.ProtoMajor, Path: r.URL.Path, Header: r.Header.Clone(), RawSize: len(body)}
		json.Unmarshal(body, &call.Body)
		a.mu.Lock()
		a.calls = append(a.calls, call)
		reply := appleReply{status: 200}
		if len(a.replies) > 0 {
			reply = a.replies[min(len(a.calls)-1, len(a.replies)-1)]
		}
		a.mu.Unlock()
		for k, v := range reply.headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(reply.status)
		if reply.reason != "" {
			json.NewEncoder(w).Encode(map[string]string{"reason": reply.reason})
		}
	}))
	a.EnableHTTP2 = true
	a.Config.ErrorLog = log.New(io.Discard, "", 0)
	a.StartTLS()
	t.Cleanup(a.Close)
	return a
}

func (a *apple) reply(rs ...appleReply) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.replies = rs
}

func (a *apple) all() []apnsCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]apnsCall(nil), a.calls...)
}

func (a *apple) sender(t *testing.T, key *ecdsa.PrivateKey) *APNsSender {
	t.Helper()
	s, err := NewAPNs([]byte(pemPKCS8(t, key)), "KEY1234567", "TEAM123456", "org.example.flare", false, a.Client())
	if err != nil {
		t.Fatal(err)
	}
	s.baseURL = a.URL
	return s
}

const apnsToken = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

var apnsDevice = Device{ID: "d1", Provider: APNs, AppID: "org.example.flare", Target: apnsToken}

func fullNotification(title, text string, priority int) Notification {
	n := notification(apnsDevice, string(FullPayload(messageWith(title, text, priority), "sub1")), priority, time.Hour)
	n.Message = messageWith(title, text, priority)
	return n
}

func TestAnAPNsSendIsWhatAppleExpects(t *testing.T) {
	key, _ := NewVAPIDKey() // any P-256 key
	ap := newApple(t, key)
	s := ap.sender(t, key)
	fixed := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return fixed }

	n := fullNotification("Disk", "Disk is almost full", 5)
	n.Now = fixed
	n.Expires = fixed.Add(time.Hour)
	if err := s.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	call := ap.all()[0]

	if call.Proto != 2 {
		t.Errorf("HTTP/%d: APNs only speaks HTTP/2", call.Proto)
	}
	if call.Path != "/3/device/"+apnsToken {
		t.Errorf("path = %q", call.Path)
	}
	for header, want := range map[string]string{
		"Apns-Topic": "org.example.flare", "Apns-Push-Type": "alert", "Apns-Priority": "10",
		"Apns-Expiration": strconv.FormatInt(fixed.Add(time.Hour).Unix(), 10),
	} {
		if got := call.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	// The provider token.
	auth := call.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "bearer ") {
		t.Fatalf("Authorization = %q", auth)
	}
	jwt := strings.TrimPrefix(auth, "bearer ")
	if !verifyES256(ap.pub, jwt) {
		t.Error("the provider token is not signed with the team's key")
	}
	parts := strings.Split(jwt, ".")
	var header map[string]string
	var claims map[string]any
	json.Unmarshal(unb64(t, parts[0]), &header)
	json.Unmarshal(unb64(t, parts[1]), &claims)
	if header["alg"] != "ES256" || header["kid"] != "KEY1234567" || claims["iss"] != "TEAM123456" || claims["iat"] != float64(fixed.Unix()) {
		t.Errorf("header %v, claims %v", header, claims)
	}

	// The body.
	aps := call.Body["aps"].(map[string]any)
	alert := aps["alert"].(map[string]any)
	if alert["title"] != "Disk" || alert["body"] != "Disk is almost full" {
		t.Errorf("alert = %v", alert)
	}
	if aps["mutable-content"] != float64(1) || aps["interruption-level"] != "time-sensitive" || aps["sound"] != "default" {
		t.Errorf("aps = %v", aps)
	}
	flare := call.Body["flare"].(map[string]any)
	if flare["event"] != "message" || flare["sub"] != "sub1" || flare["message"] != "Disk is almost full" {
		t.Errorf("flare = %v", flare)
	}
}

func TestAWakeUpShowsOnlyAPlaceholderAndRevealsNothing(t *testing.T) {
	key, _ := NewVAPIDKey()
	ap := newApple(t, key)
	s := ap.sender(t, key)
	m := messageWith("Secret title", "secret text about payroll", 3)
	m.Topic = "payroll"
	n := notification(apnsDevice, string(WakePayload("sub1", m.ID)), 3, time.Hour)
	n.Message, n.WakeUp = m, true
	if err := s.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	call := ap.all()[0]
	alert := call.Body["aps"].(map[string]any)["alert"].(map[string]any)
	if len(alert) != 1 || alert["body"] != "New message" {
		t.Errorf("alert = %v, want only the placeholder", alert)
	}
	raw, _ := json.Marshal(call.Body)
	for _, leak := range []string{"payroll", "Secret", "secret"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("the notification contains %q: %s", leak, raw)
		}
	}
	flare := call.Body["flare"].(map[string]any)
	if flare["event"] != "poll_request" || flare["id"] != m.ID || flare["sub"] != "sub1" {
		t.Errorf("flare = %v", flare)
	}
	if call.Body["aps"].(map[string]any)["mutable-content"] != float64(1) {
		t.Error("a wake-up needs mutable-content, or the extension that fetches the message never runs")
	}
}

func TestAPNsPriorityMapsToDeliveryAndInterruption(t *testing.T) {
	key, _ := NewVAPIDKey()
	ap := newApple(t, key)
	s := ap.sender(t, key)
	for _, tt := range []struct {
		priority int
		apns     string
		level    string
		sound    bool
	}{
		{1, "5", "passive", false},
		{2, "5", "passive", false},
		{3, "10", "active", true},
		{4, "10", "time-sensitive", true},
		{5, "10", "time-sensitive", true},
	} {
		if err := s.Send(context.Background(), fullNotification("", "x", tt.priority)); err != nil {
			t.Fatal(err)
		}
		call := ap.all()[len(ap.all())-1]
		aps := call.Body["aps"].(map[string]any)
		_, hasSound := aps["sound"]
		if call.Header.Get("Apns-Priority") != tt.apns || aps["interruption-level"] != tt.level || hasSound != tt.sound {
			t.Errorf("priority %d: apns-priority %s, level %v, sound %v; want %s %s %v",
				tt.priority, call.Header.Get("Apns-Priority"), aps["interruption-level"], hasSound, tt.apns, tt.level, tt.sound)
		}
	}
}

func TestLongAlertTextIsCutWithoutBreakingCharacters(t *testing.T) {
	key, _ := NewVAPIDKey()
	ap := newApple(t, key)
	s := ap.sender(t, key)

	longTitle := strings.Repeat("é", 250) // the longest title there can be
	longBody := strings.Repeat("日", 1000) // 3000 bytes
	n := fullNotification(longTitle, longBody, 3)
	// As if the payload fit, so that the alert text is what is being tested.
	if err := s.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	alert := ap.all()[0].Body["aps"].(map[string]any)["alert"].(map[string]any)
	title, body := alert["title"].(string), alert["body"].(string)
	if utf8.RuneCountInString(title) != apnsMaxTitle || !strings.HasSuffix(title, "…") {
		t.Errorf("title is %d characters: %.20q", utf8.RuneCountInString(title), title)
	}
	if len(body) > apnsMaxBody || !utf8.ValidString(body) || !strings.HasSuffix(body, "…") {
		t.Errorf("body is %d bytes, valid=%v", len(body), utf8.ValidString(body))
	}

	if got := truncateBytes("short", 500); got != "short" {
		t.Errorf("short text changed: %q", got)
	}
	for _, s := range []string{"日本語日本語", "aé日😀日本", "😀😀😀😀😀😀"} {
		for max := 4; max <= len(s); max++ {
			if got := truncateBytes(s, max); len(got) > max || !utf8.ValidString(got) {
				t.Errorf("truncateBytes(%q, %d) = %q", s, max, got)
			}
		}
	}
}

func TestTheLargestNotificationStaysUnderApplesLimit(t *testing.T) {
	key, _ := NewVAPIDKey()
	ap := newApple(t, key)
	s := ap.sender(t, key)
	// A message whose full payload just fits what APNs may carry, with the longest
	// title and a long body, all in multibyte characters.
	m := messageWith(strings.Repeat("日", 250), strings.Repeat("日", 1365), 5) // a body at the default limit of 4096 bytes
	for len(FullPayload(m, "subscription-id-0123456789")) > s.MaxPayload() {
		m.Message = string([]rune(m.Message)[1:])
	}
	payload := FullPayload(m, "subscription-id-0123456789")
	if len(payload) < s.MaxPayload()-10 {
		t.Fatalf("the test message is %d bytes, short of the %d it is meant to reach", len(payload), s.MaxPayload())
	}
	n := notification(apnsDevice, string(payload), 5, time.Hour)
	n.Message = m
	if err := s.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if size := ap.all()[0].RawSize; size > 4096 {
		t.Errorf("the request body is %d bytes; APNs refuses more than 4096", size)
	}
}

func TestTheProviderTokenIsReusedAndRenewedOnTime(t *testing.T) {
	key, _ := NewVAPIDKey()
	ap := newApple(t, key)
	s := ap.sender(t, key)
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	tokenOf := func(i int) string { return ap.all()[i].Header.Get("Authorization") }

	s.Send(context.Background(), fullNotification("", "a", 3))
	now = now.Add(39 * time.Minute) // Apple wants one token for at least 20 minutes
	s.Send(context.Background(), fullNotification("", "b", 3))
	if tokenOf(0) != tokenOf(1) {
		t.Error("a new token was made within 40 minutes: Apple throttles that")
	}
	now = now.Add(2 * time.Minute) // past 40: Apple refuses tokens older than 60
	s.Send(context.Background(), fullNotification("", "c", 3))
	if tokenOf(2) == tokenOf(0) {
		t.Error("the token was not renewed after 41 minutes")
	}
}

func TestAnExpiredProviderTokenIsRenewedOnceAndTheSendRetried(t *testing.T) {
	key, _ := NewVAPIDKey()
	ap := newApple(t, key)
	s := ap.sender(t, key)
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { now = now.Add(time.Second); return now }

	ap.reply(appleReply{status: 403, reason: "ExpiredProviderToken"}, appleReply{status: 200})
	if err := s.Send(context.Background(), fullNotification("", "x", 3)); err != nil {
		t.Fatalf("an expiry followed by success: %v", err)
	}
	calls := ap.all()
	if len(calls) != 2 || calls[0].Header.Get("Authorization") == calls[1].Header.Get("Authorization") {
		t.Errorf("%d calls; the retry must use a new token", len(calls))
	}

	// Refused again: transient, and only the one retry.
	ap2 := newApple(t, key)
	s2 := ap2.sender(t, key)
	ap2.reply(appleReply{status: 403, reason: "ExpiredProviderToken"})
	err := s2.Send(context.Background(), fullNotification("", "x", 3))
	var se *SendError
	if !errors.As(err, &se) || se.Permanent || se.Unregistered {
		t.Errorf("err = %v, want transient", err)
	}
	if n := len(ap2.all()); n != 2 {
		t.Errorf("%d requests, want one retry", n)
	}
}

func TestAPNsAnswersAreSortedOut(t *testing.T) {
	key, _ := NewVAPIDKey()
	tests := []struct {
		status     int
		reason     string
		headers    map[string]string
		kind       string
		retryAfter time.Duration
	}{
		{200, "", nil, "ok", 0},
		{410, "Unregistered", nil, "gone", 0},
		{400, "BadDeviceToken", nil, "gone", 0},
		{400, "DeviceTokenNotForTopic", nil, "gone", 0},
		{400, "BadTopic", nil, "permanent", 0},
		{400, "PayloadEmpty", nil, "permanent", 0},
		{400, "BadExpirationDate", nil, "permanent", 0},
		{413, "PayloadTooLarge", nil, "permanent", 0},
		{404, "BadPath", nil, "permanent", 0},
		{403, "InvalidProviderToken", nil, "transient", 0},
		{403, "MissingProviderToken", nil, "transient", 0},
		{429, "TooManyRequests", nil, "transient", 0},
		{429, "TooManyProviderTokenUpdates", map[string]string{"Retry-After": "60"}, "transient", 60 * time.Second},
		{500, "InternalServerError", nil, "transient", 0},
		{503, "ServiceUnavailable", nil, "transient", 0},
		{503, "Shutdown", nil, "transient", 0},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.status)+tt.reason, func(t *testing.T) {
			ap := newApple(t, key)
			s := ap.sender(t, key)
			ap.reply(appleReply{status: tt.status, reason: tt.reason, headers: tt.headers})
			err := s.Send(context.Background(), fullNotification("", "x", 3))
			var se *SendError
			got := "ok"
			if errors.As(err, &se) {
				switch {
				case se.Unregistered:
					got = "gone"
				case se.Permanent:
					got = "permanent"
				default:
					got = "transient"
				}
			} else if err != nil {
				got = "other: " + err.Error()
			}
			if got != tt.kind || (se != nil && se.RetryAfter != tt.retryAfter) {
				t.Errorf("got %s (%v); want %s retryAfter=%v", got, err, tt.kind, tt.retryAfter)
			}
			if se != nil && tt.reason != "" && !strings.Contains(se.Error(), tt.reason) {
				t.Errorf("the error should name Apple's reason: %v", se)
			}
		})
	}
}

func TestAPNsSetup(t *testing.T) {
	key, _ := NewVAPIDKey()
	p8 := []byte(pemPKCS8(t, key))
	rsa := testRSAKey(t)

	prod, err := NewAPNs(p8, "K", "T", "org.example.flare", false, nil)
	if err != nil || prod.baseURL != "https://api.push.apple.com" {
		t.Errorf("production: %v, %v", prod, err)
	}
	dev, err := NewAPNs(p8, "K", "T", "org.example.flare", true, nil)
	if err != nil || dev.baseURL != "https://api.sandbox.push.apple.com" {
		t.Errorf("sandbox: %v, %v", dev, err)
	}
	for name, args := range map[string]struct {
		pem                []byte
		keyID, team, topic string
		mention            string
	}{
		"no key ID":     {p8, "", "T", "a.b", "key ID"},
		"no team":       {p8, "K", "", "a.b", "team ID"},
		"no bundle ID":  {p8, "K", "T", "", "bundle ID"},
		"not PEM":       {[]byte("hello"), "K", "T", "a.b", ".p8"},
		"an RSA key":    {[]byte(pemPKCS8(t, rsa)), "K", "T", "a.b", "P-256"},
		"a damaged key": {[]byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"), "K", "T", "a.b", "can't be read"},
	} {
		if _, err := NewAPNs(args.pem, args.keyID, args.team, args.topic, false, nil); err == nil || !strings.Contains(err.Error(), args.mention) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, args.mention)
		}
	}
}
