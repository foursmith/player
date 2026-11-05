package subtitle

import (
	"strings"
	"testing"
)

func TestExportTrimsAndOffsetsCues(t *testing.T) {
	out := ASS([]Cue{{Start: 1, End: 3, Text: "片段\nsecond"}, {Start: 9, End: 12, Text: "outside"}}, 2, 5, 800, 600, 400)
	if !strings.Contains(out, "0:00:00.00,0:00:01.00") || !strings.Contains(out, `片段\Nsecond`) || strings.Contains(out, "outside") {
		t.Fatal(out)
	}
	if !strings.Contains(out, "Helvetica Neue,32.000") {
		t.Fatal("subtitle scale changed")
	}
}
