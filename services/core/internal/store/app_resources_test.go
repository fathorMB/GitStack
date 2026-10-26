package store

import "testing"

func TestCapacityForHardware(t *testing.T) {
	c := capacityForHardware(AppResourceAmount{CPUMillis: 4000, MemoryBytes: 8 << 30})
	if c.Share.CPUMillis != 2000 || c.Share.MemoryBytes != 4<<30 {
		t.Fatalf("share: %+v", c.Share)
	}
}
