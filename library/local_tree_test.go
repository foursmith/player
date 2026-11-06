package library

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLocalTreeSharesFolderAndCollectionHierarchy(t *testing.T) {
	root := t.TempDir()
	items := []Item{
		{Entry: Entry{Path: filepath.Join(root, "Season 10", "Episode 10.mp4")}},
		{Entry: Entry{Path: filepath.Join(root, "Season 2", "Bonus", "clip.mp4")}},
		{Entry: Entry{Path: filepath.Join(root, "Season 2", "Episode 2.mp4")}},
		{Entry: Entry{Path: filepath.Join(root, "root.mp4")}},
		{Entry: Entry{Path: filepath.Join(root, "Season 2", "missing.mp4")}, Error: os.ErrNotExist},
	}
	for _, saved := range []Source{{Plugin: LocalID, Config: EncodeConfig(LocalConfig{Folder: root})}, {Plugin: LocalID, Config: EncodeConfig(LocalConfig{Paths: LocalPaths([]Entry{items[0].Entry, items[1].Entry, items[2].Entry, items[3].Entry, items[4].Entry})})}} {
		tree := LocalTree(saved, items)
		if len(tree) != 3 || tree[0].Name != "Season 2" || tree[1].Name != "Season 10" || tree[2].Index != 3 {
			t.Fatalf("source root leaked into the tree or natural ordering was lost: %+v", tree)
		}
		season := tree[0]
		if season.ID != filepath.Join(root, "Season 2") || len(season.Children) != 3 || !season.Children[0].Folder || season.Children[0].Children[0].Index != 1 {
			t.Fatalf("nested folder identity or topology was lost: %+v", season)
		}
		if got := PlaybackOrder(tree, 3); !reflect.DeepEqual(got, []int{1, 2, 4, 0, 3}) {
			t.Fatal("hierarchy discarded or duplicated a collection member", got)
		}
		if items[0].Group != "" || items[0].Root != "" {
			t.Fatal("tree construction rewrote source metadata")
		}
	}
}

func TestLocalTreeCollectionKeepsDistinctPathsAndFlatSiblings(t *testing.T) {
	root := t.TempDir()
	items := []Item{
		{Entry: Entry{Path: filepath.Join(root, "A", "Movies", "clip.mp4")}},
		{Entry: Entry{Path: filepath.Join(root, "B", "Movies", "clip.mp4")}},
	}
	tree := LocalTree(Source{Plugin: LocalID, Config: EncodeConfig(LocalConfig{})}, items)
	if len(tree) != 2 || tree[0].ID == tree[1].ID || tree[0].Children[0].ID == tree[1].Children[0].ID {
		t.Fatal("equal folder or video names merged different paths", tree)
	}
	flat := LocalTree(Source{Plugin: LocalID, Config: EncodeConfig(LocalConfig{})}, items[:1])
	if len(flat) != 1 || flat[0].Folder || flat[0].Index != 0 {
		t.Fatal("a single-directory collection gained redundant ancestors", flat)
	}
	if tree := LocalTree(Source{Plugin: LocalID, Config: EncodeConfig(LocalConfig{})}, nil); len(tree) != 0 {
		t.Fatal("empty collection produced phantom folders", tree)
	}
}

func TestLocalTreeRetainsExplicitEmptyOfflineAndVolumeRootFolders(t *testing.T) {
	root := t.TempDir()
	items := []Item{
		{Entry: Entry{Path: filepath.Join(root, "Empty")}, Folder: true},
		{Entry: Entry{Path: filepath.Join(root, "Offline")}, Folder: true, Error: os.ErrNotExist},
	}
	tree := LocalTree(Source{Plugin: LocalID, Config: EncodeConfig(LocalConfig{})}, items)
	if len(tree) != 2 || !tree[0].Folder || tree[0].Index != 0 || tree[1].Index != 1 {
		t.Fatal("empty or offline collection folders disappeared", tree)
	}
	volume := filepath.VolumeName(root) + string(filepath.Separator)
	items = []Item{
		{Entry: Entry{Path: volume}, Folder: true},
		{Entry: Entry{Path: filepath.Join(volume, "clip.mp4")}},
	}
	tree = LocalTree(Source{Plugin: LocalID, Config: EncodeConfig(LocalConfig{})}, items)
	if len(tree) != 1 || !tree[0].Folder || tree[0].ID != volume || len(tree[0].Children) != 1 || tree[0].Children[0].Index != 1 {
		t.Fatal("adding a volume root lost its folder or duplicated its children", tree)
	}
}
