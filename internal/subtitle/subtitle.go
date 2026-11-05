package subtitle

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Cue struct {
	Start, End float64
	Text       string
}

var htmlTags = regexp.MustCompile(`<[^>]*>`)
var assTags = regexp.MustCompile(`\{\\[^}]+\}`)
var htmlBreaks = regexp.MustCompile(`(?i)<br\s*/?>`)
var blocks = regexp.MustCompile(`\n\s*\n`)

func clean(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, `\N`, "\n"), `\n`, "\n")
	return strings.TrimSpace(htmlTags.ReplaceAllString(assTags.ReplaceAllString(htmlBreaks.ReplaceAllString(s, "\n"), ""), ""))
}
func timestamp(s string) float64 {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return -1
	}
	result := 0.0
	for _, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return -1
		}
		result = result*60 + value
	}
	return result
}
func Parse(source, name string) []Cue {
	source = strings.ReplaceAll(strings.TrimPrefix(source, "\ufeff"), "\r", "")
	var cues []Cue
	appendCue := func(start, end float64, text string) {
		text = clean(text)
		if start >= 0 && end > start && text != "" {
			cues = append(cues, Cue{start, end, text})
		}
	}
	if strings.HasSuffix(strings.ToLower(name), ".ass") || strings.HasSuffix(strings.ToLower(name), ".ssa") {
		for line := range strings.SplitSeq(source, "\n") {
			if !strings.HasPrefix(strings.ToLower(line), "dialogue:") {
				continue
			}
			fields := strings.SplitN(strings.TrimSpace(line[len("Dialogue:"):]), ",", 10)
			if len(fields) == 10 {
				appendCue(timestamp(fields[1]), timestamp(fields[2]), fields[9])
			}
		}
	} else {
		for _, block := range blocks.Split(source, -1) {
			lines := strings.Split(strings.TrimSpace(block), "\n")
			for i, line := range lines {
				if !strings.Contains(line, "-->") {
					continue
				}
				timing := strings.SplitN(line, "-->", 2)
				end := strings.Fields(timing[1])
				if len(end) > 0 {
					appendCue(timestamp(timing[0]), timestamp(end[0]), strings.Join(lines[i+1:], "\n"))
				}
				break
			}
		}
	}
	sort.SliceStable(cues, func(i, j int) bool { return cues[i].Start < cues[j].Start })
	return cues
}
func Active(cues []Cue, time float64) string {
	index := sort.Search(len(cues), func(i int) bool { return cues[i].End > time })
	if index < len(cues) && cues[index].Start <= time && time < cues[index].End {
		return cues[index].Text
	}
	return ""
}
