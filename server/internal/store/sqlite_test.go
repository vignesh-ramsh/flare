package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/id"
	"github.com/vignesh-ramsh/flare/server/internal/message"
)

var base = time.Unix(1_800_000_000, 0)

func open(t *testing.T) *SQLite {
	t.Helper()
	s, err := OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "flare.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// seq makes messages with increasing IDs, the way the hub does.
type seq struct{ g id.Generator }

func (q *seq) msg(topic, body string, at time.Time, ttl time.Duration) message.Message {
	return message.Message{
		ID: q.g.New(at), Time: at.Unix(), Expires: at.Add(ttl).Unix(),
		Event: message.EventMessage, Topic: topic, Message: body, Priority: 3,
	}
}

func insert(t *testing.T, s Store, ms ...message.Message) {
	t.Helper()
	for _, m := range ms {
		if err := s.Insert(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
}

func bodies(ms []message.Message) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.Message)
	}
	return out
}

func TestRoundTripKeepsEveryField(t *testing.T) {
	s := open(t)
	var q seq
	m := q.msg("backups", "line one\nline two — ünïcode ✓", base, time.Hour)
	m.Title, m.Priority, m.Click = "Backup failed", 5, "https://example.com/logs?run=1&x=2"
	m.Tags = []string{"warning", "server-01"}
	insert(t, s, m)

	got, err := s.After(context.Background(), []string{"backups"}, "", base, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("After = %v, %v", got, err)
	}
	if !reflect.DeepEqual(got[0], m) {
		t.Errorf("stored message changed:\n got  %+v\n want %+v", got[0], m)
	}
}

func TestEmptyTagsComeBackAsNil(t *testing.T) {
	s := open(t)
	var q seq
	insert(t, s, q.msg("t", "x", base, time.Hour))
	got, _ := s.After(context.Background(), []string{"t"}, "", base, 10)
	if got[0].Tags != nil {
		t.Errorf("Tags = %#v, want nil", got[0].Tags)
	}
}

func TestAfterReturnsMessagesInOrderAfterTheGivenID(t *testing.T) {
	s := open(t)
	var q seq
	var all []message.Message
	for i := range 5 {
		all = append(all, q.msg("t", fmt.Sprint("m", i), base, time.Hour))
	}
	insert(t, s, all...)
	ctx := context.Background()

	got, _ := s.After(ctx, []string{"t"}, "", base, 100)
	if want := []string{"m0", "m1", "m2", "m3", "m4"}; !reflect.DeepEqual(bodies(got), want) {
		t.Errorf("all = %v, want %v", bodies(got), want)
	}
	got, _ = s.After(ctx, []string{"t"}, all[1].ID, base, 100)
	if want := []string{"m2", "m3", "m4"}; !reflect.DeepEqual(bodies(got), want) {
		t.Errorf("after m1 = %v, want %v", bodies(got), want)
	}
	got, _ = s.After(ctx, []string{"t"}, all[4].ID, base, 100)
	if len(got) != 0 {
		t.Errorf("after the last message = %v, want nothing", bodies(got))
	}
}

func TestAfterWorksWithAnIDThatWasNeverStored(t *testing.T) {
	// A client may hold the ID of a message that has since expired, or a
	// boundary built from a time.
	s := open(t)
	var q seq
	old := q.msg("t", "old", base, time.Hour)
	recent := q.msg("t", "recent", base.Add(10*time.Minute), time.Hour)
	insert(t, s, old, recent)

	got, _ := s.After(context.Background(), []string{"t"}, id.Boundary(base.Add(5*time.Minute)), base, 10)
	if want := []string{"recent"}; !reflect.DeepEqual(bodies(got), want) {
		t.Errorf("got %v, want %v", bodies(got), want)
	}
}

func TestAfterOnlyReturnsTheRequestedTopics(t *testing.T) {
	s := open(t)
	var q seq
	insert(t, s, q.msg("a", "a1", base, time.Hour), q.msg("b", "b1", base, time.Hour),
		q.msg("c", "c1", base, time.Hour), q.msg("a", "a2", base, time.Hour))
	ctx := context.Background()

	got, _ := s.After(ctx, []string{"a"}, "", base, 10)
	if want := []string{"a1", "a2"}; !reflect.DeepEqual(bodies(got), want) {
		t.Errorf("topic a = %v, want %v", bodies(got), want)
	}
	got, _ = s.After(ctx, []string{"a", "c"}, "", base, 10)
	if want := []string{"a1", "c1", "a2"}; !reflect.DeepEqual(bodies(got), want) {
		t.Errorf("topics a+c = %v, want %v (interleaved in ID order)", bodies(got), want)
	}
	if got, _ := s.After(ctx, nil, "", base, 10); len(got) != 0 {
		t.Errorf("no topics = %v, want nothing", bodies(got))
	}
	if got, _ := s.After(ctx, []string{"nope"}, "", base, 10); len(got) != 0 {
		t.Errorf("unknown topic = %v, want nothing", bodies(got))
	}
}

