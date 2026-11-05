// Package dlnaserver publishes saved local libraries as a UPnP media server.
package dlnaserver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/foursmith/player/internal/library"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type object struct {
	ID, Parent, Name, Source string
	File, Subtitle           string
	MetadataPath             string
	Info                     library.MediaInfo
	MIME                     string
	Size, Modified           int64
	Container                bool
	Children                 []string
}

type catalog map[string]object

func objectID(parts ...string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
}

// SharedSources takes an owned snapshot of explicitly shared local libraries.
func SharedSources(sources []library.Saved) []library.Saved {
	var local []library.Saved
	for _, source := range sources {
		if source.ShareDLNA && source.DeviceID == "" {
			local = append(local, source)
		}
	}
	return library.CloneSaved(local)
}

func emptyCatalog(name string, sources []library.Saved) catalog {
	out := catalog{"0": {ID: "0", Parent: "-1", Name: name, Container: true}}
	for _, source := range sources {
		out.add(object{ID: objectID(source.ID), Parent: "0", Name: source.Name, Source: source.ID, Container: true})
	}
	return out
}

func (c catalog) add(item object) {
	if _, exists := c[item.ID]; exists {
		return
	}
	c[item.ID] = item
	parent := c[item.Parent]
	parent.Children = append(parent.Children, item.ID)
	c[item.Parent] = parent
}

func readCatalog(ctx context.Context, name string, sources []library.Saved) (catalog, error) {
	out := emptyCatalog(name, sources)
	subtitles := map[string][]string{}
	for _, source := range sources {
		items, err := library.ReadSaved(ctx, source)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			log.Printf("[dlna-server] library %q unavailable: %v", source.Name, err)
			continue
		}
		root := ""
		if source.Folder != "" {
			root, err = filepath.EvalSymlinks(source.Folder)
			if err != nil {
				continue
			}
		}
		folderRoots := map[string]string{}
		explicitFiles := map[string]bool{}
		for _, path := range source.Paths {
			explicitFiles[path] = true
		}
		for _, folder := range source.Folders {
			if resolved, err := filepath.EvalSymlinks(folder); err == nil {
				folderRoots[folder] = resolved
			}
		}
		// Use the same hierarchy as the local browser. Create containers only
		// when they lead to a publishable file, so offline-only branches stay out.
		folders := map[int][]library.Node{}
		var visit func([]library.Node, []library.Node)
		visit = func(nodes, parents []library.Node) {
			for _, node := range nodes {
				if node.Folder {
					visit(node.Children, append(slices.Clone(parents), node))
				} else {
					folders[node.Index] = parents
				}
			}
		}
		visit(library.SavedTree(source, items), nil)
		for index, item := range items {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if item.Error != nil || item.Folder || !library.IsVideo(item.Path) {
				continue
			}
			itemRoot := root
			if source.Collection() && !explicitFiles[item.Path] {
				matched := 0
				for folder, resolved := range folderRoots {
					if len(folder) > matched && within(folder, item.Path) {
						itemRoot = resolved
						matched = len(folder)
					}
				}
				if itemRoot == "" {
					continue
				}
			}
			file, err := filepath.EvalSymlinks(item.Path)
			if err != nil || itemRoot != "" && !within(itemRoot, file) {
				continue
			}
			info, err := os.Stat(file)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			parent := objectID(source.ID)
			for _, folder := range folders[index] {
				id := objectID(source.ID, "folder", folder.ID)
				out.add(object{ID: id, Parent: parent, Name: folder.Name, Source: source.ID, Container: true})
				parent = id
			}
			subtitle := item.Subtitle
			if subtitle == "" {
				dir := filepath.Dir(item.Path)
				paths, found := subtitles[dir]
				if !found {
					paths, _ = library.SubtitleSiblings(item.Path)
					subtitles[dir] = paths
				}
				subtitle = library.MatchSubtitle(item.Entry, paths)
			}
			if subtitle != "" {
				resolved, err := filepath.EvalSymlinks(subtitle)
				if err != nil || itemRoot != "" && !within(itemRoot, resolved) {
					subtitle = ""
				} else if info, err := os.Stat(resolved); err != nil || !info.Mode().IsRegular() {
					subtitle = ""
				} else {
					subtitle = resolved
				}
			}
			out.add(object{ID: objectID(source.ID, "file", item.Path), Parent: parent, Name: item.Name(), Source: source.ID, File: file, MetadataPath: item.Path, Subtitle: subtitle, MIME: mediaType(item.Path), Size: info.Size(), Modified: info.ModTime().UnixMilli()})
		}
	}
	comparator := collate.New(language.Und, collate.Numeric, collate.IgnoreCase)
	for id, item := range out {
		slices.SortStableFunc(item.Children, func(a, b string) int {
			left, right := out[a], out[b]
			if left.Container != right.Container {
				if left.Container {
					return -1
				}
				return 1
			}
			return comparator.CompareString(left.Name, right.Name)
		})
		out[id] = item
	}
	return out, nil
}

func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func mediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv":
		return "video/x-matroska"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".avi":
		return "video/x-msvideo"
	case ".srt":
		return "application/x-subrip"
	case ".ass", ".ssa":
		return "text/plain"
	}
	if value := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); value != "" {
		return strings.SplitN(value, ";", 2)[0]
	}
	return "application/octet-stream"
}
