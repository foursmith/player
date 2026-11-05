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
