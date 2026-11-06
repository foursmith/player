package media

import (
	"math"
	"sort"
	"strings"
	"unsafe"

	"github.com/foursmith/player/internal/native"
)

// Chapter times use the same zero-based presentation clock as playback.
type Chapter struct {
	Start, End float64
	Title      string
}

func readChapters(format *avFormat, startTime float64) []Chapter {
	var chapters []Chapter
	for _, raw := range unsafe.Slice(format.Chapters, int(format.NbChapters)) {
		if raw == nil || raw.TimeBase.Den <= 0 || raw.TimeBase.Num <= 0 || raw.Start == avNoTimestamp {
			continue
		}
		chapter := Chapter{Start: float64(raw.Start)*ratio(raw.TimeBase) - startTime, End: float64(raw.End)*ratio(raw.TimeBase) - startTime}
		if entry := av.util.DictGet(raw.Metadata, "title", nil, 0); entry != nil {
			chapter.Title = strings.TrimSpace(native.String(entry.Value))
		}
		chapters = append(chapters, chapter)
	}
	duration := float64(format.Duration) / 1e6
	return normalizeChapters(chapters, duration)
}

func normalizeChapters(chapters []Chapter, duration float64) []Chapter {
	sort.SliceStable(chapters, func(i, j int) bool { return chapters[i].Start < chapters[j].Start })
	out := chapters[:0]
	for _, chapter := range chapters {
		if math.IsNaN(chapter.Start) || math.IsInf(chapter.Start, 0) || chapter.End <= 0 || duration > 0 && chapter.Start >= duration {
			continue
		}
		chapter.Start = max(0, chapter.Start)
		if len(out) > 0 && chapter.Start <= out[len(out)-1].Start {
			continue
		}
		out = append(out, chapter)
	}
	for i := range out {
		end := duration
		if i+1 < len(out) {
			end = out[i+1].Start
		}
		if end > out[i].Start {
			out[i].End = end
		}
	}
	return out
}

// ChapterAt treats a boundary as the beginning of the following chapter.
func ChapterAt(chapters []Chapter, at float64) int {
	i := sort.Search(len(chapters), func(i int) bool { return chapters[i].Start > at }) - 1
	if i >= 0 && at <= chapters[i].End {
		return i
	}
	return -1
}
