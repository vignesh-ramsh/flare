// Package message defines what a message is, and the rules a publisher's input
// has to meet before the server will store and deliver it.
package message

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Event types of the objects a subscriber receives.
const (
	EventOpen      = "open"
	EventKeepalive = "keepalive"
	EventMessage   = "message"
)

// Limits on what a publisher may send.
const (
	DefaultPriority     = 3
	MaxTitleLength      = 250 // characters
	MaxTags             = 20
	MaxTagLength        = 64 // characters
	MaxClickLength      = 2048
	MaxTopicsPerRequest = 50
)

// Message is a stored message, and the JSON a subscriber receives. The same
// shape, with only Event, ID, Time and Topic set, is used for the "open" and
// "keepalive" events of a stream.
type Message struct {
	ID       string   `json:"id"`
	Time     int64    `json:"time"`              // unix seconds
	Expires  int64    `json:"expires,omitempty"` // unix seconds
	Event    string   `json:"event"`
	Topic    string   `json:"topic"`
	Title    string   `json:"title,omitempty"`
	Message  string   `json:"message,omitempty"`
	Priority int      `json:"priority,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Click    string   `json:"click,omitempty"`
}

// Draft is a message as a publisher submitted it, before the server gives it an
// ID and times.
type Draft struct {
	Topic    string
	Title    string
	Body     string
	Priority int // 0 means the default
	Tags     []string
	Click    string
}

// Problem is a reason a publisher's input was refused. It is safe to show to the
// publisher.
type Problem struct {
	// Field names what was wrong: "topic", "message", "title", "priority", "tags"
	// or "click".
	Field string
	Msg   string
	// TooLarge marks a body over the size limit, which is answered differently
	// from other problems (HTTP 413).
	TooLarge bool
}

func (p *Problem) Error() string { return p.Msg }

func problem(field, format string, args ...any) *Problem {
	return &Problem{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// Normalize fills in defaults: the default priority, and a placeholder body for
// a message that has neither title nor body.
func (d *Draft) Normalize() {
	if d.Priority == 0 {
		d.Priority = DefaultPriority
	}
	if d.Body == "" && d.Title == "" {
		d.Body = "triggered"
	}
}

// Validate checks a normalised draft against the limits. maxBody is the largest
// allowed message body, in bytes.
func (d *Draft) Validate(maxBody int) error {
	if !ValidTopic(d.Topic) {
		return problem("topic", "invalid topic name %q: use 1-64 letters, digits, - or _", d.Topic)
	}
	if IsReserved(d.Topic) {
		return problem("topic", "%q is reserved and can't be used as a topic name", d.Topic)
	}
	if len(d.Body) > maxBody {
		return &Problem{Field: "message", Msg: fmt.Sprintf("message is larger than the limit of %d bytes", maxBody), TooLarge: true}
	}
	if !utf8.ValidString(d.Body) {
		return problem("message", "message must be valid UTF-8 text")
	}
	if err := checkLine("title", d.Title, MaxTitleLength); err != nil {
		return err
	}
	if d.Priority < 1 || d.Priority > 5 {
		return problem("priority", "priority must be between 1 and 5")
	}
	if len(d.Tags) > MaxTags {
		return problem("tags", "at most %d tags are allowed", MaxTags)
	}
	for _, tag := range d.Tags {
		if tag == "" {
			return problem("tags", "tags must not be empty")
		}
		if strings.ContainsRune(tag, ',') {
			return problem("tags", "a tag must not contain a comma")
		}
		if err := checkLine("tags", tag, MaxTagLength); err != nil {
			return err
		}
	}
	return checkClick(d.Click)
}

// checkLine requires valid, single-line text of at most max characters.
func checkLine(field, s string, max int) error {
	if !utf8.ValidString(s) {
		return problem(field, "%s must be valid UTF-8 text", field)
	}
	if utf8.RuneCountInString(s) > max {
		return problem(field, "%s is longer than %d characters", field, max)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7F {
			return problem(field, "%s must be a single line without control characters", field)
		}
	}
	return nil
}

// Schemes a click URL must not use: they run code or read local data when
// opened.
var blockedClickSchemes = map[string]bool{
	"javascript": true, "data": true, "file": true, "vbscript": true, "blob": true,
}

var schemePattern = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)

func checkClick(raw string) error {
	if raw == "" {
		return nil
	}
	if len(raw) > MaxClickLength {
		return problem("click", "click is longer than %d characters", MaxClickLength)
	}
	u, err := url.Parse(raw)
	if err != nil || !schemePattern.MatchString(strings.ToLower(u.Scheme)) {
		return problem("click", "click must be an absolute URL such as https://example.com")
	}
	if blockedClickSchemes[strings.ToLower(u.Scheme)] {
		return problem("click", "click may not use the %q scheme", u.Scheme)
	}
	return nil
}

var topicPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// reserved holds names the server's own endpoints use.
var reserved = map[string]bool{"v1": true, "healthz": true, "readyz": true, "metrics": true}

// ValidTopic reports whether s has the form of a topic name.
func ValidTopic(s string) bool { return topicPattern.MatchString(s) }

// IsReserved reports whether s is a name that topics can't use.
func IsReserved(s string) bool { return reserved[s] }

// ParseTopics splits a comma-separated list of topics, as used in subscribe
// URLs, checking each name and dropping repeats.
func ParseTopics(s string) ([]string, error) {
	var topics []string
	seen := map[string]bool{}
	for _, name := range strings.Split(s, ",") {
		if !ValidTopic(name) || IsReserved(name) {
			return nil, fmt.Errorf("invalid topic name %q", name)
		}
		if !seen[name] {
			seen[name] = true
			topics = append(topics, name)
		}
	}
	if len(topics) > MaxTopicsPerRequest {
		return nil, fmt.Errorf("too many topics: the limit is %d", MaxTopicsPerRequest)
	}
	return topics, nil
}

// ParsePriority reads a priority given as 1-5 or as a name. An empty string
// means the default.
func ParsePriority(s string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return DefaultPriority, nil
	case "1", "min":
		return 1, nil
	case "2", "low":
		return 2, nil
	case "3", "default":
		return 3, nil
	case "4", "high":
		return 4, nil
	case "5", "max", "urgent":
		return 5, nil
	}
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return 0, fmt.Errorf("priority %d is out of range: use 1-5", n)
	}
	return 0, errors.New("priority must be 1-5 or one of min, low, default, high, urgent")
}

// ParseTags splits a comma-separated list of tags, trimming spaces and dropping
// empty entries.
func ParseTags(s string) []string {
	var tags []string
	for _, tag := range strings.Split(s, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}
