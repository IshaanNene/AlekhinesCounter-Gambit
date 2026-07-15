package pubsub

import (
	"context"
	"testing"
	"time"

	"github.com/IshaanNene/AlekhinesCounter-Gambit/services/gateway/graph/model"
)

func game(id, fen string) *model.Game { return &model.Game{ID: id, Fen: fen} }

func TestPublishReachesAllSubscribersOfThatGame(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := NewMemory()

	a, err := b.Subscribe(ctx, "g1")
	if err != nil {
		t.Fatal(err)
	}
	c, err := b.Subscribe(ctx, "g1")
	if err != nil {
		t.Fatal(err)
	}
	// A subscriber on a different game must not receive g1's updates.
	other, err := b.Subscribe(ctx, "g2")
	if err != nil {
		t.Fatal(err)
	}

	if err := b.Publish(ctx, "g1", game("g1", "fen1")); err != nil {
		t.Fatal(err)
	}

	for i, ch := range []<-chan *model.Game{a, c} {
		select {
		case got := <-ch:
			if got.Fen != "fen1" {
				t.Errorf("subscriber %d: got fen %q, want %q", i, got.Fen, "fen1")
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out waiting for update", i)
		}
	}
	select {
	case got := <-other:
		t.Errorf("g2 subscriber received a g1 update: %+v", got)
	case <-time.After(50 * time.Millisecond):
		// expected: no delivery
	}
}

func TestCancelUnsubscribesAndClosesChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	b := NewMemory()

	ch, err := b.Subscribe(ctx, "g1")
	if err != nil {
		t.Fatal(err)
	}
	if n := b.SubscriberCount("g1"); n != 1 {
		t.Fatalf("SubscriberCount = %d, want 1", n)
	}

	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("channel was not closed after cancel")
	}

	// The registry must drop the entry so games do not leak subscribers.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if b.SubscriberCount("g1") == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("SubscriberCount = %d after cancel, want 0", b.SubscriberCount("g1"))
}

// A client that never drains must not block the publisher: once its buffer is
// full, updates are dropped rather than stalling a move.
func TestSlowSubscriberDoesNotBlockPublisher(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := NewMemory()

	if _, err := b.Subscribe(ctx, "g1"); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < subscriberBuffer*10; i++ {
			_ = b.Publish(ctx, "g1", game("g1", "fen"))
		}
	}()

	select {
	case <-done:
		// expected: publishing never blocked
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
}

func TestPublishWithNoSubscribersIsNoop(t *testing.T) {
	b := NewMemory()
	if err := b.Publish(context.Background(), "nobody", game("nobody", "fen")); err != nil {
		t.Errorf("Publish with no subscribers returned error: %v", err)
	}
}
