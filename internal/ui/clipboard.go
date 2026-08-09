package ui

import (
	"errors"

	"github.com/atotto/clipboard"
)

// clipboardWrite is the one place lazysql talks to the system
// clipboard. It is a variable so tests can replace it: a test run must
// never overwrite the developer's real clipboard. The copy/export flows
// will grow around this seam rather than call the library directly.
var clipboardWrite = writeClipboard

func writeClipboard(text string) error {
	// Headless sessions have no pbcopy/xclip to hand the text to.
	if clipboard.Unsupported {
		return errors.New("no clipboard available in this environment")
	}
	return clipboard.WriteAll(text)
}
