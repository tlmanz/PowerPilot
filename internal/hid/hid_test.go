package hid

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Talks to the real UPS, so it only runs with UPS_HARDWARE=1. Read-only queries.
func TestHardwareQuery(t *testing.T) {
	if os.Getenv("UPS_HARDWARE") != "1" {
		t.Skip("set UPS_HARDWARE=1 to query the attached UPS")
	}
	d, err := Open(0x0665, 0x5161)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	for _, cmd := range []string{"QS", "F"} {
		reply, err := d.Exchange(cmd+"\r", 2*time.Second)
		if err != nil {
			t.Fatalf("%s: %v (partial %q)", cmd, err, reply)
		}
		if !strings.HasPrefix(reply, "(") && !strings.HasPrefix(reply, "#") {
			t.Fatalf("%s: unexpected reply %q", cmd, reply)
		}
		t.Logf("%s -> %q", cmd, reply)
	}
}
