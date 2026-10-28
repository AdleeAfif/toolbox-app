package compare

import (
	"strings"
	"testing"
)

func TestDiff(t *testing.T) {
	res, err := Diff("red\nblue\n", "red\ngreen\n")
	if err != nil || res.Same {
		t.Fatalf("diff: same=%v err=%v", res.Same, err)
	}
	var added, removed bool
	for _, ln := range res.Lines {
		if ln.Kind == "removed" && ln.Text == "blue" {
			removed = true
		}
		if ln.Kind == "added" && ln.Text == "green" {
			added = true
		}
	}
	if !added || !removed {
		t.Fatalf("lines %#v", res.Lines)
	}

	same, err := Diff("a\nb", "a\nb")
	if err != nil || !same.Same {
		t.Fatalf("identical: %+v %v", same, err)
	}

	if _, err := Diff("", ""); err != ErrEmpty {
		t.Fatalf("empty: %v", err)
	}
	big := strings.Repeat("a", MaxBytes+1)
	if _, err := Diff(big, "a"); err != ErrTooLong {
		t.Fatalf("too long: %v", err)
	}
	if _, err := Diff("a", big); err != ErrTooLong {
		t.Fatalf("too long after: %v", err)
	}

	space, err := Diff("hello", "hello ")
	if err != nil || space.Same {
		t.Fatalf("trailing space should differ: %+v %v", space, err)
	}
	var sawSpace bool
	for _, ln := range space.Lines {
		if strings.HasSuffix(ln.Text, " ") {
			sawSpace = true
		}
	}
	if !sawSpace {
		t.Fatalf("trailing space was trimmed: %#v", space.Lines)
	}
}

func TestDiffCollapsesUnchanged(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString("line ")
		b.WriteByte(byte('0' + i))
		b.WriteByte('\n')
	}
	before := b.String()
	after := strings.Replace(before, "line 9", "changed", 1)
	res, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	var sawSkip bool
	for _, ln := range res.Lines {
		if ln.Kind == "skip" && ln.Text == "… 7 unchanged lines …" {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Fatalf("lines %#v", res.Lines)
	}
}
