package main

import (
	"context"
	"time"

	"github.com/foursmith/player/internal/providers"
	"github.com/foursmith/player/library"
)

var libraryPlugins = providers.NewRegistry()

func sourceRoot(source library.Source) string { return libraryPlugins.Manifest(source).RootID }
func sourceSnapshot(source library.Source) bool {
	return libraryPlugins.Manifest(source).Snapshot
}

// All directory reads use the plugin boundary, including local snapshots.
func readCatalog(parent context.Context, source library.Source, parentID string, dispatch func(func()), complete func(library.Source, []library.Item, error)) context.CancelFunc {
	var ctx context.Context
	var cancel context.CancelFunc
	if sourceSnapshot(source) {
		ctx, cancel = context.WithCancel(parent)
	} else {
		ctx, cancel = context.WithTimeout(parent, 20*time.Second)
	}
	source = library.CloneSources([]library.Source{source})[0]
	go func() {
		session, err := libraryPlugins.Open(ctx, source)
		var items []library.Item
		if err == nil {
			items, err = library.ReadDirectory(ctx, session, parentID)
			source = library.CloneSources([]library.Source{session.Source()})[0]
			closeErr := session.Close()
			if err == nil {
				err = closeErr
			}
		}
		dispatch(func() {
			defer cancel()
			if parent.Err() == nil && ctx.Err() != context.Canceled {
				if err == nil {
					err = ctx.Err()
				}
				complete(source, items, err)
			}
		})
	}()
	return cancel
}
