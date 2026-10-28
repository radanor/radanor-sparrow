package windowseventlog

import "testing"

func TestAllowlistAndMappings(t *testing.T) {
	for _, id := range []uint32{1006, 1116, 1117, 1118, 1119, 5001, 5007} {
		if !allowed(id) || eventType(id) == "" {
			t.Fatalf("allowlisted event %d has no mapping", id)
		}
	}
	for _, id := range []uint32{0, 1, 4688, 9999} {
		if allowed(id) {
			t.Fatalf("unexpected event %d allowed", id)
		}
	}
}
