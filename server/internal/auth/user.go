package auth

import (
	"fmt"
	"regexp"
	"time"
)

// Role is what kind of user someone is.
type Role string

const (
	// RoleUser can do what the access rules and the default allow.
	RoleUser Role = "user"
	// RoleAdmin can read and write every topic.
	RoleAdmin Role = "admin"
)

// ParseRole checks that s names a role.
func ParseRole(s string) (Role, error) {
	switch r := Role(s); r {
	case RoleUser, RoleAdmin:
		return r, nil
	}
	return "", fmt.Errorf("the role must be user or admin (found %q)", s)
}

// User is an account.
type User struct {
	ID        string
	Name      string
	Role      Role
	CreatedAt time.Time
	// Disabled means the user cannot log in and their tokens do not work, until they are
	// enabled again. Nothing of theirs is deleted.
	Disabled bool
}

// Token is an access token's record. The secret itself is not kept.
type Token struct {
	ID         string
	UserID     string
	Label      string
	CreatedAt  time.Time
	ExpiresAt  *time.Time // nil: never
	LastUsedAt *time.Time // nil: never used
}

var usernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// ValidUsername checks a new user name: 1-32 lower-case letters, digits, . _ -,
// starting with a letter or digit. (No colon, which a Basic login couldn't carry.)
// "everyone" is taken: it means every user in access rules.
func ValidUsername(name string) error {
	if name == Everyone {
		return fmt.Errorf("%q is reserved: it stands for everyone in access rules", Everyone)
	}
	if !usernameRE.MatchString(name) {
		return fmt.Errorf("a user name is 1-32 lower-case letters, digits, . _ or -, starting with a letter or digit (found %q)", name)
	}
	return nil
}
