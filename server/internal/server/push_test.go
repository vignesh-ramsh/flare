package server

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/config"
)

// distributor stands in for a UnifiedPush distributor on the same network.
type distributor struct {
	*httptest.Server
	mu   sync.Mutex
	got  []string
	hdrs []http.Header
}

func newDistributor(t *testing.T) *distributor {
	d := &distributor{}
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.got = append(d.got, string(body))
		d.hdrs = append(d.hdrs, r.Header.Clone())
		d.mu.Unlock()
		w.WriteHeader(201)
	}))
	t.Cleanup(d.Close)
	return d
}

func (d *distributor) received() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.got...)
}

func (d *distributor) waitFor(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got := d.received(); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d pushes; got %d", n, len(d.received()))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func pushConfig(t *testing.T) config.Config {
	cfg := testConfig(t)
	cfg.Push.AllowPrivateEndpoints = true // the distributor is on 127.0.0.1
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func call(t *testing.T, method, url, body string, headers ...string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

// enrol registers a UnifiedPush device and has it follow the topics.
func enrol(t *testing.T, serverURL, endpoint string, topics ...string) (id, secret string, subs map[string]string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"app_id": "org.example.flare", "platform": "android", "provider": "unifiedpush", "endpoint": endpoint})
	resp, data := call(t, "POST", serverURL+"/v1/devices", string(body))
	if resp.StatusCode != 201 {
		t.Fatalf("register: %d %s", resp.StatusCode, data)
	}
	var reg struct{ ID, Secret string }
	json.Unmarshal(data, &reg)

	list, _ := json.Marshal(map[string]any{"topics": topics})
	resp, data = call(t, "PUT", serverURL+"/v1/devices/"+reg.ID+"/subscriptions", string(list), "Device-Secret", reg.Secret)
	if resp.StatusCode != 200 {
		t.Fatalf("follow: %d %s", resp.StatusCode, data)
	}
	var out struct{ Subscriptions []struct{ Topic, ID string } }
	json.Unmarshal(data, &out)
	subs = map[string]string{}
	for _, s := range out.Subscriptions {
		subs[s.Topic] = s.ID
	}
	return reg.ID, reg.Secret, subs
}

func TestAPublishedMessageReachesADeviceThroughItsPushService(t *testing.T) {
	d := newDistributor(t)
	url, _, stop := start(t, pushConfig(t))
	defer stop()
	_, _, subs := enrol(t, url, d.URL+"/up/abc", "alerts")

	begin := time.Now()
	resp, data := call(t, "POST", url+"/alerts", "disk full", "Title", "Alert", "Priority", "5")
	if resp.StatusCode != 200 {
		t.Fatalf("publish: %d %s", resp.StatusCode, data)
	}
	var published struct{ ID string }
	json.Unmarshal(data, &published)

	got := d.waitFor(t, 1)
	// Publishing wakes the dispatcher, so the push does not wait for it to look at the
	// queue (it does so every 5 seconds).
	if took := time.Since(begin); took > 2*time.Second {
		t.Errorf("the push took %v: publishing should wake the dispatcher at once", took)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(got[0]), &payload); err != nil {
		t.Fatalf("the push is not JSON: %q", got[0])
	}
	if payload["event"] != "message" || payload["id"] != published.ID || payload["sub"] != subs["alerts"] ||
		payload["topic"] != "alerts" || payload["message"] != "disk full" || payload["title"] != "Alert" || payload["priority"] != float64(5) {
		t.Errorf("payload = %v", payload)
	}
	d.mu.Lock()
	ttl := d.hdrs[0].Get("TTL")
	d.mu.Unlock()
	if ttl == "" || ttl == "0" {
		t.Errorf("TTL = %q: the push service needs to know how long to try", ttl)
	}
}

func TestOnlyTheDevicesOfATopicAreWoken(t *testing.T) {
	a, b := newDistributor(t), newDistributor(t)
	url, _, stop := start(t, pushConfig(t))
	defer stop()
	enrol(t, url, a.URL+"/a", "alerts")
	enrol(t, url, b.URL+"/b", "other")

	call(t, "POST", url+"/alerts", "x")
	a.waitFor(t, 1)
	time.Sleep(150 * time.Millisecond)
	if n := len(b.received()); n != 0 {
		t.Errorf("a device following another topic was sent %d pushes", n)
	}
}

