package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/locales/ja"
	_ "github.com/egoist/mygo/locales/zh-Hans"
	"github.com/egoist/mygo/ui"
	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/subtitle"
	"github.com/foursmith/player/internal/tracking"
	"github.com/foursmith/player/internal/video"
)

type application struct {
	ctx                                       context.Context
	cancel                                    context.CancelFunc
	stateWriter                               *stateWriter
	subtitleCancel                            func()
	trackingLog                               []string
	recordingTracking                         bool
	trackingSampleWidth, trackingSampleHeight int
	exportTask                                *exportTask
	exportPosition                            float64
	exportFormat                              string
	presentation                              [2]presentationState
	controlsVisible                           bool
	remoteState
	folders                   []library.Folder
	expandedFolders           map[string]bool
	playlistTree              []library.Node
	playlistRows              []playlistRow
	playlistTreeValid         bool
	playlistRowsValid         bool
	playlistList              ui.ListState
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

	a := &application{preferences: state.Preferences, stored: state, view: projection.DefaultView(), playlistExpanded: true, selected: -1, lastAudible: state.Preferences.Volume}
	a.expandedFolders = map[string]bool{}
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.stateWriter = newStateWriter(path, func(err error) {
		if w := a.windowRef.Load(); w != nil {
			w.Update(func() {
				if !a.shutdown {
					a.notice = err.Error()
				}
			})
		}
	})
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
		if a.shutdown {
			return
		}
		a.persist()
		a.shutdown = true
		a.cancel()
		a.cancelSubtitle()
		a.cancelExport()
		if err := a.stateWriter.Close(); err != nil {
			log.Print(err)
		}
		a.windowRef.Store(nil)
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
		a.refreshVideoOnReveal(a.window, 0)
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
func (a *application) importPaths(paths []string) {
	window, ctx := a.window, a.ctx
	go func() {
		entries, subtitles, err := library.FromPaths(paths)
		preferred := library.FirstVideo(library.Tree(entries, nil))
		window.Update(func() {
			if ctx.Err() != nil {
				return
			}
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
			indices := make(map[string]int, len(a.entries)+len(entries))
			for i, entry := range a.entries {
				indices[entry.Path] = i
			}
			for i, entry := range entries {
				if entry.Path == a.stored.LastFile {
					preferred = i
				}
				if _, exists := indices[entry.Path]; !exists {
					indices[entry.Path] = len(a.entries)
					a.entries = append(a.entries, entry)
				}
			}
			a.invalidatePlaylist()
			var expand func([]library.Node)
			expand = func(nodes []library.Node) {
				for _, node := range nodes {
					if node.Folder != nil && node.Folder.Kind == "browser" {
						a.expandedFolders[node.ID] = true
						expand(node.Children)
					}
				}
			}
			expand(a.playlistModel())
			if !a.player.Snapshot().Playing {
				a.openEntry(indices[entries[max(0, preferred)].Path])
			}
		})
	}()
}
func (a *application) openEntry(index int) {
	if index < 0 || index >= len(a.entries) {
		return
	}
	a.cancelExport()
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
	window, ctx := a.window, a.ctx
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: window, Multiple: !directory, Directory: directory})
		window.Update(func() {
			if ctx.Err() != nil {
				return
			}
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
	order := library.PlaybackOrder(a.playlistModel(), a.selected)
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
	a.presentation[1] = presentationState{}
	a.pipWindow = mygo.NewWindow(mygo.WindowOptions{Title: appInfo.Name, Width: 360, Height: 640, MinWidth: 240, MinHeight: 180, AlwaysOnTop: true, BackgroundColor: "#000000", Content: ui.View(func(c *ui.Context) { a.renderStage(c, true) })})
	a.refreshVideoOnReveal(a.pipWindow, 1)
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

func (a *application) refreshVideoOnReveal(window *mygo.Window, index int) {
	refresh := func() {
		a.presentation[index].frame = 0
		window.Invalidate()
	}
	window.OnShow(refresh)
	window.OnRestore(refresh)
	window.OnFocus(refresh)
}
