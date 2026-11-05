package main

import (
	"image"
	"image/color"
	"math"
	"os"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
)

func TestCatalogShowsDurationBeforePlaybackWithoutCreatingHistory(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	a := modeTestApplication(t)
	entry := library.Entry{Name: "New video.mp4", Path: "/Media/New video.mp4", Size: 10, Modified: 20}
	item := library.SavedItem{Entry: entry}
	key := library.Identity(entry)
	a.catalog.thumbnails = &catalogThumbnails{images: map[string]catalogPreview{key: {duration: 125}}}
	value := a.catalogPlaybackTime(item)
	if value.duration != 125 || value.position != 0 || !value.progressKnown || a.catalogTimeLabel(value) != "0:00 / 2:05 · 0%" {
		t.Fatal("an unplayed video did not display its probed duration", value, a.catalogTimeLabel(value))
	}
	if _, exists := a.stored.History[key]; exists {
		t.Fatal("reading duration fabricated playback history")
	}
	a.stored.History[key] = videoHistory{Position: 754, Duration: 4800}
	if label := a.catalogTimeLabel(a.catalogPlaybackTime(item)); label != "12:34 / 1:20:00 · 16%" {
		t.Fatal("saved progress or long duration was formatted incorrectly", label)
	}
	a.stored.History[key] = videoHistory{Position: 200}
	if value := a.catalogPlaybackTime(item); value.position != 125 || a.catalogTimeLabel(value) != "2:05 / 2:05 · 100%" {
		t.Fatal("progress exceeded the actual duration", value)
	}
	a.stored.History[key] = videoHistory{Position: math.NaN(), Duration: math.Inf(1)}
	if value := a.catalogPlaybackTime(item); value.position != 0 || value.duration != 0 {
		t.Fatal("invalid timing data reached the formatter", value)
	}
	a.preferences.RecordPlaybackHistory = false
	if value := a.catalogPlaybackTime(item); value.progressKnown || value.duration != 125 || a.catalogTimeLabel(value) != "Duration 2:05" {
		t.Fatal("disabling history hid duration or exposed saved progress", value)
	}
}

func TestWatchProgressCompletionAndManualOverrides(t *testing.T) {
	a := modeTestApplication(t)
	entry := a.entries[0]
	key := library.Identity(entry)
	item := library.SavedItem{Entry: entry}
	state := media.State{URL: entry.Path, Position: 25, Metadata: media.Metadata{Duration: 100}}
	a.recordPlaybackHistory(entry, state)
	if h := a.stored.History[key]; h.Duration != 100 || h.watchStatus() != catalogWatching {
		t.Fatal("partial progress was not recorded", h)
	}
	state.Position = 96
	a.recordPlaybackHistory(entry, state)
	state.Position = 10
	a.recordPlaybackHistory(entry, state)
	if !a.stored.History[key].Watched || a.stored.History[key].watchStatus() != catalogWatched {
		t.Fatal("replaying a completed video lost its watched state")
	}
	a.setCatalogWatch(item, "unwatched")
	if h := a.stored.History[key]; h.watchStatus() != catalogUnwatched || h.Position != 10 {
		t.Fatal("manual status did not preserve resume position", h)
	}
	a.setCatalogWatch(item, "")
	if a.stored.History[key].watchStatus() != catalogWatched {
		t.Fatal("automatic status did not restore the completion record")
	}
	a.preferences.RecordPlaybackHistory = false
	before := a.stored.History[key]
	a.setCatalogWatch(item, "unwatched")
	a.recordPlaybackHistory(entry, state)
	if a.stored.History[key] != before || a.catalogHistory(item) != (videoHistory{}) {
		t.Fatal("disabled history still read or wrote watch state")
	}
}

func TestCatalogNaturalSortFiltersAndRecentPlayback(t *testing.T) {
	a := modeTestApplication(t)
	a.catalog.items = []library.SavedItem{
		{Entry: library.Entry{Path: "/media/Episode 10.mp4", Size: 20, Modified: 2}},
		{Entry: library.Entry{Path: "/media/Episode 2.mp4", Size: 10, Modified: 3}},
		{Entry: library.Entry{Path: "/media/Missing.mp4"}, Error: os.ErrNotExist},
	}
	a.stored.History[library.Identity(a.catalog.items[0].Entry)] = videoHistory{Position: 20, Duration: 100, UpdatedAt: 200}
	a.stored.History[library.Identity(a.catalog.items[1].Entry)] = videoHistory{Watched: true, UpdatedAt: 100}
	for _, tc := range []struct {
		sort, filter int
		want         []int
	}{
		{catalogName, catalogAll, []int{1, 0, 2}},
		{catalogNameDescending, catalogAll, []int{2, 0, 1}},
		{catalogModified, catalogAll, []int{1, 0, 2}},
		{catalogSize, catalogAll, []int{0, 1, 2}},
		{catalogRecentlyPlayed, catalogAll, []int{0, 1, 2}},
		{catalogName, catalogWatching, []int{0}},
		{catalogName, catalogWatched, []int{1}},
		{catalogName, catalogUnwatched, []int{}},
		{catalogName, catalogUnavailable, []int{2}},
	} {
		a.preferences.LibrarySort, a.preferences.LibraryFilter = tc.sort, tc.filter
		a.filterCatalog()
		if !reflect.DeepEqual(a.catalog.visible, tc.want) {
			t.Fatalf("sort=%d filter=%d got=%v want=%v", tc.sort, tc.filter, a.catalog.visible, tc.want)
		}
	}
	a.preferences.RecordPlaybackHistory = false
	a.preferences.LibraryFilter = catalogWatched
	a.filterCatalog()
	if a.preferences.LibraryFilter != catalogAll || len(a.catalog.visible) != 3 {
		t.Fatal("disabling history stranded the library in an unavailable watch filter")
	}
	unchanged := append([]library.SavedItem(nil), a.catalog.items...)
	if !sameCatalogItems(a.catalog.items, unchanged) {
		t.Fatal("an unchanged scan would unnecessarily rebuild the library")
	}
	unchanged[0].Entry.Modified++
	if sameCatalogItems(a.catalog.items, unchanged) {
		t.Fatal("automatic refresh ignored replaced file metadata")
	}
}

