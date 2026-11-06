package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"

	"github.com/foursmith/player/internal/dlna"
	"github.com/foursmith/player/library"
)

// browseDLNA loads one directory, keeping stable IDs and session-only URLs.
func browseDLNA(ctx context.Context, device dlna.Device, objectID string) ([]library.Item, error) {
	objects, err := device.Browse(ctx, objectID)
	if err != nil {
		return nil, err
	}
	var items []library.Item
	var subtitles []dlna.Item
	var videos, names []string
	var unmatched []int
	for _, object := range objects {
		if object.SubtitleFile {
			subtitles = append(subtitles, object)
			continue
		}
		entry := library.Entry{Name: object.Name, Path: object.URL, SourceID: device.ID, ItemID: object.ID, ParentID: objectID, Subtitle: object.Subtitle, Size: object.Size, Modified: object.Modified}
		item := library.Item{Entry: entry, Folder: object.Folder, Duration: object.Duration}
		if !object.Folder && object.Subtitle == "" {
			unmatched = append(unmatched, len(items))
			videos = append(videos, object.Name)
		}
		items = append(items, item)
	}
	for _, subtitle := range subtitles {
		names = append(names, subtitle.Name)
	}
	for vi, si := range library.MatchSubtitles(videos, names) {
		items[unmatched[vi]].Entry.Subtitle = subtitles[si].URL
	}
	return items, nil
}

const DLNAID = "dlna"

type DLNAConfig struct{ DeviceID, Location string }

func DLNASource(id, name, deviceID, location string) library.Source {
	return library.Source{ID: id, Name: name, Plugin: DLNAID, Config: library.EncodeConfig(DLNAConfig{deviceID, location})}
}
func DLNAConnection(source library.Source) DLNAConfig {
	var config DLNAConfig
	_ = json.Unmarshal(source.Config, &config)
	return config
}

type DLNA struct{}

func (DLNA) Manifest() library.Manifest {
	return library.Manifest{ID: DLNAID, Name: "DLNA", Icon: "source-dlna", RootID: "0"}
}
func (DLNA) Normalize(source library.Source) (library.Source, error) {
	var config DLNAConfig
	if err := json.Unmarshal(source.Config, &config); err != nil {
		return source, err
	}
	location, err := url.Parse(config.Location)
	if err != nil || config.DeviceID == "" || location.Host == "" || location.User != nil || (location.Scheme != "http" && location.Scheme != "https") {
		return source, errors.New("invalid DLNA connection")
	}
	source.Config = library.EncodeConfig(config)
	source.ShareDLNA = false
	return source, nil
}
func (DLNA) SourceKey(source library.Source) string { return DLNAConnection(source).DeviceID }
func (DLNA) Open(ctx context.Context, source library.Source, host library.Host) (library.Session, error) {
	config := DLNAConnection(source)
	device, err := dlna.Connect(ctx, config.DeviceID, config.Location)
	if err != nil {
		return nil, err
	}
	source.Config = library.EncodeConfig(DLNAConfig{device.ID, device.Location})
	return &dlnaSession{source: source, device: device, folders: map[string][]library.Item{}}, nil
}
func (DLNA) Discover(ctx context.Context) ([]library.Source, error) {
	devices, err := dlna.Discover(ctx)
	var sources []library.Source
	for _, device := range devices {
		sources = append(sources, DLNASource(device.ID, device.Name, device.ID, device.Location))
	}
	return sources, err
}

type dlnaSession struct {
	source  library.Source
	device  dlna.Device
	folders map[string][]library.Item
}

func (s *dlnaSession) Source() library.Source { return s.source }
func (s *dlnaSession) Close() error           { return nil }
func (s *dlnaSession) Browse(ctx context.Context, request library.BrowseRequest) (library.Page, error) {
	if request.Cursor != "" {
		return library.Page{}, library.ErrUnsupported
	}
	parent := request.ParentID
	if parent == "" {
		parent = "0"
	}
	if items, ok := s.folders[parent]; ok {
		return library.Page{Items: items}, ctx.Err()
	}
	items, err := browseDLNA(ctx, s.device, parent)
	for i := range items {
		items[i].SourceID = s.source.ID
	}
	if err == nil {
		s.folders[parent] = items
	}
	return library.Page{Items: items}, err
}
func (s *dlnaSession) Resolve(ctx context.Context, ref library.Reference) (library.Playback, error) {
	if ref.SourceID != s.source.ID || ref.ItemID == "" {
		return library.Playback{}, os.ErrNotExist
	}
	items, err := s.Browse(ctx, library.BrowseRequest{ParentID: ref.ParentID})
	if err != nil {
		return library.Playback{}, err
	}
	for _, item := range items.Items {
		if !item.Folder && item.ItemID == ref.ItemID {
			return library.Playback{Resource: library.Resource{URL: item.Path}, Subtitle: library.Resource{URL: item.Subtitle}, Seekable: true}, nil
		}
	}
	return library.Playback{}, os.ErrNotExist
}
