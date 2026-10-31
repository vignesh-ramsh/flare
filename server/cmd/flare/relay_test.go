package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFCMKey writes a service account key file like the one Firebase gives out.
func writeFCMKey(t *testing.T, dir string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	creds, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "test-project", "client_email": "flare@test-project.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":   "https://oauth2.googleapis.com/token",
	})
	path := filepath.Join(dir, "fcm.json")
	if err := os.WriteFile(path, creds, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func relayConfig(t *testing.T, listen string) (configPath string) {
	t.Helper()
	dir := t.TempDir()
	fcm := writeFCMKey(t, dir)
	yml := "listen: " + listen + "\nrelay:\n  database: sqlite://" + filepath.Join(dir, "relay.db") + "\npush:\n  apps:\n    - app-id: org.example.flare\n      fcm-credentials: " + fcm + "\n"
	configPath = filepath.Join(dir, "relay.yml")
	if err := os.WriteFile(configPath, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func TestTheRelayRunsAndStopsCleanly(t *testing.T) {
	addr := freePort(t)
	cfg := relayConfig(t, addr)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(t, ctx, "", nil, "relay", "--config", cfg, "--log-level", "error").err }()
	t.Cleanup(cancel)
	waitReachable(t, addr)

	resp, err := http.Get("http://" + addr + "/v1/config")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(data), `"push-relay"`) || !strings.Contains(string(data), "org.example.flare") {
		t.Errorf("config = %s", data)
	}

	// An app registers a token with the relay.
	resp, err = http.Post("http://"+addr+"/v1/handles", "application/json",
		strings.NewReader(`{"app_id":"org.example.flare","platform":"android","provider":"fcm","token":"fcm-registration-token-0123456789abcdef"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Errorf("registering a handle: %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the relay returned %v after a clean shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the relay did not stop")
	}
}

func TestTheRelayRefusesToStartWithoutWhatItNeeds(t *testing.T) {
	dir := t.TempDir()
	write := func(content string) string {
		p := filepath.Join(dir, "r.yml")
		os.WriteFile(p, []byte(content), 0o600)
		return p
	}
	db := "relay:\n  database: sqlite://" + filepath.Join(dir, "r.db") + "\n"
	bad := filepath.Join(dir, "not-a-key.json")
	os.WriteFile(bad, []byte(`{"type":"authorized_user"}`), 0o600)

	tests := map[string]struct {
		config  string
		mention string
	}{
		"no apps":                         {"listen: 127.0.0.1:0\n" + db, "push.apps"},
		"a credential file that is wrong": {"listen: 127.0.0.1:0\n" + db + "push:\n  apps:\n    - app-id: org.example.flare\n      fcm-credentials: " + bad + "\n", "not-a-key.json"},
		"a missing file":                  {"listen: 127.0.0.1:0\n" + db + "push:\n  apps:\n    - app-id: org.example.flare\n      fcm-credentials: " + filepath.Join(dir, "gone.json") + "\n", "gone.json"},
		"a bad relay database":            {"listen: 127.0.0.1:0\nrelay:\n  database: postgres://x/y\n", "relay.database"},
		"a limit of zero":                 {"listen: 127.0.0.1:0\nrelay:\n  wakes-per-ip-per-minute: 0\n", "wakes-per-ip-per-minute"},
		"a misspelling":                   {"relay:\n  wakes-per-handle: 5\n", "not a known setting"},
	}
	for name, tt := range tests {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		r := run(t, ctx, "", nil, "relay", "--config", write(tt.config))
		cancel()
		if r.err == nil || !strings.Contains(r.err.Error(), tt.mention) {
			t.Errorf("%s: err = %v, want it to mention %q", name, r.err, tt.mention)
		}
	}
}