func TestCatalogWatchMenusFiltersAndLayoutAcrossLocales(t *testing.T) {
	if err := loadAssets(); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"zh-CN", "en", "ja"} {
		t.Run(locale, func(t *testing.T) {
			a := modeTestApplication(t)
			a.preferences.Locale = locale
			name, path := "Clouds — Episode 2.mp4", "/Media/Clouds — Episode 2.mp4"
			entry := library.Entry{Name: name, Path: path, Size: 1000, Modified: 1}
			a.stored.Libraries = []library.Saved{{ID: "collection", Name: "Weekend videos", Paths: []string{path, "/Media/Missing.mp4"}}}
			a.catalog.selected, a.catalog.name = "collection", "Weekend videos"
			a.catalog.items = []library.SavedItem{{Entry: entry}, {Entry: library.Entry{Path: "/Media/Missing.mp4"}, Error: os.ErrNotExist}}
			key := library.Identity(entry)
			a.stored.History[key] = videoHistory{Position: 754, Duration: 4800}
			img := image.NewRGBA(image.Rect(0, 0, 160, 90))
			for y := range 90 {
				for x := range 160 {
					img.SetRGBA(x, y, color.RGBA{uint8(20 + x/4), uint8(70 + y), 150, 255})
				}
			}
			a.catalog.thumbnails = &catalogThumbnails{images: map[string]catalogPreview{key: {bitmap: ui.NewBitmap(img), duration: 4800}}}
			a.filterCatalog()
			view := ui.NewTester(a.catalogView, 800, 460)
			if r, ok := view.Find("12:34 / 1:20:00 · 16%"); !ok || r.W == 0 || r.X+r.W > 800 {
				t.Fatal("progress and duration text is missing or clipped", r)
			}
			if err := view.Click(a.t("library.watchStatus", name)); err != nil {
				t.Fatal(err)
			}
			if err := view.ChooseMenuItem(a.t("library.markWatched")); err != nil {
				t.Fatal(err)
			}
			if h := a.stored.History[key]; h.watchStatus() != catalogWatched || h.Position != 754 {
				t.Fatal("watch menu changed playback progress or failed to apply", h)
			}
			if err := view.Click(a.t("library.filter")); err != nil {
				t.Fatal(err)
			}
			if err := view.ChooseMenuItem(a.t("library.watched")); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a.catalog.visible, []int{0}) {
				t.Fatal("watch filter menu did not narrow the list", a.catalog.visible)
			}
			if err := view.Click(a.t("library.sort")); err != nil {
				t.Fatal(err)
			}
			if err := view.ChooseMenuItem(a.t("library.sortModified")); err != nil || a.preferences.LibrarySort != catalogModified {
				t.Fatal("sort menu did not save the selected order", err)
			}
			for _, key := range []string{"library.filter", "library.sort", "library.autoRefresh", "library.playAll", "library.append"} {
				if r, ok := view.Find(a.t(key)); !ok || r.X < 0 || r.Y < 0 || r.X+r.W > 800 || r.Y+r.H > 460 {
					t.Fatalf("control is outside the minimum window: %s %+v", key, r)
				}
			}
			// Optional diagnostic captures use neutral fixtures, never user videos.
			if os.Getenv("FSVR_UI_SNAPSHOT_DIR") != "" {
				for _, size := range [][2]int{{800, 460}, {920, 600}} {
					a.catalog.list = ui.ListState{}
					a.catalog.sidebar = ui.ScrollState{}
					v := ui.NewTester(a.catalogView, size[0], size[1])
					suffix := "-minimum.png"
					if size[0] == 920 {
						suffix = "-regular.png"
					}
					writeUISnapshot(t, locale+suffix, v)
				}
			}
		})
	}
}
