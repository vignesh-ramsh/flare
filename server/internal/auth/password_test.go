package auth

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// Cheap parameters, so that tests aren't slow. DefaultParams has its own test.
var fast = Params{Time: 1, MemoryKiB: 64, Threads: 1}

func newFastHasher() *Hasher { return NewHasher(fast, 4) }

func TestHashAndVerify(t *testing.T) {
	h, ctx := newFastHasher(), context.Background()
	hash, err := h.Hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Errorf("hash = %q: the encoded form should carry its parameters", hash)
	}
	if strings.Contains(hash, "correct") {
		t.Error("the hash contains the password")
	}
	for password, want := range map[string]bool{
		"correct horse battery":  true,
		"correct horse batterY":  false,
		"correct horse battery ": false,
		"":                       false,
	} {
		if ok, err := h.Verify(ctx, password, hash); err != nil || ok != want {
			t.Errorf("Verify(%q) = %v, %v; want %v", password, ok, err, want)
		}
	}
}

func TestEveryHashHasItsOwnSalt(t *testing.T) {
	h, ctx := newFastHasher(), context.Background()
	a, _ := h.Hash(ctx, "same password!")
	b, _ := h.Hash(ctx, "same password!")
	if a == b {
		t.Error("hashing the same password twice gave the same hash: the salt is not random")
	}
	for _, hash := range []string{a, b} {
		if ok, _ := h.Verify(ctx, "same password!", hash); !ok {
			t.Error("a salted hash did not verify")
		}
	}
}

func TestVerifyUsesTheParametersStoredInTheHash(t *testing.T) {
	// A hash made with other settings still verifies, which is what makes changing
	// the settings later possible.
	old := NewHasher(Params{Time: 2, MemoryKiB: 128, Threads: 2}, 1)
	hash, _ := old.Hash(context.Background(), "an older password")
	if ok, err := newFastHasher().Verify(context.Background(), "an older password", hash); err != nil || !ok {
		t.Errorf("Verify = %v, %v", ok, err)
	}
}

func TestVerifyRefusesHashesItCannotRead(t *testing.T) {
	h, ctx := newFastHasher(), context.Background()
	good, _ := h.Hash(ctx, "whatever it is")
	parts := strings.Split(good, "$")
	with := func(i int, v string) string {
		c := append([]string(nil), parts...)
		c[i] = v
		return strings.Join(c, "$")
	}
	for name, bad := range map[string]string{
		"empty":                   "",
		"plain text":              "password123",
		"another algorithm":       with(1, "argon2i"),
		"another version":         with(2, "v=16"),
		"missing parameters":      with(3, "m=64,t=1"),
		"unknown parameter":       with(3, "m=64,t=1,p=1,x=1"),
		"not a number":            with(3, "m=a,t=1,p=1"),
		"no memory":               with(3, "m=0,t=1,p=1"),
		"absurd memory":           with(3, "m=4194304,t=1,p=1"),
		"absurd time":             with(3, "m=64,t=1000,p=1"),
		"absurd threads":          with(3, "m=64,t=1,p=200"),
		"salt that is not base64": with(4, "!!!!"),
		"salt that is too short":  with(4, "AAAA"),
		"key that is not base64":  with(5, "!!!!"),
		"key that is too short":   with(5, "AAAA"),
		"too few parts":           strings.Join(parts[:5], "$"),
		"too many parts":          good + "$extra",
	} {
		if ok, err := h.Verify(ctx, "whatever it is", bad); err == nil || ok {
			t.Errorf("%s: Verify = %v, %v; want an error", name, ok, err)
		}
	}
}

func TestDummyIsAValidHashThatMatchesNothingAnyoneKnows(t *testing.T) {
	h, ctx := newFastHasher(), context.Background()
	d := h.Dummy(ctx)
	if h.Dummy(ctx) != d {
		t.Error("Dummy should be computed once")
	}
	if ok, err := h.Verify(ctx, "", d); err != nil || ok {
		t.Errorf("Verify against the dummy = %v, %v; it must be readable and must not match", ok, err)
	}
}

func TestOnlyAFewHashesRunAtOnce(t *testing.T) {
	h := NewHasher(Params{Time: 1, MemoryKiB: 32 * 1024, Threads: 1}, 2)
	var mu sync.Mutex
	running, peak := 0, 0
	var wg sync.WaitGroup
	// Wrap the slot logic to observe how many run together.
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := h.acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			running++
			peak = max(peak, running)
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			running--
			mu.Unlock()
			release()
		}()
	}
	wg.Wait()
	if peak != 2 {
		t.Errorf("up to %d ran at once, want the limit of 2", peak)
	}
}

func TestWaitingForASlotGivesUpWhenTheContextEnds(t *testing.T) {
	h := NewHasher(fast, 1)
	release, _ := h.acquire(context.Background()) // the only slot is taken
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := h.Hash(ctx, "a password!!"); err == nil {
		t.Error("Hash succeeded without a free slot")
	}
	good, _ := newFastHasher().Hash(context.Background(), "a password!!")
	if _, err := h.Verify(ctx, "a password!!", good); err == nil {
		t.Error("Verify succeeded without a free slot")
	}
}

func TestDefaultParamsMeetTheRecommendation(t *testing.T) {
	h := NewHasher(DefaultParams, 1)
	start := time.Now()
	hash, err := h.Hash(context.Background(), "a realistic password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash = %q", hash)
	}
	if took := time.Since(start); took < time.Millisecond || took > 5*time.Second {
		t.Errorf("hashing took %v: not plausible for 19 MiB and two passes", took)
	}
}

func TestValidPassword(t *testing.T) {
	for _, ok := range []string{"0123456789", "a long passphrase with spaces", "pässwörd-ünïcode ✓✓", strings.Repeat("x", MaxPasswordLength)} {
		if err := ValidPassword(ok); err != nil {
			t.Errorf("ValidPassword(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "short", "123456789", strings.Repeat("x", MaxPasswordLength+1), "\xff\xfe\xfd\xfc\xfb\xfa\xf9\xf8\xf7\xf6"} {
		if ValidPassword(bad) == nil {
			t.Errorf("ValidPassword(%q) succeeded", bad)
		}
	}
}
