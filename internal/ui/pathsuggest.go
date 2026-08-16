package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"lazysql/internal/pathcomplete"
)

// pathsuggest.go is the form-side glue for the shared path completion engine:
// a tiny state holder the form modal keeps for the field under the cursor when
// that field opted into completion (`withSuggest`). The form refreshes it on
// every edit, applies tab through complete, and renders the candidates as a
// bordered box floating under the field while any exist.
//
// The box is a *layer*, not form rows: it is composited over the modal by the
// same lipgloss compositor the query editor's autocomplete uses (see
// internal/ui/complete.go), so the form's own layout keeps the same height
// whether or not candidates are up. The form records where the field's value
// cell landed inside its rendered box (formModal.anchor*) and View turns that
// into the absolute cell the layer is drawn at.

// maxSuggestLines caps the rendered suggestion rows; the remainder collapses
// into a "+N more" tail so an ambiguous prefix cannot flood the screen. The
// overlay shrinks this further when the terminal is too short.
const maxSuggestLines = 8

// minSuggestWidth is the narrowest text column the box is worth drawing at.
const minSuggestWidth = 8

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

// suggestRow is one row of the floating list: the text to draw plus what it
// is — the highlighted candidate, or the "+N more" tail — so the renderer
// picks the style instead of the row baking markers into its text.
type suggestRow struct {
	text     string
	selected bool
	tail     bool
}

// rows returns the rows of the floating list. Every candidate shares the typed
// directory, so only the final path component is shown (a directory keeps its
// trailing separator) — long absolute prefixes would otherwise truncate away
// the distinguishing part. maxRows caps the rows including the "+N more" tail;
// 0 or less renders nothing. Empty when there is nothing to suggest. While
// cycling, the row applied to the field is marked selected and the window
// scrolls to keep it visible.
func (s *pathSuggest) rows(maxRows int) []suggestRow {
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
	if shown < 1 {
		// A one-row budget spends it on a candidate: a box containing
		// nothing but "+4 more" tells the user less than the box costs.
		shown = 1
	}
	start := 0
	if s.cycling && s.selected >= shown {
		start = s.selected - shown + 1
	}
	out := make([]suggestRow, 0, maxRows)
	for i := start; i < start+shown && i < n; i++ {
		out = append(out, suggestRow{
			text:     lastPathComponent(s.candidates[i]),
			selected: s.cycling && i == s.selected,
		})
	}
	if rest := n - (start + shown); rest > 0 && len(out) < maxRows {
		out = append(out, suggestRow{text: fmt.Sprintf("… +%d more", rest), tail: true})
	}
	return out
}

// popup renders the candidate list as the bordered box View composites over
// the form modal. maxW is the widest text column it may use — the box itself
// costs two more cells of border — and it shrinks to the longest row when
// that is narrower, so a list of short names does not draw an empty gutter.
// maxRows is the vertical budget the terminal has left. It returns "" when
// there is nothing to draw, so the caller can skip the layer entirely.
//
// Every row is padded to the full text column: the highlight has to read as a
// bar across the box, the way the editor popup's selection does, not as a tint
// on however many cells the name happens to occupy.
func (s *pathSuggest) popup(st styles, maxW, maxRows int) string {
	rows := s.rows(maxRows)
	if len(rows) == 0 || maxW < 1 {
		return ""
	}
	w := 0
	for _, r := range rows {
		if rw := lipgloss.Width(r.text); rw > w {
			w = rw
		}
	}
	if w > maxW {
		w = maxW
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		text := truncate(r.text, w)
		pad := strings.Repeat(" ", maxInt(w-lipgloss.Width(text), 0))
		switch {
		case r.selected:
			lines = append(lines, st.popupSelected.Render(text+pad))
		case r.tail:
			lines = append(lines, st.muted.Render(text+pad))
		default:
			lines = append(lines, text+pad)
		}
	}
	return st.popup.Render(strings.Join(lines, "\n"))
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
