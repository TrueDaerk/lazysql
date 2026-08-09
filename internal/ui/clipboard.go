package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/atotto/clipboard"
)

// clipboardWrite is the one place lazysql talks to the system
// clipboard. It is a variable so tests can replace it: a test run must
// never overwrite the developer's real clipboard. The copy/export flows
// grow around this seam rather than call the library directly.
var clipboardWrite = writeClipboard

func writeClipboard(text string) error {
	// Headless sessions have no pbcopy/xclip to hand the text to.
	if clipboard.Unsupported {
		return errors.New("no clipboard available in this environment")
	}
	return clipboard.WriteAll(text)
}

// spillFile is where a failed clipboard write lands. It is a variable
// for the same reason clipboardWrite is: tests must not leave files in
// the developer's temp directory.
var spillFile = writeSpillFile

func writeSpillFile(name, text string) (string, error) {
	f, err := os.CreateTemp("", spillPattern(name))
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.WriteString(f, text); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// spillPattern turns a copy's subject into a recognizable temp file
// name, keeping the extension after the random part so the file opens
// in the right thing.
func spillPattern(name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, base)
	if base == "" {
		base = "copy"
	}
	return "lazysql-" + base + "-*" + ext
}

// copyOut is every copy in the app: it puts text on the system
// clipboard and, when there is no clipboard to put it on — an SSH
// session, a bare tty, a container — spills it to a temp file and
// reports the path instead. A copy therefore never simply fails; the
// worst case is that the user has to reach for the file.
//
// The returned line is ready for the command log.
func copyOut(subject, filename, text string) string {
	err := clipboardWrite(text)
	if err == nil {
		return fmt.Sprintf("-- copy %s to clipboard (%s)", subject, byteCount(len(text)))
	}
	path, spillErr := spillFile(filename, text)
	if spillErr != nil {
		return fmt.Sprintf("-- copy %s FAILED: %v (and no temp file: %v)", subject, err, spillErr)
	}
	return fmt.Sprintf("-- copy %s: no clipboard (%v) — wrote %s to %s", subject, err, byteCount(len(text)), path)
}

// byteCount renders a size the way the command log wants it: exact for
// anything small enough to read, rounded once it stops being useful.
func byteCount(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d bytes", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	}
}
