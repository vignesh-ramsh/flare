package auth

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern, topic string
		want           bool
	}{
		{"alerts", "alerts", true},
		{"alerts", "alert", false},
		{"alerts", "alerts2", false},
		{"alerts", "Alerts", false}, // case matters
		{"*", "anything", true},
		{"*", "", true},
		{"alerts-*", "alerts-db", true},
		{"alerts-*", "alerts-", true},
		{"alerts-*", "alerts", false},
		{"alerts-*", "xalerts-db", false},
		{"*-prod", "db-prod", true},
		{"*-prod", "db-prod-1", false},
		{"a*c", "abc", true},
		{"a*c", "ac", true},
		{"a*c", "abcd", false},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "abbbc", true},
		{"a*b*c", "acb", false},
		{"*ab", "aab", true},  // the star must be able to give characters back
		{"a*ab", "aab", true}, // ... and so must one that matched too much
		{"a*ab", "aaab", true},
		{"*a*a*a", "banana", true},
		{"*a*a*a", "bnn", false},
		{"**", "x", true},
		{"a**b", "ab", true},
		{"", "", true}, // not a valid pattern, but must not misbehave
		{"", "a", false},
		{"abc", "", false},
	}
	for _, tt := range tests {
		if got := Match(tt.pattern, tt.topic); got != tt.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tt.pattern, tt.topic, got, tt.want)
		}
	}
}

func TestMatchIsFastOnAdversarialPatterns(t *testing.T) {
	// Patterns like this make naive recursive matchers take exponential time.
	pattern := strings.Repeat("a*", 30) + "b"
	topic := strings.Repeat("a", 64)
	for range 1000 {
		if Match(pattern, topic) {
			t.Fatal("unexpected match")
		}
	}
}

