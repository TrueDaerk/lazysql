package db

import (
	"strings"
	"testing"
)

func TestRedactSecretsMasksPasswords(t *testing.T) {
	cases := map[string]string{
		"dial root:s3cret@tcp(db:3306)/app failed":             "dial root:***@tcp(db:3306)/app failed",
		"open postgres://bob:hunter2@host:5432/db: refused":    "open postgres://bob:***@host:5432/db: refused",
		"host=x user=u password=topsecret dbname=d":            "host=x user=u password=*** dbname=d",
		"write tcp 10.0.0.1:58858->10.0.0.2:3306: broken pipe": "write tcp 10.0.0.1:58858->10.0.0.2:3306: broken pipe",
	}
	for in, want := range cases {
		if got := RedactSecrets(in); got != want {
			t.Errorf("RedactSecrets(%q) = %q, want %q", in, got, want)
		}
	}
}

// A burst past the queue's capacity never blocks the logging goroutine;
// the next line delivered says how many were lost, once.
func TestDriverMessagesDropInsteadOfBlocking(t *testing.T) {
	drainDriverMessages()
	for i := 0; i < driverMessageBuffer+5; i++ {
		emitDriverMessage("mysql", "boom", nil)
	}
	if m := NextDriverMessage(); !strings.Contains(m.Text, "(5 more lines dropped)") {
		t.Fatalf("first line = %q, want the drop count", m.Text)
	}
	if m := NextDriverMessage(); m.Text != "boom" {
		t.Fatalf("second line = %q, want it unchanged", m.Text)
	}
	drainDriverMessages()
}

// drainDriverMessages empties the queue other tests may have filled.
func drainDriverMessages() {
	for {
		select {
		case <-driverMessages:
		default:
			droppedMu.Lock()
			dropped = 0
			droppedMu.Unlock()
			return
		}
	}
}
