package compare

import (
	"fmt"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

const (
	MaxBytes = 256 << 10
	context  = 2
)

// InputError is a problem with what the user sent, safe to show them.
type InputError struct{ msg string }

func (e *InputError) Error() string { return e.msg }

var (
	ErrEmpty   = &InputError{"enter text to compare"}
	ErrTooLong = &InputError{"text is longer than 256 KB"}
)

// Line is one row of a diff. Kind is added, removed, context, or skip.
type Line struct {
	Kind string
	Text string
}

// Result is a line diff. Same is set when the texts are identical.
type Result struct {
	Same  bool
	Lines []Line
}

// Diff compares before and after without trimming. Identical text reports no
// differences. Changed lines keep up to two unchanged lines of context.
func Diff(before, after string) (Result, error) {
	if len(before) > MaxBytes || len(after) > MaxBytes {
		return Result{}, ErrTooLong
	}
	if before == "" && after == "" {
		return Result{}, ErrEmpty
	}
	if before == after {
		return Result{Same: true}, nil
	}

	dmp := diffmatchpatch.New()
	a, b, lineArray := dmp.DiffLinesToRunes(before, after)
	diffs := dmp.DiffMainRunes(a, b, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)
	return Result{Lines: withContext(flatten(diffs))}, nil
}

func flatten(diffs []diffmatchpatch.Diff) []Line {
	var out []Line
	for _, d := range diffs {
		kind := "context"
		switch d.Type {
		case diffmatchpatch.DiffInsert:
			kind = "added"
		case diffmatchpatch.DiffDelete:
			kind = "removed"
		}
		text := d.Text
		if text == "" {
			continue
		}
		parts := strings.Split(text, "\n")
		// A trailing newline is the line break, not an extra blank line.
		if strings.HasSuffix(text, "\n") {
			parts = parts[:len(parts)-1]
		}
		for _, part := range parts {
			out = append(out, Line{Kind: kind, Text: part})
		}
	}
	return out
}

func withContext(lines []Line) []Line {
	keep := make([]bool, len(lines))
	for i, ln := range lines {
		if ln.Kind == "context" {
			continue
		}
		keep[i] = true
		for d := 1; d <= context; d++ {
			if i-d >= 0 {
				keep[i-d] = true
			}
			if i+d < len(lines) {
				keep[i+d] = true
			}
		}
	}

	var out []Line
	for i := 0; i < len(lines); {
		if lines[i].Kind == "context" && !keep[i] {
			j := i + 1
			for j < len(lines) && lines[j].Kind == "context" && !keep[j] {
				j++
			}
			out = append(out, Line{Kind: "skip", Text: fmt.Sprintf("… %d unchanged lines …", j-i)})
			i = j
			continue
		}
		out = append(out, lines[i])
		i++
	}
	return out
}
