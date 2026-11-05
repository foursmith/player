package library

import "math"

// MediaInfo is the container metadata shared by previews and DLNA publishing.
type MediaInfo struct {
	Duration      float64
	Width, Height int
	BitRate       int64
}

func (info MediaInfo) Normalized() MediaInfo {
	if math.IsNaN(info.Duration) || math.IsInf(info.Duration, 0) || info.Duration < 0 {
		info.Duration = 0
	}
	info.Width, info.Height, info.BitRate = max(0, info.Width), max(0, info.Height), max(0, info.BitRate)
	return info
}
