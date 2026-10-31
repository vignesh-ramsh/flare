// Package theme holds the themes a server offers its apps: a unique name, light or
// dark, and the colours the theme sets, such as its background, surface and border.
//
// The list of colours, the pairs of text and background that must stay readable and
// the rules for names and colours are shared with the app, the brand tool and the
// Admin UI in api/theme-tokens.json. A copy of it is embedded here, and a test fails
// if the copy and the original differ.
package theme

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

//go:embed theme-tokens.json
var contractJSON []byte

// Token is one colour a theme can set.
type Token struct {
	Key   string `json:"key"`
	Group string `json:"group"`
	Label string `json:"label"`
	Help  string `json:"help"`
}

// Group collects colours that belong together in an editor.
type Group struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Pair is a text colour and the background it must stay readable on.
type Pair struct {
	Text       string `json:"text"`
	Background string `json:"background"`
}

type contract struct {
	Version             int      `json:"version"`
	NamePattern         string   `json:"name_pattern"`
	ColorPattern        string   `json:"color_pattern"`
	RequiredTokens      []string `json:"required_tokens"`
	MinimumContrast     float64  `json:"minimum_contrast"`
	RecommendedContrast float64  `json:"recommended_contrast"`
	Groups              []Group  `json:"groups"`
	Tokens              []Token  `json:"tokens"`
	ReadablePairs       []Pair   `json:"readable_pairs"`
}

var (
	spec      contract
	namePat   *regexp.Regexp
	colorPat  *regexp.Regexp
	tokenKeys map[string]bool
)

func init() {
	if err := json.Unmarshal(contractJSON, &spec); err != nil {
		panic("theme: theme-tokens.json: " + err.Error())
	}
	namePat = regexp.MustCompile(spec.NamePattern)
	colorPat = regexp.MustCompile(spec.ColorPattern)
	tokenKeys = make(map[string]bool, len(spec.Tokens))
	for _, t := range spec.Tokens {
		tokenKeys[t.Key] = true
	}
	for _, p := range spec.ReadablePairs {
		if !tokenKeys[p.Text] || !tokenKeys[p.Background] {
			panic(fmt.Sprintf("theme: theme-tokens.json: pair %s/%s names an unknown colour", p.Text, p.Background))
		}
	}
}

// Contract is the shared description of themes (api/theme-tokens.json), as an editor
// needs it: the colours with their groups, labels and help, the readable pairs and
// the contrast limits.
func Contract() []byte { return contractJSON }

// Tokens lists the colours a theme can set, in order.
func Tokens() []Token { return append([]Token(nil), spec.Tokens...) }

// Pairs lists the pairs of text and background that must stay readable.
func Pairs() []Pair { return append([]Pair(nil), spec.ReadablePairs...) }

// MinimumContrast is the least contrast between text and its background that a theme
// may set.
func MinimumContrast() float64 { return spec.MinimumContrast }

// MaxThemes is the most themes a server holds, to keep what apps download small.
const MaxThemes = 50

// Theme is a unique name, light or dark, and the colours the theme sets as "#RRGGBB".
type Theme struct {
	Name       string            `json:"name"`
	Brightness string            `json:"brightness"`
	Colors     map[string]string `json:"colors"`
}

// Stored is a Theme with when it was added and last changed.
type Stored struct {
	Theme
	CreatedAt time.Time
	UpdatedAt time.Time
}

// InputError is a theme that is not valid, in words an admin can act on.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, args...)}
}

