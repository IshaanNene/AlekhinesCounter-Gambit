package redisx

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// uniqueTC gives each test its own queue so they cannot interfere.
func uniqueTC(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("test-%s-%d", t.Name(), time.Now().UnixNano())
}

func TestMatchmakingPairsTwoPlayers(t *testing.T) {
	ctx := context.Background()
	m := NewMatchmaking(testClient(t))
	tc := uniqueTC(t)
	t.Cleanup(func() { m.client.Del(ctx, queueKey(tc), waitKey(tc)) })

	// First player finds nobody and waits.
	opp, paired, _, err := m.Enqueue(ctx, "alice", 1200, tc)
	if err != nil {
		t.Fatal(err)
	}
	if paired {
		t.Fatalf("alice should have no opponent yet, got %q", opp)
	}

	// Second player, similar rating, pairs with the first.
	opp, paired, _, err = m.Enqueue(ctx, "bob", 1220, tc)
	if err != nil {
		t.Fatal(err)
	}
	if !paired || opp != "alice" {
		t.Fatalf("bob should pair with alice, got %q paired=%v", opp, paired)
	}

	// Both must be gone from the queue: a matched player left waiting would end
	// up in two games.
	if n, _ := m.QueueDepth(ctx, tc); n != 0 {
		t.Errorf("queue depth = %d after pairing, want 0", n)
	}
}

// A fresh 900 and a fresh 2400 are far outside each other's starting band and
// must not be thrown together just because they are the only two waiting.
func TestMatchmakingRefusesDistantRatings(t *testing.T) {
	ctx := context.Background()
	m := NewMatchmaking(testClient(t))
	tc := uniqueTC(t)
	t.Cleanup(func() { m.client.Del(ctx, queueKey(tc), waitKey(tc)) })

	if _, paired, _, _ := m.Enqueue(ctx, "beginner", 900, tc); paired {
		t.Fatal("first player cannot pair with an empty queue")
	}
	_, paired, _, err := m.Enqueue(ctx, "master", 2400, tc)
	if err != nil {
		t.Fatal(err)
	}
	if paired {
		t.Error("a 900 and a 2400 should not be paired on arrival")
	}
	if n, _ := m.QueueDepth(ctx, tc); n != 2 {
		t.Errorf("queue depth = %d, want both still waiting", n)
	}
}

// Given several candidates in band, take the closest rating, not the first.
func TestMatchmakingPicksNearestRating(t *testing.T) {
	ctx := context.Background()
	m := NewMatchmaking(testClient(t))
	tc := uniqueTC(t)
	t.Cleanup(func() { m.client.Del(ctx, queueKey(tc), waitKey(tc)) })

	// The candidates must be within the seeker's band but outside each other's,
	// or they pair with each other before the seeker ever arrives (which is
	// correct behaviour, just not what this test is measuring).
	//   far  1280 → 80 from seeker
	//   near 1130 → 70 from seeker, and 150 from far, so the two do not pair.
	if _, paired, _, _ := m.Enqueue(ctx, "far", 1280, tc); paired {
		t.Fatal("far should find an empty queue")
	}
	if _, paired, _, _ := m.Enqueue(ctx, "near", 1130, tc); paired {
		t.Fatal("near and far are 150 apart and must not pair")
	}

	opp, paired, _, err := m.Enqueue(ctx, "seeker", 1200, tc)
	if err != nil {
		t.Fatal(err)
	}
	if !paired || opp != "near" {
		t.Errorf("paired with %q, want the nearest rating (near, 70 away vs far's 80)", opp)
	}
}

func TestMatchmakingLeaveRemovesPlayer(t *testing.T) {
	ctx := context.Background()
	m := NewMatchmaking(testClient(t))
	tc := uniqueTC(t)
	t.Cleanup(func() { m.client.Del(ctx, queueKey(tc), waitKey(tc)) })

	m.Enqueue(ctx, "quitter", 1500, tc)
	if err := m.Leave(ctx, "quitter", tc); err != nil {
		t.Fatal(err)
	}
	if n, _ := m.QueueDepth(ctx, tc); n != 0 {
		t.Errorf("queue depth = %d after leaving, want 0", n)
	}
	// Someone else must not then pair with the departed player.
	if _, paired, _, _ := m.Enqueue(ctx, "other", 1500, tc); paired {
		t.Error("paired with a player who left the queue")
	}
}

// The property that matters most: with N players rushing the queue at once,
// every pairing must be mutual and nobody may be handed to two opponents.
func TestMatchmakingIsAtomicUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	m := NewMatchmaking(testClient(t))
	tc := uniqueTC(t)
	t.Cleanup(func() { m.client.Del(ctx, queueKey(tc), waitKey(tc)) })

	const players = 20
	var mu sync.Mutex
	pairs := map[string]string{} // matcher -> opponent

	var wg sync.WaitGroup
	for i := 0; i < players; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			me := fmt.Sprintf("p%02d", i)
			// All within one band, so everyone is a valid opponent for everyone.
			opp, paired, _, err := m.Enqueue(ctx, me, 1500, tc)
			if err != nil {
				t.Errorf("enqueue %s: %v", me, err)
				return
			}
			if paired {
				mu.Lock()
				pairs[me] = opp
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	// Nobody may appear as an opponent twice.
	seen := map[string]string{}
	for matcher, opp := range pairs {
		if prev, dup := seen[opp]; dup {
			t.Errorf("%s was paired with both %s and %s", opp, prev, matcher)
		}
		seen[opp] = matcher
		if _, alsoMatched := pairs[opp]; alsoMatched {
			t.Errorf("%s both matched someone and was matched by %s", opp, matcher)
		}
	}

	// Every player is either paired, or still waiting — never lost.
	depth, _ := m.QueueDepth(ctx, tc)
	accounted := int64(len(pairs)*2) + depth
	if accounted != players {
		t.Errorf("accounted for %d of %d players (%d pairs + %d waiting)",
			accounted, players, len(pairs), depth)
	}
}

func TestMatchmakingSeparatesTimeControls(t *testing.T) {
	ctx := context.Background()
	m := NewMatchmaking(testClient(t))
	blitz := uniqueTC(t) + "-blitz"
	classical := uniqueTC(t) + "-classical"
	t.Cleanup(func() {
		m.client.Del(ctx, queueKey(blitz), waitKey(blitz), queueKey(classical), waitKey(classical))
	})

	m.Enqueue(ctx, "blitzer", 1500, blitz)
	// Same rating, different time control: must not be pulled into the blitz game.
	if _, paired, _, _ := m.Enqueue(ctx, "classicist", 1500, classical); paired {
		t.Error("players on different time controls must not be paired")
	}
}

func TestNilMatchmakingDegrades(t *testing.T) {
	m := NewMatchmaking(nil)
	if m.Enabled() {
		t.Error("nil client should report disabled")
	}
	if _, paired, _, err := m.Enqueue(context.Background(), "x", 1200, "tc"); paired || err != nil {
		t.Errorf("disabled matchmaking should no-op, got paired=%v err=%v", paired, err)
	}
}
