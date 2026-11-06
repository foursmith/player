package library

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Saved is a folder, a DLNA device, or an explicit collection of local paths.
// Only locations are stored; opening a library reads current file information.
type Saved struct {
	ID, Name  string
	Folder    string   `json:",omitempty"`
	Paths     []string `json:",omitempty"`
	Folders   []string `json:",omitempty"`
	DeviceID  string   `json:",omitempty"`
	Location  string   `json:",omitempty"`
	ShareDLNA bool     `json:",omitempty"`
}

type SavedItem struct {
	Entry
	Error    error
	Folder   bool
	Duration float64
}

func (s Saved) Collection() bool { return s.Folder == "" && s.DeviceID == "" }

func (s SavedItem) Name() string {
	if s.Entry.Name != "" {
		return s.Entry.Name
	}
	return filepath.Base(s.Path)
}

func PlayableEntries(items []SavedItem) []Entry {
	var entries []Entry
	for _, item := range items {
		if !item.Folder && item.Error == nil {
			entries = append(entries, item.Entry)
		}
	}
	return entries
}

func CloneSaved(saved []Saved) []Saved {
	out := slices.Clone(saved)
	for i := range out {
		out[i].Paths = slices.Clone(out[i].Paths)
		out[i].Folders = slices.Clone(out[i].Folders)
	}
	return out
}

// NormalizeSaved does no filesystem IO: disconnected disks remain in the library.
func NormalizeSaved(saved []Saved) []Saved {
	var out []Saved
	ids, folders := map[string]bool{}, map[string]bool{}
	devices := map[string]bool{}
	for _, item := range saved {
		item.Name = strings.TrimSpace(item.Name)
		if item.ID == "" || item.Name == "" || ids[item.ID] {
			continue
		}
		if item.DeviceID == "" {
			item.Location = ""
		}
		if item.DeviceID != "" {
			location, err := url.Parse(item.Location)
			if err != nil || location.Host == "" || location.User != nil || (location.Scheme != "http" && location.Scheme != "https") || devices[item.DeviceID] {
				continue
			}
			devices[item.DeviceID] = true
			item.Folder, item.Paths = "", nil
			item.Folders = nil
			item.ShareDLNA = false
		} else if item.Folder != "" {
			if !filepath.IsAbs(item.Folder) {
				continue
			}
			item.Folder = filepath.Clean(item.Folder)
			if folders[item.Folder] {
				continue
			}
			folders[item.Folder] = true
			item.Paths = nil
			item.Folders = nil
		} else {
			item.Paths = MergePaths(nil, item.Paths)
			item.Folders = MergeFolders(nil, item.Folders)
		}
		ids[item.ID] = true
		out = append(out, item)
	}
	return out
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
		if entry.DeviceID == "" {
			paths = append(paths, entry.Path)
		}
	}
	return MergePaths(nil, paths)
}

// ReadSaved preserves missing collection members, so a temporary disconnect
// cannot silently rewrite a collection. Folder errors leave the source intact.
func ReadSaved(ctx context.Context, saved Saved) ([]SavedItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if saved.DeviceID != "" {
		return nil, errors.New("DLNA libraries require network browsing")
	}
	if saved.Folder != "" {
		return readSavedFolder(ctx, saved.Folder)
	}
	items := make([]SavedItem, 0, len(saved.Paths))
	seen := map[string]int{}
	addVideo := func(item SavedItem) {
		if index, found := seen[item.Path]; found {
			if item.Error == nil {
				items[index] = item
			}
			return
		}
		seen[item.Path] = len(items)
		items = append(items, item)
	}
	for _, path := range saved.Paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if slices.Contains(saved.Folders, path) {
			continue
		}
		item := SavedItem{Entry: Entry{Path: path}}
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
	for _, folder := range saved.Folders {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		children, err := readSavedFolder(ctx, folder)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		items = append(items, SavedItem{Entry: Entry{Name: filepath.Base(folder), Path: folder}, Folder: true, Error: err})
		for _, item := range children {
			addVideo(item)
		}
	}
	if len(saved.Folders) > 0 {
		// Overlapping scans must not assign different playlist roots to siblings.
		root := savedTreeRoot(saved, items)
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

func readSavedFolder(ctx context.Context, path string) ([]SavedItem, error) {
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
	items := make([]SavedItem, len(entries))
	for i, entry := range entries {
		items[i] = SavedItem{Entry: entry}
	}
	return items, nil
}
