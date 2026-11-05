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

func CloneSaved(saved []Saved) []Saved {
	out := slices.Clone(saved)
	for i := range out {
		out[i].Paths = slices.Clone(out[i].Paths)
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
		} else {
			item.Paths = MergePaths(nil, item.Paths)
		}
		ids[item.ID] = true
		out = append(out, item)
	}
	return out
}

func MergePaths(current, incoming []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, paths := range [][]string{current, incoming} {
		for _, path := range paths {
			if !filepath.IsAbs(path) || !IsVideo(path) || IsAppleDouble(path) {
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
		info, err := os.Stat(saved.Folder)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.New("library source is no longer a folder")
		}
		entries, _, err := FromPaths(ctx, []string{saved.Folder})
		if err != nil {
			return nil, err
		}
		var items []SavedItem
		var visit func([]Node)
		visit = func(nodes []Node) {
			for _, node := range nodes {
				if node.Folder {
					visit(node.Children)
				} else {
					entry := entries[node.Index]
					items = append(items, SavedItem{Entry: entry})
				}
			}
		}
		visit(Tree(entries))
		return items, nil
	}
	items := make([]SavedItem, 0, len(saved.Paths))
	for _, path := range saved.Paths {
		if err := ctx.Err(); err != nil {
			return nil, err
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
		items = append(items, item)
	}
	return items, nil
}
