package subtitle

import (
	"fmt"
	"math"
	"strings"
)

// ASS describes the existing bottom-14% outlined subtitle treatment in output
// pixels. FFmpeg/libass performs system-font fallback and Unicode shaping.
func ASS(cues []Cue, start, end float64, width, height int, cssWidth float64) string {
	if cssWidth <= 0 {
		cssWidth = float64(width)
	}
	scale := float64(width) / cssWidth
	size := math.Min(28, math.Max(16, cssWidth*.022)) * scale
	margin := int(float64(height) * .14)
	horizontal := int(float64(width) * .07)
	var out strings.Builder
	fmt.Fprintf(&out, "[Script Info]\nScriptType: v4.00+\nPlayResX: %d\nPlayResY: %d\nWrapStyle: 0\nScaledBorderAndShadow: yes\n\n[V4+ Styles]\nFormat: Name,Fontname,Fontsize,PrimaryColour,SecondaryColour,OutlineColour,BackColour,Bold,Italic,Underline,StrikeOut,ScaleX,ScaleY,Spacing,Angle,BorderStyle,Outline,Shadow,Alignment,MarginL,MarginR,MarginV,Encoding\nStyle: Default,Helvetica Neue,%.3f,&H00FFFFFF,&H00FFFFFF,&H2E000000,&H00000000,-1,0,0,0,100,100,0,0,1,%.3f,0,2,%d,%d,%d,1\n\n[Events]\nFormat: Layer,Start,End,Style,Name,MarginL,MarginR,MarginV,Effect,Text\n", width, height, size, math.Max(2, 3*scale)/2, horizontal, horizontal, margin)
	for _, cue := range cues {
		a, b := math.Max(start, cue.Start), math.Min(end, cue.End)
		if b <= a {
			continue
		}
		text := strings.NewReplacer("\\", "\\\\", "{", "\\{", "}", "\\}", "\n", "\\N").Replace(cue.Text)
		fmt.Fprintf(&out, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", assTime(a-start), assTime(b-start), text)
	}
	return out.String()
}
func assTime(s float64) string {
	n := int(math.Round(s * 100))
	return fmt.Sprintf("%d:%02d:%02d.%02d", n/360000, n/6000%60, n/100%60, n%100)
}
