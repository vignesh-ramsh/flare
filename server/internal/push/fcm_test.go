package push

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	rsaOnce sync.Once
	rsaKey  *rsa.PrivateKey
)

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	rsaOnce.Do(func() {
		var err error
		if rsaKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
	})
	return rsaKey
}

func pemPKCS8(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func verifyRS256(pub *rsa.PublicKey, jwt string) bool {
	i := lastDot(jwt)
	if i < 0 {
		return false
	}
	sig, err := decodeBase64URL(jwt[i+1:])
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(jwt[:i]))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) == nil
}

// google stands in for Google's token endpoint and FCM.
type google struct {
	*httptest.Server
	t   *testing.T
	pub *rsa.PublicKey

	mu             sync.Mutex
	tokenRequests  int
	tokenStatus    int
	tokenBody      string
	expiresIn      int64
	assertions     []string
	sends          []fcmCall
	sendStatus     []int // consumed in order; the last repeats
	sendBody       string
	sendHeaders    map[string]string
	rejectedTokens map[string]bool
}

type fcmCall struct {
	Authorization string
	Body          map[string]any
}

func newGoogle(t *testing.T) *google {
	t.Helper()
	g := &google{t: t, pub: &testRSAKey(t).PublicKey, expiresIn: 3600, tokenStatus: 200}
	g.Server = httptest.NewUnstartedServer(http.HandlerFunc(g.serve))
	g.Config.ErrorLog = log.New(io.Discard, "", 0) // clients closing connections is not news
	g.StartTLS()
	t.Cleanup(g.Close)
	return g
}

func (g *google) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch r.URL.Path {
	case "/token":
		r.ParseForm()
		g.tokenRequests++
		g.assertions = append(g.assertions, r.PostForm.Get("assertion"))
		if r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			w.WriteHeader(400)
			return
		}
		if g.tokenStatus != 200 {
			w.WriteHeader(g.tokenStatus)
			io.WriteString(w, g.tokenBody)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-token-" + string(rune('0'+g.tokenRequests)),
			"expires_in":   g.expiresIn, "token_type": "Bearer",
		})
	case "/v1/projects/test-project/messages:send":
		body, _ := io.ReadAll(r.Body)
		call := fcmCall{Authorization: r.Header.Get("Authorization")}
		json.Unmarshal(body, &call.Body)
		g.sends = append(g.sends, call)
		status := 200
		if len(g.sendStatus) > 0 {
			i := min(len(g.sends)-1, len(g.sendStatus)-1)
			status = g.sendStatus[i]
		}
		for k, v := range g.sendHeaders {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		io.WriteString(w, g.sendBody)
	default:
		w.WriteHeader(404)
	}
}

func (g *google) credentials(t *testing.T) []byte {
	b, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "test-project", "private_key_id": "key-1",
		"private_key": pemPKCS8(t, testRSAKey(t)), "client_email": "flare@test-project.iam.gserviceaccount.com",
		"token_uri": g.URL + "/token",
	})
	return b
}

func (g *google) sender(t *testing.T) *FCMSender {
	t.Helper()
	s, err := NewFCM(g.credentials(t), g.Client())
	if err != nil {
		t.Fatal(err)
	}
	s.sendURL = g.URL + "/v1/projects/test-project/messages:send"
	return s
}

func (g *google) state() (tokens, sends int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tokenRequests, len(g.sends)
}

var fcmDevice = Device{ID: "d1", Provider: FCM, AppID: "org.example.flare", Target: "fcm-registration-token-0123456789"}

