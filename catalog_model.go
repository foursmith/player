package main

import (
	"math"
	"sort"
	"strings"

	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type catalogTime struct {
	position, duration float64
	progressKnown      bool
}

func (a *application) catalogPlaybackTime(item library.SavedItem) catalogTime {
	if item.Error != nil || item.Folder {
		return catalogTime{}
	}
	history := a.catalogHistory(item)
	value := catalogTime{position: history.Position, duration: history.Duration, progressKnown: a.preferences.RecordPlaybackHistory}
	if item.Duration > 0 {
		value.duration = item.Duration
	}
	if value.duration <= 0 && a.catalog.thumbnails != nil {
		value.duration = a.catalog.thumbnails.images[library.Identity(item.Entry)].duration
	}
	if a.player != nil {
		state := a.player.Snapshot()
		if a.catalogMatchesPlayback(item.Entry, state) && !state.Loading && state.Error == "" {
			value.position, value.progressKnown = state.Position, true
			if state.Metadata.Duration > 0 {
				value.duration = state.Metadata.Duration
			}
		}
	}
	if math.IsNaN(value.duration) || math.IsInf(value.duration, 0) || value.duration < 0 {
		value.duration = 0
	}
	if math.IsNaN(value.position) || math.IsInf(value.position, 0) || value.position < 0 {
		value.position = 0
	}
	if value.duration > 0 {
		value.position = min(value.position, value.duration)
	}
	return value
}

func (a *application) catalogMatchesPlayback(entry library.Entry, state media.State) bool {
	if a.selected < 0 || a.selected >= len(a.entries) {
		return false
	}
	active := a.entries[a.selected]
	return state.URL == active.Path && library.Identity(active) == library.Identity(entry)
}

func (a *application) catalogTimeLabel(value catalogTime) string {
	duration := "—"
	if value.duration > 0 {
		duration = formatTime(value.duration)
	}
	if !value.progressKnown {
		return a.t("library.duration", duration)
	}
	if value.duration > 0 {
		return a.t("library.playbackProgress", formatTime(value.position), duration, value.position/value.duration*100)
	}
	return a.t("library.playbackTime", formatTime(value.position), duration)
}

const (
	catalogAll = iota
	catalogUnwatched
	catalogWatching
	catalogWatched
	catalogUnavailable
	catalogFilterCount
)

const (
	catalogName = iota
	catalogNameDescending
	catalogModified
	catalogSize
	catalogRecentlyPlayed
	catalogSortCount
)

var catalogFilterKeys = [...]string{"library.filterAll", "library.unwatched", "library.watching", "library.watched", "library.unavailable"}
var catalogSortKeys = [...]string{"library.sortName", "library.sortNameDescending", "library.sortModified", "library.sortSize", "library.sortPlayed"}

func (h videoHistory) watchStatus() int {
	switch h.WatchOverride {
	case "watched":
		return catalogWatched
	case "unwatched":
		return catalogUnwatched
	}
	if h.Watched || h.Duration > 0 && h.Position >= h.Duration*.95 {
		return catalogWatched
	}
	if h.Position > 0 {
		return catalogWatching
	}
	return catalogUnwatched
}

func (a *application) catalogHistory(item library.SavedItem) videoHistory {
	if !a.preferences.RecordPlaybackHistory || item.Error != nil || item.Folder {
		return videoHistory{}
	}
	return a.stored.History[library.Identity(item.Entry)]
}

func (a *application) filterCatalog() {
	query := strings.ToLower(strings.TrimSpace(a.catalog.search))
	filter := a.preferences.LibraryFilter
	if !a.preferences.RecordPlaybackHistory && filter >= catalogUnwatched && filter <= catalogWatched {
		filter, a.preferences.LibraryFilter = catalogAll, catalogAll
	}
	// Keep row snapshots independent of a filter changed by a row action.
	visible := make([]int, 0, len(a.catalog.items))
	for i, item := range a.catalog.items {
		if query != "" && !strings.Contains(strings.ToLower(item.Name()+" "+item.Path), query) {
			continue
		}
		if !item.Folder && filter == catalogUnavailable {
			if item.Error == nil {
				continue
			}
		} else if !item.Folder && filter != catalogAll && (item.Error != nil || a.catalogHistory(item).watchStatus() != filter) {
			continue
		}
		visible = append(visible, i)
	}
	comparator := collate.New(language.Und, collate.Numeric, collate.IgnoreCase, collate.IgnoreDiacritics)
	sort.SliceStable(visible, func(i, j int) bool {
		left, right := a.catalog.items[visible[i]], a.catalog.items[visible[j]]
		if left.Folder != right.Folder {
			return left.Folder
		}
		switch a.preferences.LibrarySort {
		case catalogModified:
			if left.Entry.Modified != right.Entry.Modified {
				return left.Entry.Modified > right.Entry.Modified
			}
		case catalogSize:
			if left.Entry.Size != right.Entry.Size {
				return left.Entry.Size > right.Entry.Size
			}
		case catalogRecentlyPlayed:
			l, r := a.catalogHistory(left).UpdatedAt, a.catalogHistory(right).UpdatedAt
			if l != r {
				return l > r
			}
		}
		order := comparator.CompareString(left.Name(), right.Name())
		if order == 0 {
			order = strings.Compare(left.Path, right.Path)
		}
		if a.preferences.LibrarySort == catalogNameDescending {
			return order > 0
		}
		return order < 0
	})
	a.catalog.visible = visible
}

func (a *application) setCatalogWatch(item library.SavedItem, override string) {
	if !a.preferences.RecordPlaybackHistory || item.Error != nil || item.Folder {
		return
	}
	key := library.Identity(item.Entry)
	history := a.stored.History[key]
	history.WatchOverride = override
	a.stored.History[key] = history
	a.persist()
}

func sameCatalogItems(left, right []library.SavedItem) bool {
	if len(left) != len(right) {
		return false
	}
	for i, item := range left {
		other := right[i]
		if item.Entry != other.Entry || item.Folder != other.Folder || item.Duration != other.Duration || (item.Error == nil) != (other.Error == nil) {
			return false
		}
	}
	return true
}
