#!/usr/bin/env python3
"""Drive a command in a real PTY, feed it key sequences, dump the screen.

Usage: ptycheck.py <cols> <rows> <cmd> [args...]  — key sequences are read
from stdin as python-escaped lines, one per step, and the last screen is
printed to stdout.
"""
import os
import pty
import select
import sys
import time

cols, rows = int(sys.argv[1]), int(sys.argv[2])
argv = sys.argv[3:]
steps = [l.rstrip("\n").encode().decode("unicode_escape") for l in sys.stdin if l.strip()]

pid, fd = pty.fork()
if pid == 0:
    os.environ["TERM"] = "xterm-256color"
    os.environ["LINES"], os.environ["COLUMNS"] = str(rows), str(cols)
    os.execvp(argv[0], argv)

import fcntl
import struct
import termios

fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))

buf = b""


def pump(seconds):
    global buf
    end = time.time() + seconds
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.05)
        if fd in r:
            try:
                data = os.read(fd, 65536)
            except OSError:
                return
            if not data:
                return
            buf += data


pump(2.0)
for s in steps:
    os.write(fd, s.encode())
    pump(1.0)

import signal

try:
    os.kill(pid, signal.SIGKILL)
except OSError:
    pass

# The renderer only writes the cells that changed, so the raw stream is not
# the screen: feed it through a terminal emulator to see what the user sees.
try:
    import pyte

    screen = pyte.Screen(cols, rows)
    stream = pyte.Stream(screen)
    stream.feed(buf.decode("utf-8", "replace"))
    sys.stdout.write("\n".join(screen.display) + "\n")
except ImportError:
    sys.stdout.write(buf.decode("utf-8", "replace"))
