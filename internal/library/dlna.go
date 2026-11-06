package library

import (
	"context"

	"github.com/foursmith/player/internal/dlna"
)

// BrowseDLNA loads one directory, keeping stable IDs and session-only URLs.
func BrowseDLNA(ctx context.Context, device dlna.Device, objectID string) ([]SavedItem, error) {
	objects, err := device.Browse(ctx, objectID)
	if err != nil {
		return nil, err
	}
	var items []SavedItem
	var subtitles []dlna.Item
	var videos, names []string
	var unmatched []int
	for _, object := range objects {
		if object.SubtitleFile {
			subtitles = append(subtitles, object)
			continue
		}
		entry := Entry{Name: object.Name, Path: object.URL, DeviceID: device.ID, ObjectID: object.ID, ParentID: objectID, Subtitle: object.Subtitle, Size: object.Size, Modified: object.Modified}
		item := SavedItem{Entry: entry, Folder: object.Folder, Duration: object.Duration}
		if !object.Folder && object.Subtitle == "" {
			unmatched = append(unmatched, len(items))
			videos = append(videos, object.Name)
		}
		items = append(items, item)
	}
	for _, subtitle := range subtitles {
		names = append(names, subtitle.Name)
	}
	for vi, si := range MatchSubtitles(videos, names) {
		items[unmatched[vi]].Entry.Subtitle = subtitles[si].URL
	}
	return items, nil
}
