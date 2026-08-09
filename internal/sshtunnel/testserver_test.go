package sshtunnel

import (
	"errors"
	"io"
	"net"

	"lazysql/internal/sshtunnel/sshtest"
)

// The tests in this package run a real SSH server in-process (see the sshtest
// package). Nothing is stubbed on the protocol side: a tunnel is opened
// against it, a forwarded connection carries bytes to a second in-process TCP
// server, and teardown is observed from both ends. That is the only way to
// check the two things this package promises — that host keys are enforced and
// that Close leaves nothing behind.

// isClosedErr reports the "everything already shut down" errors teardown
// tests are allowed to see.
func isClosedErr(err error) bool {
	return err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) ||
		errors.Is(err, ErrTunnelClosed)
}

// Aliases keep the test bodies short.
type testSSHServer = sshtest.Server
