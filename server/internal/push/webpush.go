package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Web Push (RFC 8030) delivers to browsers, and to UnifiedPush distributors that
// give keys. The notification is encrypted for the one device (RFC 8291) so that
// the push service carrying it can't read it, and the request is signed with the
// server's VAPID key (RFC 8292), which proves to the push service which server is
// sending and binds a subscription to it.

const (
	// webPushMaxPayload is what fits in the single 4096-byte record every push
	// service must accept: 4096 less the header (salt, record size, key ID and our
	// public key: 86 bytes), the padding delimiter (1) and the authentication tag (16).
	webPushMaxPayload = 4096 - 86 - 1 - 16
	// maxWebPushTTL is the longest time to live asked of a push service. Most
	// refuse more than four weeks.
	maxWebPushTTL = 28 * 24 * time.Hour
	vapidLifetime = 12 * time.Hour // the limit is 24
)

// WebPushSender sends Web Push notifications.
type WebPushSender struct {
	client       *http.Client
	allowPrivate bool
	key          *ecdsa.PrivateKey
	public       string
	subject      string
	now          func() time.Time
}

// NewWebPush returns a sender that signs with key. subject is how a push service
// can reach the operator, a mailto: or https: URL; some services insist on it.
// client should be an EndpointClient.
func NewWebPush(client *http.Client, key *ecdsa.PrivateKey, subject string, allowPrivate bool) *WebPushSender {
	pub, _ := key.PublicKey.ECDH()
	return &WebPushSender{
		client: client, allowPrivate: allowPrivate, key: key, subject: subject, now: time.Now,
		public: base64.RawURLEncoding.EncodeToString(pub.Bytes()),
	}
}

// PublicKey is the key browsers subscribe with, as base64url.
func (s *WebPushSender) PublicKey() string { return s.public }

// MaxPayload implements Sender.
func (s *WebPushSender) MaxPayload() int { return webPushMaxPayload }

// Send implements Sender.
func (s *WebPushSender) Send(ctx context.Context, n Notification) error {
	endpoint, err := CheckEndpoint(n.Device.Target, s.allowPrivate)
	if err != nil {
		// Registered when the rules were different, or tampered with.
		return Permanent(fmt.Errorf("the endpoint is not allowed: %w", err))
	}
	uaPublic, authSecret, err := decodeKeys(n.Device.Keys)
	if err != nil {
		return Permanent(err)
	}
	body, err := encryptRecord(n.Payload, uaPublic, authSecret, nil, nil)
	if err != nil {
		return Permanent(err)
	}
	jwt, err := s.vapid(endpoint)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return Permanent(err)
	}
	h := req.Header
	h.Set("Authorization", "vapid t="+jwt+", k="+s.public)
	h.Set("Content-Encoding", "aes128gcm")
	h.Set("Content-Type", "application/octet-stream")
	h.Set("TTL", strconv.FormatInt(int64(min(n.TTL(), maxWebPushTTL)/time.Second), 10))
	h.Set("Urgency", urgency(n.Message.Priority))
	return roundTrip(s.client, req)
}

// urgency maps a message priority to the Urgency header (RFC 8030 section 5.3),
// which tells the push service how readily to wake a sleeping device.
func urgency(priority int) string {
	switch {
	case priority <= 1:
		return "very-low"
	case priority == 2:
		return "low"
	case priority == 3:
		return "normal"
	}
	return "high"
}

// vapid returns the JWT that proves who is sending to this endpoint. Its audience
// is the push service's origin.
func (s *WebPushSender) vapid(endpoint *url.URL) (string, error) {
	claims := map[string]any{
		"aud": endpoint.Scheme + "://" + endpoint.Host,
		"exp": s.now().Add(vapidLifetime).Unix(),
	}
	if s.subject != "" {
		claims["sub"] = s.subject
	}
	return signES256(s.key, map[string]string{"typ": "JWT", "alg": "ES256"}, claims)
}