func TestTopicsAreNotMatchedAsPatterns(t *testing.T) {
	s := open(t)
	var q seq
	insert(t, s, q.msg("abc", "x", base, time.Hour), q.msg("a_c", "y", base, time.Hour))
	got, _ := s.After(context.Background(), []string{"a_c"}, "", base, 10)
	if want := []string{"y"}; !reflect.DeepEqual(bodies(got), want) {
		t.Errorf("got %v, want %v (an underscore must not act as a wildcard)", bodies(got), want)
	}
}

func TestLimitAllowsPaging(t *testing.T) {
	s := open(t)
	var q seq
	for i := range 7 {
		insert(t, s, q.msg("t", fmt.Sprint(i), base, time.Hour))
	}
	var seen []string
	after := ""
	for {
		page, err := s.After(context.Background(), []string{"t"}, after, base, 3)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page {
			seen = append(seen, m.Message)
			after = m.ID
		}
		if len(page) < 3 {
			break
		}
	}
	if want := []string{"0", "1", "2", "3", "4", "5", "6"}; !reflect.DeepEqual(seen, want) {
		t.Errorf("paged = %v, want %v", seen, want)
	}
}

func TestExpiredMessagesAreHiddenAndPurged(t *testing.T) {
	s := open(t)
	var q seq
	insert(t, s,
		q.msg("t", "expired", base.Add(-2*time.Hour), time.Hour),
		q.msg("t", "just expired", base.Add(-time.Hour), time.Hour), // expires exactly now
		q.msg("t", "alive", base.Add(-time.Minute), time.Hour),
	)
	ctx := context.Background()

	got, _ := s.After(ctx, []string{"t"}, "", base, 10)
	if want := []string{"alive"}; !reflect.DeepEqual(bodies(got), want) {
		t.Errorf("visible = %v, want %v", bodies(got), want)
	}
	n, err := s.DeleteExpired(ctx, base)
	if err != nil || n != 2 {
		t.Errorf("DeleteExpired = %d, %v; want 2 deleted", n, err)
	}
	if n, _ := s.DeleteExpired(ctx, base); n != 0 {
		t.Errorf("second purge deleted %d, want 0", n)
	}
	got, _ = s.After(ctx, []string{"t"}, "", base.Add(-3*time.Hour), 10) // time travel: alive was kept
	if len(got) != 1 || got[0].Message != "alive" {
		t.Errorf("after purge = %v, want only \"alive\"", bodies(got))
	}
}

func TestDuplicateIDIsRejected(t *testing.T) {
	s := open(t)
	var q seq
	m := q.msg("t", "x", base, time.Hour)
	insert(t, s, m)
	if err := s.Insert(context.Background(), m); err == nil {
		t.Error("inserting the same ID twice succeeded")
	}
}

func TestDataSurvivesReopeningAndMigrationsRunOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flare.db")
	ctx := context.Background()
	var q seq

	s, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	insert(t, s, q.msg("t", "kept", base, time.Hour))
	s.Close()

	s, err = OpenSQLite(ctx, path) // runs migrations again; must be a no-op
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, _ := s.After(ctx, []string{"t"}, "", base, 10)
	if len(got) != 1 || got[0].Message != "kept" {
		t.Errorf("after reopening got %v, want [kept]", bodies(got))
	}
}

func TestOpenCreatesMissingFolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "flare.db")
	s, err := OpenSQLite(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("database file was not created: %v", err)
	}
}

func TestPathsWithSpecialCharactersWork(t *testing.T) {
	for _, name := range []string{"with space.db", "what?.db", "hash#.db", "100%.db", "ünï.db"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := OpenSQLite(context.Background(), filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Errorf("expected the database at exactly that path: %v", err)
			}
		})
	}
}

func TestOpenFailsClearlyOnAnUnusableLocation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plain-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSQLite(context.Background(), filepath.Join(file, "flare.db")); err == nil {
		t.Error("opening a database below a regular file succeeded")
	}
}

func TestConcurrentWritersAndReaders(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	var (
		mu sync.Mutex
		q  seq
		wg sync.WaitGroup
	)
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 25 {
				mu.Lock() // the hub serialises ID creation and insertion the same way
				m := q.msg("t", fmt.Sprintf("w%d-%d", w, i), base, time.Hour)
				err := s.Insert(ctx, m)
				mu.Unlock()
				if err != nil {
					t.Error(err)
					return
				}
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				if _, err := s.After(ctx, []string{"t"}, "", base, 1000); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	got, err := s.After(ctx, []string{"t"}, "", base, 1000)
	if err != nil || len(got) != 100 {
		t.Fatalf("stored %d messages, %v; want 100", len(got), err)
	}
}

func TestPing(t *testing.T) {
	s := open(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Errorf("Ping on an open store: %v", err)
	}
	s.Close()
	if err := s.Ping(context.Background()); err == nil {
		t.Error("Ping on a closed store succeeded")
	}
}