func TestAnFCMSendSignsInAndPostsTheMessage(t *testing.T) {
	g := newGoogle(t)
	s := g.sender(t)
	fixed := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return fixed }

	n := notification(fcmDevice, `{"event":"message","sub":"sub1","message":"disk full"}`, 5, 2*time.Hour)
	if err := s.Send(context.Background(), n); err != nil {
		t.Fatal(err)
	}

	// The sign-in: an RS256 JWT from the service account, for the messaging scope.
	jwt := g.assertions[0]
	if !verifyRS256(g.pub, jwt) {
		t.Fatal("the assertion is not signed with the service account's key")
	}
	parts := strings.Split(jwt, ".")
	var header map[string]string
	var claims map[string]any
	json.Unmarshal(unb64(t, parts[0]), &header)
	json.Unmarshal(unb64(t, parts[1]), &claims)
	if header["alg"] != "RS256" || header["typ"] != "JWT" || header["kid"] != "key-1" {
		t.Errorf("header = %v", header)
	}
	if claims["iss"] != "flare@test-project.iam.gserviceaccount.com" || claims["scope"] != "https://www.googleapis.com/auth/firebase.messaging" ||
		claims["aud"] != g.URL+"/token" || claims["iat"] != float64(fixed.Unix()) || claims["exp"] != float64(fixed.Add(time.Hour).Unix()) {
		t.Errorf("claims = %v", claims)
	}

	// The message.
	call := g.sends[0]
	if call.Authorization != "Bearer access-token-1" {
		t.Errorf("Authorization = %q", call.Authorization)
	}
	msg := call.Body["message"].(map[string]any)
	if msg["token"] != fcmDevice.Target {
		t.Errorf("token = %v", msg["token"])
	}
	data := msg["data"].(map[string]any)
	if len(data) != 1 || data["flare"] != `{"event":"message","sub":"sub1","message":"disk full"}` {
		t.Errorf("data = %v: every value must be a string, and the payload goes in one key", data)
	}
	if msg["notification"] != nil {
		t.Error("a notification block was sent: the app's service must build the notification, so it needs a data message")
	}
	android := msg["android"].(map[string]any)
	if android["priority"] != "HIGH" || android["ttl"] != "7200s" {
		t.Errorf("android = %v", android)
	}
}

func TestFCMPriorityAndLifetime(t *testing.T) {
	g := newGoogle(t)
	s := g.sender(t)
	for _, tt := range []struct {
		priority int
		ttl      time.Duration
		want     string
		wantTTL  string
	}{
		{1, time.Hour, "NORMAL", "3600s"},
		{2, time.Hour, "NORMAL", "3600s"},
		{3, time.Hour, "HIGH", "3600s"},
		{5, time.Hour, "HIGH", "3600s"},
		{3, 90 * 24 * time.Hour, "HIGH", "2419200s"}, // capped at four weeks
		{3, 0, "HIGH", "0s"},
	} {
		if err := s.Send(context.Background(), notification(fcmDevice, "{}", tt.priority, tt.ttl)); err != nil {
			t.Fatal(err)
		}
		g.mu.Lock()
		android := g.sends[len(g.sends)-1].Body["message"].(map[string]any)["android"].(map[string]any)
		g.mu.Unlock()
		if android["priority"] != tt.want || android["ttl"] != tt.wantTTL {
			t.Errorf("priority %d, ttl %v: %v; want %s %s", tt.priority, tt.ttl, android, tt.want, tt.wantTTL)
		}
	}
}

