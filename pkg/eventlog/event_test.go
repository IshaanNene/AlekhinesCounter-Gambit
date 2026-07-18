package eventlog

import "testing"

// toStrings mirrors what Redis hands back: the field map with string values.
func toStrings(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v.(string)
	}
	return out
}

func TestMoveEventRoundTrip(t *testing.T) {
	cases := []MoveEvent{
		{GameID: "g1", Ply: 1, UCI: "e2e4", FENAfter: "after-e4", Status: "IN_PROGRESS"},
		{GameID: "g2", Ply: 63, UCI: "e7e8q", FENAfter: "promo", Status: "WHITE_WON", EndReason: "CHECKMATE", Ended: true},
		{GameID: "g3", Ply: 0, Status: "IN_PROGRESS"}, // sparse move, zero values
	}
	for _, want := range cases {
		got, err := decodeEvent(toStrings(want.fields()))
		if err != nil {
			t.Fatalf("decodeEvent(%+v): %v", want, err)
		}
		if got != want {
			t.Errorf("round trip mismatch:\n got  %+v\n want %+v", got, want)
		}
	}
}

func TestDecodeEventRejectsBadPly(t *testing.T) {
	if _, err := decodeEvent(map[string]string{"ply": "not-a-number", "uci": "e2e4"}); err == nil {
		t.Fatal("expected an error decoding a non-numeric ply, got nil")
	}
}
