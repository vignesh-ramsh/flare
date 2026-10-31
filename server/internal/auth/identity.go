package auth

import (
	"context"
	"net/http"
)

// credentialKind is how a user proved who they are.
type credentialKind int

const (
	byNothing credentialKind = iota
	byPassword
	byToken
)

// credential remembers what an identity was established with, so that it can be
// checked again later (see Authorizer.Revalidate).
type credential struct {
	kind credentialKind
	// secret is the stored password hash that was matched, or the token's hash.
	secret string
}

// Identity is who a request comes from: a user, or an anonymous visitor.
type Identity struct {
	// User is nil for an anonymous visitor.
	User *User
	cred credential
}

// Anonymous reports whether nobody is logged in.
func (i Identity) Anonymous() bool { return i.User == nil }

// UserID is the user's ID, or "" for an anonymous visitor.
func (i Identity) UserID() string {
	if i.User == nil {
		return ""
	}
	return i.User.ID
}

// Authorizer works out who a request comes from and what they may do.
type Authorizer interface {
	// Authenticate reads the request's credentials, an Authorization header with a
	// Bearer token or a Basic user and password. Without any, the identity is
	// anonymous. Credentials that are present but wrong are an error
	// (ErrBadCredentials), never anonymous; too many such errors from one address
	// give ErrThrottled.
	Authenticate(r *http.Request) (Identity, error)

	// AuthenticateToken does the same for a token that arrived some other way, such
	// as in a WebSocket frame. clientIP is the address to count failures against.
	AuthenticateToken(ctx context.Context, token, clientIP string) (Identity, error)

	// Authorize says whether id may do p to topic, or returns ErrUnauthorized (an
	// anonymous visitor who would be allowed if logged in) or ErrForbidden.
	Authorize(ctx context.Context, id Identity, topic string, p Permission) error

	// Revalidate checks that id is still good: the token not revoked or expired,
	// the password not changed, the user not deleted. It returns the identity as it
	// is now, with the current role. Open connections call it periodically so that
	// cutting someone off takes effect without waiting for them to reconnect.
	Revalidate(ctx context.Context, id Identity) (Identity, error)
}
