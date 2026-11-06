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
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/projection"
	"github.com/foursmith/player/internal/scriptplugin"
	"github.com/foursmith/player/internal/subtitle"
	"github.com/foursmith/player/internal/tracking"
	"github.com/foursmith/player/internal/video"
	"github.com/foursmith/player/library"
)

type application struct {
	pluginCatalog                             *scriptplugin.Catalog
	credentials                               library.CredentialWriter
	connectionEditor                          *connectionEditor
	playbackSession                           *playbackSession
	playbackRefreshAttempt                    string
	playbackID                                uint64
	ctx                                       context.Context
	cancel                                    context.CancelFunc
	stateWriter                               *stateWriter
	subtitleCancel                            func()
	trackingLog                               []string
	recordingTracking                         bool
	trackingSampleWidth, trackingSampleHeight int
	presentation                              [2]presentationState
	controlsVisible                           bool
	homeAnimation                             homeAnimationClock
	scanning                                  bool
	importGeneration                          uint64
	playbackRestoreCancel                     context.CancelFunc
	playlistSnapshot                          *playlistSnapshot
	menuLocale                                string
	dialogOpen                                bool
	expandedFolders                           map[string]bool
	playlistTree                              []library.Node
	playlistRows                              []playlistRow
	playlistRemote                            map[string]*playlistRemoteSource
	playlistTreeValid                         bool
	playlistRowsValid                         bool
	playlistList                              ui.ListState
	timelineHandle                            ui.Handle
	controlsPanel                             ui.Handle
	projectionButton                          ui.Handle
	adjustmentsButton                         ui.Handle
	scrubbing                                 bool
	scrubTime                                 float64
	seekUI                                    seekInteraction
	seekPreviews                              *seekPreviews
	tracker                                   tracking.Tracker
	inference                                 *inferenceWorker
	lastTrackingAt                            float64
	trackingImage                             *ui.Bitmap
	window                                    *mygo.Window
	windowRef                                 atomic.Pointer[mygo.Window]
	player                                    *media.Player
	surface                                   video.Surface
	pipWindow                                 *mygo.Window
	pipWindowRef                              atomic.Pointer[mygo.Window]
	pipSurface                                video.Surface
	settingsWindow                            *mygo.Window
	catalogWindow                             *mygo.Window
	catalog                                   catalogState
	dlnaSharing                               dlnaSharingState
	preferences                               preferences
	stored                                    storedState
	entries                                   []library.Entry
	subtitlePaths                             []string
	pendingPaths                              []string
	openFilesAt                               time.Time
	cues                                      []subtitle.Cue
	selected                                  int
	view                                      projection.View
	flatView                                  video.FlatView
	mode                                      projection.Mode
	detectMode                                bool
	modeManual                                bool
	modeDetection                             *modeDetectionTask
	recognitionTest                           bool
	modeDetectionSequence                     uint64
	settingsTab                               int
	settingsScroll                            [settingsTabCount]ui.ScrollState
	shortcutRecording                         string
	shortcutError                             string
	playlistExpanded                          bool
	projectionOpen                            bool
	projectionFocused                         int
	projectionItems                           [8]ui.Handle
	adjustmentsOpen                           bool
	debugOpen                                 bool
	controlsUntil                             time.Time
	controlsHeld                              bool
	lastPointer, pressPointer                 ui.Point
	dragging                                  bool
	dragDistance                              float64
	singleClickAt                             time.Time
	width, height                             float32
	loopA, loopB                              *float64
	loopSeekGeneration                        uint64
	notice                                    string
	lastAudible                               float64
	lastGeneration                            uint64
	lastSaved                                 time.Time
	endedGeneration                           uint64
	manuallyPaused                            bool
	manualUntil                               time.Time
	shutdown                                  bool
}

