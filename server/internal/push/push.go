// Package push wakes devices that are not connected: when a message is
// published, the topic's push subscribers are sent a notification through
// whichever service reaches them (FCM, APNs, Web Push, UnifiedPush, or a relay).
//
// Sending is a queue. A job is stored in the same transaction as the message, so
// a stored message is never left without its notifications, and workers send
// them afterwards, retrying what failed for as long as the message lives.
package push

import (
	"context"
	"errors"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/message"
)

// Provider is the service that delivers a notification to a device.
type Provider string

const (
	FCM         Provider = "fcm"         // Firebase Cloud Messaging, for Android
	APNs        Provider = "apns"        // Apple Push Notification service, for iOS and macOS
	WebPush     Provider = "webpush"     // browsers, and UnifiedPush distributors that speak it
	UnifiedPush Provider = "unifiedpush" // a UnifiedPush distributor's endpoint
	Relay       Provider = "relay"       // another Flare server, run by the app's publisher
)

// Providers lists every provider.
var Providers = []Provider{FCM, APNs, WebPush, UnifiedPush, Relay}

// Valid reports whether p is one of the providers.
func (p Provider) Valid() bool {
	for _, q := range Providers {
		if p == q {
			return true
		}
	}
	return false
}

// Platforms are the systems an app can run on.
var Platforms = []string{"android", "ios", "macos", "windows", "linux", "web"}

// Keys are what Web Push needs to encrypt a notification for one browser.
type Keys struct {
	P256DH string // the browser's public key, base64url
	Auth   string // the shared authentication secret, base64url
}

// Device is one app installation that can receive notifications.
type Device struct {
	ID string
	// SecretHash is the SHA-256 of the secret the app holds. The app proves it is
	// the device by presenting the secret; see HashSecret.
	SecretHash string
	// UserID is the owner, or "" for an anonymous visitor. The device may be
	// notified of what its owner may read.
	UserID     string
	AppID      string
	Platform   string
	Provider   Provider
	Target     string // token, endpoint or relay handle
	Keys       Keys
	RelayURL   string
	AppVersion string
	CreatedAt  time.Time
	SeenAt     time.Time
}

// Subscription says that a device is to be woken for a topic.
type Subscription struct {
	ID       string // opaque; sent with every notification
	DeviceID string
	Topic    string
}

// Job is a notification waiting to be sent.
type Job struct {
	ID             int64
	SubscriptionID string
	Attempts       int
	ExpiresAt      time.Time
	Device         Device
	Topic          string
	Message        message.Message
}

var (
	// ErrNotFound is returned by Store when a device or subscription isn't there.
	ErrNotFound = errors.New("not found")
	// ErrTooManyDevices is returned when the owner already has as many devices as
	// are allowed.
	ErrTooManyDevices = errors.New("too many devices")
)

// Store is where devices, their subscriptions and the queue are kept. SQLite
// implements it for a single server.
type Store interface {
	// RegisterDevice saves d. Any device with the same provider, relay and target
	// is removed first, with its subscriptions and queued jobs, because a target
	// reaches one device. It returns ErrTooManyDevices if d's owner has max
	// others already.
	RegisterDevice(ctx context.Context, d Device, max int) error
	// Device returns the device, or ErrNotFound.
	Device(ctx context.Context, id string) (Device, error)
	// DeleteDevice removes a device with its subscriptions and queued jobs, or
	// returns ErrNotFound.
	DeleteDevice(ctx context.Context, id string) error
	// Touch records that the device was seen at the given time.
	Touch(ctx context.Context, id string, at time.Time) error

	// SetSubscriptions makes topics the device's complete list, keeping the IDs of
	// the ones it already had, and returns the list in the order given.
	SetSubscriptions(ctx context.Context, deviceID string, topics []string, now time.Time) ([]Subscription, error)
	// DeleteSubscription removes one subscription and its queued jobs.
	DeleteSubscription(ctx context.Context, id string) error

	// ClaimJobs returns up to limit jobs that are due at now, and hides them from
	// other callers until now+lease, so that a worker that dies only delays them.
	ClaimJobs(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]Job, error)
	// CompleteJob removes a job that has been sent, or given up on.
	CompleteJob(ctx context.Context, id int64) error
	// RetryJob records a failed attempt and says when to try again.
	RetryJob(ctx context.Context, id int64, attempts int, next time.Time) error
	// DeleteExpiredJobs removes the jobs whose message has expired.
	DeleteExpiredJobs(ctx context.Context, now time.Time) (int64, error)

	// Setting returns a stored value, or "" and false.
	Setting(ctx context.Context, key string) (string, bool, error)
	// SetSettingIfAbsent stores value unless the key has one, and returns the
	// value the key has afterwards.
	SetSettingIfAbsent(ctx context.Context, key, value string) (string, error)
}
