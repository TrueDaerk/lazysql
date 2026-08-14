package ui

import (
	"os"
	"strings"
	"testing"
)

// keybindingsDoc is the user documentation's keybinding reference. It is
// meant to be the complete, published copy of the same table `?` renders,
// so a binding added to slots() without a line in it is a documentation
// bug — the one drift the reference cannot survive.
const keybindingsDoc = "../../userdocs/reference/keybindings.md"

func TestKeybindingsReferenceDocumentsEveryAction(t *testing.T) {
	data, err := os.ReadFile(keybindingsDoc)
	if err != nil {
		t.Fatalf("read %s: %v", keybindingsDoc, err)
	}
	doc := string(data)

	var km keyMap
	for _, s := range km.slots() {
		// The action names are rendered as inline code in the reference's
		// "Action name" column, so the backticks are part of the match:
		// `up` must not be satisfied by the word "up" in a sentence.
		if !strings.Contains(doc, "`"+s.name+"`") {
			t.Errorf("action %q has no entry in %s", s.name, keybindingsDoc)
		}
	}
}
