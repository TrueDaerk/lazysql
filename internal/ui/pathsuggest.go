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
	// cycling is true once tab has run out of prefix to extend and started
	// stepping through candidates one at a time instead; selected is the
	// index currently applied to the input. Any fresh keystroke (refresh)
	// cancels a cycle — it means the user typed instead of continuing it.
	cycling  bool
	selected int
}

// query runs the engine in this input's flavor.
func (s *pathSuggest) query(input string) pathcomplete.Result {
	if s.dirs {
		return pathcomplete.Dirs(input)
	}
	return pathcomplete.Complete(input)
}

// refresh recomputes the candidates for the current input and cancels any
// in-progress cycle.
func (s *pathSuggest) refresh(input string) {
	s.candidates = s.query(input).Candidates
	s.cycling = false
}

// complete applies a tab press. It first extends the input to the longest
// prefix shared by every candidate, same as shell tab completion. Once that
// prefix is already the whole input — an ambiguous prefix with nothing left
// to extend — repeated tab presses instead cycle through the candidates one
// at a time, so an ambiguous match can still be resolved from the keyboard
// without touching the mouse or retyping.
func (s *pathSuggest) complete(input string) string {
	if s.cycling {
		return s.step(1)
	}
	out := s.query(input).Completed
	if out == input && len(s.candidates) > 1 {
		s.cycling = true
		s.selected = 0
		return s.candidates[0]
	}
	s.refresh(out)
	return out
}

// completeBack cycles backward — shift+tab's meaning while a cycle from
// complete is already in progress. Callers should only invoke this when
// cycling is true; the form falls back to its usual "previous field"
// binding otherwise.
func (s *pathSuggest) completeBack() string { return s.step(-1) }

// navigate moves the highlighted candidate by delta — ↓/↑'s meaning while
// the list is up, sharing the same selected index tab-cycling uses instead
// of tracking a second one. The first press with nothing highlighted yet
// starts cycling from whichever end delta points at, so a single down (or
// up) always lands on a candidate rather than skipping past one.
func (s *pathSuggest) navigate(delta int) string {
	if len(s.candidates) == 0 {
		return ""
	}
	if !s.cycling {
		s.cycling = true
		if delta < 0 {
			s.selected = len(s.candidates) - 1
		} else {
			s.selected = 0
		}
		return s.candidates[s.selected]
	}
	return s.step(delta)
}

// accept returns the candidate enter should apply to the field: the one
// already highlighted by a cycle or a navigate, or the first candidate when
// the list is up but nothing has been highlighted yet.
func (s *pathSuggest) accept() string {
	if len(s.candidates) == 0 {
		return ""
	}
	if !s.cycling {
		return s.candidates[0]
	}
	return s.candidates[s.selected]
}

// step moves the selected candidate by delta, wrapping around the list.
func (s *pathSuggest) step(delta int) string {
	n := len(s.candidates)
	if n == 0 {
		return ""
	}
	s.selected = (s.selected + delta + n) % n
	return s.candidates[s.selected]
}

// active reports whether there is anything to show — and, with it, whether tab
// completes instead of moving the cursor to the next field.
func (s *pathSuggest) active() bool { return len(s.candidates) > 0 }

// clear drops the candidates (field blurred, form closed or submitted).
func (s *pathSuggest) clear() {
	s.candidates = nil
	s.cycling = false
}

// lines returns the rows to render under the field, indented to sit under the
// input. Every candidate shares the typed directory, so only the final path
// component is shown (a directory keeps its trailing separator) — long
// absolute prefixes would otherwise truncate away the distinguishing part.
// maxRows caps the rows including the "+N more" tail; 0 or less renders
// nothing. Empty when there is nothing to suggest. While cycling, the row
// applied to the field is marked with "▸ " and the window scrolls to keep it
// visible.
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
	start := 0
	if s.cycling && s.selected >= shown {
		start = s.selected - shown + 1
	}
	out := make([]string, 0, maxRows)
	for i := start; i < start+shown && i < n; i++ {
		name := lastPathComponent(s.candidates[i])
		if s.cycling {
			if i == s.selected {
				name = "▸ " + name
			} else {
				name = "  " + name
			}
		}
		out = append(out, name)
	}
	if rest := n - (start + shown); rest > 0 {
		out = append(out, fmt.Sprintf("… +%d more", rest))
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