// The whole of the wake-up flow: a server that keeps content out of push services
// tells the device only that a message is waiting, and the device fetches it.
func TestAContentFreeServerWakesTheDeviceWhichFetchesTheMessage(t *testing.T) {
	d := newDistributor(t)
	cfg := pushConfig(t)
	cfg.Push.ContentFree = true
	url, _, stop := start(t, cfg)
	defer stop()
	_, _, subs := enrol(t, url, d.URL+"/up/abc", "payroll")

	_, data := call(t, "POST", url+"/payroll", "salaries attached", "Title", "Confidential")
	var published struct{ ID string }
	json.Unmarshal(data, &published)

	got := d.waitFor(t, 1)[0]
	if strings.Contains(got, "salaries") || strings.Contains(got, "Confidential") || strings.Contains(got, "payroll") {
		t.Fatalf("the push service was shown content or the topic: %s", got)
	}
	var wake struct{ Event, Sub, ID string }
	json.Unmarshal([]byte(got), &wake)
	if wake.Event != "poll_request" || wake.Sub != subs["payroll"] || wake.ID != published.ID {
		t.Fatalf("wake = %+v", wake)
	}

	resp, body := call(t, "GET", url+"/v1/messages/"+wake.ID, "")
	var m struct{ Message, Title, Topic string }
	json.Unmarshal(body, &m)
	if resp.StatusCode != 200 || m.Message != "salaries attached" || m.Title != "Confidential" || m.Topic != "payroll" {
		t.Errorf("fetching the message: %d %s", resp.StatusCode, body)
	}
}

func TestPushSurvivesARestartOfTheServer(t *testing.T) {
	d := newDistributor(t)
	cfg := pushConfig(t)
	url, _, stop := start(t, cfg)
	enrol(t, url, d.URL+"/up/abc", "alerts")
	call(t, "POST", url+"/alerts", "first", "Cache", "")
	d.waitFor(t, 1)
	stop()

	// The same database, a new process: the device is still registered and followed.
	url2, _, stop2 := start(t, cfg)
	defer stop2()
	call(t, "POST", url2+"/alerts", "second")
	got := d.waitFor(t, 2)
	if !strings.Contains(got[1], "second") {
		t.Errorf("got %q", got[1])
	}
}

func TestShutdownStopsThePushWorkersAndReturnsPromptly(t *testing.T) {
	// A distributor that never answers: a send is in flight when the server stops.
	hang, reached := make(chan struct{}), make(chan struct{}, 1)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reached <- struct{}{}:
		default:
		}
		<-hang
	}))
	defer slow.Close()
	defer close(hang)
	url, _, stop := start(t, pushConfig(t))
	enrol(t, url, slow.URL+"/up/abc", "alerts")
	call(t, "POST", url+"/alerts", "x")
	select {
	case <-reached: // the push is on its way, and will not come back
	case <-time.After(10 * time.Second):
		t.Fatal("the push never reached the distributor")
	}

	begin := time.Now()
	if err := stop(); err != nil {
		t.Errorf("Serve returned %v", err)
	}
	if took := time.Since(begin); took > 5*time.Second {
		t.Errorf("shutdown took %v with a push in flight", took)
	}
}

func TestPushCanBeSwitchedOff(t *testing.T) {
	cfg := pushConfig(t)
	cfg.Push.Enabled = false
	url, _, stop := start(t, cfg)
	defer stop()
	resp, data := call(t, "POST", url+"/v1/devices", `{"app_id":"a","platform":"android","provider":"unifiedpush","endpoint":"https://up.example.com/x"}`)
	if resp.StatusCode != 404 || !strings.Contains(string(data), "does not send push") {
		t.Errorf("%d %s", resp.StatusCode, data)
	}
	_, body := call(t, "GET", url+"/v1/config", "")
	if strings.Contains(string(body), `"push"`) {
		t.Errorf("a server without push announces it: %s", body)
	}
}

