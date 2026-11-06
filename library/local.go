package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

type Item struct {
	Entry
	Error    error
	Folder   bool
	Duration float64
}

func (s Item) Name() string {
	if s.Entry.Name != "" {
		return s.Entry.Name
	}
	return filepath.Base(s.Path)
}

func PlayableEntries(items []Item) []Entry {
	var entries []Entry
	for _, item := range items {
		if !item.Folder && item.Error == nil {
			entries = append(entries, item.Entry)
		}
	}
	return entries
}

func MergePaths(current, incoming []string) []string {
	return mergeLocalPaths(current, incoming, true)
}

// MergeFolders preserves offline locations without filesystem IO.
func MergeFolders(current, incoming []string) []string {
	return mergeLocalPaths(current, incoming, false)
}

func mergeLocalPaths(current, incoming []string, videosOnly bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, paths := range [][]string{current, incoming} {
		for _, path := range paths {
			if !filepath.IsAbs(path) || videosOnly && !IsVideo(path) || IsAppleDouble(path) {
				continue
			}
			path = filepath.Clean(path)
			if !seen[path] {
				seen[path] = true
				out = append(out, path)
			}
		}
	}
	return out
}

func LocalPaths(entries []Entry) []string {
	var paths []string
	for _, entry := range entries {
		if entry.SourceID == "" {
			paths = append(paths, entry.Path)
		}
	}
	return MergePaths(nil, paths)
}

// ReadLocal preserves missing collection members, so a temporary disconnect
// cannot silently rewrite a collection. Folder errors leave the source intact.
func ReadLocal(ctx context.Context, saved Source) ([]Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !saved.IsLocal() {
		return nil, errors.New("library requires its source plugin")
	}
	config := saved.Local()
	if config.Folder != "" {
		return readLocalFolder(ctx, config.Folder)
	}
	items := make([]Item, 0, len(config.Paths))
	seen := map[string]int{}
	addVideo := func(item Item) {
		if index, found := seen[item.Path]; found {
			if item.Error == nil {
				items[index] = item
			}
			return
		}
		seen[item.Path] = len(items)
		items = append(items, item)
	}
	for _, path := range config.Paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if slices.Contains(config.Folders, path) {
			continue
		}
		item := Item{Entry: Entry{Path: path}}
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			err = errors.New("collection member is no longer a video file")
		}
		if err == nil {
			item.Entry = Entry{Name: filepath.Base(path), Path: path, Size: info.Size(), Modified: info.ModTime().UnixMilli()}
		}
		item.Error = err
		addVideo(item)
	}
	for _, folder := range config.Folders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		children, err := readLocalFolder(ctx, folder)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		items = append(items, Item{Entry: Entry{Name: filepath.Base(folder), Path: folder}, Folder: true, Error: err})
		for _, item := range children {
			addVideo(item)
		}
	}
	if len(config.Folders) > 0 {
		// Overlapping scans must not assign different playlist roots to siblings.
		root := localTreeRoot(saved, items)
		for i := range items {
			entry := &items[i].Entry
			entry.Group, entry.Root = filepath.Dir(entry.Path), root
			if !containsPath(root, entry.Group) {
				entry.Root = filepath.VolumeName(entry.Path) + string(filepath.Separator)
			}
			if entry.Group == entry.Root {
				entry.Group = ""
			}
		}
	}
	return items, nil
}

func readLocalFolder(ctx context.Context, path string) ([]Item, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("library source is no longer a folder")
	}
	entries, _, err := FromPaths(ctx, []string{path})
	if err != nil {
		return nil, err
	}
	items := make([]Item, len(entries))
	for i, entry := range entries {
		items[i] = Item{Entry: entry}
	}
	return items, nil
}
