package scriptplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/eventloop"
	"github.com/MoeForge/moejs/webapi"
	"github.com/foursmith/player/library"
)

// A compiled module is reused, but each operation owns its VM. Canceling a
// browse/preview cannot terminate another connection's actual playback work.
type engine struct {
	rt     *moejs.Runtime
	loop   *eventloop.Loop
	api    *webapi.API
	plugin *Plugin
	helper moejs.Value
	allow  func(*http.Request) error
	closed chan struct{}
	once   sync.Once
	mu     sync.Mutex
}

var dispatchScript = func() *moejs.Script {
	script, err := moejs.CompileFunction("plugin-call", "fn,arg,ok,bad", `
 const controller = new AbortController();
 globalThis.__foursmithCancel = () => controller.abort();
 const accepted = value => { globalThis.__foursmithCancel=undefined;ok(value===undefined?null:value); };
 const rejected = error => { globalThis.__foursmithCancel=undefined;bad(String(error?.code||""),String(error?.message||error)); };
 try {
   Promise.resolve(fn({...arg, signal:controller.signal})).then(
     accepted,rejected
   );
 } catch(error){rejected(error);}
 `, moejs.FunctionOptions{})
	if err != nil {
		panic(err)
	}
	return script
}()

var abortScript = func() *moejs.Script {
	script, err := moejs.CompileFunction("plugin-abort", "", `globalThis.__foursmithCancel?.();`, moejs.FunctionOptions{})
	if err != nil {
		panic(err)
	}
	return script
}()

func (p *Plugin) start(ctx context.Context, source library.Source, config map[string]string) (*engine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rt := moejs.NewRuntime(moejs.Options{MemoryLimit: 64 << 20})
	loop, err := eventloop.New(rt, eventloop.Options{})
	if err != nil {
		return nil, err
	}
	e := &engine{rt: rt, loop: loop, plugin: p, allow: p.networkPolicy(config), closed: make(chan struct{})}
	e.api, err = webapi.Install(loop, webapi.Options{Client: &http.Client{Timeout: 30 * time.Second}, Allow: e.allow})
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { rt.Interrupt(ctx.Err()) })
	defer stop()
	fail := func(err error) (*engine, error) {
		e.close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if err = rt.SetGlobal("host", map[string]any{"platform": runtime.GOOS, "arch": runtime.GOARCH, "sourceId": source.ID}); err != nil {
		return fail(err)
	}
	e.helper, err = rt.RunScript(dispatchScript)
	if err != nil {
		return fail(err)
	}
	if err = rt.Load(p.module); err != nil && !errors.Is(err, moejs.ErrModulePending) {
		return fail(err)
	}
	if err = loop.Start(); err != nil {
		return fail(err)
	}
	return e, nil
}
func (e *engine) close() { e.once.Do(func() { close(e.closed); e.api.Close(); e.loop.Terminate() }) }

type answer struct {
	data []byte
	err  error
}

func (e *engine) call(parent context.Context, method string, params any, out any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.plugin.exports[method] {
		return library.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	result := make(chan answer, 1)
	if !e.loop.RunOnLoop(func(rt *moejs.Runtime) {
		fn, ok := rt.Export(method)
		if !ok {
			result <- answer{err: library.ErrUnsupported}
			return
		}
		arg, err := rt.ParseJSON(encoded)
		if err != nil {
			result <- answer{err: err}
			return
		}
		var once sync.Once
		done := func(a answer) {
			once.Do(func() { result <- a; e.loop.RunOnLoop(func(rt *moejs.Runtime) { rt.ReleaseCallData() }) })
		}
		okFn := rt.Function("answer", 1, func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			var data json.RawMessage
			err := rt.UnmarshalWith(moejs.Arg(args, 0), &data, moejs.DecodeOptions{MaxBytes: 8 << 20, MaxNodes: 100000})
			done(answer{data, err})
			return moejs.Undefined(), nil
		})
		badFn := rt.Function("reject", 2, func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			code, message := moejs.Arg(args, 0).String(), moejs.Arg(args, 1).String()
			var err error = &library.SourceError{Code: code, Message: message}
			if code == "unsupported" {
				err = library.ErrUnsupported
			}
			done(answer{err: err})
			return moejs.Undefined(), nil
		})
		if _, err := rt.CallFunction(e.helper, moejs.Undefined(), fn, arg, okFn, badFn); err != nil {
			done(answer{err: err})
		}
	}) {
		return errors.New("plugin operation closed")
	}
	select {
	case result := <-result:
		if result.err != nil {
			return result.err
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(result.data, out)
	case <-ctx.Done():
		// Cooperating code observes AbortSignal first. A CPU loop, or an abort
		// handler that never returns, is interrupted after this short grace period.
		aborted := make(chan struct{})
		if e.loop.RunOnLoop(func(rt *moejs.Runtime) {
			fn, err := rt.RunScript(abortScript)
			if err == nil {
				_, _ = rt.CallFunction(fn, moejs.Undefined())
			}
			close(aborted)
		}) {
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-aborted:
			case <-timer.C:
			}
			timer.Stop()
		}
		e.close()
		return ctx.Err()
	case <-e.closed:
		return errors.New("plugin operation closed")
	}
}
func (p *Plugin) networkPolicy(config map[string]string) func(*http.Request) error {
	origins := map[string]bool{}
	var hosts []string
	for _, rule := range p.descriptor.Network {
		if strings.HasPrefix(rule, "$") {
			u, err := url.Parse(config[rule[1:]])
			if err == nil {
				origins[strings.ToLower(u.Scheme+"://"+u.Host)] = true
			}
		} else {
			hosts = append(hosts, rule)
		}
	}
	allowHost := webapi.AllowHosts(hosts...)
	return func(request *http.Request) error {
		u := request.URL
		if (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return errors.New("plugin may only access HTTP resources")
		}
		if request.Host != "" && !strings.EqualFold(request.Host, u.Host) {
			return errors.New("plugin cannot override the request host")
		}
		if origins[strings.ToLower(u.Scheme+"://"+u.Host)] {
			return nil
		}
		if len(hosts) > 0 && allowHost(request) == nil {
			return nil
		}
		return fmt.Errorf("plugin network permission denied for %s", u.Hostname())
	}
}
