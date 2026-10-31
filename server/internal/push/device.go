package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/message"
)

// SecretPrefix starts every device secret, so that a leaked one is recognisable.
const SecretPrefix = "flrd_"

// NewSecret returns a new random device secret: the prefix and 32 random bytes in
// URL-safe base64. A device is given it once, when it registers; only its hash is
// stored.
func NewSecret() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return SecretPrefix + base64.RawURLEncoding.EncodeToString(b[:])
}

// HashSecret returns what is stored for a device secret.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func newDeviceID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// Limits on what a device may register.
const (
	MaxAppIDLength      = 128
	MaxTokenLength      = 4096
	MaxAppVersionLength = 64
)

var (
	appIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	apnsRE  = regexp.MustCompile(`^[0-9a-fA-F]{32,200}$`)
)

// looksLikeFCMToken accepts what an FCM registration token can be: 16 to 4096
// printable characters, no spaces. The format is Google's to change, so this
// only rules out what can't be a token.
func looksLikeFCMToken(s string) bool {
	if len(s) < 16 || len(s) > MaxTokenLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// ValidAppID reports whether s can be an app's identifier: an Android application
// ID or an Apple bundle ID.
func ValidAppID(s string) bool {
	return s != "" && len(s) <= MaxAppIDLength && appIDRE.MatchString(s)
}

// InputError is a registration the server refuses, with a reason that is safe to
// show to whoever sent it.
type InputError struct{ Field, Msg string }

func (e *InputError) Error() string { return e.Field + ": " + e.Msg }

func bad(field, format string, args ...any) error {
	return &InputError{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// Errors returned by Service.
var (
	// ErrUnsupported means the server can't push to this kind of device.
	ErrUnsupported = errors.New("the server cannot send push notifications to this device")
	// ErrBadSecret means the secret is not the device's.
	ErrBadSecret = errors.New("wrong device secret")
)

// ForbiddenError is a topic the device's owner may not read.
type ForbiddenError struct {
	Topic     string
	Anonymous bool
}

func (e *ForbiddenError) Error() string {
	if e.Anonymous {
		return fmt.Sprintf("this device was registered without logging in, so it may only follow topics that anyone may read; %q is not one", e.Topic)
	}
	return fmt.Sprintf("the device's owner may not read the topic %q", e.Topic)
}

// Registration is what an app tells the server about itself.
type Registration struct {
	AppID      string
	Platform   string
	Provider   Provider
	Token      string // FCM registration token, or APNs device token
	Endpoint   string // Web Push or UnifiedPush endpoint
	Keys       Keys   // Web Push
	AppVersion string
}

// Access says what a device's owner may read.
type Access interface {
	// MayRead reports whether the user, or an anonymous visitor for "", may read
	// the topic.
	MayRead(ctx context.Context, userID, topic string) (bool, error)
}

// ServiceConfig tunes a Service. Zero values get defaults.
type ServiceConfig struct {
	MaxDevicesPerUser   int // 20
	MaxAnonymousDevices int // 1000, for all anonymous visitors together
	MaxTopicsPerDevice  int // 100
	// AllowPrivateEndpoints lets Web Push and UnifiedPush endpoints be on private
	// networks and use plain http, for a distributor on the same network.
	AllowPrivateEndpoints bool
	Now                   func() time.Time
}

func (c *ServiceConfig) fill() {
	if c.MaxDevicesPerUser <= 0 {
		c.MaxDevicesPerUser = 20
	}
	if c.MaxAnonymousDevices <= 0 {
		c.MaxAnonymousDevices = 1000
	}
	if c.MaxTopicsPerDevice <= 0 {
		c.MaxTopicsPerDevice = 100
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// Service registers devices and manages what they follow.
type Service struct {
	store    Store
	access   Access
	registry *Registry
	cfg      ServiceConfig
}

// NewService returns a Service. Devices it registers must be reachable through
// registry.
func NewService(st Store, access Access, registry *Registry, cfg ServiceConfig) *Service {
	cfg.fill()
	return &Service{store: st, access: access, registry: registry, cfg: cfg}
}

// Registry returns the push services the server can use.
func (s *Service) Registry() *Registry { return s.registry }

// Register records a device for the user, or for an anonymous visitor (""), and
// returns it with its secret. The secret is not stored, so this is the only time
// it can be seen.
func (s *Service) Register(ctx context.Context, userID string, r Registration) (Device, string, error) {
	d, err := s.validate(r)
	if err != nil {
		return Device{}, "", err
	}
	if !s.registry.Supports(d) {
		return Device{}, "", fmt.Errorf("%w: %s", ErrUnsupported, s.explainUnsupported(d))
	}
	secret := NewSecret()
	now := s.cfg.Now()
	d.ID, d.SecretHash, d.UserID, d.CreatedAt, d.SeenAt = newDeviceID(), HashSecret(secret), userID, now, now
	max := s.cfg.MaxDevicesPerUser
	if userID == "" {
		max = s.cfg.MaxAnonymousDevices
	}
	if err := s.store.RegisterDevice(ctx, d, max); err != nil {
		return Device{}, "", err
	}
	return d, secret, nil
}

func (s *Service) explainUnsupported(d Device) string {
	switch d.Provider {
	case FCM, APNs:
		return fmt.Sprintf("it has no %s credentials for the app %q", strings.ToUpper(string(d.Provider)), d.AppID)
	case Relay:
		return "it does not use that relay"
	}
	return fmt.Sprintf("%s notifications are not enabled", d.Provider)
}

func (s *Service) validate(r Registration) (Device, error) {
	d := Device{AppID: r.AppID, Platform: r.Platform, Provider: r.Provider, AppVersion: r.AppVersion}
	if !ValidAppID(r.AppID) {
		return d, bad("app_id", "required: the app's identifier, such as org.example.flare (letters, digits, . _ -)")
	}
	if !contains(Platforms, r.Platform) {
		return d, bad("platform", "must be one of %s", strings.Join(Platforms, ", "))
	}
	if len(r.AppVersion) > MaxAppVersionLength {
		return d, bad("app_version", "must be at most %d characters", MaxAppVersionLength)
	}
	switch r.Provider {
	case FCM:
		if !looksLikeFCMToken(r.Token) {
			return d, bad("token", "must be the registration token Firebase gave the app")
		}
		d.Target = r.Token
		return d, noEndpoint(r)
	case APNs:
		if !apnsRE.MatchString(r.Token) {
			return d, bad("token", "must be the device token Apple gave the app, in hexadecimal")
		}
		d.Target = strings.ToLower(r.Token)
		return d, noEndpoint(r)
	case WebPush:
		if r.Token != "" {
			return d, bad("token", "is not used with webpush; give endpoint and keys")
		}
		if _, err := CheckEndpoint(r.Endpoint, s.cfg.AllowPrivateEndpoints); err != nil {
			return d, bad("endpoint", "%v", err)
		}
		keys, err := checkKeys(r.Keys)
		if err != nil {
			return d, err
		}
		d.Target, d.Keys = r.Endpoint, keys
		return d, nil
	case UnifiedPush:
		if r.Token != "" || r.Keys != (Keys{}) {
			return d, bad("provider", "unifiedpush takes only an endpoint; a distributor that gives keys should be registered as webpush, which encrypts the notification")
		}
		if _, err := CheckEndpoint(r.Endpoint, s.cfg.AllowPrivateEndpoints); err != nil {
			return d, bad("endpoint", "%v", err)
		}
		d.Target = r.Endpoint
		return d, nil
	}
	return d, bad("provider", "must be one of fcm, apns, webpush, unifiedpush")
}

// noEndpoint refuses fields that don't belong to the provider, instead of
// ignoring them: a client that sends them has misunderstood what it is doing.
func noEndpoint(r Registration) error {
	if r.Endpoint != "" || r.Keys != (Keys{}) {
		return bad("endpoint", "is not used with %s; give token", r.Provider)
	}
	return nil
}

// checkKeys validates Web Push keys and returns them in the form that is stored.
func checkKeys(k Keys) (Keys, error) {
	p256dh, err := decodeBase64URL(k.P256DH)
	if err != nil {
		return k, bad("keys.p256dh", "must be base64url")
	}
	if _, err := ecdh.P256().NewPublicKey(p256dh); err != nil {
		return k, bad("keys.p256dh", "must be an uncompressed P-256 public key (65 bytes)")
	}
	secret, err := decodeBase64URL(k.Auth)
	if err != nil || len(secret) != 16 {
		return k, bad("keys.auth", "must be 16 bytes, base64url")
	}
	return Keys{
		P256DH: base64.RawURLEncoding.EncodeToString(p256dh),
		Auth:   base64.RawURLEncoding.EncodeToString(secret),
	}, nil
}

// decodeBase64URL accepts base64url with or without padding, which is how
// browsers and libraries differ.
func decodeBase64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// authenticate finds the device and checks the secret.
func (s *Service) authenticate(ctx context.Context, id, secret string) (Device, error) {
	d, err := s.store.Device(ctx, id)
	if err != nil {
		return Device{}, err
	}
	if subtle.ConstantTimeCompare([]byte(HashSecret(secret)), []byte(d.SecretHash)) != 1 {
		return Device{}, ErrBadSecret
	}
	return d, nil
}

// Follow makes topics the complete list the device is woken for, and returns the
// subscriptions with their IDs. Nothing changes unless the device's owner may
// read every one of them.
func (s *Service) Follow(ctx context.Context, id, secret string, topics []string) ([]Subscription, error) {
	d, err := s.authenticate(ctx, id, secret)
	if err != nil {
		return nil, err
	}
	if len(topics) > s.cfg.MaxTopicsPerDevice {
		return nil, bad("topics", "at most %d topics per device", s.cfg.MaxTopicsPerDevice)
	}
	seen := map[string]bool{}
	for _, t := range topics {
		if !message.ValidTopic(t) || message.IsReserved(t) {
			return nil, bad("topics", "invalid topic name %q: use 1-64 letters, digits, - or _", t)
		}
		if seen[t] {
			return nil, bad("topics", "the topic %q is listed twice", t)
		}
		seen[t] = true
		ok, err := s.access.MayRead(ctx, d.UserID, t)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &ForbiddenError{Topic: t, Anonymous: d.UserID == ""}
		}
	}
	now := s.cfg.Now()
	subs, err := s.store.SetSubscriptions(ctx, d.ID, topics, now)
	if err != nil {
		return nil, err
	}
	_ = s.store.Touch(ctx, d.ID, now) // when it was last seen is a convenience, not worth failing for
	return subs, nil
}

// Remove forgets the device, with what it follows.
func (s *Service) Remove(ctx context.Context, id, secret string) error {
	if _, err := s.authenticate(ctx, id, secret); err != nil {
		return err
	}
	return s.store.DeleteDevice(ctx, id)
}