func TestTheConfigurationDecidesWhichServicesAreOffered(t *testing.T) {
	cfg := pushConfig(t)
	cfg.Push.UnifiedPush = false
	url, _, stop := start(t, cfg)
	defer stop()

	_, body := call(t, "GET", url+"/v1/config", "")
	var c struct {
		Features []string
		Push     struct {
			Providers  []string `json:"providers"`
			WebPushKey string   `json:"web_push_public_key"`
		}
	}
	json.Unmarshal(body, &c)
	if strings.Join(c.Push.Providers, ",") != "webpush" || c.Push.WebPushKey == "" {
		t.Errorf("push = %+v", c.Push)
	}
	resp, data := call(t, "POST", url+"/v1/devices", `{"app_id":"a","platform":"android","provider":"unifiedpush","endpoint":"https://up.example.com/x"}`)
	if resp.StatusCode != 400 || !strings.Contains(string(data), "not enabled") {
		t.Errorf("a service that is off: %d %s", resp.StatusCode, data)
	}
}

func TestTheWebPushKeyIsTheSameAfterARestart(t *testing.T) {
	cfg := pushConfig(t)
	key := func() string {
		url, _, stop := start(t, cfg)
		defer stop()
		_, body := call(t, "GET", url+"/v1/config", "")
		var c struct {
			Push struct {
				Key string `json:"web_push_public_key"`
			}
		}
		json.Unmarshal(body, &c)
		return c.Push.Key
	}
	first := key()
	if first == "" || key() != first {
		t.Error("browsers' subscriptions are bound to this key; it must survive a restart")
	}
}

func TestACredentialThatCannotBeUsedStopsTheServerAndNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "fcm.json")
	os.WriteFile(bad, []byte(`{"type":"authorized_user"}`), 0o600)
	missing := filepath.Join(dir, "gone.json")
	notPEM := filepath.Join(dir, "key.p8")
	os.WriteFile(notPEM, []byte("hello"), 0o600)

	for name, app := range map[string]config.PushApp{
		"a key that is not a service account": {AppID: "org.example.flare", FCMCredentials: bad},
		"a file that does not exist":          {AppID: "org.example.flare", FCMCredentials: missing},
		"an APNs key that is not PEM":         {AppID: "org.example.flare", APNsKey: notPEM, APNsKeyID: "K", APNsTeamID: "T"},
		"an APNs key that does not exist":     {AppID: "org.example.flare", APNsKey: missing, APNsKeyID: "K", APNsTeamID: "T"},
	} {
		cfg := pushConfig(t)
		cfg.Push.Apps = []config.PushApp{app}
		_, err := New(context.Background(), cfg, slog_discard())
		path := app.FCMCredentials
		if path == "" {
			path = app.APNsKey
		}
		if err == nil || !strings.Contains(err.Error(), "org.example.flare") || !strings.Contains(err.Error(), filepath.Base(path)) {
			t.Errorf("%s: err = %v, want it to name the app and the file", name, err)
		}
	}
}

func TestConfiguredAppsAreAnnouncedAndTheirCredentialsAreNot(t *testing.T) {
	dir := t.TempDir()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p8 := filepath.Join(dir, "AuthKey_ABC123.p8")
	os.WriteFile(p8, []byte(pemEC(t, key)), 0o600)
	cfg := pushConfig(t)
	cfg.Push.Apps = []config.PushApp{{AppID: "org.example.flare", APNsKey: p8, APNsKeyID: "ABC123", APNsTeamID: "TEAM123"}}

	url, _, stop := start(t, cfg)
	defer stop()
	_, body := call(t, "GET", url+"/v1/config", "")
	var c struct {
		Push struct{ Apps map[string][]string }
	}
	json.Unmarshal(body, &c)
	if strings.Join(c.Push.Apps["apns"], ",") != "org.example.flare" {
		t.Errorf("apps = %v", c.Push.Apps)
	}
	for _, secret := range []string{"ABC123", "TEAM123", "AuthKey", "PRIVATE"} {
		if strings.Contains(string(body), secret) {
			t.Errorf("the public config mentions %q: %s", secret, body)
		}
	}
	// And a device for that app can register for APNs; one for another app cannot.
	reg := func(app string) int {
		resp, _ := call(t, "POST", url+"/v1/devices", `{"app_id":"`+app+`","platform":"ios","provider":"apns","token":"`+strings.Repeat("ab", 32)+`"}`)
		return resp.StatusCode
	}
	if got := reg("org.example.flare"); got != 201 {
		t.Errorf("the configured app: %d", got)
	}
	if got := reg("com.other.app"); got != 400 {
		t.Errorf("another app: %d", got)
	}
}

