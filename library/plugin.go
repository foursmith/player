package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

var ErrPluginUnavailable = errors.New("library plugin unavailable")
var ErrUnsupported = errors.New("library operation unsupported")

// Reference is durable identity, never a temporary playback URL. ParentID is a
// browsing hint, not part of identity. For standalone local files, SourceID is
// empty and ItemID is the absolute filesystem path.
type Reference struct{ SourceID, ItemID, ParentID string }

func (e Entry) Reference() Reference {
	itemID := e.ItemID
	if e.SourceID == "" {
		itemID = e.Path
	}
	return Reference{SourceID: e.SourceID, ItemID: itemID, ParentID: e.ParentID}
}

// Resource and Playback live only for the current session. Providers may renew
// signed URLs and authenticated headers whenever playback is requested.
type Resource struct {
	URL     string
	Headers http.Header
}
type Playback struct {
	Resource
	Subtitle    Resource
	Live        bool
	Seekable    bool
	ExpiresAt   time.Time
	SessionData json.RawMessage `json:"-"`
}

type Manifest struct {
	ID, Name, Icon, RootID string
	// Snapshot sources expose a complete local hierarchy. Others browse on demand.
	Snapshot bool
}
type BrowseRequest struct {
	ParentID, Cursor string
	Limit            int
}
type Page struct {
	Items      []Item
	NextCursor string
}

// Plugin is implemented by built-ins and future adapters. Normalize is pure;
// Open may authenticate/connect using host-provided credentials. A Session is
// owned by one operation and must release resources in Close, including errors.
type Plugin interface {
	Manifest() Manifest
	Normalize(Source) (Source, error)
	Open(context.Context, Source, Host) (Session, error)
}
type Session interface {
	Source() Source
	Browse(context.Context, BrowseRequest) (Page, error)
	Resolve(context.Context, Reference) (Playback, error)
	Close() error
}

// Optional capabilities keep filesystem, server, drive and live-TV plugins small.
type Discoverer interface {
	Discover(context.Context) ([]Source, error)
}

// SourceKeyer can deduplicate connections to the same backend, without merging
// distinct accounts or collections that happen to use the same URL.
type SourceKeyer interface{ SourceKey(Source) string }

// Credentials resolves opaque references through the host's secure store.
// A plugin must return an error if credentials are required but unavailable.
type Credentials interface {
	Lookup(context.Context, string) (map[string]string, error)
}
type Host struct{ Credentials Credentials }

// Registry explicitly assembles plugins; registration is independent of OS shared libraries.
type Registry struct {
	host    Host
	mu      sync.RWMutex
	plugins map[string]Plugin
}

func NewRegistry(host Host, plugins ...Plugin) *Registry {
	r := &Registry{host: host, plugins: map[string]Plugin{}}
	for _, plugin := range plugins {
		if err := r.Register(plugin); err != nil {
			panic(err)
		}
	}
	return r
}
func (r *Registry) Register(plugin Plugin) error {
	m := plugin.Manifest()
	if strings.TrimSpace(m.ID) == "" {
		return errors.New("plugin ID is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.plugins[m.ID]; exists {
		return fmt.Errorf("duplicate library plugin %q", m.ID)
	}
	r.plugins[m.ID] = plugin
	return nil
}
func (r *Registry) Plugin(id string) (Plugin, error) {
	r.mu.RLock()
	p := r.plugins[id]
	r.mu.RUnlock()
	if p == nil {
		return nil, fmt.Errorf("%w: %s", ErrPluginUnavailable, id)
	}
	return p, nil
}
func (r *Registry) Manifests() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Manifest
	for _, p := range r.plugins {
		out = append(out, p.Manifest())
	}
	slices.SortFunc(out, func(a, b Manifest) int { return strings.Compare(a.ID, b.ID) })
	return out
}
func (r *Registry) Manifest(source Source) Manifest {
	p, err := r.Plugin(source.Plugin)
	if err != nil {
		return Manifest{ID: source.Plugin, Name: source.Plugin, Icon: "library"}
	}
	return p.Manifest()
}
func (r *Registry) NormalizeSources(sources []Source) []Source {
	var out []Source
	ids, keys := map[string]bool{}, map[string]bool{}
	for _, source := range CloneSources(sources) {
		source.Name = strings.TrimSpace(source.Name)
		if ValidateSource(source) != nil || ids[source.ID] {
			continue
		}
		if !source.IsLocal() {
			source.ShareDLNA = false
		}
		key := ""
		if p, err := r.Plugin(source.Plugin); err == nil {
			normalized, err := p.Normalize(source)
			if err != nil {
				continue
			}
			source = normalized
			if keyer, ok := p.(SourceKeyer); ok {
				key = keyer.SourceKey(source)
			}
		}
		if key != "" {
			key = source.Plugin + ":" + key
			if keys[key] {
				continue
			}
			keys[key] = true
		}
		ids[source.ID] = true
		out = append(out, source)
	}
	return out
}
func (r *Registry) Open(ctx context.Context, source Source) (Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateSource(source); err != nil {
		return nil, err
	}
	p, err := r.Plugin(source.Plugin)
	if err != nil {
		return nil, err
	}
	source, err = p.Normalize(CloneSources([]Source{source})[0])
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	host := r.host
	r.mu.RUnlock()
	session, err := p.Open(ctx, source, host)
	if err != nil {
		if session != nil {
			_ = session.Close()
		}
		return nil, err
	}
	if session == nil {
		return nil, errors.New("plugin returned an empty session")
	}
	return session, nil
}

