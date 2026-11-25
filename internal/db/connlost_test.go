package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"
)

func TestConnectionLost(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"statement error", errors.New(`relation "nope" does not exist`), false},
		{"cancelled", context.Canceled, false},
		{"timed out", fmt.Errorf("query: %w", context.DeadlineExceeded), false},
		{"read-only refusal", ErrReadOnly, false},
		{"bad connection", driver.ErrBadConn, true},
		{"connection done", fmt.Errorf("exec: %w", sql.ErrConnDone), true},
		{"peer closed", io.EOF, true},
		{"wrapped unexpected EOF", fmt.Errorf("read: %w", io.ErrUnexpectedEOF), true},
		{"closed transport", net.ErrClosed, true},
		{"reset", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, true},
		{"broken pipe", &net.OpError{Op: "write", Err: syscall.EPIPE}, true},
		{"refused redial", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true},
		{"failed redial", &net.OpError{Op: "dial", Err: errors.New("ssh: tunnel is closed")}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConnectionLost(tc.err); got != tc.want {
				t.Fatalf("ConnectionLost(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