// Key is what names are compared by: trimmed and lower case.
func Key(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Normalize checks t and returns it with its colours in upper case, so that the same
// theme is always stored the same way. It returns an *InputError for a theme that is
// not valid.
//
// Colours the theme does not set are derived by the app, so only those a theme sets
// can be checked here: where it sets both a text colour and its background, the
// contrast must be at least MinimumContrast.
func Normalize(t Theme) (Theme, error) {
	if !namePat.MatchString(t.Name) {
		return Theme{}, invalid("the name must be 1 to 32 letters, digits, spaces, dots, dashes or underscores, starting with a letter or digit (found %q)", t.Name)
	}
	if t.Brightness != "light" && t.Brightness != "dark" {
		return Theme{}, invalid("brightness must be light or dark (found %q)", t.Brightness)
	}
	colors := make(map[string]string, len(t.Colors))
	for key, value := range t.Colors {
		if !tokenKeys[key] {
			return Theme{}, invalid("%q is not a colour a theme can set", key)
		}
		if !colorPat.MatchString(value) {
			return Theme{}, invalid("%s must be a colour like #RRGGBB (found %q)", key, value)
		}
		colors[key] = strings.ToUpper(value)
	}
	for _, key := range spec.RequiredTokens {
		if _, ok := colors[key]; !ok {
			return Theme{}, invalid("%s is required: every colour a theme leaves out is derived from it", key)
		}
	}
	for _, p := range spec.ReadablePairs {
		text, behind := colors[p.Text], colors[p.Background]
		if text == "" || behind == "" {
			continue
		}
		if ratio := Contrast(text, behind); ratio < spec.MinimumContrast {
			return Theme{}, invalid("%s (%s) on %s (%s) has a contrast of %.2f:1; it must be at least %.1f:1 to be readable",
				p.Text, text, p.Background, behind, ratio, spec.MinimumContrast)
		}
	}
	return Theme{Name: t.Name, Brightness: t.Brightness, Colors: colors}, nil
}

func channel(c uint64) float64 {
	s := float64(c) / 255
	if s <= 0.03928 {
		return s / 12.92
	}
	return math.Pow((s+0.055)/1.055, 2.4)
}

func luminance(hex string) float64 {
	var v uint64
	for _, r := range hex[1:] {
		v = v<<4 | uint64(strings.IndexRune("0123456789abcdef", r|0x20))
	}
	return 0.2126*channel(v>>16&0xFF) + 0.7152*channel(v>>8&0xFF) + 0.0722*channel(v&0xFF)
}

// Contrast is the WCAG contrast ratio, 1 to 21, of two "#RRGGBB" colours.
func Contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	hi, lo := math.Max(la, lb), math.Min(la, lb)
	return (hi + 0.05) / (lo + 0.05)
}

// Errors the store reports.
var (
	// ErrExists means a theme with that name (in any case) is already there.
	ErrExists = errors.New("a theme with that name already exists")
	// ErrNotFound means there is no such theme.
	ErrNotFound = errors.New("there is no such theme")
	// ErrLimit means the server already holds MaxThemes themes.
	ErrLimit = errors.New("the server already holds as many themes as it may")
)

// Store keeps themes.
type Store interface {
	// Themes returns every theme, ordered by name.
	Themes(ctx context.Context) ([]Stored, error)
	// CreateTheme adds a theme, or returns ErrExists, or ErrLimit if there are already max.
	CreateTheme(ctx context.Context, t Theme, now time.Time, max int) error
	// ReplaceTheme changes a theme's colours and brightness (and the case of its name),
	// or returns ErrNotFound.
	ReplaceTheme(ctx context.Context, t Theme, now time.Time) error
	// DeleteTheme removes a theme, or returns ErrNotFound.
	DeleteTheme(ctx context.Context, nameKey string) error
}

// Service is what the API uses to read and change themes.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService returns a Service over store.
func NewService(store Store) *Service { return &Service{store: store, now: time.Now} }

// List returns every theme, ordered by name.
func (s *Service) List(ctx context.Context) ([]Stored, error) { return s.store.Themes(ctx) }

// Create checks and adds a theme.
func (s *Service) Create(ctx context.Context, t Theme) (Theme, error) {
	t, err := Normalize(t)
	if err != nil {
		return Theme{}, err
	}
	return t, s.store.CreateTheme(ctx, t, s.now(), MaxThemes)
}

// Replace checks a theme and stores it in place of the one with the same name.
func (s *Service) Replace(ctx context.Context, t Theme) (Theme, error) {
	t, err := Normalize(t)
	if err != nil {
		return Theme{}, err
	}
	return t, s.store.ReplaceTheme(ctx, t, s.now())
}

// Delete removes the theme with the given name.
func (s *Service) Delete(ctx context.Context, name string) error {
	return s.store.DeleteTheme(ctx, Key(name))
}