func TestValidPattern(t *testing.T) {
	for _, ok := range []string{"a", "alerts", "alerts-*", "*", "*-prod", "a_b-c", strings.Repeat("a", 64)} {
		if !ValidPattern(ok) {
			t.Errorf("ValidPattern(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "a b", "a/b", "a.b", "a?", "a[b]", "(a)", "é", strings.Repeat("a", 65)} {
		if ValidPattern(bad) {
			t.Errorf("ValidPattern(%q) = true", bad)
		}
	}
}

func r(user, pattern string, a Access) Rule { return Rule{UserID: user, Pattern: pattern, Access: a} }

func TestDecide(t *testing.T) {
	const alice, bob = "alice", "bob"
	tests := []struct {
		name  string
		rules []Rule
		user  string
		topic string
		def   Access
		want  Access
	}{
		{"no rules: the default", nil, alice, "t", ReadOnly, ReadOnly},
		{"no matching rule: the default", []Rule{r(alice, "other", ReadWrite)}, alice, "t", DenyAll, DenyAll},
		{"a rule for another user is ignored", []Rule{r(bob, "t", ReadWrite)}, alice, "t", DenyAll, DenyAll},
		{"a rule for the user", []Rule{r(alice, "t", ReadWrite)}, alice, "t", DenyAll, ReadWrite},
		{"a rule for everyone reaches users", []Rule{r("", "t", ReadOnly)}, alice, "t", DenyAll, ReadOnly},
		{"a rule for everyone reaches anonymous visitors", []Rule{r("", "t", ReadOnly)}, "", "t", DenyAll, ReadOnly},
		{"a user's rule does not reach anonymous visitors", []Rule{r(alice, "t", ReadWrite)}, "", "t", DenyAll, DenyAll},

		{"exact beats a wildcard", []Rule{r(alice, "*", DenyAll), r(alice, "t", ReadWrite)}, alice, "t", DenyAll, ReadWrite},
		{"exact beats a wildcard, either order", []Rule{r(alice, "t", ReadWrite), r(alice, "*", DenyAll)}, alice, "t", ReadOnly, ReadWrite},
		{"a longer pattern beats a shorter one", []Rule{r(alice, "a*", ReadOnly), r(alice, "alerts-*", ReadWrite)}, alice, "alerts-db", DenyAll, ReadWrite},
		{"a longer pattern beats a shorter one, either order", []Rule{r(alice, "alerts-*", ReadWrite), r(alice, "a*", DenyAll)}, alice, "alerts-db", DenyAll, ReadWrite},
		{"* is the fallback", []Rule{r(alice, "*", ReadOnly), r(alice, "x-*", ReadWrite)}, alice, "y", DenyAll, ReadOnly},
		{"a deny on an exact name", []Rule{r(alice, "*", ReadWrite), r(alice, "secret", DenyAll)}, alice, "secret", ReadWrite, DenyAll},
		{"the same deny leaves other topics alone", []Rule{r(alice, "*", ReadWrite), r(alice, "secret", DenyAll)}, alice, "public", DenyAll, ReadWrite},

		{"at equal specificity the user's own rule beats everyone's", []Rule{r("", "t", ReadWrite), r(alice, "t", ReadOnly)}, alice, "t", DenyAll, ReadOnly},
		{"at equal specificity, own beats everyone's: the other way round", []Rule{r("", "t", DenyAll), r(alice, "t", ReadWrite)}, alice, "t", DenyAll, ReadWrite},
		{"specificity beats ownership", []Rule{r("", "t", ReadWrite), r(alice, "*", DenyAll)}, alice, "t", DenyAll, ReadWrite},
		{"an anonymous visitor has no own rules", []Rule{r("", "t", ReadOnly)}, "", "t", ReadWrite, ReadOnly},

		{"equally specific rules: a deny wins", []Rule{r(alice, "a*", ReadWrite), r(alice, "*b", DenyAll)}, alice, "ab", DenyAll, DenyAll},
		{"equally specific rules add up", []Rule{r(alice, "a*", ReadOnly), r(alice, "*b", WriteOnly)}, alice, "ab", DenyAll, ReadWrite},
		{"equally specific rules that agree", []Rule{r(alice, "a*", ReadOnly), r(alice, "*b", ReadOnly)}, alice, "ab", DenyAll, ReadOnly},
		{"a less specific deny does not join the tie", []Rule{r(alice, "ab*", ReadOnly), r(alice, "*", DenyAll)}, alice, "abc", DenyAll, ReadOnly},

		{"a rule with an unknown level fails closed", []Rule{r(alice, "t", "admin")}, alice, "t", ReadWrite, DenyAll},
		{"an empty level fails closed", []Rule{r(alice, "t", "")}, alice, "t", ReadWrite, DenyAll},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Decide(tt.rules, tt.user, tt.topic, tt.def); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

// Properties that must hold for any set of rules, checked on random ones.
func TestDecideProperties(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	levels := []Access{ReadWrite, ReadOnly, WriteOnly, DenyAll}
	pieces := []string{"a", "b", "ab", "*", "-", "x"}
	randomPattern := func() string {
		var b strings.Builder
		for range 1 + rng.IntN(4) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		return b.String()
	}
	users := []string{"", "u1", "u2"}

	for i := range 5000 {
		// A rule set with at most one rule per (user, pattern), as the database has.
		seen := map[string]bool{}
		var rules []Rule
		for range rng.IntN(8) {
			u, p := []string{"", "u1", "u2"}[rng.IntN(3)], randomPattern()
			if seen[u+"|"+p] {
				continue
			}
			seen[u+"|"+p] = true
			rules = append(rules, r(u, p, levels[rng.IntN(len(levels))]))
		}
		topic := randomPattern()
		topic = strings.ReplaceAll(topic, "*", "q")
		user := users[rng.IntN(len(users))]
		def := levels[rng.IntN(len(levels))]
		got := Decide(rules, user, topic, def)

		// 1. Nothing matches: the default.
		matches := false
		for _, rule := range rules {
			if (rule.UserID == user || rule.UserID == "") && Match(rule.Pattern, topic) {
				matches = true
			}
		}
		if !matches && got != def {
			t.Fatalf("#%d: no rule matches %q but got %s, not the default %s: %+v", i, topic, got, def, rules)
		}

		// 2. A rule naming the topic exactly, for this very user, decides.
		for _, rule := range rules {
			if user != "" && rule.UserID == user && rule.Pattern == topic {
				want := rule.Access
				if got != want {
					t.Fatalf("#%d: exact own rule %+v should decide %q but got %s: %+v", i, rule, topic, got, rules)
				}
			}
		}

		// 3. The answer is always one of the four levels.
		switch got {
		case ReadWrite, ReadOnly, WriteOnly, DenyAll:
		default:
			t.Fatalf("#%d: Decide returned %q, not a level", i, got)
		}

		// 4. The result does not depend on the order of the rules.
		shuffled := append([]Rule(nil), rules...)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if again := Decide(shuffled, user, topic, def); again != got {
			t.Fatalf("#%d: %s, then %s after shuffling the rules: %+v", i, got, again, rules)
		}

		// 5. Another user's rules never matter.
		var withoutOthers []Rule
		for _, rule := range rules {
			if rule.UserID == user || rule.UserID == "" {
				withoutOthers = append(withoutOthers, rule)
			}
		}
		if again := Decide(withoutOthers, user, topic, def); again != got {
			t.Fatalf("#%d: %s, but %s without other users' rules: %+v", i, got, again, rules)
		}
	}
}
