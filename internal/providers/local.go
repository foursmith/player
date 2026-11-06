// Package providers contains built-in library plugins. Protocol-specific
// configuration and clients stay here, outside the application and data model.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/foursmith/player/internal/scriptplugin"
	"github.com/foursmith/player/library"
)

func NewRegistry() *library.Registry {
	registry := library.NewRegistry(library.Host{}, Local{}, DLNA{})
	plugins, err := scriptplugin.Bundled()
	if err != nil {
		panic(err)
	}
	for _, plugin := range plugins {
		if err := registry.Register(plugin); err != nil {
			panic(err)
		}
	}
	return registry
}

type Local struct{}

func (Local) Manifest() library.Manifest {
	return library.Manifest{ID: library.LocalID, Name: "Local", Icon: "folder", Snapshot: true}
}
func (Local) Normalize(source library.Source) (library.Source, error) {
	var config library.LocalConfig
	if err := json.Unmarshal(source.Config, &config); err != nil {
		return source, err
	}
	if config.Folder != "" {
		if !filepath.IsAbs(config.Folder) {
			return source, errors.New("library folder must be absolute")
		}
		config.Folder = filepath.Clean(config.Folder)
		config.Paths = nil
		config.Folders = nil
	} else {
		config.Paths = library.MergePaths(nil, config.Paths)
		config.Folders = library.MergeFolders(nil, config.Folders)
	}
	source.SetLocal(config)
	return source, nil
}
func (Local) SourceKey(source library.Source) string { return source.LocalFolder() }
func (Local) Open(ctx context.Context, source library.Source, host library.Host) (library.Session, error) {
	return &localSession{source}, ctx.Err()
}

type localSession struct{ source library.Source }

func (s *localSession) Source() library.Source { return s.source }
func (s *localSession) Close() error           { return nil }
func (s *localSession) Browse(ctx context.Context, request library.BrowseRequest) (library.Page, error) {
	if request.Cursor != "" || request.ParentID != "" {
		return library.Page{}, library.ErrUnsupported
	}
	items, err := library.ReadLocal(ctx, s.source)
	return library.Page{Items: items}, err
}
func (s *localSession) Resolve(ctx context.Context, ref library.Reference) (library.Playback, error) {
	entry, err := ResolveLocal(ctx, ref.ItemID)
	if err != nil {
		return library.Playback{}, err
	}
	return library.Playback{Resource: library.Resource{URL: entry.Path}, Subtitle: library.Resource{URL: entry.Subtitle}, Seekable: true}, nil
}

// Local playback reads paths directly, retaining the current file identity.
func ResolveLocal(ctx context.Context, path string) (library.Entry, error) {
	if !filepath.IsAbs(path) {
		return library.Entry{}, os.ErrNotExist
	}
	source := library.Source{Plugin: library.LocalID, Config: library.EncodeConfig(library.LocalConfig{Paths: []string{path}})}
	items, err := library.ReadLocal(ctx, source)
	if err != nil {
		return library.Entry{}, err
	}
	if len(items) == 0 {
		return library.Entry{}, os.ErrNotExist
	}
	if items[0].Error != nil {
		return library.Entry{}, items[0].Error
	}
	return items[0].Entry, nil
}
