package video

import (
	"errors"
	"fmt"
	"sync"

	"github.com/ebitengine/purego/objc"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/native"
	"github.com/foursmith/player/internal/projection"
)

// macOS AVKit cannot present custom decoded frames through its public sample
// buffer PiP API. PIP is the system framework used by WebKit for custom views.
// Keep its private API isolated here and fail explicitly if it is unavailable.
var loadPIP = sync.OnceValue(func() error {
	if err := loadMetal(); err != nil {
		return err
	}
	if _, err := native.Open("/System/Library/PrivateFrameworks/PIP.framework/PIP"); err != nil {
		return err
	}
	if class("PIPViewController") == 0 {
		return errors.New("system picture-in-picture unavailable")
	}
	for _, name := range []string{"setDelegate:", "setPlaying:", "setUserCanResize:", "setAspectRatio:", "setReplacementWindow:", "setReplacementRect:", "presentViewControllerAsPictureInPicture:", "dismissViewController:"} {
		if !objc.Send[bool](class("PIPViewController"), selector("instancesRespondToSelector:"), selector(name)) {
			return fmt.Errorf("system picture-in-picture missing %s", name)
		}
	}
	_, err := objc.RegisterClass("FoursmithPIPDelegate", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		{Cmd: selector("pipShouldClose:"), Fn: func(objc.ID, objc.SEL, objc.ID) bool { return true }},
		{Cmd: selector("pipDidClose:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) {
			if p := pipDelegates[self]; p != nil {
				// Defer destruction until the system has finished its delegate call.
				p.closed = true
			}
		}},
		{Cmd: selector("pipActionPlay:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) {
			if p := pipDelegates[self]; p != nil {
				p.actions.Play()
			}
		}},
		{Cmd: selector("pipActionPause:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) {
			if p := pipDelegates[self]; p != nil {
				p.actions.Pause()
			}
		}},
		{Cmd: selector("pipActionStop:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) {
			if p := pipDelegates[self]; p != nil {
				p.actions.Pause()
			}
		}},
	})
	return err
})

// Accessed only on the main thread, including native delegate callbacks.
var pipDelegates = map[objc.ID]*systemPIP{}

type systemPIP struct {
	controller, content, view, delegate, caption objc.ID
	renderer                                     *metal
	actions                                      PictureInPictureActions
	closed, closing, started                     bool
	lastFrame                                    uint64
	lastParams                                   Parameters
	lastWidth, lastHeight, lastScale             float64
	lastCaption                                  string
	lastPlaying                                  bool
	aspect                                       float64
}

func NewPictureInPicture(window uintptr, width, height float64, actions PictureInPictureActions) (PictureInPicture, error) {
	if err := loadPIP(); err != nil {
		return nil, err
	}
	defer metalPool()()
	p := &systemPIP{actions: actions, aspect: width / height}
	p.controller = send(class("PIPViewController"), "new")
	p.content = send(class("NSViewController"), "new")
	p.view = newPIPVideoView(width, height)
	p.delegate = send(class("FoursmithPIPDelegate"), "new")
	pipDelegates[p.delegate] = p
	send(p.content, "setView:", p.view)
	var err error
	p.renderer, err = newMetal(p.view)
	if err != nil {
		p.closed = true
		p.Close()
		return nil, err
	}
	p.caption = send(class("CATextLayer"), "new")
	send(p.caption, "setAlignmentMode:", send(class("NSString"), "stringWithUTF8String:", "center"))
	send(p.caption, "setWrapped:", true)
	send(p.caption, "setShadowOpacity:", float32(1))
	send(p.caption, "setShadowRadius:", float64(2))
	send(send(p.view, "layer"), "addSublayer:", p.caption)
	send(p.controller, "setDelegate:", p.delegate)
	send(p.controller, "setUserCanResize:", true)
	send(p.controller, "setAspectRatio:", cgSize{Width: width, Height: height})
	send(p.controller, "setReplacementWindow:", objc.ID(window))
	send(p.controller, "setReplacementRect:", objc.Send[cgRect](send(objc.ID(window), "contentView"), selector("bounds")))
	return p, nil
}

func newPIPVideoView(width, height float64) objc.ID {
	view := send(send(class("NSView"), "alloc"), "initWithFrame:", cgRect{Width: width, Height: height})
	// PIP owns the parent container. Follow both dimensions so Size() and the
	// Metal drawable track its live resize, including during paused playback.
	send(view, "setAutoresizingMask:", uintptr(18)) // NSViewWidthSizable | NSViewHeightSizable
	return view
}

func (p *systemPIP) Size() (float64, float64) {
	b := objc.Send[cgRect](p.view, selector("bounds"))
	return b.Width, b.Height
}

func (p *systemPIP) Present(frame *media.Frame, params Parameters, caption string, playing bool) error {
	if p.closed || p.closing {
		return nil
	}
	defer metalPool()()
	w, h := p.Size()
	aspect := float64(9) / 16
	if params.Mode == projection.Flat {
		fw, fh := frame.Size()
		aspect = float64(fw) / float64(max(1, fh))
	}
	if aspect != p.aspect {
		p.aspect = aspect
		send(p.controller, "setAspectRatio:", cgSize{Width: aspect, Height: 1})
	}
	window := send(p.view, "window")
	scale := float64(2)
	if window != 0 {
		scale = objc.Send[float64](window, selector("backingScaleFactor"))
	}
	if w <= 0 || h <= 0 {
		return nil
	}
	if p.started && window != 0 && frame.ID() == p.lastFrame && params == p.lastParams && w == p.lastWidth && h == p.lastHeight && scale == p.lastScale && caption == p.lastCaption && playing == p.lastPlaying {
		return nil
	}
	drawn, err := p.renderer.present(frame, params, w, h, scale)
	if err != nil {
		return fmt.Errorf("picture-in-picture: %w", err)
	}
	send(class("CATransaction"), "begin")
	send(class("CATransaction"), "setDisableActions:", true)
	send(p.caption, "setContentsScale:", scale)
	send(p.caption, "setFontSize:", min(float64(24), max(float64(12), w*.04)))
	send(p.caption, "setFrame:", cgRect{X: w * .06, Y: h * .10, Width: w * .88, Height: h * .22})
	send(p.caption, "setString:", send(class("NSString"), "stringWithUTF8String:", caption))
	send(class("CATransaction"), "commit")
	send(p.controller, "setPlaying:", playing)
	// Present only after the first frame is ready, including paused playback.
	if !p.started {
		p.started = true
		send(p.controller, "presentViewControllerAsPictureInPicture:", p.content)
	}
	if window != 0 && drawn {
		p.lastFrame, p.lastParams = frame.ID(), params
		p.lastWidth, p.lastHeight, p.lastScale = w, h, scale
		p.lastCaption, p.lastPlaying = caption, playing
	}
	return nil
}

func (p *systemPIP) Closed() bool { return p.closed }

func (p *systemPIP) Close() {
	if p.controller == 0 {
		return
	}
	defer metalPool()()
	if !p.started && !p.closed {
		p.closed = true
	}
	if !p.closed {
		if !p.closing {
			p.closing = true
			send(p.controller, "dismissViewController:", p.content)
		}
		return
	}
	send(p.controller, "setDelegate:", objc.ID(0))
	delete(pipDelegates, p.delegate)
	if p.renderer != nil {
		p.renderer.Close()
		p.renderer = nil
	}
	for _, object := range []*objc.ID{&p.caption, &p.content, &p.view, &p.delegate, &p.controller} {
		if *object != 0 {
			send(*object, "release")
			*object = 0
		}
	}
}
