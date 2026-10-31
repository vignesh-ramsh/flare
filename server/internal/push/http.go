package push

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxResponseRead is how much of a push service's answer is read: enough for any
// error it explains itself with.
const maxResponseRead = 4 << 10

// roundTrip sends req and reads the answer the way Web Push and UnifiedPush
// services give it: success is any 2xx, an endpoint that is gone is 404 or 410,
// and the rest is sorted into what is worth another try and what is not.
func roundTrip(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
	if err != nil {
		return Transient(cleanError(err), 0)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseRead))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	err = fmt.Errorf("the push service answered %s%s", resp.Status, excerpt(body))
	switch {
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusGone:
		return Gone(err)
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500, resp.StatusCode == http.StatusRequestTimeout:
		return Transient(err, retryAfter(resp.Header.Get("Retry-After"), time.Now()))
	}
	// Any other refusal (a bad request, a rejected signature, a payload too large)
	// will be refused again.
	return Permanent(err)
}

// excerpt returns a short, single-line piece of a response body for an error
// message, or nothing if there is none worth showing.
func excerpt(body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ")
	if s == "" {
		return ""
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return ": " + s
}

// retryAfter reads a Retry-After header, which is a number of seconds or a date.
func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// cleanError removes the URL from a request error. The URL of a push endpoint
// identifies a device and acts as a password for it, so it must not end up in a
// log.
func cleanError(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return fmt.Errorf("%s: %w", uerr.Op, uerr.Err)
	}
	return err
}