func main() {
	options, err := parseApplicationOptions(os.Args[1:])
	if err != nil {
		if err != flag.ErrHelp {
			os.Exit(2)
		}
		return
	}
	if err := loadAssets(); err != nil {
		log.Fatal(err)
	}
	mygo.App.SetName(appInfo.Name)
	mygo.App.SetLanguages("en", "zh-CN", "ja")
	if options.dataDir != "" {
		mygo.App.SetPath(mygo.PathUserData, options.dataDir)
	}
	userData, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	libraryPlugins.SetCredentials(nativeCredentials{})
	pluginCatalog := &scriptplugin.Catalog{Dir: filepath.Join(userData, "plugins"), Registry: libraryPlugins}
	for _, err := range pluginCatalog.Load() {
		log.Printf("[plugin] %v", err)
	}
	path := filepath.Join(userData, "state.json")
	state := loadState(path)

	a := &application{pluginCatalog: pluginCatalog, credentials: nativeCredentials{}, preferences: state.Preferences, stored: state, view: projection.DefaultView(), mode: projection.Flat, playlistExpanded: true, selected: -1, lastAudible: state.Preferences.Volume, recognitionTest: options.recognitionTest}
	mygo.Theme.SetSource(a.preferences.Appearance)
	if a.recognitionTest {
		log.Print("[recognition-test] enabled: saved video types bypassed; history types preserved")
	}
	a.expandedFolders = map[string]bool{}
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	stopAppearanceUpdates := mygo.Theme.OnUpdated(a.refreshAppearance)
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
		if w := a.pipWindowRef.Load(); w != nil {
			w.Invalidate()
		}
	})
	a.player.SetTrace(a.recognitionTest)
	a.player.SetVolume(a.preferences.Volume)
	a.player.SetRate(a.preferences.PlaybackRate)
	mygo.App.OnSecondInstance(func(args []string, _ string) {
		if a.window != nil {
			a.window.Update(func() {
				a.showWindow()
				if len(args) == 0 {
					return
				}
				incoming, err := parseApplicationOptions(args[1:])
				if err != nil {
					log.Printf("Second-instance arguments: %v", err)
					return
				}
				if incoming.recognitionTest {
					a.recognitionTest = true
					a.player.SetTrace(true)
					log.Print("[recognition-test] enabled by second instance: saved video types bypassed")
				}
				if len(incoming.paths) > 0 {
					a.importPaths(incoming.paths, false)
				} else if incoming.recognitionTest {
					a.redetectMode()
				}
			})
		}
	})
	mygo.App.OnOpenFile(func(path string) {
		if w := a.windowRef.Load(); w != nil {
			w.Update(func() {
				a.showWindow()
				a.pendingPaths = append(a.pendingPaths, path)
				a.openFilesAt = time.Now().Add(30 * time.Millisecond)
			})
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
		a.cancelConnectionEditor()
		sessionDone := a.stopPlaybackSession()
		if sessionDone != nil {
			select {
			case <-sessionDone:
			case <-time.After(2 * time.Second):
			}
		}
		stopAppearanceUpdates()
		sharing := a.dlnaSharing.server
		a.cancel()
		a.stopDLNASharing()
		if sharing != nil {
			sharing.Close()
		}
		a.cancelCatalogRead()
		a.stopCatalogThumbnails()
		a.stopSeekPreviews()
		a.cancelModeDetection()
		a.cancelSubtitle()
		if err := a.stateWriter.Close(); err != nil {
			log.Print(err)
		}
		a.windowRef.Store(nil)
		a.pipWindowRef.Store(nil)
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
	mygo.App.OnActivate(func(bool) {
		if a.window != nil && !a.shutdown && !a.window.IsVisible() {
			a.showWindow()
		}
	})
	mygo.App.WhenReady(func() {
		a.window = mygo.NewWindow(mygo.WindowOptions{Title: appInfo.Name, Width: 1200, Height: 800, MinWidth: 360, MinHeight: 320, UseContentSize: true, TitleBarStyle: mygo.TitleBarHiddenInset, BackgroundColor: "#000000", Transparent: video.TransparentWindow, Hidden: true, Content: ui.View(a.render)})
		a.window.SetWindowButtonsTheme(mygo.ThemeDark)
		a.windowRef.Store(a.window)
		a.window.OnFocus(func() {
			a.homeAnimation.setFocused(time.Now(), true)
			a.window.Invalidate()
		})
		a.window.OnBlur(func() {
			a.homeAnimation.setFocused(time.Now(), false)
			a.window.Invalidate()
		})
		a.inference = newInferenceWorker(a)
		var err error
		a.surface, err = video.New(a.window.NativeHandle())
		if err != nil {
			a.notice = err.Error()
		}
		a.window.OnReadyToShow(a.window.Show)
		a.refreshVideoOnReveal(a.window, 0)
		a.window.OnFileDrop(func(event *mygo.FileDropEvent) { a.importPaths(event.Paths, false) })
		a.window.OnClose(a.closePlayerWindow)
		a.refreshMenu()
		initialPaths := append(append([]string{}, options.paths...), a.pendingPaths...)
		a.pendingPaths = nil
		if len(initialPaths) > 0 {
			a.importPaths(initialPaths, false)
		} else if a.preferences.RecordPlaybackHistory && a.preferences.AutoResume && a.stored.LastPlayback != nil {
			a.openSavedPlayback(*a.stored.LastPlayback)
		}
		a.persist()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func (a *application) activity(delay time.Duration) { a.controlsUntil = time.Now().Add(delay) }

// Opening replaces the queue and always plays the requested selection.
// Appending leaves the current selection alone, including when it is paused.
func (a *application) importPaths(paths []string, appendOnly bool) {
	if !appendOnly {
		a.cancelImports()
	}
	generation := a.importGeneration
	window, ctx := a.window, a.ctx
	go func() {
		entries, subtitles, err := library.FromPaths(ctx, paths)
		window.Update(func() {
			if ctx.Err() != nil || generation != a.importGeneration {
				return
			}
			if err != nil {
				a.notice = err.Error()
				return
			}
			if len(entries) == 0 {
				if len(subtitles) > 0 {
					a.subtitlePaths = append(a.subtitlePaths, subtitles...)
					a.playlistSnapshot = nil
					a.loadSubtitle(subtitles[0])
				} else {
					a.notice = a.t("media.noVideos")
				}
				return
			}
			a.applyImportedEntries(entries, subtitles, appendOnly, "")
		})
	}()
}
func (a *application) openEntry(index int) {
	a.cancelImports()
	a.playEntry(index)
}

func (a *application) startEntry(index int, source library.Source, position float64) {
	if index < 0 || index >= len(a.entries) {
		return
	}
	a.persist()
	a.stopPlaybackSession()
	a.cancelModeDetection()
	a.stopSeekPreviews()
	a.seekUI = seekInteraction{}
	a.selected = index
	a.view = projection.DefaultView()
	a.flatView = video.FlatView{}
	a.tracker.Reset()
	entry := a.entries[index]
	start := a.prepareVideoMode(entry)
	if source.ID != "" && entry.Playback != nil && !entry.Playback.Live && entry.Playback.Seekable {
		start = position
	}
	a.loopA = nil
	a.loopB = nil
	a.manuallyPaused = false
	a.notice = ""
	a.cues = nil
	a.singleClickAt = time.Time{}
	a.scrubbing, a.dragging = false, false
	input := media.FromEntry(entry)
	input.Start = start
	a.playbackID = a.player.Open(input)
	a.startPlaybackSession(source, entry, start)
	a.window.SetTitle(entry.Name + " — " + appInfo.Name)
	a.rememberStartupPlayback(entry)
	if a.rememberRecentMedia(entry) {
		a.refreshMenu()
	}
	a.persist()
	a.activity(3600 * time.Millisecond)
	a.readSubtitle(entry, "", true)
}
func (a *application) choose(directory, appendOnly bool) {
	if a.dialogOpen {
		return
	}
	a.showWindow()
	a.dialogOpen = true
	window, ctx := a.window, a.ctx
	title := a.t("media.openFiles")
	if directory && appendOnly {
		title = a.t("media.addFolder")
	} else if directory {
		title = a.t("media.openFolder")
	} else if appendOnly {
		title = a.t("media.addFiles")
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: window, Title: title, Multiple: !directory, Directory: directory})
		window.Update(func() {
			a.dialogOpen = false
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				a.notice = err.Error()
			} else if len(paths) > 0 {
				a.importPaths(paths, appendOnly)
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
		if automatic {
			a.playEntry(order[next])
		} else {
			a.openEntry(order[next])
		}
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
	if a.mode == projection.Flat {
		return video.Parameters{Mode: a.mode, FlatView: a.flatView, SplitCount: 1}
	}
	count := a.preferences.SplitCount
	if pip {
		count = 1
	}
	return video.Parameters{Mode: a.mode, View: a.view, SplitCount: count, Portrait: a.preferences.PreferredSplitAspect == "9:16"}
}
func (a *application) togglePip() {
	if a.pipWindow != nil {
		a.pipWindow.Close()
		return
	}
	a.presentation[1] = presentationState{}
	width, height := 360, 640
	if a.mode == projection.Flat {
		metadata := a.player.Snapshot().Metadata
		width, height = 480, 270
		if metadata.Width > 0 && metadata.Height > 0 {
			if metadata.Width >= metadata.Height {
				height = max(180, int(480*float64(metadata.Height)/float64(metadata.Width)))
			} else {
				width, height = max(240, int(480*float64(metadata.Width)/float64(metadata.Height))), 480
			}
		}
	}
	a.pipWindow = mygo.NewWindow(mygo.WindowOptions{Title: appInfo.Name, Width: width, Height: height, MinWidth: 240, MinHeight: 180, AlwaysOnTop: true, BackgroundColor: "#000000", Transparent: video.TransparentWindow, Content: ui.View(func(c *ui.Context) { a.renderStage(c, true) })})
	a.pipWindow.SetWindowButtonsTheme(mygo.ThemeDark)
	a.pipWindowRef.Store(a.pipWindow)
	a.refreshVideoOnReveal(a.pipWindow, 1)
	var err error
	a.pipSurface, err = video.New(a.pipWindow.NativeHandle())
	if err != nil {
		a.notice = err.Error()
	}
	a.pipWindow.OnClosed(func() {
		a.pipWindowRef.Store(nil)
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
