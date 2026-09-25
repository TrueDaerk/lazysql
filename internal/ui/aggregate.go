package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// aggregateCellLimit bounds how many cells the status line's selection
// aggregate walks per frame. A page holds at most the configured page
// size, but that size is user-configurable, and the status line is built
// on every frame — so a selection past the bound says it is too large
// instead of costing a frame proportional to it. See
// wiki/design/grid-selection-aggregate.md.
const aggregateCellLimit = 50_000

// selectionAggregate is count/sum/avg/min/max over the numeric cells of
// the grid selection. cells is every cell the selection covers; numeric
// is how many of them took part, so cells-numeric were skipped (NULL,
// text, booleans, dates).
type selectionAggregate struct {
	cells, numeric int
	sum, min, max  float64
	// tooLarge marks a selection past aggregateCellLimit: nothing was
	// summed, and nothing above is meaningful.
	tooLarge bool
}

func (a selectionAggregate) skipped() int { return a.cells - a.numeric }

func (a selectionAggregate) avg() float64 { return a.sum / float64(a.numeric) }

// selectionAggregate walks the selected block — the selected rows of the
// current page, cut down to the column span — and aggregates its numeric
// values. It reads the values the grid shows: a staged cell edit counts
// with its new value, the same way the copy scopes read rowValues.
func (m Model) selectionAggregate() selectionAggregate {
	d := m.grid.data
	rows, cols := d.selectedRows(), d.selectedCols()
	var a selectionAggregate
	a.cells = len(rows) * len(cols)
	if a.cells > aggregateCellLimit {
		a.tooLarge = true
		return a
	}
	decimal := make([]bool, len(d.cols))
	for _, c := range cols {
		decimal[c] = decimalType(d.cols[c].DataType)
	}
	staged := m.grid.changes.Len() > 0 && d.browsing()
	for _, r := range rows {
		vals := d.rows[r]
		if staged {
			vals, _ = m.rowValues(r)
		}
		for _, c := range cols {
			if c >= len(vals) {
				continue
			}
			f, ok := numericValue(vals[c], decimal[c])
			if !ok {
				continue
			}
			if a.numeric == 0 {
				a.min, a.max = f, f
			}
			a.numeric++
			a.sum += f
			a.min = math.Min(a.min, f)
			a.max = math.Max(a.max, f)
		}
	}
	return a
}

// numericValue reads a typed grid value as a number. The driver hands
// integers and floats over typed (see db.normalizeValue), but exact
// numerics — MySQL DECIMAL, PostgreSQL NUMERIC — arrive as strings so no
// digit is lost; those are parsed only when the column says it is one,
// so a TEXT column of digits stays text, exactly as it renders.
func numericValue(v any, decimal bool) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return x, true
	case string:
		if !decimal {
			return 0, false
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// decimalType reports whether a column's database type is an exact
// numeric whose values the driver delivers as text.
func decimalType(t string) bool {
	t = strings.ToUpper(t)
	for _, k := range []string{"DECIMAL", "NUMERIC", "NUMBER", "HUGEINT"} {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
}

// variants renders the aggregate from its fullest form to its most
// compact one; the status line takes the longest that fits its width.
// Every form says "page", so it can never be read as a total over the
// whole (filtered) table, and every form that shows a figure also shows
// the skipped count, so a mixed column is never read as a clean total.
func (a selectionAggregate) variants() []string {
	switch {
	case a.tooLarge:
		return []string{" · too many cells to sum", ""}
	case a.numeric == 0:
		return []string{" · nothing numeric selected", " · no numbers", ""}
	}
	skip := ""
	if n := a.skipped(); n > 0 {
		skip = fmt.Sprintf(" · %d skipped", n)
	}
	sum, avg := formatAggregate(a.sum), formatAggregate(a.avg())
	return []string{
		fmt.Sprintf(" · this page: count %d  sum %s  avg %s  min %s  max %s%s",
			a.numeric, sum, avg, formatAggregate(a.min), formatAggregate(a.max), skip),
		fmt.Sprintf(" · page: sum %s  avg %s%s", sum, avg, skip),
		fmt.Sprintf(" · page sum %s%s", sum, strings.Replace(skip, " · ", " +", 1)),
		"",
	}
}

// formatAggregate prints a figure short enough for a status line: whole
// numbers without a fraction, others to four decimals with trailing
// zeros trimmed, and the extremes in exponent form.
func formatAggregate(f float64) string {
	abs := math.Abs(f)
	switch {
	case abs >= 1e15 || (abs != 0 && abs < 1e-4):
		return strconv.FormatFloat(f, 'g', 6, 64)
	case f == math.Trunc(f):
		return strconv.FormatFloat(f, 'f', 0, 64)
	}
	s := strconv.FormatFloat(f, 'f', 4, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
