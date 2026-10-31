package push

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// UnifiedPushSender sends to a UnifiedPush distributor's endpoint (unifiedpush.org):
// an app on the phone that stays connected to a push server of the user's choosing
// and hands what it receives to the apps. The notification is posted to the
// endpoint as it is, so the distributor can read it. A distributor that gives the
// app encryption keys is registered as Web Push instead.
type UnifiedPushSender struct {
	client       *http.Client
	allowPrivate bool
}

// NewUnifiedPush returns a sender. client should be an EndpointClient.
func NewUnifiedPush(client *http.Client, allowPrivate bool) *UnifiedPushSender {
	return &UnifiedPushSender{client: client, allowPrivate: allowPrivate}
}

// MaxPayload implements Sender. 4 KiB is what the common distributors accept.
func (s *UnifiedPushSender) MaxPayload() int { return 4000 }

// Send implements Sender.
func (s *UnifiedPushSender) Send(ctx context.Context, n Notification) error {
	endpoint, err := CheckEndpoint(n.Device.Target, s.allowPrivate)
	if err != nil {
		return Permanent(fmt.Errorf("the endpoint is not allowed: %w", err))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(n.Payload))
	if err != nil {
		return Permanent(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("TTL", strconv.FormatInt(int64(min(n.TTL(), maxWebPushTTL)/time.Second), 10))
	req.Header.Set("Urgency", urgency(n.Message.Priority))
	return roundTrip(s.client, req)
}
