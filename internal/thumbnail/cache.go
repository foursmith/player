// Package thumbnail stores disposable previews under opaque media identities.
package thumbnail

import (
	"context"
	"encoding/json"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/foursmith/player/internal/library"
	"github.com/foursmith/player/internal/media"
)

// Cache has one owner, the thumbnail worker. A file replacement gets a new key.
type Cache struct {
	Dir    string
	writes int
	render func(context.Context, string) (media.Preview, error)
}

type previewMetadata struct {
	Duration float64
}

func (c *Cache) Load(ctx context.Context, entry library.Entry) (media.Preview, error) {
	if err := ctx.Err(); err != nil {
		return media.Preview{}, err
	}
	path := filepath.Join(c.Dir, library.Identity(entry)+".jpg")
	metadataPath := strings.TrimSuffix(path, ".jpg") + ".json"
	if c.Dir != "" {
		if file, err := os.Open(path); err == nil {
			img, err := jpeg.Decode(file)
			file.Close()
			if err == nil {
				var metadata previewMetadata
				data, err := os.ReadFile(metadataPath)
				if err == nil && json.Unmarshal(data, &metadata) == nil {
					now := time.Now()
					os.Chtimes(path, now, now)
					return media.Preview{Image: img, Duration: validDuration(metadata.Duration)}, nil
				}
			}
			os.Remove(path)
			os.Remove(metadataPath)
		}
	}
	render := c.render
	if render == nil {
		render = media.Thumbnail
	}
	preview, err := render(ctx, entry.Path)
	preview.Duration = validDuration(preview.Duration)
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

func validDuration(duration float64) float64 {
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return 0
	}
	return duration
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
	metadata, err := json.Marshal(previewMetadata{Duration: preview.Duration})
	if err != nil || os.WriteFile(strings.TrimSuffix(path, ".jpg")+".json", metadata, 0600) != nil {
		return
	}
	c.writes++
	if c.writes == 1 || c.writes%32 == 0 {
		c.prune(512)
	}
}

func (c *Cache) prune(limit int) {
	files, err := os.ReadDir(c.Dir)
	if err != nil {
		return
	}
	type cached struct {
		name string
		at   time.Time
	}
	var previews []cached
	for _, file := range files {
		name := strings.TrimSuffix(file.Name(), ".jpg")
		if file.IsDir() || !library.ValidIdentity(name) || !strings.HasSuffix(file.Name(), ".jpg") {
			continue
		}
		if info, err := file.Info(); err == nil {
			previews = append(previews, cached{file.Name(), info.ModTime()})
		}
	}
	sort.Slice(previews, func(i, j int) bool { return previews[i].at.Before(previews[j].at) })
	for _, preview := range previews[:max(0, len(previews)-limit)] {
		os.Remove(filepath.Join(c.Dir, preview.name))
		os.Remove(filepath.Join(c.Dir, strings.TrimSuffix(preview.name, ".jpg")+".json"))
	}
}
