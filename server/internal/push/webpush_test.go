package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/message"
)

func unb64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("bad base64 %q: %v", s, err)
	}
	return b
}

// RFC 8291 appendix A: a complete example, with the keys, the salt, the message
// and the encrypted result. Reproducing its output exactly is the evidence that a
// browser can decrypt what the server sends.
func TestEncryptionMatchesTheRFC8291TestVector(t *testing.T) {
	plaintext := unb64(t, "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24")
	if string(plaintext) != "When I grow up, I want to be a watermelon" {
		t.Fatalf("plaintext = %q", plaintext)
	}
	asPrivate, err := ecdh.P256().NewPrivateKey(unb64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	if got := b64(asPrivate.PublicKey().Bytes()); got != "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8" {
		t.Fatalf("the sender's public key = %s: the vector is not the one in the RFC", got)
	}
	uaPublic, err := ecdh.P256().NewPublicKey(unb64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"))
	if err != nil {
		t.Fatal(err)
	}
	authSecret := unb64(t, "BTBZMqHH6r4Tts7J_aSIgg")
	salt := unb64(t, "DGv6ra1nlYgDCS1FRnbzlw")

	got, err := encryptRecord(plaintext, uaPublic, authSecret, asPrivate, salt)
	if err != nil {
		t.Fatal(err)
	}
	want := unb64(t, "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN")
	if !bytes.Equal(got, want) {
		t.Errorf("encrypted body differs from the RFC's:\n got  %s\n want %s", b64(got), b64(want))
	}
}

// decrypt is what a browser does with the body (RFC 8188 and 8291), written from
// the receiving side: it starts from the user agent's private key.
func decrypt(t *testing.T, body []byte, uaPrivate *ecdh.PrivateKey, authSecret []byte) ([]byte, error) {
	t.Helper()
	if len(body) < 86 {
		return nil, errors.New("too short")
	}
	salt, rs, idlen := body[:16], int(body[16])<<24|int(body[17])<<16|int(body[18])<<8|int(body[19]), int(body[20])
	if rs != 4096 || idlen != 65 {
		t.Fatalf("header says record size %d and key ID length %d", rs, idlen)
	}
	asPublic, err := ecdh.P256().NewPublicKey(body[21 : 21+idlen])
	if err != nil {
		return nil, err
	}
	shared, err := uaPrivate.ECDH(asPublic)
	if err != nil {
		return nil, err
	}
	prk, _ := hkdf.Extract(sha256.New, shared, authSecret)
	info := append(append([]byte("WebPush: info\x00"), uaPrivate.PublicKey().Bytes()...), asPublic.Bytes()...)
	ikm, _ := hkdf.Expand(sha256.New, prk, string(info), 32)
	prk2, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk2, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk2, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		return nil, err
	}
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		return nil, errors.New("the record does not end with the delimiter")
	}
	return plain[:len(plain)-1], nil
}

type browser struct {
	private *ecdh.PrivateKey
	auth    []byte
}

func newBrowser(t *testing.T) browser {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	rand.Read(auth)
	return browser{priv, auth}
}

func (b browser) keys() Keys {
	return Keys{P256DH: b64(b.private.PublicKey().Bytes()), Auth: b64(b.auth)}
}