// ReadDirectory adapts paged plugins to the current directory UI. Cursors are
// opaque; a repeating cursor is an error, not an unbounded network loop.
func ReadDirectory(ctx context.Context, session Session, parent string) ([]Item, error) {
	var items []Item
	cursor := ""
	seen := map[string]bool{"": true}
	identities := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := session.Browse(ctx, BrowseRequest{ParentID: parent, Cursor: cursor, Limit: 200})
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			if !session.Source().IsLocal() {
				if item.ItemID == "" {
					return nil, errors.New("plugin returned an item without identity")
				}
				item.SourceID = session.Source().ID
				item.ParentID = parent
			}
			key := item.Key()
			if !identities[key] {
				identities[key] = true
				items = append(items, item)
			}
		}
		if page.NextCursor == "" {
			return items, nil
		}
		if seen[page.NextCursor] {
			return nil, errors.New("plugin returned a repeating page cursor")
		}
		cursor = page.NextCursor
		seen[cursor] = true
	}
}

// ResolvePlayback validates the source boundary and freezes mutable HTTP headers
// before the provider session is closed. The returned URLs remain memory-only.
func ResolvePlayback(ctx context.Context, session Session, ref Reference) (Playback, error) {
	source := session.Source()
	if (ref.SourceID != source.ID && !(source.IsLocal() && ref.SourceID == "")) || ref.ItemID == "" {
		return Playback{}, errors.New("invalid media reference")
	}
	playback, err := session.Resolve(ctx, ref)
	if err != nil {
		return Playback{}, err
	}
	return validatePlayback(ctx, playback)
}

func validatePlayback(ctx context.Context, playback Playback) (Playback, error) {
	playback.Headers = playback.Headers.Clone()
	playback.Subtitle.Headers = playback.Subtitle.Headers.Clone()
	playback.SessionData = slices.Clone(playback.SessionData)
	if err := ctx.Err(); err != nil {
		return playback, err
	}
	if strings.TrimSpace(playback.URL) == "" {
		return playback, errors.New("plugin returned an empty playback resource")
	}
	if !playback.ExpiresAt.IsZero() && !playback.ExpiresAt.After(time.Now()) {
		return playback, errors.New("plugin returned an expired playback resource")
	}
	return playback, nil
}

// PreparePlayback creates only the session for the item that will actually play.
func PreparePlayback(ctx context.Context, session Session, request PlayRequest) (Playback, error) {
	if err := ctx.Err(); err != nil {
		return Playback{}, err
	}
	source := session.Source()
	if request.SourceID != source.ID && !(source.IsLocal() && request.SourceID == "") {
		return Playback{}, errors.New("invalid media reference")
	}
	if request.ItemID == "" {
		return Playback{}, errors.New("invalid media reference")
	}
	if preparer, ok := session.(Preparer); ok {
		playback, err := preparer.Prepare(ctx, request)
		if err != nil {
			return playback, err
		}
		return validatePlayback(ctx, playback)
	}
	return ResolvePlayback(ctx, session, request.Reference)
}
func (r *Registry) Report(ctx context.Context, source Source, ref Reference, playback Playback, event PlaybackEvent) error {
	if source.ID != ref.SourceID || ref.ItemID == "" {
		return errors.New("invalid playback reference")
	}
	plugin, err := r.Plugin(source.Plugin)
	if err != nil {
		return err
	}
	if observer, ok := plugin.(PlaybackObserver); ok {
		r.mu.RLock()
		host := r.host
		r.mu.RUnlock()
		return observer.Report(ctx, source, ref, playback, event, host)
	}
	return nil
}
func (r *Registry) SetCredentials(credentials Credentials) {
	r.mu.Lock()
	r.host.Credentials = credentials
	r.mu.Unlock()
}
