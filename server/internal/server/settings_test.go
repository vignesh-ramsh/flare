package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vignesh-ramsh/flare/server/internal/auth"
)

// adminToken makes an administrator in the server's database and returns a token for them.
func adminToken(t *testing.T, srv *Server) string {
	t.Helper()
	svc := auth.NewService(srv.store, auth.ServiceConfig{HashParams: auth.Params{Time: 1, MemoryKiB: 64, Threads: 1}})
	u, err := svc.CreateUser(context.Background(), "root", "a long password for tests", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := svc.IssueToken(context.Background(), u.ID, "test", 0)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func apiCall(t *testing.T, method, url, token, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, string(data)
}

func change(t *testing.T, url, token, key, value string) {
	t.Helper()
	if resp, data := apiCall(t, "PUT", url+"/v1/admin/settings/"+key, token, `{"value":`+value+`}`); resp.StatusCode != 200 {
		t.Fatalf("PUT %s = %s: %d %s", key, value, resp.StatusCode, data)
	}
}

func TestSettingsChangedFromTheAdminAPITakeEffectOnARunningServer(t *testing.T) {
	url, srv, _ := start(t, testConfig(t))
	token := adminToken(t, srv)

	// The largest message: refused at once, by the hub and by the handler.
	change(t, url, token, "message_size_limit", "10")
	if resp, _ := apiCall(t, "POST", url+"/alerts", "", strings.Repeat("x", 50)); resp.StatusCode != 413 {
		t.Errorf("a message over the new limit: %d", resp.StatusCode)
	}
	resp, data := apiCall(t, "POST", url+"/v1/messages", "", `{"topic":"alerts","message":"`+strings.Repeat("y", 50)+`"}`)
	if resp.StatusCode != 413 {
		t.Errorf("a JSON message over the new limit: %d %s", resp.StatusCode, data)
	}
	if resp, _ := apiCall(t, "POST", url+"/alerts", "", "small"); resp.StatusCode != 200 {
		t.Errorf("a message within it: %d", resp.StatusCode)
	}

	// How long messages are kept: from the next one on.
	change(t, url, token, "cache_duration", "120")
	resp, data = apiCall(t, "POST", url+"/alerts", "", "brief")
	var m struct{ Time, Expires int64 }
	if err := json.Unmarshal([]byte(data), &m); err != nil || resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	if kept := m.Expires - m.Time; kept != 120 {
		t.Errorf("a message published after the change is kept %d seconds, not 120", kept)
	}

	// What anyone may do: an anonymous visitor is turned away straight away, and let in again.
	change(t, url, token, "auth.default_access", `"deny-all"`)
	if resp, _ := apiCall(t, "POST", url+"/alerts", "", "hello"); resp.StatusCode != 401 {
		t.Errorf("anonymous publish on a closed server: %d", resp.StatusCode)
	}
	if resp, _ := apiCall(t, "POST", url+"/alerts", token, "hello"); resp.StatusCode != 200 {
		t.Errorf("an administrator on a closed server: %d", resp.StatusCode)
	}
	if resp, _ := apiCall(t, "DELETE", url+"/v1/admin/settings/auth.default_access", token, ""); resp.StatusCode != 200 {
		t.Fatalf("reset: %d", resp.StatusCode)
	}
	if resp, _ := apiCall(t, "POST", url+"/alerts", "", "again"); resp.StatusCode != 200 {
		t.Errorf("anonymous publish after the reset: %d", resp.StatusCode)
	}

	// How the server presents itself.
	change(t, url, token, "branding.name", `"Acme Alerts"`)
	if _, data := apiCall(t, "GET", url+"/v1/config", "", ""); !strings.Contains(data, `"name":"Acme Alerts"`) {
		t.Errorf("/v1/config: %s", data)
	}
}

func TestPushSettingsWaitForARestartAndChoicesSurviveIt(t *testing.T) {
	cfg := testConfig(t)
	url, srv, stop := start(t, cfg)
	token := adminToken(t, srv)

	if _, data := apiCall(t, "GET", url+"/v1/config", "", ""); !strings.Contains(data, `"push"`) {
		t.Fatalf("push is not on to begin with: %s", data)
	}
	change(t, url, token, "push.enabled", "false")
	change(t, url, token, "branding.name", `"Restarted"`)

	// Still running with push, and saying a restart is needed.
	if _, data := apiCall(t, "GET", url+"/v1/config", "", ""); !strings.Contains(data, `"push"`) {
		t.Errorf("push was switched off without a restart: %s", data)
	}
	_, data := apiCall(t, "GET", url+"/v1/admin/settings", token, "")
	if !strings.Contains(data, `"key":"push.enabled"`) || !strings.Contains(data, `"pending_restart":true`) {
		t.Errorf("the settings do not say a restart is needed: %s", data)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	// Started again on the same database, with the same configuration file.
	url2, _, _ := start(t, cfg)
	_, data = apiCall(t, "GET", url2+"/v1/config", "", "")
	if strings.Contains(data, `"push"`) || strings.Contains(data, `"features":["publish","poll","sse","websocket","accounts","themes","push"]`) {
		t.Errorf("push is still on after the restart: %s", data)
	}
	if !strings.Contains(data, `"name":"Restarted"`) {
		t.Errorf("the name did not survive: %s", data)
	}
	_, data = apiCall(t, "GET", url2+"/v1/admin/settings", token, "")
	var list struct {
		Settings []struct {
			Key            string `json:"key"`
			Overridden     bool   `json:"overridden"`
			PendingRestart bool   `json:"pending_restart"`
		} `json:"settings"`
	}
	if err := json.Unmarshal([]byte(data), &list); err != nil {
		t.Fatal(err)
	}
	for _, s := range list.Settings {
		if s.PendingRestart {
			t.Errorf("%s is still waiting for a restart", s.Key)
		}
		if s.Key == "push.enabled" && !s.Overridden {
			t.Error("push.enabled lost its override")
		}
	}
}

func TestAChoiceThatNoLongerAppliesDoesNotStopTheServerStarting(t *testing.T) {
	cfg := testConfig(t)
	url, srv, stop := start(t, cfg)
	token := adminToken(t, srv)
	_ = token
	change(t, url, token, "message_size_limit", "2048")
	// Another version's value, or a hand edit, that is not valid.
	if err := srv.store.PutSetting(context.Background(), "admin.message_size_limit", "999999"); err != nil {
		t.Fatal(err)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	url2, _, _ := start(t, cfg)
	if _, data := apiCall(t, "GET", url2+"/v1/config", "", ""); !strings.Contains(data, `"message_size":4096`) {
		t.Errorf("the server did not fall back to the configuration: %s", data)
	}
}
