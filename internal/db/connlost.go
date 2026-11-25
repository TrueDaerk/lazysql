package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/go-sql-driver/mysql"
)

// ConnectionLost reports whether err says the connection to the server is
// gone — closed by the peer, reset, refused on the reconnect, or carried
// by a transport that no longer exists — rather than that a statement
// failed on a connection that still works.
//
// It errs towards false. A caller acts on true by giving the session up,
// so a timeout or a cancellation must not count: the query was stopped,
// the connection was not.
func ConnectionLost(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) ||
		errors.Is(err, mysql.ErrInvalidConn) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	for _, errno := range []syscall.Errno{
		syscall.ECONNRESET, syscall.ECONNREFUSED, syscall.ECONNABORTED,
		syscall.EPIPE, syscall.ENETUNREACH, syscall.EHOSTUNREACH,
	} {
		if errors.Is(err, errno) {
			return true
		}
	}
	// A dial that failed outright: the pool tried to replace a dead
	// connection and could not reach the server at all.
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial" && !op.Timeout()
}
