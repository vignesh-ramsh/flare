package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/push"
	"github.com/vignesh-ramsh/flare/server/internal/relay"
)

// recordingSender stands in for FCM at the relay.
type recordingSender struct {
	mu   sync.Mutex
	got  []push.Notification
	fail error
}

func (s *recordingSender) MaxPayload() int { return 4000 }
func (s *recordingSender) Send(_ context.Context, n push.Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, n)
	return s.fail
}
func (s *recordingSender) notifications() []push.Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]push.Notification(nil), s.got...)
}

func startRelay(t *testing.T, fcm push.Sender) (*httptest.Server, *relay.Store) {
	t.Helper()
	st, err := relay.OpenStore(context.Background(), filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	senders := push.NewRegistry()
	senders.AddApp(push.FCM, "org.example.flare", fcm)
	srv := httptest.NewServer(relay.New(relay.Config{Senders: senders, Store: st}).Handler())
	t.Cleanup(srv.Close)
	return srv, st
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The whole of section 5.3 of the architecture: an app that a self-hosted server
// has no credentials for is woken through its publisher's relay.
func TestADeviceIsWokenThroughARelayWithoutTheRelayLearningAnything(t *testing.T) {
	fcm := &recordingSender{}
	rel, _ := startRelay(t, fcm)
	cfg := pushConfig(t)
	cfg.Push.AllowedRelays = []string{rel.URL}
	cfg.Push.ContentFree = false // even so: nothing goes through a relay but a poll request
	url, _, stop := start(t, cfg)
	defer stop()

	// 1. The app gives the relay its push token, and gets a handle.
	const token = "fcm-registration-token-0123456789abcdef"
	resp, data := call(t, "POST", rel.URL+"/v1/handles",
		`{"app_id":"org.example.flare","platform":"android","provider":"fcm","token":"`+token+`"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("relay registration: %d %s", resp.StatusCode, data)
	}
	var h struct{ Handle, Secret string }
	json.Unmarshal(data, &h)

	// 2. It gives the server the handle, not the token.
	body, _ := json.Marshal(map[string]string{"app_id": "org.example.flare", "platform": "android", "provider": "relay", "relay": rel.URL + "/", "handle": h.Handle})
	resp, data = call(t, "POST", url+"/v1/devices", string(body))
	if resp.StatusCode != 201 {
		t.Fatalf("device registration: %d %s", resp.StatusCode, data)
	}
	var dev struct{ ID, Secret string }
	json.Unmarshal(data, &dev)
	resp, data = call(t, "PUT", url+"/v1/devices/"+dev.ID+"/subscriptions", `{"topics":["payroll"]}`, "Device-Secret", dev.Secret)
	var subs struct{ Subscriptions []struct{ Topic, ID string } }
	json.Unmarshal(data, &subs)
	if resp.StatusCode != 200 || len(subs.Subscriptions) != 1 {
		t.Fatalf("follow: %d %s", resp.StatusCode, data)
	}
	sub := subs.Subscriptions[0].ID

	// 3. Something is published.
	_, data = call(t, "POST", url+"/payroll", "salaries attached", "Title", "Confidential", "Priority", "5")
	var published struct{ ID string }
	json.Unmarshal(data, &published)

	// 4. The relay is asked to wake the device, and passes it to the push service.
	waitUntil(t, "the relay to be asked", func() bool { return len(fcm.notifications()) == 1 })
	n := fcm.notifications()[0]
	if n.Device.Target != token || n.Device.AppID != "org.example.flare" || n.SubscriptionID != sub || n.Message.ID != published.ID {
		t.Errorf("the relay sent %+v", n)
	}
	if !n.WakeUp || n.Message.Priority != 5 {
		t.Errorf("wake-up=%v priority=%d, want a poll request as urgent as the message", n.WakeUp, n.Message.Priority)
	}
	var wake map[string]string
	json.Unmarshal(n.Payload, &wake)
	if len(wake) != 3 || wake["event"] != "poll_request" || wake["sub"] != sub || wake["id"] != published.ID {
		t.Errorf("the notification = %s", n.Payload)
	}
	seen := string(n.Payload) + n.Message.Topic + n.Message.Title + n.Message.Message + strings.Join(n.Message.Tags, "")
	for _, leak := range []string{"payroll", "Confidential", "salaries"} {
		if strings.Contains(seen, leak) {
			t.Errorf("the relay learned %q", leak)
		}
	}

	// 5. The app fetches the message from its server, and knows the topic by sub.
	resp, data = call(t, "GET", url+"/v1/messages/"+wake["id"], "")
	var m struct{ Topic, Title, Message string }
	json.Unmarshal(data, &m)
	if resp.StatusCode != 200 || m.Topic != "payroll" || m.Message != "salaries attached" {
		t.Errorf("fetching: %d %s", resp.StatusCode, data)
	}
}

func TestAServerOnlyUsesTheRelaysItIsToldTo(t *testing.T) {
	fcm := &recordingSender{}
	rel, _ := startRelay(t, fcm)
	url, _, stop := start(t, pushConfig(t)) // no allowed-relays
	defer stop()
	body, _ := json.Marshal(map[string]string{"app_id": "org.example.flare", "platform": "android", "provider": "relay", "relay": rel.URL, "handle": "relay-handle-0123456789abcdef"})
	resp, data := call(t, "POST", url+"/v1/devices", string(body))
	if resp.StatusCode != 400 || !strings.Contains(string(data), "that relay") {
		t.Errorf("%d %s", resp.StatusCode, data)
	}
	_, cfg := call(t, "GET", url+"/v1/config", "")
	if strings.Contains(string(cfg), `"relays"`) {
		t.Errorf("a server with no relays announces some: %s", cfg)
	}
}

func TestADeviceTheRelayCannotReachAnyMoreIsForgottenByTheServer(t *testing.T) {
	fcm := &recordingSender{fail: push.Gone(context.DeadlineExceeded)}
	rel, relayStore := startRelay(t, fcm)
	cfg := pushConfig(t)
	cfg.Push.AllowedRelays = []string{rel.URL}
	url, _, stop := start(t, cfg)
	defer stop()

	_, data := call(t, "POST", rel.URL+"/v1/handles", `{"app_id":"org.example.flare","platform":"android","provider":"fcm","token":"fcm-registration-token-0123456789abcdef"}`)
	var h struct{ Handle string }
	json.Unmarshal(data, &h)
	body, _ := json.Marshal(map[string]string{"app_id": "org.example.flare", "platform": "android", "provider": "relay", "relay": rel.URL, "handle": h.Handle})
	_, data = call(t, "POST", url+"/v1/devices", string(body))
	var dev struct{ ID, Secret string }
	json.Unmarshal(data, &dev)
	call(t, "PUT", url+"/v1/devices/"+dev.ID+"/subscriptions", `{"topics":["news"]}`, "Device-Secret", dev.Secret)

	call(t, "POST", url+"/news", "x")
	// The push service says the app is gone: the relay drops the handle and says so,
	// and the server drops the device.
	waitUntil(t, "the device to be forgotten", func() bool {
		resp, _ := call(t, "PUT", url+"/v1/devices/"+dev.ID+"/subscriptions", `{"topics":[]}`, "Device-Secret", dev.Secret)
		return resp.StatusCode == 404
	})
	if n, _ := relayStore.Count(context.Background()); n != 0 {
		t.Errorf("the relay still has %d handles", n)
	}
}
