package message

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func valid() Draft {
	d := Draft{Topic: "backups", Body: "done"}
	d.Normalize()
	return d
}

func TestNormalizeFillsDefaults(t *testing.T) {
	d := Draft{Topic: "t"}
	d.Normalize()
	if d.Priority != DefaultPriority || d.Body != "triggered" {
		t.Errorf("empty draft became %+v, want default priority and body \"triggered\"", d)
	}
	d = Draft{Topic: "t", Title: "Only a title"}
	d.Normalize()
	if d.Body != "" {
		t.Errorf("a draft with a title should keep an empty body, got %q", d.Body)
	}
	d = Draft{Topic: "t", Body: "x", Priority: 5}
	d.Normalize()
	if d.Priority != 5 {
		t.Errorf("explicit priority was changed to %d", d.Priority)
	}
}

func TestValidate(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	tests := []struct {
		name   string
		edit   func(*Draft)
		field  string // "" means valid
		large  bool
		msgHas string
	}{
		{"valid draft", func(*Draft) {}, "", false, ""},
		{"empty topic", func(d *Draft) { d.Topic = "" }, "topic", false, "invalid topic"},
		{"topic with a slash", func(d *Draft) { d.Topic = "a/b" }, "topic", false, ""},
		{"topic with a dot", func(d *Draft) { d.Topic = "a.b" }, "topic", false, ""},
		{"topic of 64 characters", func(d *Draft) { d.Topic = long(64) }, "", false, ""},
		{"topic of 65 characters", func(d *Draft) { d.Topic = long(65) }, "topic", false, ""},
		{"reserved topic", func(d *Draft) { d.Topic = "v1" }, "topic", false, "reserved"},
		{"body at the limit", func(d *Draft) { d.Body = long(100) }, "", false, ""},
		{"body over the limit", func(d *Draft) { d.Body = long(101) }, "message", true, "limit of 100 bytes"},
		{"body that is not UTF-8", func(d *Draft) { d.Body = "\xff\xfe" }, "message", false, "UTF-8"},
		{"body with newlines", func(d *Draft) { d.Body = "a\nb\r\nc" }, "", false, ""},
		{"title at the limit", func(d *Draft) { d.Title = long(MaxTitleLength) }, "", false, ""},
		{"title over the limit", func(d *Draft) { d.Title = long(MaxTitleLength + 1) }, "title", false, ""},
		{"title counted in characters, not bytes", func(d *Draft) { d.Title = strings.Repeat("é", MaxTitleLength) }, "", false, ""},
		{"title with a newline", func(d *Draft) { d.Title = "a\nb" }, "title", false, "single line"},
		{"priority 0 after normalising is fine", func(d *Draft) { d.Priority = 0; d.Normalize() }, "", false, ""},
		{"priority too high", func(d *Draft) { d.Priority = 6 }, "priority", false, ""},
		{"priority negative", func(d *Draft) { d.Priority = -1 }, "priority", false, ""},
		{"too many tags", func(d *Draft) {
			d.Tags = make([]string, MaxTags+1)
			for i := range d.Tags {
				d.Tags[i] = "t"
			}
		}, "tags", false, ""},
		{"empty tag", func(d *Draft) { d.Tags = []string{"a", ""} }, "tags", false, ""},
		{"tag with a comma", func(d *Draft) { d.Tags = []string{"a,b"} }, "tags", false, "comma"},
		{"tag too long", func(d *Draft) { d.Tags = []string{long(MaxTagLength + 1)} }, "tags", false, ""},
		{"click with https", func(d *Draft) { d.Click = "https://example.com/x?y=1" }, "", false, ""},
		{"click with an app scheme", func(d *Draft) { d.Click = "myapp://open/thing" }, "", false, ""},
		{"click with mailto", func(d *Draft) { d.Click = "mailto:a@example.com" }, "", false, ""},
		{"click with javascript", func(d *Draft) { d.Click = "javascript:alert(1)" }, "click", false, "scheme"},
		{"click with mixed-case javascript", func(d *Draft) { d.Click = "JavaScript:alert(1)" }, "click", false, "scheme"},
		{"click with data", func(d *Draft) { d.Click = "data:text/html,hi" }, "click", false, ""},
		{"click with file", func(d *Draft) { d.Click = "file:///etc/passwd" }, "click", false, ""},
		{"click that is relative", func(d *Draft) { d.Click = "/just/a/path" }, "click", false, "absolute"},
		{"click that is not a URL", func(d *Draft) { d.Click = "not a url" }, "click", false, ""},
		{"click that is too long", func(d *Draft) { d.Click = "https://example.com/" + long(MaxClickLength) }, "click", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := valid()
			tt.edit(&d)
			err := d.Validate(100)
			if tt.field == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var p *Problem
			if !errors.As(err, &p) {
				t.Fatalf("got %v, want a *Problem about %q", err, tt.field)
			}
			if p.Field != tt.field || p.TooLarge != tt.large {
				t.Errorf("got field %q (too large: %v), want %q (too large: %v)", p.Field, p.TooLarge, tt.field, tt.large)
			}
			if tt.msgHas != "" && !strings.Contains(p.Msg, tt.msgHas) {
				t.Errorf("message %q should mention %q", p.Msg, tt.msgHas)
			}
		})
	}
}

func TestParseTopics(t *testing.T) {
	got, err := ParseTopics("a,b,a,c-1,D_2")
	if err != nil || !reflect.DeepEqual(got, []string{"a", "b", "c-1", "D_2"}) {
		t.Errorf("ParseTopics = %v, %v", got, err)
	}
	for _, bad := range []string{"", "a,,b", "a,b/c", "a,v1", "a, b", strings.Repeat("a", 65)} {
		if _, err := ParseTopics(bad); err == nil {
			t.Errorf("ParseTopics(%q) succeeded, want an error", bad)
		}
	}
	many := make([]string, MaxTopicsPerRequest+1)
	for i := range many {
		many[i] = "t" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	if _, err := ParseTopics(strings.Join(many, ",")); err == nil {
		t.Error("too many topics were accepted")
	}
}

func TestParsePriority(t *testing.T) {
	good := map[string]int{
		"": 3, "1": 1, "2": 2, "3": 3, "4": 4, "5": 5, " 4 ": 4,
		"min": 1, "low": 2, "default": 3, "high": 4, "max": 5, "urgent": 5, "HIGH": 4,
	}
	for in, want := range good {
		if got, err := ParsePriority(in); err != nil || got != want {
			t.Errorf("ParsePriority(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"0", "6", "-1", "loud", "1.5"} {
		if _, err := ParsePriority(bad); err == nil {
			t.Errorf("ParsePriority(%q) succeeded, want an error", bad)
		}
	}
}

func TestParseTags(t *testing.T) {
	got := ParseTags(" warning, server-01 ,,db ")
	if !reflect.DeepEqual(got, []string{"warning", "server-01", "db"}) {
		t.Errorf("ParseTags = %q", got)
	}
	if ParseTags("") != nil || ParseTags(" , ") != nil {
		t.Error("empty input should give no tags")
	}
}
