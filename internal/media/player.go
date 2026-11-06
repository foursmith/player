package media

import (
	"context"
	"errors"
	"io"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Player owns the decoder, audio clock and video frame. All decoder operations
// run on its worker; consumers receive immutable snapshots and retained frames.
type Player struct {
	commands    chan command
	done        chan struct{}
	state       atomic.Pointer[State]
	frame       frameSlot
	changed     func()
	closeOnce   sync.Once
	seekMu      sync.Mutex
	seekPending bool
	seekValue   float64
	cancelMu    sync.Mutex
	cancel      context.CancelFunc
	trace       atomic.Bool
}
type State struct {
	URL                     string
	Metadata                Metadata
	Position, Volume, Rate  float64
	Playing, Loading, Ended bool
	Generation              uint64
	FrameID                 uint64
	Error                   string
}
type command struct {
	source Source
	kind   string
	value  float64
	ctx    context.Context
}

// NewPlayer notifies changed of playback state transitions. Views sample Frame
// and Snapshot during painting; ordinary video frames do not rebuild their UI.
func NewPlayer(changed func()) *Player {
	p := &Player{commands: make(chan command, 64), done: make(chan struct{}), changed: changed}
	p.state.Store(&State{Volume: 1, Rate: 1})
	go p.run()
	return p
}
func (p *Player) Snapshot() State       { return *p.state.Load() }
func (p *Player) SetTrace(enabled bool) { p.trace.Store(enabled) }
func (p *Player) send(c command) {
	select {
	case p.commands <- c:
	case <-p.done:
	}
}
func (p *Player) cancelActive() {
	p.cancelMu.Lock()
	cancel := p.cancel
	p.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (p *Player) Open(source Source) {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancelMu.Lock()
	old := p.cancel
	p.cancel = cancel
	p.cancelMu.Unlock()
	if old != nil {
		old()
	}
	p.send(command{kind: "open", source: source, ctx: ctx})
}
func (p *Player) Reset() { p.cancelActive(); p.send(command{kind: "reset"}) }
func (p *Player) Play()  { p.send(command{kind: "play"}) }
func (p *Player) Pause() { p.send(command{kind: "pause"}) }
func (p *Player) Toggle() {
	if p.Snapshot().Playing {
		p.Pause()
	} else {
		p.Play()
	}
}
func (p *Player) Seek(seconds float64) {
	p.seekMu.Lock()
	p.seekValue = seconds
	pending := p.seekPending
	p.seekPending = true
	p.seekMu.Unlock()
	if !pending {
		p.send(command{kind: "seek"})
	}
}
func (p *Player) hasPendingSeek() bool {
	p.seekMu.Lock()
	defer p.seekMu.Unlock()
	return p.seekPending
}
func (p *Player) SetVolume(volume float64) {
	p.send(command{kind: "volume", value: math.Max(0, math.Min(1, volume))})
}
func (p *Player) SetRate(rate float64) {
	p.send(command{kind: "rate", value: math.Max(.25, math.Min(4, rate))})
}
func (p *Player) Frame() *Frame { return p.frame.load() }
func (p *Player) Close() {
	p.closeOnce.Do(func() { p.cancelActive(); p.send(command{kind: "close"}); <-p.done })
}
func (p *Player) run() {
	defer close(p.done)
	state := p.Snapshot()
	var decoder *Decoder
	var queue []*Frame
	loadingAt := time.Now()
	reportError := func(stage string, err error) {
		state.Error = err.Error()
		name := state.Metadata.Decoder
		if decoder != nil {
			name = decoder.Metadata().Decoder
		}
		log.Printf("[playback] error stage=%s file=%q decoder=%s position=%.3fs generation=%d detail=%v", stage, state.URL, name, state.Position, state.Generation, err)
	}
	first, drained := false, false
	anchor, anchorAt := 0.0, time.Now()
	var timeline audioTimeline
	publish := func() {
		previous := p.Snapshot()
		copy := state
		p.state.Store(&copy)
		// New pictures and timestamps change the paint input, not the UI.
		if p.changed != nil && playbackViewChanged(previous, copy) {
			p.changed()
		}
	}
	clearFrames := func() {
		for _, frame := range queue {
			frame.Close()
		}
		queue = nil
	}
	clear := func() {
		clearFrames()
		p.frame.store(nil)
		state.FrameID = 0
		if decoder != nil {
			decoder.Close()
			decoder = nil
		}
	}
	defer clear()
	clock := func() float64 {
		if !state.Playing {
			return state.Position
		}
		if decoder != nil {
			if t := decoder.Clock(); !math.IsNaN(t) {
				now := time.Now()
				position := timeline.sample(now, t, decoder.bufferedAudioEnd(), state.Rate)
				// Continue from the latest audio position if its stream ends
				// before the remaining video, rather than the original anchor.
				anchor, anchorAt = position, now
				return position
			}
		}
		return anchor + time.Since(anchorAt).Seconds()*state.Rate
	}
	resetClock := func() {
		anchor, anchorAt = state.Position, time.Now()
		timeline.reset(state.Position)
	}
	seek := func(at float64) error {
		clearFrames()
		decoder.Pause(true)
		state.Generation++
		state.Position = at
		state.Ended = false
		state.Loading = true
		state.Error = ""
		loadingAt = time.Now()
		if p.trace.Load() {
			log.Printf("[playback] seek file=%q requested=%.3fs decoder=%s generation=%d", state.URL, at, decoder.Metadata().Decoder, state.Generation)
		}
		first, drained = true, false
		resetClock()
		// The retained display frame still belongs to the previous position.
		// Publish that the new generation is loading before any seek I/O or
		// decode, so consumers cannot treat that old frame as a fresh sample.
		publish()
		if err := decoder.Seek(at); err != nil {
			first = false
			state.Playing = false
			state.Loading = false
			return err
		}
		return nil
	}
	ticker := time.NewTicker(4 * time.Millisecond)
	ticker.Stop()
	defer ticker.Stop()
	var ticks <-chan time.Time
	for {
		// Paused and empty players wait for commands without polling.
		active := decoder != nil && (state.Playing || first)
		if active && ticks == nil {
			ticker.Reset(4 * time.Millisecond)
			ticks = ticker.C
		} else if !active && ticks != nil {
			ticker.Stop()
			ticks = nil
		}
		select {
		case cmd := <-p.commands:
			switch cmd.kind {
			case "close":
				return
			case "reset":
				clear()
				first, drained = false, false
				state.Generation++
				state.URL = ""
				state.Metadata = Metadata{}
				state.Position = 0
				state.Playing = false
				state.Ended = false
				state.Loading = false
				state.Error = ""
			case "open":
				// A newer selection has already canceled this queued request.
				// Do not tear down or initialize a decoder for obsolete sources.
				if cmd.ctx.Err() != nil {
					continue
				}
				clear()
				state.Generation++
				state.URL = cmd.source.URL
				state.Position = 0
				state.Playing = false
				state.Ended = false
				state.Loading = true
				state.Error = ""
				state.Metadata = Metadata{}
				loadingAt = time.Now()
				if p.trace.Load() {
					log.Printf("[playback] open file=%q resume=%.3fs generation=%d", state.URL, cmd.source.Start, state.Generation)
				}
				publish()
				var err error
				decoder, err = Open(cmd.ctx, cmd.source, true)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						continue
					}
					reportError("open", err)
					state.Loading = false
					publish()
					continue
				}
				state.Metadata = decoder.Metadata()
				if start := cmd.source.Start; start > 0 && start < state.Metadata.Duration-5 {
					if err = decoder.Seek(start); err != nil {
						reportError("resume_seek", err)
					} else {
						state.Position = start
					}
				}
				decoder.Volume(state.Volume)
				if err = decoder.SetRate(state.Rate); err != nil {
					reportError("initial_rate", err)
				}
				first, drained, state.Playing = true, false, true
				resetClock()
			case "play":
				if decoder != nil {
					if state.Ended {
						if err := seek(0); err != nil {
							reportError("replay_seek", err)
						}
					}
					state.Playing = true
					resetClock()
					decoder.Pause(false)
				}
			case "pause":
				if decoder != nil {
					state.Position = clock()
					state.Playing = false
					decoder.Pause(true)
				}
			case "seek":
				p.seekMu.Lock()
				value := p.seekValue
				p.seekPending = false
				p.seekMu.Unlock()
				if decoder != nil {
					state.Playing = true
					if err := seek(math.Max(0, math.Min(state.Metadata.Duration, value))); err != nil {
						reportError("seek", err)
					}
				}
			case "volume":
				state.Volume = cmd.value
				if decoder != nil {
					decoder.Volume(cmd.value)
				}
			case "rate":
				state.Position = clock()
				state.Rate = cmd.value
				resetClock()
				if decoder != nil {
					if err := decoder.SetRate(cmd.value); err != nil {
						reportError("set_rate", err)
					} else if err = seek(state.Position); err != nil {
						reportError("rate_seek", err)
					}
				}
			}
			publish()
		case <-ticks:
			if p.hasPendingSeek() {
				continue
			}
			// Feed audio independently of video decode-ahead: interleaved MP4
			// chunks may put the next audio packet behind many video packets.
			// Publish the first picture before prefetching future audio chunks;
			// the next tick fills audio before ordinary decode-ahead resumes.
			audioAhead := .25
			if first {
				audioAhead = 0
			}
			if err := decoder.bufferAudio(audioAhead, p.hasPendingSeek); err != nil {
				if err == errSeekPending {
					continue
				}
				if errors.Is(err, context.Canceled) {
					continue
				}
				reportError("buffer_audio", err)
				state.Playing = false
				state.Loading = false
				decoder.Pause(true)
				publish()
				continue
			}
			now := clock()
			ahead := len(queue) > 0 && queue[len(queue)-1].PTS()-now >= .12*state.Rate
			if !drained && len(queue) < 16 && (!ahead || len(queue) == 0) {
				frame, err := decoder.next(p.hasPendingSeek)
				if err == io.EOF {
					drained = true
				} else if err == errSeekPending {
					continue
				} else if err != nil {
					if errors.Is(err, context.Canceled) {
						continue
					}
					reportError("decode", err)
					state.Playing = false
					state.Loading = false
					decoder.Pause(true)
					publish()
					continue
				} else {
					queue = append(queue, frame)
				}
			}
			// Decode and demux can take longer than a frame interval. Present
			// against the audio clock after that work, not the stale timestamp
			// sampled before it, and retire every picture already superseded.
			now = clock()
			if len(queue) == 0 {
				if drained && now >= state.Metadata.Duration-.02 {
					state.Playing = false
					state.Ended = true
					state.Position = state.Metadata.Duration
					state.Loading = false
					publish()
				}
				continue
			}
			if first {
				if p.trace.Load() {
					log.Printf("[playback] ready file=%q requested=%.3fs pts=%.3fs decoder=%s generation=%d elapsed=%.2fms", state.URL, state.Position, queue[0].PTS(), decoder.Metadata().Decoder, state.Generation, float64(time.Since(loadingAt))/float64(time.Millisecond))
				}
				state.Position = queue[0].PTS()
				resetClock()
				decoder.Pause(!state.Playing)
				first = false
				now = state.Position
			}
			if queue[0].PTS() > now+.002 {
				continue
			}
			frame := queue[0]
			queue = queue[1:]
			for len(queue) > 0 && queue[0].PTS() <= now {
				frame.Close()
				frame = queue[0]
				queue = queue[1:]
			}
			state.Position = frame.PTS()
			state.Loading = false
			p.frame.store(frame)
			state.FrameID = frame.ID()
			publish()
		}
	}
}

// Chapters and other container metadata are immutable within a source
// generation. A codec fallback can still change the decoder description.
func playbackViewChanged(a, b State) bool {
	return a.URL != b.URL || a.Generation != b.Generation || a.Volume != b.Volume || a.Rate != b.Rate ||
		a.Playing != b.Playing || a.Loading != b.Loading || a.Ended != b.Ended || a.Error != b.Error ||
		a.Metadata.Decoder != b.Metadata.Decoder
}
