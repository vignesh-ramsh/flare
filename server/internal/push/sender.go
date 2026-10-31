package push

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/message"
)

// EventPollRequest is the event of a notification that carries no message: the
// app fetches the message itself, with its own credentials.
const EventPollRequest = "poll_request"

// Notification is what one sender is asked to deliver to one device.
type Notification struct {
	Device         Device
	SubscriptionID string
	// Message is the message being announced. A sender that must not reveal its
	// content to a third party, or can't carry it, uses Payload instead of it.
	Message message.Message
	// Payload is the JSON the app receives: the message (see FullPayload), or a
	// poll request when WakeUp is set (see WakePayload).
	Payload []byte
	WakeUp  bool
	// Expires is when the notification stops being worth delivering. Senders pass
	// it to the service as a time to live.
	Expires time.Time
	// Now is the time the notification is being sent.
	Now time.Time
}

// TTL is how long the push service should keep trying to reach the device.
func (n Notification) TTL() time.Duration {
	if d := n.Expires.Sub(n.Now); d > 0 {
		return d
	}
	return 0
}

// A Sender delivers notifications through one push service.
type Sender interface {
	// MaxPayload is the most bytes of Payload the service carries. A larger
	// message is announced with a poll request instead.
	MaxPayload() int
	// Send delivers n. A failure is a *SendError, which says what to do about it;
	// any other error is treated as a transient one.
	Send(ctx context.Context, n Notification) error
}

// SendError is a failed delivery, with what the dispatcher should do about it.
type SendError struct {
	Err error
	// Unregistered means the push service says the device can't be reached any
	// more (the app was uninstalled, or the token is no good): forget the device.
	Unregistered bool
	// Permanent means that trying again won't help, so the notification is given up.
	Permanent bool
	// RetryAfter is the earliest the service wants to be asked again, if it said.
	RetryAfter time.Duration
}

func (e *SendError) Error() string { return e.Err.Error() }
func (e *SendError) Unwrap() error { return e.Err }

// Transient marks a failure worth retrying.
func Transient(err error, retryAfter time.Duration) error {
	return &SendError{Err: err, RetryAfter: retryAfter}
}

// Permanent marks a failure that retrying won't fix.
func Permanent(err error) error { return &SendError{Err: err, Permanent: true} }

// Gone marks a device that the push service no longer knows.
func Gone(err error) error { return &SendError{Err: err, Unregistered: true, Permanent: true} }

// classify reads err as a *SendError, treating anything else as transient.
func classify(err error) *SendError {
	var se *SendError
	if errors.As(err, &se) {
		return se
	}
	return &SendError{Err: err}
}

// fullPayload is a message as an app receives it in a notification: the same JSON
// as on a stream, with the ID of the subscription it is for.
type fullPayload struct {
	message.Message
	Sub string `json:"sub"`
}

// FullPayload is the notification that carries a whole message.
func FullPayload(m message.Message, subscriptionID string) []byte {
	m.Event = message.EventMessage
	b, _ := json.Marshal(fullPayload{Message: m, Sub: subscriptionID})
	return b
}

// WakePayload is the notification that only says a message is waiting. It holds
// no topic and no content, so a push service or relay learns nothing from it.
func WakePayload(subscriptionID, messageID string) []byte {
	b, _ := json.Marshal(struct {
		Event string `json:"event"`
		Sub   string `json:"sub"`
		ID    string `json:"id"`
	}{EventPollRequest, subscriptionID, messageID})
	return b
}

// PayloadFor builds what the sender is to deliver: the whole message if that is
// allowed and fits, otherwise a poll request.
func PayloadFor(s Sender, m message.Message, subscriptionID string, contentFree bool) (payload []byte, wakeUp bool) {
	if !contentFree {
		if full := FullPayload(m, subscriptionID); len(full) <= s.MaxPayload() {
			return full, false
		}
	}
	return WakePayload(subscriptionID, m.ID), true
}
