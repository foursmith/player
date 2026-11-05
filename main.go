package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/subtitle"
	"github.com/foursmith/player/internal/tracking"
	"github.com/foursmith/player/internal/video"
)

type application struct {
	subtitleCancel                            func()
	trackingLog                               []string
	recordingTracking                         bool
	trackingSampleWidth, trackingSampleHeight int
	exportPreview                             media.FrameSlot
	exportPosition                            float64
	exportFormat                              string
	exportCancel                              func()
	presentation                              [2]presentationState
	controlsVisible                           bool
	remoteState
	folders                   []library.Folder
	expandedFolders           map[string]bool
	timelineHandle            ui.Handle
	controlsPanel             ui.Handle
	projectionButton          ui.Handle
	adjustmentsButton         ui.Handle
	scrubbing                 bool
	scrubTime                 float64
	tracker                   tracking.Tracker
	inference                 *inferenceWorker
	lastTrackingAt            float64
	trackingImage             *ui.Bitmap
	window                    *mygo.Window
	windowRef                 atomic.Pointer[mygo.Window]
	player                    *media.Player
	surface                   video.Surface
	pipWindow                 *mygo.Window
	pipSurface                video.Surface
	preferences               preferences
	stored                    storedState
	statePath                 string
	entries                   []library.Entry
	subtitlePaths             []string
	pendingPaths              []string
	cues                      []subtitle.Cue
	selected                  int
	view                      projection.View
	mode                      projection.Mode
	settingsOpen              bool
	shortcutsOpen             bool
	playlistExpanded          bool
	projectionOpen            bool
	projectionFocused         int
	projectionItems           [7]ui.Handle
	adjustmentsOpen           bool
	debugOpen                 bool
	controlsUntil             time.Time
	controlsHeld              bool
	lastPointer, pressPointer ui.Point
	dragging                  bool
	dragDistance              float64
	singleClickAt             time.Time
	width, height             float32
	loopA, loopB              *float64
	exportProgress            int
	exporting                 bool
	exportMessage, notice     string
	lastAudible               float64
	lastGeneration            uint64
	lastSaved                 time.Time
	endedGeneration           uint64
	manuallyPaused            bool
	manualUntil               time.Time
	shutdown                  bool
}

