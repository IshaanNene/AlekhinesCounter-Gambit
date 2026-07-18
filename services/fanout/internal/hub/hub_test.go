package hub

import (
	"log/slog"
	"testing"
)

// newTestHub builds a hub with no stream; tests drive broadcast/attach directly.
func newTestHub() *hub {
	return &hub{
		gameID:  "g",
		log:     slog.Default(),
		metrics: Nop{},
		subs:    make(map[*Sub]struct{}),
	}
}

func mv(id, uci string) Delta { return Delta{Type: "move", ID: id, UCI: uci} }

func TestBroadcastReachesAllSubscribers(t *testing.T) {
	h := newTestHub()
	a := h.attach("")
	b := h.attach("")

	h.broadcast(mv("1-0", "e2e4"))

	for name, s := range map[string]*Sub{"a": a, "b": b} {
		select {
		case d := <-s.Deltas():
			if d.UCI != "e2e4" {
				t.Errorf("sub %s got %q, want e2e4", name, d.UCI)
			}
		default:
			t.Errorf("sub %s received nothing", name)
		}
	}
}

func TestInitialSnapshotAndReconnectFilter(t *testing.T) {
	h := newTestHub()
	// Prime some history, then attach fresh vs. reconnecting subscribers.
	h.broadcast(mv("1-0", "e2e4"))
	h.broadcast(mv("2-0", "e7e5"))
	h.broadcast(mv("3-0", "g1f3"))

	fresh := h.attach("")
	if len(fresh.Initial) != 3 {
		t.Fatalf("fresh viewer got %d backlog moves, want 3", len(fresh.Initial))
	}

	reconnect := h.attach("2-0") // already has moves through id 2-0
	if len(reconnect.Initial) != 1 || reconnect.Initial[0].UCI != "g1f3" {
		t.Fatalf("reconnecting viewer backlog = %+v, want just g1f3", reconnect.Initial)
	}
}

func TestSlowSubscriberIsKicked(t *testing.T) {
	h := newTestHub()
	s := h.attach("")

	// Fill the buffer without draining, then one more overflows and drops it.
	for i := 0; i < sendBuffer+1; i++ {
		h.broadcast(mv("x", "e2e4"))
	}

	select {
	case <-s.Kicked():
		// expected
	default:
		t.Fatal("slow subscriber was not kicked after overflowing its buffer")
	}
	if _, ok := h.subs[s]; ok {
		t.Error("kicked subscriber is still registered on the hub")
	}
}

func TestDetachReportsEmpty(t *testing.T) {
	h := newTestHub()
	a := h.attach("")
	b := h.attach("")

	if h.detach(a) {
		t.Error("hub reported empty while one subscriber remained")
	}
	if !h.detach(b) {
		t.Error("hub did not report empty after the last subscriber left")
	}
}

func TestFilterAfter(t *testing.T) {
	hist := []Delta{mv("1-0", "a"), mv("2-0", "b"), mv("3-0", "c")}
	if got := filterAfter(hist, ""); len(got) != 3 {
		t.Errorf("empty from returned %d, want 3", len(got))
	}
	if got := filterAfter(hist, "2-0"); len(got) != 1 || got[0].UCI != "c" {
		t.Errorf("from=2-0 returned %+v, want just c", got)
	}
	if got := filterAfter(hist, "3-0"); len(got) != 0 {
		t.Errorf("from=latest returned %d, want 0", len(got))
	}
}
