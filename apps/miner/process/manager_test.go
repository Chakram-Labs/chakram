package process

import (
	"fmt"
	"strings"
	"testing"
)

func TestLogBuf_RingWrap(t *testing.T) {
	buf := newLogBuf(nil)
	// Write 2001 lines (one over capacity).
	for i := 0; i < 2001; i++ {
		buf.Append(fmt.Sprintf("line %d", i))
	}
	last := buf.Last(10)
	if len(last) != 10 {
		t.Fatalf("want 10 lines, got %d", len(last))
	}
	// The newest line should be "line 2000".
	if !strings.HasSuffix(last[9], "2000") {
		t.Errorf("want last line to end with 2000, got %q", last[9])
	}
	// The 10th from end should be "line 1991".
	if !strings.HasSuffix(last[0], "1991") {
		t.Errorf("want first of last-10 to end with 1991, got %q", last[0])
	}
}

func TestLogBuf_CountCapAtSize(t *testing.T) {
	buf := newLogBuf(nil)
	for i := 0; i < 5; i++ {
		buf.Append(fmt.Sprintf("x%d", i))
	}
	all := buf.Last(100)
	if len(all) != 5 {
		t.Errorf("want 5 lines, got %d", len(all))
	}
}

func TestLogBuf_EmitCallback(t *testing.T) {
	var got []string
	buf := newLogBuf(func(s string) { got = append(got, s) })
	buf.Append("hello")
	buf.Append("world")
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Errorf("emit callback wrong: %v", got)
	}
}

func TestPlainStatusStrings(t *testing.T) {
	states := []State{
		StateIdle, StateStartingGeth, StateWaitingGeth,
		StateStartingChakram, StateWaitingChakram,
		StateRunning, StateShuttingDown, StateErrored,
	}
	for _, s := range states {
		p := PlainStatus(s, "")
		if p == "" {
			t.Errorf("PlainStatus(%q) returned empty string", s)
		}
	}
	// Errored with reason should include the reason.
	p := PlainStatus(StateErrored, "disk full")
	if !strings.Contains(p, "disk full") {
		t.Errorf("Errored status should contain reason, got %q", p)
	}
}
