package auth

import (
	"regexp"
	"strings"
)

// Rule gives a user, or everyone, an access level on the topics a pattern
// matches.
type Rule struct {
	// UserID is the user the rule is for, or "" for everyone.
	UserID string
	// Pattern is a topic name, or a name with * standing for any run of
	// characters: "alerts", "alerts-*", "*".
	Pattern string
	Access  Access
}

// Everyone is the name that stands for "all users, and anonymous visitors" where
// a user name is expected.
const Everyone = "everyone"

var patternRE = regexp.MustCompile(`^[A-Za-z0-9_*-]{1,64}$`)

// ValidPattern reports whether s can be used as a rule's pattern.
func ValidPattern(s string) bool { return patternRE.MatchString(s) }

// Match reports whether topic matches pattern, where * stands for any run of
// characters, including none. Case matters.
func Match(pattern, topic string) bool {
	p, n := 0, 0
	star, mark := -1, 0
	for n < len(topic) {
		switch {
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, n
			p++
		case p < len(pattern) && pattern[p] == topic[n]:
			p++
			n++
		case star >= 0: // backtrack: let the last * swallow one more character
			mark++
			p, n = star+1, mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// rank orders the rules that match a topic, most specific first.
type rank struct {
	exact   bool // no wildcard
	literal int  // characters that aren't wildcards
	own     bool // a rule for this very user, not for everyone
}

func (a rank) beats(b rank) bool {
	switch {
	case a.exact != b.exact:
		return a.exact
	case a.literal != b.literal:
		return a.literal > b.literal
	}
	return a.own && !b.own
}

func rankOf(r Rule, userID string) rank {
	wild := strings.Count(r.Pattern, "*")
	return rank{exact: wild == 0, literal: len(r.Pattern) - wild, own: r.UserID == userID && userID != ""}
}

// Decide works out what userID may do with topic. userID is "" for an anonymous
// visitor.
//
// Of the rules that apply (the user's own, and everyone's) and match the topic,
// the most specific one decides: an exact name beats a pattern, a longer pattern
// beats a shorter one, and at equal specificity the user's own rule beats the one
// for everyone. If several rules are equally specific, a deny wins, and otherwise
// they add up. With no matching rule, def applies.
func Decide(rules []Rule, userID, topic string, def Access) Access {
	var best rank
	var winners []Rule
	for _, r := range rules {
		if (r.UserID != userID && r.UserID != "") || !Match(r.Pattern, topic) {
			continue
		}
		rk := rankOf(r, userID)
		switch {
		case len(winners) == 0 || rk.beats(best):
			best, winners = rk, []Rule{r}
		case !best.beats(rk): // equally specific
			winners = append(winners, r)
		}
	}
	if len(winners) == 0 {
		return def
	}
	result := DenyAll
	for _, w := range winners {
		if w.Access == DenyAll || !w.Access.Open() {
			return DenyAll
		}
		result = union(result, w.Access)
	}
	return result
}
