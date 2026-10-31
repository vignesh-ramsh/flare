// Package apierr holds the error codes the API reports, shared by the HTTP
// handlers and the WebSocket protocol so that one problem has one number.
//
// A code is the HTTP status followed by two digits: 40301 is a 403.
package apierr

// Error codes.
const (
	InvalidTopic       = 40001 // the topic name is malformed or reserved
	InvalidParameter   = 40002 // a header, query parameter or field has a bad value
	Unsupported        = 40003 // a feature that is not supported (yet)
	InvalidBody        = 40004 // the request body or WebSocket frame can't be understood
	Unauthorized       = 40101 // the request needs a logged-in user
	InvalidCredentials = 40102 // a password or token was given and is wrong, unknown, expired or revoked
	Forbidden          = 40301 // the request is not allowed to do this
	NotFound           = 40401 // there is nothing here
	AlreadySubscribed  = 40901 // the WebSocket is already subscribed to this topic
	TokenLimit         = 40902 // the user already has as many tokens as are allowed
	TooLarge           = 41301 // the message is over the size limit
	TooMany            = 42901 // a limit on the number of subscriptions was reached
	Throttled          = 42902 // too many failed login attempts from this address
	Internal           = 50001 // something went wrong on the server
	Lagged             = 50002 // a subscription was dropped because the client fell behind
	Unavailable        = 50301 // the server can't take messages right now
)

// HTTPStatus returns the HTTP status that goes with code.
func HTTPStatus(code int) int { return code / 100 }