func TestTheAccessTokenIsReusedUntilItNearlyExpires(t *testing.T) {
	g := newGoogle(t)
	s := g.sender(t)
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }

	for i := 0; i < 5; i++ {
		if err := s.Send(context.Background(), notification(fcmDevice, "{}", 3, time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if tokens, sends := g.state(); tokens != 1 || sends != 5 {
		t.Fatalf("%d token requests for %d sends, want 1", tokens, sends)
	}
	now = now.Add(58 * time.Minute) // the token has two minutes left: still good
	s.Send(context.Background(), notification(fcmDevice, "{}", 3, time.Hour))
	if tokens, _ := g.state(); tokens != 1 {
		t.Errorf("%d token requests after 58 minutes, want the token still in use", tokens)
	}
	now = now.Add(90 * time.Second) // 30 seconds left: inside the minute of slack
	s.Send(context.Background(), notification(fcmDevice, "{}", 3, time.Hour))
	if tokens, _ := g.state(); tokens != 2 {
		t.Errorf("%d token requests with 30 seconds left, want a renewal", tokens)
	}
	if g.sends[len(g.sends)-1].Authorization != "Bearer access-token-2" {
		t.Error("the renewed token was not used")
	}
}

func TestManySendsAtOnceShareOneSignIn(t *testing.T) {
	g := newGoogle(t)
	s := g.sender(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Send(context.Background(), notification(fcmDevice, "{}", 3, time.Hour)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if tokens, sends := g.state(); tokens != 1 || sends != 20 {
		t.Errorf("%d token requests for %d sends, want 1", tokens, sends)
	}
}

func TestARevokedAccessTokenIsReplacedOnceAndTheSendRetried(t *testing.T) {
	g := newGoogle(t)
	s := g.sender(t)
	g.sendStatus = []int{401, 200}
	g.sendBody = `{"error":{"code":401,"status":"UNAUTHENTICATED"}}`
	if err := s.Send(context.Background(), notification(fcmDevice, "{}", 3, time.Hour)); err != nil {
		t.Fatalf("a 401 followed by success: %v", err)
	}
	if tokens, sends := g.state(); tokens != 2 || sends != 2 {
		t.Errorf("%d token requests and %d sends, want 2 and 2", tokens, sends)
	}

	// Still refused with a fresh token: the credentials are wrong, which is not for
	// retrying in a loop but is worth another try later.
	g2 := newGoogle(t)
	s2 := g2.sender(t)
	g2.sendStatus = []int{401}
	err := s2.Send(context.Background(), notification(fcmDevice, "{}", 3, time.Hour))
	var se *SendError
	if !errors.As(err, &se) || se.Permanent || se.Unregistered {
		t.Errorf("err = %v, want a transient failure", err)
	}
	if tokens, sends := g2.state(); tokens != 2 || sends != 2 {
		t.Errorf("%d token requests and %d sends, want exactly one retry", tokens, sends)
	}
}

func TestFCMAnswersAreSortedOut(t *testing.T) {
	body := func(status, code, msg string) string {
		return `{"error":{"code":0,"message":"` + msg + `","status":"` + status + `","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"` + code + `"}]}}`
	}
	tests := []struct {
		name       string
		status     int
		body       string
		headers    map[string]string
		kind       string
		retryAfter time.Duration
	}{
		{"unregistered", 404, body("NOT_FOUND", "UNREGISTERED", "Requested entity was not found."), nil, "gone", 0},
		{"another project's token", 403, body("PERMISSION_DENIED", "SENDER_ID_MISMATCH", "SenderId mismatch"), nil, "gone", 0},
		{"a malformed token", 400, `{"error":{"code":400,"message":"The registration token is not a valid FCM registration token","status":"INVALID_ARGUMENT"}}`, nil, "gone", 0},
		{"a bad request of ours", 400, `{"error":{"code":400,"message":"Invalid JSON payload received.","status":"INVALID_ARGUMENT"}}`, nil, "permanent", 0},
		{"quota", 429, body("RESOURCE_EXHAUSTED", "QUOTA_EXCEEDED", "Quota exceeded"), map[string]string{"Retry-After": "30"}, "transient", 30 * time.Second},
		{"unavailable", 503, body("UNAVAILABLE", "UNAVAILABLE", "The server is overloaded"), nil, "transient", 0},
		{"internal", 500, body("INTERNAL", "INTERNAL", "Internal error"), nil, "transient", 0},
		{"the API is not enabled", 403, `{"error":{"code":403,"message":"Firebase Cloud Messaging API has not been used in project","status":"PERMISSION_DENIED"}}`, nil, "transient", 0},
		{"something unknown", 418, `{}`, nil, "permanent", 0},
		{"not even JSON", 502, `<html>Bad gateway</html>`, nil, "transient", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newGoogle(t)
			s := g.sender(t)
			g.sendStatus, g.sendBody, g.sendHeaders = []int{tt.status}, tt.body, tt.headers
			err := s.Send(context.Background(), notification(fcmDevice, "{}", 3, time.Hour))
			var se *SendError
			if !errors.As(err, &se) {
				t.Fatalf("err = %v, want a SendError", err)
			}
			got := "transient"
			switch {
			case se.Unregistered:
				got = "gone"
			case se.Permanent:
				got = "permanent"
			}
			if got != tt.kind || se.RetryAfter != tt.retryAfter {
				t.Errorf("%s retryAfter=%v; want %s %v (err: %v)", got, se.RetryAfter, tt.kind, tt.retryAfter, err)
			}
		})
	}
}

func TestAFailedSignInIsATransientProblemWithNoSendAttempted(t *testing.T) {
	g := newGoogle(t)
	s := g.sender(t)
	g.tokenStatus = 400
	g.tokenBody = `{"error":"invalid_grant","error_description":"Invalid JWT: Token must be a short-lived token"}`
	err := s.Send(context.Background(), notification(fcmDevice, "{}", 3, time.Hour))
	var se *SendError
	if !errors.As(err, &se) || se.Permanent || se.Unregistered {
		t.Fatalf("err = %v, want transient", err)
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("the error should carry Google's explanation, such as a clock that is wrong: %v", err)
	}
	if _, sends := g.state(); sends != 0 {
		t.Errorf("%d sends without an access token", sends)
	}

	g.Close() // the network is down
	err = s.Send(context.Background(), notification(fcmDevice, "{}", 3, time.Hour))
	if !errors.As(err, &se) || se.Permanent {
		t.Errorf("an unreachable Google: %v", err)
	}
}

func TestFCMCredentialsAreChecked(t *testing.T) {
	g := newGoogle(t)
	good := map[string]string{
		"type": "service_account", "project_id": "p", "private_key": pemPKCS8(t, testRSAKey(t)),
		"client_email": "x@p.iam.gserviceaccount.com", "token_uri": g.URL + "/token",
	}
	with := func(edit func(map[string]string)) []byte {
		m := map[string]string{}
		for k, v := range good {
			m[k] = v
		}
		edit(m)
		b, _ := json.Marshal(m)
		return b
	}
	ec, _ := NewVAPIDKey()
	tests := map[string]struct {
		creds   []byte
		mention string
	}{
		"not JSON":               {[]byte("hello"), "JSON"},
		"a different key type":   {with(func(m map[string]string) { m["type"] = "authorized_user" }), "service_account"},
		"no project":             {with(func(m map[string]string) { delete(m, "project_id") }), "project_id"},
		"no email":               {with(func(m map[string]string) { delete(m, "client_email") }), "client_email"},
		"no key":                 {with(func(m map[string]string) { delete(m, "private_key") }), "private_key"},
		"a key that is not PEM":  {with(func(m map[string]string) { m["private_key"] = "abc" }), "PEM"},
		"an EC key":              {with(func(m map[string]string) { m["private_key"] = pemPKCS8(t, ec) }), "RSA"},
		"a plain http token_uri": {with(func(m map[string]string) { m["token_uri"] = "http://evil.example/token" }), "https"},
	}
	for name, tt := range tests {
		if _, err := NewFCM(tt.creds, g.Client()); err == nil || !strings.Contains(err.Error(), tt.mention) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tt.mention)
		}
	}
	if _, err := NewFCM(with(func(map[string]string) {}), g.Client()); err != nil {
		t.Errorf("good credentials: %v", err)
	}
	// A key file without token_uri uses Google's.
	s, err := NewFCM(with(func(m map[string]string) { delete(m, "token_uri") }), g.Client())
	if err != nil || s.tokenURI != "https://oauth2.googleapis.com/token" {
		t.Errorf("default token_uri: %v, %v", s, err)
	}
	if s.sendURL != "https://fcm.googleapis.com/v1/projects/p/messages:send" {
		t.Errorf("sendURL = %q", s.sendURL)
	}
}

func TestNoSecretAppearsInAnFCMError(t *testing.T) {
	g := newGoogle(t)
	s := g.sender(t)
	g.sendStatus, g.sendBody = []int{500}, `{"error":{"status":"INTERNAL"}}`
	err := s.Send(context.Background(), notification(fcmDevice, "{}", 3, time.Hour))
	for _, secret := range []string{fcmDevice.Target, "access-token", "PRIVATE KEY", url.QueryEscape(fcmDevice.Target)} {
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("the error contains %q: %v", secret, err)
		}
	}
}
