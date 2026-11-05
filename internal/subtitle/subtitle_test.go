package subtitle

import "testing"

func TestFormatsAndBoundaries(t *testing.T) {
	for _, test := range []struct{ name, source string }{{"a.srt", "1\n00:00:01,000 --> 00:00:02,500\n<b>Hello</b> &amp; world\n"}, {"a.vtt", "WEBVTT\n\n00:01.000 --> 00:02.500 align:start\nHello &amp; world"}, {"a.ass", "Dialogue: 0,0:00:01.00,0:00:02.50,Default,,0,0,0,,{\\b1}Hello & world"}} {
		cues := Parse(test.source, test.name)
		expected := "Hello &amp; world"
		if test.name == "a.ass" {
			expected = "Hello & world"
		}
		if len(cues) != 1 || Active(cues, 1) != expected || Active(cues, 2.5) != "" {
			t.Fatalf("%s: %#v", test.name, cues)
		}
	}
}
func TestWebParserLineBreaksAndWhitespaceBlocks(t *testing.T) {
	cues := Parse("1\n00:00:01,200 --> 00:00:03,000\nHello<br>world\n \n00:04.000 --> 00:05.500\nNext", "movie.srt")
	if len(cues) != 2 || cues[0].Text != "Hello\nworld" {
		t.Fatal(cues)
	}
}

func TestOverlappingCuesAndSeeks(t *testing.T) {
	cues := Parse("1\n00:00:01 --> 00:00:10\nlong\n\n2\n00:00:02 --> 00:00:03\nshort\n\n3\n00:00:04 --> 00:00:05\nlater", "movie.srt")
	for _, at := range []float64{6, 2.5, 8, 1} {
		if got := Active(cues, at); got != "long" {
			t.Fatalf("overlapping cue disappeared at %v: %q", at, got)
		}
	}
	if Active(cues, 10) != "" || Active(cues, 0) != "" {
		t.Fatal("subtitle leaked beyond its range")
	}
	if got := Active([]Cue{{Start: 1, End: 10, Text: "direct"}, {Start: 2, End: 3, Text: "short"}}, 6); got != "direct" {
		t.Fatal("direct cues require a parser index", got)
	}
}

func TestNonFiniteTimestampsAreRejected(t *testing.T) {
	if got := Parse("00:01 --> 00:Inf\ninvalid", "movie.vtt"); len(got) != 0 {
		t.Fatal(got)
	}
}
