package push

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Firebase Cloud Messaging, through its HTTP v1 API. The server signs in with the
// Firebase project's service account: it signs a JWT with the account's private
// key, exchanges it at Google's token endpoint for an access token that lasts an
// hour, and sends with that. Doing it directly avoids the Firebase SDK and its
// dependencies for what is two requests.

const (
	fcmScope         = "https://www.googleapis.com/auth/firebase.messaging"
	defaultTokenURI  = "https://oauth2.googleapis.com/token"
	fcmSendURL       = "https://fcm.googleapis.com/v1/projects/%s/messages:send"
	fcmMaxPayload    = 4000 // FCM allows 4096 bytes of data in all
	fcmMaxTTL        = 28 * 24 * time.Hour
	accessTokenSlack = time.Minute // renew this long before a token expires
)

// FCMSender sends to Android devices through FCM.
type FCMSender struct {
	client      *http.Client
	projectID   string
	clientEmail string
	keyID       string
	key         *rsa.PrivateKey
	tokenURI    string
	sendURL     string
	now         func() time.Time

	mu      sync.Mutex // held while renewing, so that one request does it for all
	token   string
	expires time.Time
}

// serviceAccount is the part of a Google service account's JSON key file that is
// needed.
type serviceAccount struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	TokenURI     string `json:"token_uri"`
}

// NewFCM returns a sender that signs in with the service account in credentials,
// the JSON file downloaded from the Firebase console.
func NewFCM(credentials []byte, client *http.Client) (*FCMSender, error) {
	var sa serviceAccount
	if err := json.Unmarshal(credentials, &sa); err != nil {
		return nil, fmt.Errorf("not a JSON service account key: %w", err)
	}
	if sa.Type != "service_account" {
		return nil, errors.New(`not a service account key: its "type" must be "service_account"`)
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, errors.New("the service account key lacks project_id, client_email or private_key")
	}
	key, err := parseRSAKey(sa.PrivateKey)
	if err != nil {
		return nil, err
	}
	tokenURI := sa.TokenURI
	if tokenURI == "" {
		tokenURI = defaultTokenURI
	}
	u, err := url.Parse(tokenURI)
	if err != nil || u.Scheme != "https" {
		return nil, fmt.Errorf("the service account's token_uri %q is not an https URL", tokenURI)
	}
	return &FCMSender{
		client: client, projectID: sa.ProjectID, clientEmail: sa.ClientEmail, keyID: sa.PrivateKeyID,
		key: key, tokenURI: tokenURI, sendURL: fmt.Sprintf(fcmSendURL, url.PathEscape(sa.ProjectID)), now: time.Now,
	}, nil
}

func parseRSAKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("the service account's private_key is not PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the service account's private_key can't be read: %w", err)
	}
	key, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the service account's private_key is not an RSA key")
	}
	return key, nil
}

// ProjectID is the Firebase project the sender belongs to.
func (s *FCMSender) ProjectID() string { return s.projectID }

// MaxPayload implements Sender.
func (s *FCMSender) MaxPayload() int { return fcmMaxPayload }

// accessToken returns a valid access token, getting a new one when needed.
func (s *FCMSender) accessToken(ctx context.Context, renew bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if !renew && s.token != "" && now.Before(s.expires.Add(-accessTokenSlack)) {
		return s.token, nil
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	if s.keyID != "" {
		header["kid"] = s.keyID
	}
	assertion, err := signRS256(s.key, header, map[string]any{
		"iss": s.clientEmail, "scope": fcmScope, "aud": s.tokenURI,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("get an FCM access token: %w", cleanError(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseRead))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("get an FCM access token: Google answered %s%s", resp.Status, excerpt(body))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return "", errors.New("get an FCM access token: Google's answer has no access_token")
	}
	s.token = tok.AccessToken
	s.expires = now.Add(time.Duration(tok.ExpiresIn) * time.Second)
	return s.token, nil
}

// Send implements Sender.
func (s *FCMSender) Send(ctx context.Context, n Notification) error {
	body, err := json.Marshal(map[string]any{"message": map[string]any{
		"token": n.Device.Target,
		// The app's service builds the notification from this: the message itself,
		// or a poll request.
		"data": map[string]string{"flare": string(n.Payload)},
		"android": map[string]any{
			"priority": fcmPriority(n.Message.Priority),
			"ttl":      strconv.FormatInt(int64(min(n.TTL(), fcmMaxTTL)/time.Second), 10) + "s",
		},
	}})
	if err != nil {
		return Permanent(err)
	}

	renew := false
	for attempt := 0; ; attempt++ {
		token, err := s.accessToken(ctx, renew)
		if err != nil {
			return Transient(err, 0) // the credentials or the network: nothing about this device
		}
		status, header, reply, err := s.post(ctx, token, body)
		if err != nil {
			return Transient(cleanError(err), 0)
		}
		if status == http.StatusUnauthorized && attempt == 0 {
			renew = true // the token may have been revoked or expired early: get a fresh one
			continue
		}
		return fcmResult(status, header, reply)
	}
}

func (s *FCMSender) post(ctx context.Context, token string, body []byte) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sendURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseRead))
	return resp.StatusCode, resp.Header, reply, nil
}

// fcmPriority: a high-priority data message wakes a device that is dozing, which
// every notification worth a sound needs; the low priorities can wait for the
// device to wake for other reasons.
func fcmPriority(priority int) string {
	if priority <= 2 {
		return "NORMAL"
	}
	return "HIGH"
}

// fcmResult sorts out FCM's answer (https://firebase.google.com/docs/reference/fcm/rest/v1/ErrorCode).
func fcmResult(status int, header http.Header, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}
	code, text := fcmError(body)
	err := fmt.Errorf("FCM answered %d %s%s", status, code, excerptOf(text))
	switch {
	case code == "UNREGISTERED", code == "SENDER_ID_MISMATCH":
		// The token is for another project, or the app is gone.
		return Gone(err)
	case code == "INVALID_ARGUMENT" && strings.Contains(text, "registration token"):
		return Gone(err)
	case status == http.StatusTooManyRequests, status >= 500:
		return Transient(err, retryAfter(header.Get("Retry-After"), time.Now()))
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		// Our credentials, or the Firebase project's setup: something the operator
		// can fix, after which the notification is still worth sending.
		return Transient(err, 0)
	}
	return Permanent(err)
}

// fcmError reads FCM's error body: the error code (in the details if there, else
// the status) and its message.
func fcmError(body []byte) (code, message string) {
	var e struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return "", strings.TrimSpace(string(body))
	}
	code = e.Error.Status
	for _, d := range e.Error.Details {
		if d.ErrorCode != "" {
			code = d.ErrorCode
		}
	}
	return code, e.Error.Message
}

func excerptOf(s string) string { return excerpt([]byte(s)) }
