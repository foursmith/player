package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestVideoImportRetainsMIMERecognition(t *testing.T) {
	if err := mime.AddExtensionType(".fsvr-video", "video/mp4"); err != nil {
		t.Fatal(err)
	}
	if !IsVideo("clip.fsvr-video") || !IsVideo("clip.MKV") || IsVideo("notes.txt") {
		t.Fatal("video MIME/extension classification changed")
	}
}

func TestImportTopologyMatchingAndNaturalOrder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Show/Episode 2.mp4", "Show/Episode 10.mp4", "Show/Episode.2.zh-CN.srt", "Show/Episode 10 English.vtt", "Show/Specials/extra.mov", "Show/._bad.mp4"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entries, _, err := FromPaths(context.Background(), []string{filepath.Join(dir, "Show")})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries: %+v", entries)
	}
	tree := Tree(entries)
	if first := FirstVideo(tree); first < 0 || entries[first].Name != "extra.mov" {
		t.Fatal("first video must follow folder-first tree order", first)
	}
	children := tree[0].Children
	if children[0].Name != "Specials" || children[1].Name != "Episode 2.mp4" || children[2].Name != "Episode 10.mp4" {
		t.Fatalf("order: %+v", children)
	}
	if got := filepath.Base(entries[children[1].Index].Subtitle); got != "Episode.2.zh-CN.srt" {
		t.Fatal(got)
	}
	if order := PlaybackOrder(tree, children[1].Index); len(order) != 3 {
		t.Fatal(order)
	}
	if order := PlaybackOrder(tree, children[0].Children[0].Index); len(order) != 1 {
		t.Fatal(order)
	}
}
func TestSubtitleAssignment(t *testing.T) {
	got := MatchSubtitles([]string{"Episode 01.mp4", "Episode 02.mp4"}, []string{"Episode.02.zh-CN.srt", "Episode 01 English.vtt"})
	if !reflect.DeepEqual(got, map[int]int{0: 1, 1: 0}) {
		t.Fatal(got)
	}
	for _, names := range [][2]string{{"Movie Director Cut.mkv", "Movie.Director.Cut.zh.srt"}, {"Café.mp4", "Cafe.srt"}} {
		if score := SubtitleScore(names[0], names[1]); score < .9 {
			t.Fatal(names, score)
		}
	}
}
func TestRootPlaybackIncludesRemoteVideosWithStableRowKeys(t *testing.T) {
	entries := []Entry{{Name: "root.mp4", Path: "r"}, {Name: "inside.mp4", Path: "v", SourceID: "dlna-nas", ItemID: "video"}}
	tree := Tree(entries)
	if len(tree) != 2 || tree[0].Folder || tree[0].ID != Identity(entries[1]) {
		t.Fatal("remote videos must be queue rows with stable identities", tree)
	}
	if got := PlaybackOrder(tree, 0); !reflect.DeepEqual(got, []int{1, 0}) {
		t.Fatal(got)
	}
}

func TestPlaylistTreeKeepsVideosAtVolumeRoot(t *testing.T) {
	volume := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	entries := []Entry{
		{Name: "root.mp4", Path: filepath.Join(volume, "root.mp4"), Group: volume, Root: volume},
		{Name: "nested.mp4", Path: filepath.Join(volume, "Shows", "nested.mp4"), Group: filepath.Join(volume, "Shows"), Root: volume},
	}
	tree := Tree(entries)
	if len(tree) != 2 || !tree[0].Folder || tree[0].Children[0].Index != 1 || tree[1].Index != 0 {
		t.Fatal("volume-root playback lost a direct or nested video", tree)
	}
}

func TestHistoryIdentityDistinguishesPathsAndReplacedFiles(t *testing.T) {
	first := Entry{Name: "video.mp4", Path: filepath.Join(t.TempDir(), "video.mp4"), Size: 100, Modified: 123}
	second := first
	second.Path = filepath.Join(t.TempDir(), "video.mp4")
	if Identity(first) == Identity(second) {
		t.Fatal("different local files shared playback history")
	}
	second = first
	second.Modified++
	if Identity(first) == Identity(second) {
		t.Fatal("a replaced file inherited stale playback history")
	}
}

func TestSubtitleDiscoveryAndSingleMatch(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Movie.mp4", "Movie.en.srt", "Movie.zh.srt", "other.vtt", "._Movie.srt", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "folder.srt"), 0700); err != nil {
		t.Fatal(err)
	}
	entry := Entry{Name: "Movie.mp4", Path: filepath.Join(dir, "Movie.mp4")}
	paths, err := SubtitleSiblings(entry.Path)
	if err != nil || len(paths) != 3 {
		t.Fatalf("subtitle-only discovery: %v, %v", paths, err)
	}
	matched := MatchSubtitles([]string{entry.Name}, paths)[0]
	if got := MatchSubtitle(entry, paths); got != paths[matched] {
		t.Fatalf("single match changed greedy tie order: %q", got)
	}
}

func TestHistoryIdentityUsesSHA256OfPathSizeAndModificationTime(t *testing.T) {
	entry := Entry{Name: "private.mp4", Path: "/private/media/private.mp4", Size: 123, Modified: 456}
	sum := sha256.Sum256([]byte("/private/media/private.mp4:123:456"))
	got := Identity(entry)
	if got != hex.EncodeToString(sum[:]) || len(got) != 64 || strings.Contains(got, entry.Name) {
		t.Fatalf("unexpected cache identity: %s", got)
	}
}
