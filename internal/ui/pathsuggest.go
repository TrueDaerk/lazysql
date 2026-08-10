package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"lazysql/internal/pathcomplete"
)

// pathsuggest.go is the form-side glue for the shared path completion engine:
// a tiny state holder the form modal keeps for the field under the cursor when
// that field opted into completion (`withSuggest`). The form refreshes it on
// every edit, applies tab through complete, and renders rows under the field
// while candidates exist.

// maxSuggestLines caps the rendered suggestion rows; the remainder collapses
// into a "+N more" tail so an ambiguous prefix cannot flood the modal. The
// modal shrinks this further when the terminal is too short.
const maxSuggestLines = 8

// pathSuggest holds the live candidate list for one path input.
type pathSuggest struct {
	candidates []string
	// dirs runs the engine's directories-only flavor: an input whose value
	// names a folder must not offer files, because picking one could never
	// produce a valid value. No form field needs it yet.
	dirs bool
}

// query runs the engine in this input's flavor.
func (s *pathSuggest) query(input string) pathcomplete.Result {
	if s.dirs {
		return pathcomplete.Dirs(input)
	}
	return pathcomplete.Complete(input)
}

// refresh recomputes the candidates for the current input.
func (s *pathSuggest) refresh(input string) {
	s.candidates = s.query(input).Candidates
}

// complete applies a tab press: it returns the input extended to the longest
// unambiguous prefix and refreshes the candidates for the new input.
func (s *pathSuggest) complete(input string) string {
	out := s.query(input).Completed
	s.refresh(out)
	return out
}

// active reports whether there is anything to show — and, with it, whether tab
// completes instead of moving the cursor to the next field.
func (s *pathSuggest) active() bool { return len(s.candidates) > 0 }

// clear drops the candidates (field blurred, form closed or submitted).
func (s *pathSuggest) clear() { s.candidates = nil }

// lines returns the rows to render under the field, indented to sit under the
// input. Every candidate shares the typed directory, so only the final path
// component is shown (a directory keeps its trailing separator) — long
// absolute prefixes would otherwise truncate away the distinguishing part.
// maxRows caps the rows including the "+N more" tail; 0 or less renders
// nothing. Empty when there is nothing to suggest.
func (s *pathSuggest) lines(maxRows int) []string {
	n := len(s.candidates)
	if n == 0 || maxRows <= 0 {
		return nil
	}
	if maxRows > maxSuggestLines {
		maxRows = maxSuggestLines
	}
	shown := n
	if shown > maxRows {
		// Leave the last row for the "+N more" tail.
		shown = maxRows - 1
	}
	if shown < 0 {
		shown = 0
	}
	out := make([]string, 0, maxRows)
	for _, c := range s.candidates[:shown] {
		out = append(out, lastPathComponent(c))
	}
	if n > shown {
		out = append(out, fmt.Sprintf("… +%d more", n-shown))
	}
	return out
}

// lastPathComponent is the candidate's final path element, keeping a
// directory's trailing separator.
func lastPathComponent(c string) string {
	sep := string(filepath.Separator)
	if i := strings.LastIndexByte(strings.TrimSuffix(c, sep), filepath.Separator); i >= 0 {
		return c[i+1:]
	}
	return c
}
