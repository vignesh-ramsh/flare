package broker

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/message"
)

func msg(topic, body string) message.Message {
	return message.Message{Topic: topic, Message: body, Event: message.EventMessage}
}

func next(t *testing.T, s Subscription) message.Message {
	t.Helper()
	select {
	case m, ok := <-s.C():
		if !ok {
			t.Fatalf("subscription ended early: %v", s.Err())
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a message")
		return message.Message{}
	}
}

func expectNothing(t *testing.T, s Subscription) {
	t.Helper()
	select {
	case m, ok := <-s.C():
		t.Fatalf("unexpected delivery: %+v (open: %v)", m, ok)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDeliversInOrderToEverySubscriber(t *testing.T) {
	b := NewInProc()
	s1, s2 := b.Subscribe([]string{"t"}, 10), b.Subscribe([]string{"t"}, 10)
	defer s1.Close()
	defer s2.Close()
	for i := range 3 {
		b.Publish(msg("t", fmt.Sprint(i)))
	}
	for _, s := range []Subscription{s1, s2} {
		for i := range 3 {
			if got := next(t, s).Message; got != fmt.Sprint(i) {
				t.Errorf("got %q, want %d", got, i)
			}
		}
	}
}

func TestOnlyMatchingTopicsAreDelivered(t *testing.T) {
	b := NewInProc()
	s := b.Subscribe([]string{"a", "b"}, 10)
	defer s.Close()
	b.Publish(msg("c", "no"))
	b.Publish(msg("a", "yes-a"))
	b.Publish(msg("b", "yes-b"))
	if got := next(t, s).Message; got != "yes-a" {
		t.Errorf("got %q", got)
	}
	if got := next(t, s).Message; got != "yes-b" {
		t.Errorf("got %q", got)
	}
	expectNothing(t, s)
}

func TestListeningToTheSameTopicTwiceDeliversOnce(t *testing.T) {
	b := NewInProc()
	s := b.Subscribe([]string{"a", "a"}, 10)
	defer s.Close()
	b.Publish(msg("a", "once"))
	next(t, s)
	expectNothing(t, s)
}

func TestSlowSubscriberIsEndedWithoutBlockingOthers(t *testing.T) {
	b := NewInProc()
	slow, fast := b.Subscribe([]string{"t"}, 2), b.Subscribe([]string{"t"}, 100)
	defer fast.Close()

	done := make(chan struct{})
	go func() {
		for i := range 10 { // never read from slow
			b.Publish(msg("t", fmt.Sprint(i)))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}

	for i := range 10 {
		if got := next(t, fast).Message; got != fmt.Sprint(i) {
			t.Errorf("fast subscriber got %q, want %d", got, i)
		}
	}
	// The slow one gets what fitted in its buffer, then is ended as lagging.
	next(t, slow)
	next(t, slow)
	if _, ok := <-slow.C(); ok {
		t.Fatal("slow subscription should be closed after its buffered messages")
	}
	if slow.Err() != ErrLagged {
		t.Errorf("Err = %v, want ErrLagged", slow.Err())
	}
}

func TestCloseEndsOnlyThatSubscription(t *testing.T) {
	b := NewInProc()
	a, c := b.Subscribe([]string{"t"}, 10), b.Subscribe([]string{"t"}, 10)
	defer c.Close()
	a.Close()
	a.Close() // safe to repeat
	if _, ok := <-a.C(); ok {
		t.Error("closed subscription still open")
	}
	if a.Err() != nil {
		t.Errorf("Err after the listener's own Close = %v, want nil", a.Err())
	}
	b.Publish(msg("t", "x"))
	next(t, c)
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.topics["t"]) != 1 {
		t.Errorf("%d subscriptions registered, want 1", len(b.topics["t"]))
	}
}

func TestEndedSubscriptionsAreForgotten(t *testing.T) {
	b := NewInProc()
	for range 100 {
		b.Subscribe([]string{"a", "b"}, 1).Close()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.topics) != 0 {
		t.Errorf("broker still tracks %d topics after every subscription closed", len(b.topics))
	}
}

func TestBrokerCloseEndsEverythingAndRefusesNewSubscribers(t *testing.T) {
	b := NewInProc()
	s := b.Subscribe([]string{"t"}, 10)
	b.Close()
	if _, ok := <-s.C(); ok || s.Err() != ErrClosed {
		t.Errorf("existing subscription: open=%v err=%v, want closed with ErrClosed", ok, s.Err())
	}
	late := b.Subscribe([]string{"t"}, 10)
	if _, ok := <-late.C(); ok || late.Err() != ErrClosed {
		t.Errorf("new subscription: open=%v err=%v, want closed with ErrClosed", ok, late.Err())
	}
	late.Close() // must not panic
	b.Publish(msg("t", "after close"))
}

func TestConcurrentUseIsSafe(t *testing.T) {
	b := NewInProc()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 200 {
				b.Publish(msg("t", "x"))
			}
		}()
		go func() {
			defer wg.Done()
			for range 50 {
				s := b.Subscribe([]string{"t"}, 4)
				for range 3 {
					select {
					case <-s.C():
					case <-time.After(time.Millisecond):
					}
				}
				s.Close()
			}
		}()
	}
	wg.Wait()
}
