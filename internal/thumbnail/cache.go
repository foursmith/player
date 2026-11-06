// Package thumbnail stores disposable previews under opaque media identities.
package thumbnail

import (
	"context"
	"encoding/json"
	"image/jpeg"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/library"
)

// Cache shares atomic metadata sidecars with DLNA indexing. Replacements get new keys.
type Cache struct {
	Dir    string
	writes atomic.Uint64
	render func(context.Context, media.Source) (media.Preview, error)
	probe  func(context.Context, media.Source) (library.MediaInfo, error)
}

func (c *Cache) readMetadata(entry library.Entry) (library.MediaInfo, bool) {
	if c.Dir == "" {
		return library.MediaInfo{}, false
	}
	var info library.MediaInfo
	data, err := os.ReadFile(filepath.Join(c.Dir, library.Identity(entry)+".json"))
	if err != nil || json.Unmarshal(data, &info) != nil {
		return library.MediaInfo{}, false
	}
	info = info.Normalized()
	return info, info.Width > 0 && info.Height > 0
}

// Metadata does no frame decoding and can run on the sharing workers while
// the library's thumbnail worker uses the same cache directory.
func (c *Cache) Metadata(ctx context.Context, entry library.Entry) (library.MediaInfo, error) {
	if err := ctx.Err(); err != nil {
		return library.MediaInfo{}, err
	}
	if info, ok := c.readMetadata(entry); ok {
		return info, nil
	}
	probe := c.probe
	if probe == nil {
		probe = media.Probe
	}
	info, err := probe(ctx, media.FromEntry(entry))
	if ctx.Err() != nil {
		return library.MediaInfo{}, ctx.Err()
	}
	info = info.Normalized()
	if err == nil && c.Dir != "" {
		c.storeMetadata(filepath.Join(c.Dir, library.Identity(entry)+".json"), info)
	}
	return info, err
}

func (c *Cache) Load(ctx context.Context, entry library.Entry) (media.Preview, error) {
	if err := ctx.Err(); err != nil {
		return media.Preview{}, err
	}
	path := filepath.Join(c.Dir, library.Identity(entry)+".jpg")
	if c.Dir != "" {
		if file, err := os.Open(path); err == nil {
			img, err := jpeg.Decode(file)
			file.Close()
			if err == nil {
				if info, ok := c.readMetadata(entry); ok {
					now := time.Now()
					os.Chtimes(path, now, now)
					return media.Preview{Image: img, MediaInfo: info}, nil
				}
			}
			os.Remove(path)
		}
	}
	render := c.render
	if render == nil {
		render = media.Thumbnail
	}
	preview, err := render(ctx, media.FromEntry(entry))
	preview.MediaInfo = preview.MediaInfo.Normalized()
	if err != nil {
		return preview, err
	}
	if err := ctx.Err(); err != nil {
		return media.Preview{}, err
	}
	if c.Dir != "" {
		c.store(path, preview)
	}
	return preview, nil
}

func (c *Cache) store(path string, preview media.Preview) {
	if os.MkdirAll(c.Dir, 0700) != nil {
		return
	}
	file, err := os.CreateTemp(c.Dir, ".thumbnail-*")
	if err != nil {
		return
	}
	defer os.Remove(file.Name())
	err = jpeg.Encode(file, preview.Image, &jpeg.Options{Quality: 80})
	closeErr := file.Close()
	if err != nil || closeErr != nil || os.Rename(file.Name(), path) != nil {
		return
	}
	c.storeMetadata(strings.TrimSuffix(path, ".jpg")+".json", preview.MediaInfo)
}

func (c *Cache) storeMetadata(path string, info library.MediaInfo) {
	if info.Width <= 0 || info.Height <= 0 {
		return
	}
	metadata, err := json.Marshal(info)
	if err != nil || os.MkdirAll(c.Dir, 0700) != nil {
		return
	}
	file, err := os.CreateTemp(c.Dir, ".metadata-*")
	if err != nil {
		return
	}
	defer os.Remove(file.Name())
	_, err = file.Write(metadata)
	closeErr := file.Close()
	if err != nil || closeErr != nil || os.Rename(file.Name(), path) != nil {
		return
	}
	if writes := c.writes.Add(1); writes == 1 || writes%32 == 0 {
		c.prune(512)
	}
}

func (c *Cache) prune(limit int) {
	files, err := os.ReadDir(c.Dir)
	if err != nil {
		return
	}
	type cached struct {
		name  string
		at    time.Time
		image bool
	}
	entries := map[string]cached{}
	for _, file := range files {
		ext := filepath.Ext(file.Name())
		name := strings.TrimSuffix(file.Name(), ext)
		if file.IsDir() || !library.ValidIdentity(name) || (ext != ".jpg" && ext != ".json") {
			continue
		}
		if info, err := file.Info(); err == nil {
			if previous := entries[name]; !previous.image {
				entries[name] = cached{name: name, at: info.ModTime(), image: ext == ".jpg"}
			}
		}
	}
	var previews []cached
	for _, entry := range entries {
		previews = append(previews, entry)
	}
	sort.Slice(previews, func(i, j int) bool { return previews[i].at.Before(previews[j].at) })
	for _, preview := range previews[:max(0, len(previews)-limit)] {
		os.Remove(filepath.Join(c.Dir, preview.name+".jpg"))
		os.Remove(filepath.Join(c.Dir, preview.name+".json"))
	}
}