func main() {
	dataDir := flag.String("data-dir", "", "application data directory")
	server := flag.String("server", "", "FSVR server origin")
	flag.Parse()
	if err := loadAssets(); err != nil {
		log.Fatal(err)
	}
	mygo.App.SetName(appInfo.Name)
	mygo.App.SetLanguages("en", "zh-CN", "ja")
	if *dataDir != "" {
		mygo.App.SetPath(mygo.PathUserData, *dataDir)
	}
	userData, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	path := filepath.Join(userData, "state.json")
	previousPath := ""
	if *dataDir == "" {
		appData, err := mygo.App.Path(mygo.PathAppData)
		if err != nil {
			log.Fatal(err)
		}
		previousPath = filepath.Join(appData, "Foursmith VR", "state.json")
	}
	state := loadState(path, previousPath)

	a := &application{preferences: state.Preferences, stored: state, statePath: path, view: projection.DefaultView(), playlistExpanded: true, selected: -1, lastAudible: state.Preferences.Volume}
	a.expandedFolders = map[string]bool{}
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	a.player = media.NewPlayer(func() {
		if w := a.windowRef.Load(); w != nil {
			w.Invalidate()
		}
	})
	a.player.SetVolume(a.preferences.Volume)
	a.player.SetRate(a.preferences.PlaybackRate)
	mygo.App.OnSecondInstance(func(args []string, _ string) {
		if a.window != nil {
			a.window.Update(func() {
				a.window.Show()
				a.window.Focus()
				if len(args) > 1 {
					a.importPaths(args[1:])
				}
			})
		}
	})
	mygo.App.OnOpenFile(func(path string) {
		if w := a.windowRef.Load(); w != nil {
			w.Update(func() { a.importPaths([]string{path}) })
		} else {
			a.pendingPaths = append(a.pendingPaths, path)
		}
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		a.shutdown = true
		a.cancelSubtitle()
		if a.exportCancel != nil {
			a.exportCancel()
		}
		a.exportPreview.Close()
		a.persist()
		a.player.Close()
		if a.inference != nil {
			a.inference.close()
		}
		if a.surface != nil {
			a.surface.Close()
		}
		if a.pipSurface != nil {
			a.pipSurface.Close()
		}
	})
	mygo.App.OnWindowAllClosed(mygo.App.Quit)
	mygo.App.WhenReady(func() {
		a.window = mygo.NewWindow(mygo.WindowOptions{Title: appInfo.Name, Width: 1200, Height: 800, MinWidth: 360, MinHeight: 320, UseContentSize: true, TitleBarStyle: mygo.TitleBarHiddenInset, BackgroundColor: "#000000", Hidden: true, Content: ui.View(a.render)})
		a.windowRef.Store(a.window)
		a.inference = newInferenceWorker(a)
		var err error
		a.surface, err = video.New(a.window.NativeHandle())
		if err != nil {
			a.notice = err.Error()
		}
		a.window.OnReadyToShow(a.window.Show)
		a.window.OnFileDrop(func(event *mygo.FileDropEvent) { a.importPaths(event.Paths) })
		a.window.OnClose(func(*mygo.CloseEvent) {
			a.persist()
			if a.pipWindow != nil {
				a.pipWindow.Close()
			}
		})
		if *server != "" {
			a.connectServer(*server)
		}
		initialPaths := append(append([]string{}, flag.Args()...), a.pendingPaths...)
		a.pendingPaths = nil
		if len(initialPaths) > 0 {
			a.importPaths(initialPaths)
		} else if *server == "" && a.preferences.AutoResume && state.LastFile != "" {
			if _, err := os.Stat(state.LastFile); err == nil {
				a.importPaths([]string{state.LastFile})
			}
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
func (a *application) activity(delay time.Duration) { a.controlsUntil = time.Now().Add(delay) }
func (a *application) persist() {
	if a.selected >= 0 && a.selected < len(a.entries) && (a.entries[a.selected].SourceID == "" || a.entries[a.selected].SourceID == "local") {
		state := a.player.Snapshot()
		entry := a.entries[a.selected]
		a.stored.History[library.Identity(entry)] = videoHistory{Projection: a.mode, Position: state.Position, UpdatedAt: time.Now().UnixMilli()}
		a.stored.LastFile = entry.Path
	}
	if len(a.stored.History) > 10000 {
		keys := make([]string, 0, len(a.stored.History))
		for key := range a.stored.History {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return a.stored.History[keys[i]].UpdatedAt < a.stored.History[keys[j]].UpdatedAt })
		for _, key := range keys[:len(keys)-10000] {
			delete(a.stored.History, key)
		}
	}
	a.stored.Preferences = a.preferences
	if err := saveState(a.statePath, a.stored); err != nil {
		a.notice = err.Error()
	}
	a.lastSaved = time.Now()
}
func (a *application) importPaths(paths []string) {
	go func() {
		entries, subtitles, err := library.FromPaths(paths)
		a.window.Update(func() {
			if err != nil {
				a.notice = err.Error()
				return
			}
			a.subtitlePaths = append(a.subtitlePaths, subtitles...)
			if len(entries) == 0 {
				if len(subtitles) > 0 {
					a.loadSubtitle(subtitles[0])
				}
				return
			}
			seen := map[string]bool{}
			for _, entry := range a.entries {
				seen[entry.Path] = true
			}
			first := -1
			preferred := library.FirstVideo(library.Tree(entries, nil))
			if preferred >= 0 {
				preferredPath := entries[preferred].Path
				for _, entry := range entries {
					if entry.Path == a.stored.LastFile {
						preferredPath = entry.Path
						break
					}
				}
				for i, entry := range entries {
					if entry.Path == preferredPath {
						preferred = i
						break
					}
				}
			}
			for _, entry := range entries {
				if !seen[entry.Path] {
					if first < 0 {
						first = len(a.entries)
					}
					a.entries = append(a.entries, entry)
					seen[entry.Path] = true
				}
			}
			if first < 0 {
				for i, entry := range a.entries {
					if entry.Path == entries[0].Path {
						first = i
						break
					}
				}
			}
			tree := library.Tree(a.entries, a.folders)
			if preferred >= 0 {
				for i, entry := range a.entries {
					if entry.Path == entries[preferred].Path {
						first = i
						break
					}
				}
			}
			var expand func([]library.Node)
			expand = func(nodes []library.Node) {
				for _, node := range nodes {
					if node.Folder != nil && node.Folder.Kind == "browser" {
						a.expandedFolders[node.ID] = true
						expand(node.Children)
					}
				}
			}
			expand(tree)
			if !a.player.Snapshot().Playing {
				a.openEntry(first)
			}
		})
	}()
}
func (a *application) openEntry(index int) {
	if index < 0 || index >= len(a.entries) {
		return
	}
	if a.exportCancel != nil {
		a.exportCancel()
	}
	a.persist()
	a.selected = index
	a.view = projection.DefaultView()
	a.mode = projection.SBS180
	a.tracker.Reset()
	entry := a.entries[index]
	if history, ok := a.stored.History[library.Identity(entry)]; ok {
		a.mode = history.Projection
	}
	a.loopA = nil
	a.loopB = nil
	a.manuallyPaused = false
	a.notice = ""
	a.cues = nil
	start := a.stored.History[library.Identity(entry)].Position
	a.player.Open(media.Source{URL: entry.Path, Cookies: entry.Cookies, Start: start})
	a.activity(3600 * time.Millisecond)
	a.readSubtitle(entry, "", true)
}
func (a *application) choose(directory bool) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.window, Multiple: !directory, Directory: directory})
		a.window.Update(func() {
			if err != nil {
				a.notice = err.Error()
			} else if len(paths) > 0 {
				a.importPaths(paths)
			}
		})
	}()
}
func (a *application) next(automatic bool) {
	if a.selected < 0 {
		return
	}
	if automatic && a.preferences.RepeatMode == "file" {
		a.player.Seek(0)
		return
	}
	order := library.PlaybackOrder(library.Tree(a.entries, a.folders), a.selected)
	next := 0
	for i, index := range order {
		if index == a.selected {
			next = i + 1
			break
		}
	}
	if next >= len(order) {
		if automatic && a.preferences.RepeatMode != "folder" {
			return
		}
		next = 0
	}
	if len(order) > 0 {
		a.openEntry(order[next])
	}
}
func (a *application) setVolume(v float64) {
	a.preferences.Volume = projection.Clamp(v, 0, 1)
	if v > 0 {
		a.lastAudible = a.preferences.Volume
	}
	a.player.SetVolume(a.preferences.Volume)
	a.persist()
}
func (a *application) mute() {
	if a.preferences.Volume > 0 {
		a.setVolume(0)
	} else {
		v := a.lastAudible
		if v <= 0 {
			v = .7
		}
		a.setVolume(v)
	}
}
func (a *application) setRate(v float64) {
	a.preferences.PlaybackRate = projection.Clamp(v, .25, 4)
	a.player.SetRate(a.preferences.PlaybackRate)
	a.persist()
}
func (a *application) params(pip bool) video.Parameters {
	count := a.preferences.SplitCount
	if pip {
		count = 1
	}
	return video.Parameters{Mode: a.mode, View: a.view, SplitCount: count, Portrait: a.preferences.PreferredSplitAspect == "9:16", Quality: a.preferences.QualityID}
}
func (a *application) togglePip() {
	if a.pipWindow != nil {
		a.pipWindow.Close()
		return
	}
	a.pipWindow = mygo.NewWindow(mygo.WindowOptions{Title: appInfo.Name, Width: 360, Height: 640, MinWidth: 240, MinHeight: 180, AlwaysOnTop: true, BackgroundColor: "#000000", Content: ui.View(func(c *ui.Context) { a.renderStage(c, true) })})
	var err error
	a.pipSurface, err = video.New(a.pipWindow.NativeHandle())
	if err != nil {
		a.notice = err.Error()
	}
	a.pipWindow.OnClosed(func() {
		if a.pipSurface != nil {
			a.pipSurface.Close()
			a.pipSurface = nil
		}
		a.pipWindow = nil
		a.window.Invalidate()
	})
}