func decodeKeys(k Keys) (*ecdh.PublicKey, []byte, error) {
	raw, err := decodeBase64URL(k.P256DH)
	if err != nil {
		return nil, nil, errors.New("the device's public key is not base64url")
	}
	pub, err := ecdh.P256().NewPublicKey(raw)
	if err != nil {
		return nil, nil, errors.New("the device's public key is not a P-256 point")
	}
	auth, err := decodeBase64URL(k.Auth)
	if err != nil || len(auth) != 16 {
		return nil, nil, errors.New("the device's authentication secret is not 16 bytes")
	}
	return pub, auth, nil
}

// encryptRecord encrypts plaintext for a browser as one record of Content-Encoding
// aes128gcm (RFC 8188), keyed as RFC 8291 says. The ephemeral key and the salt are
// fresh random values unless the caller supplies them, as the RFC's test vector
// does.
func encryptRecord(plaintext []byte, uaPublic *ecdh.PublicKey, authSecret []byte, asPrivate *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(plaintext) > webPushMaxPayload {
		return nil, fmt.Errorf("the notification is %d bytes; at most %d fit in one record", len(plaintext), webPushMaxPayload)
	}
	if asPrivate == nil {
		var err error
		if asPrivate, err = ecdh.P256().GenerateKey(rand.Reader); err != nil {
			return nil, err
		}
	}
	if salt == nil {
		salt = make([]byte, 16)
		if _, err := io.ReadFull(rand.Reader, salt); err != nil {
			return nil, err
		}
	}
	asPublic := asPrivate.PublicKey().Bytes()

	shared, err := asPrivate.ECDH(uaPublic)
	if err != nil {
		return nil, err
	}
	// RFC 8291 section 3.4: mix the authentication secret into the shared secret,
	// binding the keys of both sides ...
	prk, err := hkdf.Extract(sha256.New, shared, authSecret)
	if err != nil {
		return nil, err
	}
	keyInfo := append(append([]byte("WebPush: info\x00"), uaPublic.Bytes()...), asPublic...)
	ikm, err := hkdf.Expand(sha256.New, prk, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	// ... and RFC 8188 section 2.2: derive the content key and nonce from it.
	prk2, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk2, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk2, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// The record is the plaintext and a 0x02 delimiter that marks it as the last.
	sealed := gcm.Seal(nil, nonce, append(append([]byte{}, plaintext...), 0x02), nil)

	// The header: salt, record size (4096), the length of the key ID, and the key
	// ID, which for Web Push is the sender's public key.
	out := make([]byte, 0, 86+len(sealed))
	out = append(out, salt...)
	out = append(out, 0x00, 0x00, 0x10, 0x00)
	out = append(out, byte(len(asPublic)))
	out = append(out, asPublic...)
	return append(out, sealed...), nil
}

// NewVAPIDKey makes a signing key for Web Push.
func NewVAPIDKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// vapidSetting is where the server keeps its VAPID key.
const vapidSetting = "vapid_key"

// LoadVAPIDKey returns the server's VAPID key, making and saving one the first
// time. It must stay the same for as long as browsers are subscribed: a
// subscription is bound to the key it was made with.
func LoadVAPIDKey(ctx context.Context, st Store) (*ecdsa.PrivateKey, error) {
	if stored, ok, err := st.Setting(ctx, vapidSetting); err != nil {
		return nil, err
	} else if ok {
		return parseVAPIDKey(stored)
	}
	key, err := NewVAPIDKey()
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	// If two servers start at once on one database, the first to save wins and the
	// other uses its key.
	stored, err := st.SetSettingIfAbsent(ctx, vapidSetting, base64.RawURLEncoding.EncodeToString(der))
	if err != nil {
		return nil, err
	}
	return parseVAPIDKey(stored)
}

func parseVAPIDKey(stored string) (*ecdsa.PrivateKey, error) {
	der, err := base64.RawURLEncoding.DecodeString(stored)
	if err != nil {
		return nil, fmt.Errorf("the saved Web Push key is damaged: %w", err)
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("the saved Web Push key is damaged: %w", err)
	}
	key, ok := k.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("the saved Web Push key is not a P-256 key")
	}
	return key, nil
}
