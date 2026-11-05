package dlnaserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/foursmith/player/internal/library"
)

type Options struct {
	UUID, Name string
	Sources    []library.Saved
	Metadata   func(context.Context, library.Entry) (library.MediaInfo, error)
}

// NewUUID returns a persistent device identity, independent of its LAN address.
func NewUUID() string {
	var id [16]byte
	_, _ = rand.Read(id[:])
	id[6], id[8] = id[6]&15|64, id[8]&63|128
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}

func ValidUUID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil
}

type Server struct {
	ctx        context.Context
	cancel     context.CancelFunc
	name       string
	udn        string
	port       int
	http       *http.Server
	ssdp       *discovery
	wg         sync.WaitGroup
	stop       sync.Once
	reload     chan struct{}
	events     chan struct{}
	mu         sync.RWMutex
	sources    []library.Saved
	scanCancel context.CancelFunc
	revision   uint64
	objects    catalog
	updateID   uint32
	metadata   *metadataIndex
	subs       map[string]*subscription
	transfers  map[*http.Request]transfer
}

type transfer struct {
	source string
	stop   func()
}

// Start performs startup in its caller's worker, never on the UI thread.
func Start(parent context.Context, options Options) (*Server, error) {
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if !ValidUUID(options.UUID) {
		return nil, errors.New("invalid media server UUID")
	}
	ctx, cancel := context.WithCancel(parent)
	s := &Server{ctx: ctx, cancel: cancel, name: options.Name, udn: "uuid:" + options.UUID, sources: SharedSources(options.Sources), reload: make(chan struct{}, 1), events: make(chan struct{}, 1), subs: map[string]*subscription{}}
	var err error
	s.objects, err = readCatalog(ctx, s.name, s.sources)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		cancel()
		return nil, err
	}
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		cancel()
		return nil, err
	}
	s.port = listener.Addr().(*net.TCPAddr).Port
	s.ssdp, err = startDiscovery(s)
	if err != nil {
		listener.Close()
		cancel()
		return nil, err
	}
	s.http = &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	s.startMetadata(options.Metadata)
	s.wg.Go(func() {
		if err := s.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[dlna-server] HTTP stopped: %v", err)
			s.cancel()
		}
	})
	s.wg.Go(s.refreshLoop)
	s.wg.Go(s.eventLoop)
	s.ssdp.run()
	go func() { <-ctx.Done(); s.Close() }()
	log.Printf("[dlna-server] sharing %d local libraries on port %d", len(s.sources), s.port)
	return s, nil
}

func (s *Server) Done() <-chan struct{} { return s.ctx.Done() }

func (s *Server) Locations() []string {
	var locations []string
	for _, link := range s.ssdp.links {
		locations = append(locations, s.baseURL(link.ip)+"/device.xml")
	}
	return locations
}

func (s *Server) baseURL(ip net.IP) string {
	return fmt.Sprintf("http://%s", net.JoinHostPort(ip.String(), fmt.Sprint(s.port)))
}

// SetSources revokes old URLs immediately when the user's library changes.
func (s *Server) SetSources(sources []library.Saved) {
	local := SharedSources(sources)
	s.mu.Lock()
	if reflect.DeepEqual(s.sources, local) {
		s.mu.Unlock()
		return
	}
	// Retain unchanged libraries while rescanning. Unsharing one library must
	// neither interrupt another library's stream nor temporarily hide its files.
	previous := map[string]library.Saved{}
	for _, source := range s.sources {
		previous[source.ID] = source
	}
	kept := map[string]bool{}
	names := map[string]string{}
	for _, source := range local {
		old, exists := previous[source.ID]
		old.Name = source.Name
		if exists && reflect.DeepEqual(old, source) {
			kept[source.ID], names[source.ID] = true, source.Name
		}
	}
	objects := emptyCatalog(s.name, local)
	for id, item := range s.objects {
		if kept[item.Source] {
			if item.Parent == "0" {
				item.Name = names[item.Source]
			}
			objects[id] = item
		}
	}
	for _, active := range s.transfers {
		if !kept[active.source] {
			active.stop()
		}
	}
	s.sources = local
	if s.scanCancel != nil {
		s.scanCancel()
	}
	s.revision++
	s.objects = objects
	s.indexMetadata(objects)
	s.updateID++
	s.mu.Unlock()
	s.Refresh()
	s.queueEvents()
	s.scheduleMetadata()
}

func (s *Server) Refresh() {
	select {
	case s.reload <- struct{}{}:
	default:
	}
}

func (s *Server) refreshLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		case <-s.reload:
		}
		s.mu.Lock()
		sources, revision := library.CloneSaved(s.sources), s.revision
		ctx, cancel := context.WithCancel(s.ctx)
		s.scanCancel = cancel
		s.mu.Unlock()
		objects, err := readCatalog(ctx, s.name, sources)
		cancel()
		s.mu.Lock()
		s.scanCancel = nil
		if err == nil && revision == s.revision {
			s.indexMetadata(objects)
		}
		changed := err == nil && revision == s.revision && !reflect.DeepEqual(objects, s.objects)
		if changed {
			s.objects = objects
			s.updateID++
		}
		s.mu.Unlock()
		if changed {
			s.queueEvents()
		}
		if err == nil {
			s.scheduleMetadata()
		}
	}
}

// Close withdraws advertisements and closes active transfers as well as listeners.
func (s *Server) Close() {
	s.stop.Do(func() {
		s.cancel()
		s.http.Close()
		s.ssdp.close()
		s.wg.Wait()
		log.Print("[dlna-server] sharing stopped")
	})
}