// A browser's side of Web Push: it holds the private key, and the push service it
// subscribed through is a server of its own that checks the sender's signature.
func TestWebPushThroughTheWholeStack(t *testing.T) {
	browserKey, _ := ecdh.P256().GenerateKey(rand.Reader)
	authSecret := make([]byte, 16)
	rand.Read(authSecret)
	enc := base64.RawURLEncoding.EncodeToString

	type arrival struct {
		auth string
		body []byte
		ttl  string
	}
	arrived := make(chan arrival, 4)
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		arrived <- arrival{r.Header.Get("Authorization"), body, r.Header.Get("TTL")}
		w.WriteHeader(201)
	}))
	defer vendor.Close()

	cfg := pushConfig(t)
	cfg.BaseURL = "https://flare.example.com"
	url, _, stop := start(t, cfg)
	defer stop()

	// The key the server announces is the one a browser subscribes with.
	_, body := call(t, "GET", url+"/v1/config", "")
	var c struct {
		Push struct {
			Key string `json:"web_push_public_key"`
		}
	}
	json.Unmarshal(body, &c)
	announced, err := base64.RawURLEncoding.DecodeString(c.Push.Key)
	if err != nil || len(announced) != 65 {
		t.Fatalf("the announced key %q is not an uncompressed P-256 point", c.Push.Key)
	}

	reg, _ := json.Marshal(map[string]any{
		"app_id": "web", "platform": "web", "provider": "webpush", "endpoint": vendor.URL + "/wp/abc",
		"keys": map[string]string{"p256dh": enc(browserKey.PublicKey().Bytes()), "auth": enc(authSecret)},
	})
	resp, data := call(t, "POST", url+"/v1/devices", string(reg))
	if resp.StatusCode != 201 {
		t.Fatalf("register: %d %s", resp.StatusCode, data)
	}
	var dev struct{ ID, Secret string }
	json.Unmarshal(data, &dev)
	call(t, "PUT", url+"/v1/devices/"+dev.ID+"/subscriptions", `{"topics":["news"]}`, "Device-Secret", dev.Secret)

	call(t, "POST", url+"/news", "breaking: it works", "Title", "News")
	var got arrival
	select {
	case got = <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("nothing reached the push service")
	}

	// The signature is checked as the push service would: with the announced key.
	jwt := strings.TrimPrefix(strings.SplitN(got.auth, ",", 2)[0], "vapid t=")
	x, y := elliptic.Unmarshal(elliptic.P256(), announced)
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 || !strings.HasSuffix(got.auth, "k="+c.Push.Key) {
		t.Fatalf("Authorization = %q", got.auth)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Error("the VAPID signature does not verify against the key in /v1/config")
	}
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if !strings.Contains(string(claims), `"aud":"`+vendor.URL+`"`) || !strings.Contains(string(claims), `"sub":"https://flare.example.com"`) {
		t.Errorf("claims = %s: the audience is the push service, and the contact defaults to base-url", claims)
	}

	// And the browser can read it.
	plain := decryptWebPush(t, got.body, browserKey, authSecret)
	var payload map[string]any
	json.Unmarshal(plain, &payload)
	if payload["message"] != "breaking: it works" || payload["title"] != "News" || payload["topic"] != "news" || payload["event"] != "message" {
		t.Errorf("decrypted %s", plain)
	}
	if got.ttl == "" || got.ttl == "0" {
		t.Errorf("TTL = %q", got.ttl)
	}
}

// decryptWebPush is what a browser does with an aes128gcm body (RFC 8188, 8291).
func decryptWebPush(t *testing.T, body []byte, ua *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	if len(body) < 86 || body[20] != 65 {
		t.Fatalf("body of %d bytes has no header", len(body))
	}
	salt := body[:16]
	asPublic, err := ecdh.P256().NewPublicKey(body[21:86])
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := ua.ECDH(asPublic)
	prk, _ := hkdf.Extract(sha256.New, shared, authSecret)
	info := append(append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...), asPublic.Bytes()...)
	ikm, _ := hkdf.Expand(sha256.New, prk, string(info), 32)
	prk2, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk2, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk2, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[86:], nil)
	if err != nil {
		t.Fatalf("the browser cannot decrypt the notification: %v", err)
	}
	return plain[:len(plain)-1]
}