func TestEncryptedNotificationsRoundTripAndEachIsDifferent(t *testing.T) {
	br := newBrowser(t)
	ua := br.private.PublicKey()
	for _, plain := range [][]byte{[]byte("x"), []byte(strings.Repeat("é", 1000)), bytes.Repeat([]byte{0, 1, 2, 0x02}, 500), make([]byte, webPushMaxPayload)} {
		a, err := encryptRecord(plain, ua, br.auth, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := encryptRecord(plain, ua, br.auth, nil, nil)
		if bytes.Equal(a, b) {
			t.Error("two encryptions of the same text are identical: the key or salt is reused")
		}
		got, err := decrypt(t, a, br.private, br.auth)
		if err != nil || !bytes.Equal(got, plain) {
			t.Errorf("round trip of %d bytes: %v", len(plain), err)
		}
		if len(a) > 4096 {
			t.Errorf("%d bytes of plaintext became %d bytes, over the 4096 every push service accepts", len(plain), len(a))
		}
	}
}

// Both the salt and the sender's key must be new for every notification: the keys
// for a record come from the two together, and reusing either weakens what the
// other provides.
func TestEverySaltAndEverySendersKeyIsNew(t *testing.T) {
	br := newBrowser(t)
	salts, keys := map[string]bool{}, map[string]bool{}
	for i := 0; i < 50; i++ {
		body, err := encryptRecord([]byte("same text"), br.private.PublicKey(), br.auth, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		salts[string(body[:16])] = true
		keys[string(body[21:86])] = true
	}
	if len(salts) != 50 {
		t.Errorf("%d different salts in 50 notifications", len(salts))
	}
	if len(keys) != 50 {
		t.Errorf("%d different sender keys in 50 notifications", len(keys))
	}
}

func TestEncryptionRefusesWhatDoesNotFitAndWhatWasTamperedWithFailsToDecrypt(t *testing.T) {
	br := newBrowser(t)
	if _, err := encryptRecord(make([]byte, webPushMaxPayload+1), br.private.PublicKey(), br.auth, nil, nil); err == nil {
		t.Error("an oversize payload was encrypted")
	}
	full, _ := encryptRecord(make([]byte, webPushMaxPayload), br.private.PublicKey(), br.auth, nil, nil)
	if len(full) != 4096 {
		t.Errorf("a full record is %d bytes, want exactly 4096", len(full))
	}

	body, _ := encryptRecord([]byte("secret"), br.private.PublicKey(), br.auth, nil, nil)
	for _, i := range []int{0, 30, len(body) - 1} {
		bad := append([]byte(nil), body...)
		bad[i] ^= 1
		if _, err := decrypt(t, bad, br.private, br.auth); err == nil {
			t.Errorf("flipping byte %d went unnoticed", i)
		}
	}
	other := newBrowser(t)
	if _, err := decrypt(t, body, other.private, other.auth); err == nil {
		t.Error("another browser could decrypt it")
	}
	wrongAuth := append([]byte(nil), br.auth...)
	wrongAuth[0] ^= 1
	if _, err := decrypt(t, body, br.private, wrongAuth); err == nil {
		t.Error("it decrypted without the right authentication secret")
	}
}

func TestVerifyES256RejectsWhatWasNotSignedByTheKey(t *testing.T) {
	key, _ := NewVAPIDKey()
	other, _ := NewVAPIDKey()
	jwt, err := signES256(key, map[string]string{"alg": "ES256"}, map[string]any{"aud": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !verifyES256(&key.PublicKey, jwt) {
		t.Fatal("a signature does not verify against its own key")
	}
	if verifyES256(&other.PublicKey, jwt) {
		t.Error("verified against another key")
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 || len(unb64(t, parts[2])) != 64 {
		t.Errorf("a JWT's ES256 signature is 64 bytes (r and s), got %d", len(unb64(t, parts[2])))
	}
	forged := parts[0] + "." + b64([]byte(`{"aud":"y"}`)) + "." + parts[2]
	if verifyES256(&key.PublicKey, forged) {
		t.Error("verified a forged payload")
	}
	if verifyES256(&key.PublicKey, "garbage") || verifyES256(&key.PublicKey, "a.b.c") {
		t.Error("verified garbage")
	}
}

// pushServer stands in for a browser vendor's push service.
type pushServer struct {
	*httptest.Server
	mu      sync.Mutex
	last    *http.Request
	body    []byte
	status  int
	headers map[string]string
}

func newPushServer(t *testing.T) *pushServer {
	t.Helper()
	ps := &pushServer{status: 201}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ps.mu.Lock()
		ps.last, ps.body = r, body
		status, headers := ps.status, ps.headers
		ps.mu.Unlock()
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		io.WriteString(w, `{"reason":"because"}`)
	}))
	t.Cleanup(ps.Close)
	return ps
}

func (ps *pushServer) reply(status int, headers map[string]string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.status, ps.headers = status, headers
}

func (ps *pushServer) request() (*http.Request, []byte) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.last, ps.body
}

func notification(device Device, payload string, priority int, ttl time.Duration) Notification {
	now := time.Unix(1_800_000_000, 0)
	return Notification{
		Device: device, SubscriptionID: "sub1", Payload: []byte(payload), Now: now, Expires: now.Add(ttl),
		Message: message.Message{ID: "m1", Priority: priority},
	}
}

func webPushDevice(ps *pushServer, br browser) Device {
	return Device{ID: "d1", Provider: WebPush, Target: ps.URL + "/wp/abc123", Keys: br.keys()}
}

func TestAWebPushRequestIsWhatAPushServiceExpects(t *testing.T) {
	ps := newPushServer(t)
	br := newBrowser(t)
	key, _ := NewVAPIDKey()
	s := NewWebPush(EndpointClient(true), key, "mailto:admin@example.com", true)
	fixed := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return fixed }

	if err := s.Send(context.Background(), notification(webPushDevice(ps, br), `{"event":"message","sub":"sub1"}`, 5, 2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	req, body := ps.request()
	if req.Method != "POST" || req.URL.Path != "/wp/abc123" {
		t.Errorf("%s %s", req.Method, req.URL.Path)
	}
	for header, want := range map[string]string{
		"Content-Encoding": "aes128gcm", "Content-Type": "application/octet-stream", "Ttl": "7200", "Urgency": "high",
	} {
		if got := req.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	// The body decrypts, for that browser only, to the payload.
	plain, err := decrypt(t, body, br.private, br.auth)
	if err != nil || string(plain) != `{"event":"message","sub":"sub1"}` {
		t.Errorf("decrypted %q, %v", plain, err)
	}

	// The VAPID header: a JWT the server's key signed, naming the push service as its
	// audience, and the key itself.
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "vapid t=") || !strings.HasSuffix(auth, ", k="+s.PublicKey()) {
		t.Fatalf("Authorization = %q", auth)
	}
	jwt := strings.TrimSuffix(strings.TrimPrefix(auth, "vapid t="), ", k="+s.PublicKey())
	if !verifyES256(&key.PublicKey, jwt) {
		t.Error("the JWT is not signed by the server's key")
	}
	parts := strings.Split(jwt, ".")
	var header map[string]string
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	json.Unmarshal(unb64(t, parts[0]), &header)
	json.Unmarshal(unb64(t, parts[1]), &claims)
	if header["alg"] != "ES256" || header["typ"] != "JWT" {
		t.Errorf("header = %v", header)
	}
	if claims.Aud != ps.URL {
		t.Errorf("aud = %q, want the origin %q", claims.Aud, ps.URL)
	}
	if claims.Exp != fixed.Add(12*time.Hour).Unix() || claims.Exp-fixed.Unix() > 24*3600 {
		t.Errorf("exp = %d: at most 24 hours from now is allowed", claims.Exp)
	}
	if claims.Sub != "mailto:admin@example.com" {
		t.Errorf("sub = %q", claims.Sub)
	}
	// And the key is a point on P-256 that a push service can parse.
	if _, err := ecdh.P256().NewPublicKey(unb64(t, s.PublicKey())); err != nil {
		t.Errorf("PublicKey() is not an uncompressed P-256 key: %v", err)
	}
}

func TestWebPushHeadersFollowThePriorityAndTheMessagesLife(t *testing.T) {
	ps := newPushServer(t)
	br := newBrowser(t)
	key, _ := NewVAPIDKey()
	s := NewWebPush(EndpointClient(true), key, "", true)
	for _, tt := range []struct {
		priority int
		ttl      time.Duration
		urgency  string
		wantTTL  string
	}{
		{1, time.Hour, "very-low", "3600"},
		{2, time.Hour, "low", "3600"},
		{3, 90 * time.Second, "normal", "90"},
		{4, time.Hour, "high", "3600"},
		{5, 24 * time.Hour, "high", "86400"},
		{3, 60 * 24 * time.Hour, "normal", strconv.Itoa(28 * 24 * 3600)}, // capped at four weeks
		{3, 0, "normal", "0"},
	} {
		if err := s.Send(context.Background(), notification(webPushDevice(ps, br), "x", tt.priority, tt.ttl)); err != nil {
			t.Fatal(err)
		}
		req, _ := ps.request()
		if req.Header.Get("Urgency") != tt.urgency || req.Header.Get("TTL") != tt.wantTTL {
			t.Errorf("priority %d, ttl %v: Urgency=%q TTL=%q; want %q %q", tt.priority, tt.ttl, req.Header.Get("Urgency"), req.Header.Get("TTL"), tt.urgency, tt.wantTTL)
		}
	}

	// Without a subject the claim is left out rather than made up.
	if err := s.Send(context.Background(), notification(webPushDevice(ps, br), "x", 3, time.Hour)); err != nil {
		t.Fatal(err)
	}
	req, _ := ps.request()
	jwt := strings.Split(strings.TrimPrefix(req.Header.Get("Authorization"), "vapid t="), ",")[0]
	if strings.Contains(string(unb64(t, strings.Split(jwt, ".")[1])), "sub") {
		t.Error("a sub claim was invented")
	}
}

func TestHowAPushServiceSAnswersAreSortedOut(t *testing.T) {
	ps := newPushServer(t)
	br := newBrowser(t)
	key, _ := NewVAPIDKey()
	s := NewWebPush(EndpointClient(true), key, "", true)
	tests := []struct {
		status       int
		headers      map[string]string
		ok           bool
		gone         bool
		permanent    bool
		retryAfterAt time.Duration
	}{
		{status: 201, ok: true},
		{status: 200, ok: true},
		{status: 202, ok: true},
		{status: 404, gone: true, permanent: true},
		{status: 410, gone: true, permanent: true},
		{status: 400, permanent: true},
		{status: 401, permanent: true},
		{status: 403, permanent: true}, // a subscription made with another server key
		{status: 413, permanent: true},
		{status: 429, headers: map[string]string{"Retry-After": "120"}, retryAfterAt: 120 * time.Second},
		{status: 429},
		{status: 500},
		{status: 503, headers: map[string]string{"Retry-After": "30"}, retryAfterAt: 30 * time.Second},
		{status: 408},
		{status: 301, permanent: true}, // redirects are not followed
	}
	for _, tt := range tests {
		ps.reply(tt.status, tt.headers)
		err := s.Send(context.Background(), notification(webPushDevice(ps, br), "x", 3, time.Hour))
		if tt.ok {
			if err != nil {
				t.Errorf("%d: %v", tt.status, err)
			}
			continue
		}
		var se *SendError
		if !errors.As(err, &se) {
			t.Errorf("%d: err = %v, want a SendError", tt.status, err)
			continue
		}
		if se.Unregistered != tt.gone || se.Permanent != tt.permanent || se.RetryAfter != tt.retryAfterAt {
			t.Errorf("%d: gone=%v permanent=%v retryAfter=%v; want %v %v %v", tt.status, se.Unregistered, se.Permanent, se.RetryAfter, tt.gone, tt.permanent, tt.retryAfterAt)
		}
		if !strings.Contains(se.Error(), strconv.Itoa(tt.status)) || !strings.Contains(se.Error(), "because") {
			t.Errorf("%d: the error should say what the service said: %v", tt.status, se)
		}
	}
}

func TestTheEndpointNeverAppearsInAnError(t *testing.T) {
	br := newBrowser(t)
	key, _ := NewVAPIDKey()
	s := NewWebPush(EndpointClient(true), key, "", true)
	// Nothing listens there.
	d := Device{ID: "d1", Provider: WebPush, Target: "http://127.0.0.1:1/wp/very-secret-path", Keys: br.keys()}
	err := s.Send(context.Background(), notification(d, "x", 3, time.Hour))
	var se *SendError
	if !errors.As(err, &se) || se.Permanent || se.Unregistered {
		t.Fatalf("err = %v, want a transient failure", err)
	}
	if strings.Contains(err.Error(), "very-secret-path") || strings.Contains(err.Error(), "127.0.0.1:1/") {
		t.Errorf("the endpoint is in the error: %v", err)
	}
}

func TestWebPushRefusesWhatShouldNotBeSent(t *testing.T) {
	ps := newPushServer(t)
	br := newBrowser(t)
	key, _ := NewVAPIDKey()
	strict := NewWebPush(EndpointClient(false), key, "", false)

	// A private endpoint is refused when the operator hasn't allowed them, whatever
	// was accepted when the device registered.
	err := strict.Send(context.Background(), notification(webPushDevice(ps, br), "x", 3, time.Hour))
	var se *SendError
	if !errors.As(err, &se) || !se.Permanent {
		t.Errorf("a private endpoint: %v", err)
	}
	if n := func() int { ps.mu.Lock(); defer ps.mu.Unlock(); return len(ps.body) }(); n != 0 {
		t.Error("a request reached the private server")
	}

	lax := NewWebPush(EndpointClient(true), key, "", true)
	bad := webPushDevice(ps, br)
	bad.Keys.P256DH = b64(make([]byte, 65))
	if err := lax.Send(context.Background(), notification(bad, "x", 3, time.Hour)); !errors.As(err, &se) || !se.Permanent {
		t.Errorf("a bad key: %v", err)
	}
	bad = webPushDevice(ps, br)
	bad.Keys.Auth = "short"
	if err := lax.Send(context.Background(), notification(bad, "x", 3, time.Hour)); !errors.As(err, &se) || !se.Permanent {
		t.Errorf("a bad auth secret: %v", err)
	}
	tooBig := notification(webPushDevice(ps, br), strings.Repeat("x", webPushMaxPayload+1), 3, time.Hour)
	if err := lax.Send(context.Background(), tooBig); !errors.As(err, &se) || !se.Permanent {
		t.Errorf("an oversize payload: %v", err)
	}
}

func TestTheVAPIDKeyIsMadeOnceAndKept(t *testing.T) {
	st := newMemSettings()
	first, err := LoadVAPIDKey(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadVAPIDKey(context.Background(), st)
	if err != nil || !first.Equal(second) {
		t.Errorf("a second load gave another key: %v", err)
	}

	// Servers starting together on one database all end up with the same key.
	race := newMemSettings()
	keys := make([]*ecdsa.PrivateKey, 8)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			keys[i], _ = LoadVAPIDKey(context.Background(), race)
		}()
	}
	wg.Wait()
	for i, k := range keys {
		if k == nil || !k.Equal(keys[0]) {
			t.Errorf("key %d differs from key 0", i)
		}
	}

	// A damaged value is an error, not a reason to make a new key and strand every
	// subscribed browser.
	for _, damaged := range []string{"!!!", b64([]byte("not a key")), b64(nil)} {
		bad := newMemSettings()
		bad.values[vapidSetting] = damaged
		if _, err := LoadVAPIDKey(context.Background(), bad); err == nil {
			t.Errorf("%q was accepted as a key", damaged)
		}
		if bad.values[vapidSetting] != damaged {
			t.Error("the damaged key was overwritten")
		}
	}
}

// memSettings is a Store that keeps only settings.
type memSettings struct {
	Store
	mu     sync.Mutex
	values map[string]string
}

func newMemSettings() *memSettings { return &memSettings{values: map[string]string{}} }

func (m *memSettings) Setting(_ context.Context, k string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.values[k]
	return v, ok, nil
}

func (m *memSettings) SetSettingIfAbsent(_ context.Context, k, v string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.values[k]; ok {
		return old, nil
	}
	m.values[k] = v
	return v, nil
}

func TestRetryAfterReadsSecondsAndDates(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	for header, want := range map[string]time.Duration{
		"":     0,
		"120":  120 * time.Second,
		" 5 ":  5 * time.Second,
		"0":    0,
		"-5":   0,
		"soon": 0,
		now.Add(90 * time.Second).Format(http.TimeFormat): 90 * time.Second,
		now.Add(-time.Hour).Format(http.TimeFormat):       0,
	} {
		if got := retryAfter(header, now); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", header, got, want)
		}
	}
}

func messageWith(title, text string, priority int) message.Message {
	return message.Message{ID: "01933c3e-7a4f-7cc1-9b2e-5f1d6a8e4c21", Time: 1_800_000_000, Event: message.EventMessage, Topic: "alerts", Title: title, Message: text, Priority: priority}
}
