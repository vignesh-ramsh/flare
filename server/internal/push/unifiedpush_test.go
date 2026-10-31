package push

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUnifiedPushPostsThePayloadAsItIs(t *testing.T) {
	ps := newPushServer(t)
	s := NewUnifiedPush(EndpointClient(true), true)
	d := Device{ID: "d1", Provider: UnifiedPush, Target: ps.URL + "/up/abc"}

	payload := `{"event":"message","sub":"s","message":"hello"}`
	if err := s.Send(context.Background(), notification(d, payload, 4, 30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	req, body := ps.request()
	if req.Method != "POST" || req.URL.Path != "/up/abc" || string(body) != payload {
		t.Errorf("%s %s %q", req.Method, req.URL.Path, body)
	}
	if req.Header.Get("Content-Type") != "application/json" || req.Header.Get("TTL") != "1800" || req.Header.Get("Urgency") != "high" {
		t.Errorf("headers = %v", req.Header)
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("credentials were sent to the distributor")
	}
}

func TestUnifiedPushSortsOutTheAnswers(t *testing.T) {
	ps := newPushServer(t)
	s := NewUnifiedPush(EndpointClient(true), true)
	d := Device{ID: "d1", Provider: UnifiedPush, Target: ps.URL + "/up/abc"}
	for status, kind := range map[int]string{200: "ok", 201: "ok", 404: "gone", 410: "gone", 400: "permanent", 413: "permanent", 429: "transient", 502: "transient"} {
		ps.reply(status, nil)
		err := s.Send(context.Background(), notification(d, "x", 3, time.Hour))
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
		if got != kind {
			t.Errorf("%d: %s, want %s", status, got, kind)
		}
	}
}

func TestUnifiedPushWillNotReachIntoTheNetwork(t *testing.T) {
	ps := newPushServer(t) // on 127.0.0.1
	s := NewUnifiedPush(EndpointClient(false), false)
	d := Device{ID: "d1", Provider: UnifiedPush, Target: ps.URL + "/up/abc"}
	err := s.Send(context.Background(), notification(d, "x", 3, time.Hour))
	var se *SendError
	if !errors.As(err, &se) || !se.Permanent {
		t.Errorf("err = %v, want a permanent refusal", err)
	}
	if req, _ := ps.request(); req != nil {
		t.Error("the request was made")
	}
}
