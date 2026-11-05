// Package library owns playlist topology, ordering and subtitle matching.
package library

import (
	"fmt"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
)

type Entry struct {
	Name, Path, Group, Root, Subtitle, DeviceID string
	Size, Modified                              int64
}

// Local folders are derived from entry ancestry; DLNA records also represent
// unloaded folders. The UI derives its tree instead of maintaining another copy.
type Folder struct {
	ID, Parent, Name, DeviceID, ObjectID string
	Loaded                               bool
}
type Node struct {
	ID, Name string
	Index    int
	Folder   *Folder
	Children []Node
}

var videoExtensions = map[string]bool{".mp4": true, ".m4v": true, ".mov": true, ".webm": true, ".mkv": true, ".avi": true, ".ogv": true, ".mpeg": true, ".mpg": true}

func IsVideo(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return videoExtensions[extension] || strings.HasPrefix(mime.TypeByExtension(extension), "video/")
}
func IsSubtitle(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".srt", ".vtt", ".ass", ".ssa":
		return true
	}
	return false
}
func IsAppleDouble(path string) bool {
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if strings.HasPrefix(part, "._") {
			return true
		}
	}
	return false
}
func FromPaths(paths []string) ([]Entry, []string, error) {
	var entries []Entry
	var subtitles []string
	add := func(path, root string, info fs.FileInfo) {
		if IsAppleDouble(path) {
			return
		}
		if IsVideo(path) {
			group := ""
			if root != "" {
				group = filepath.Dir(path)
			}
			entries = append(entries, Entry{Name: info.Name(), Path: path, Group: group, Root: root, Size: info.Size(), Modified: info.ModTime().UnixMilli()})
		} else if IsSubtitle(path) {
			subtitles = append(subtitles, path)
		}
	}
	for _, path := range paths {
		path, err := filepath.Abs(path)
		if err != nil {
			return nil, nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, nil, err
		}
		if !info.IsDir() {
			add(path, "", info)
			continue
		}
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if IsAppleDouble(d.Name()) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			add(p, filepath.Dir(path), info)
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	byDir := map[string][]int{}
	for i, e := range entries {
		dir := filepath.Dir(e.Path)
		byDir[dir] = append(byDir[dir], i)
	}
	for dir, indices := range byDir {
		var videos, candidates []string
		for _, i := range indices {
			videos = append(videos, entries[i].Name)
		}
		for _, p := range subtitles {
			if filepath.Dir(p) == dir {
				candidates = append(candidates, p)
			}
		}
		for vi, si := range MatchSubtitles(videos, candidates) {
			entries[indices[vi]].Subtitle = candidates[si]
		}
	}
	return entries, subtitles, nil
}
func Identity(e Entry) string {
	return fmt.Sprintf("%s:%d:%d", e.Path, e.Size, e.Modified)
}
func Tree(entries []Entry, dlnaFolders []Folder) []Node {
	folders := map[string]Folder{}
	for _, f := range dlnaFolders {
		folders[f.ID] = f
	}
	for _, e := range entries {
		if e.DeviceID != "" || e.Group == "" {
			continue
		}
		for p := e.Group; p != e.Root && p != "."; p = filepath.Dir(p) {
			if _, ok := folders[p]; ok {
				break
			}
			parent := filepath.Dir(p)
			if parent == e.Root {
				parent = ""
			}
			folders[p] = Folder{ID: p, Parent: parent, Name: filepath.Base(p), Loaded: true}
			if parent == "" {
				break
			}
		}
	}
	children := map[string][]Node{}
	for _, f := range folders {
		copy := f
		children[f.Parent] = append(children[f.Parent], Node{ID: f.ID, Name: f.Name, Index: -1, Folder: &copy})
	}
	for i, e := range entries {
		children[e.Group] = append(children[e.Group], Node{ID: e.Path, Name: e.Name, Index: i})
	}
	comparator := collate.New(language.Und, collate.Numeric, collate.IgnoreCase, collate.IgnoreDiacritics)
	var build func(string) []Node
	build = func(parent string) []Node {
		nodes := children[parent]
		sort.SliceStable(nodes, func(i, j int) bool {
			if (nodes[i].Folder != nil) != (nodes[j].Folder != nil) {
				return nodes[i].Folder != nil
			}
			return comparator.CompareString(nodes[i].Name, nodes[j].Name) < 0
		})
		for i := range nodes {
			if nodes[i].Folder != nil {
				nodes[i].Children = build(nodes[i].ID)
			}
		}
		return nodes
	}
	return build("")
}
func PlaybackOrder(nodes []Node, selected int) []int {
	var collect func([]Node) []int
	collect = func(items []Node) []int {
		var out []int
		for _, n := range items {
			if n.Folder == nil {
				out = append(out, n.Index)
			} else {
				out = append(out, collect(n.Children)...)
			}
		}
		return out
	}
	var find func([]Node) ([]int, bool)
	find = func(items []Node) ([]int, bool) {
		for _, n := range items {
			if n.Folder == nil && n.Index == selected {
				return collect(items), true
			}
			if n.Folder != nil {
				if out, ok := find(n.Children); ok {
					return out, true
				}
			}
		}
		return nil, false
	}
	out, _ := find(nodes)
	return out
}

func FirstVideo(nodes []Node) int {
	for _, n := range nodes {
		if n.Folder == nil {
			return n.Index
		}
		if index := FirstVideo(n.Children); index >= 0 {
			return index
		}
	}
	return -1
}

var languageTokens = regexp.MustCompile(`\b(zh|zho|chi|chs|cht|cn|eng|en|english|subtitle|sub)\b`)

func normalized(name string) string {
	name = strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	name = languageTokens.ReplaceAllString(strings.ToLower(norm.NFKD.String(name)), " ")
	var out strings.Builder
	space := false
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			out.WriteRune(r)
			space = false
		} else if !space {
			out.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(out.String())
}
func bigrams(s string) map[[2]uint16]bool {
	u := utf16.Encode([]rune(strings.Join(strings.Fields(s), "")))
	out := map[[2]uint16]bool{}
	if len(u) == 1 {
		out[[2]uint16{u[0], 0}] = true
	}
	for i := 0; i < len(u)-1; i++ {
		out[[2]uint16{u[i], u[i+1]}] = true
	}
	return out
}
func SubtitleScore(video, subtitle string) float64 {
	l, r := normalized(video), normalized(subtitle)
	if l == "" || r == "" {
		return 0
	}
	if l == r {
		return 1
	}
	if strings.Contains(l, r) || strings.Contains(r, l) {
		a, b := len(utf16.Encode([]rune(l))), len(utf16.Encode([]rune(r)))
		return .82 + .18*float64(min(a, b))/float64(max(a, b))
	}
	left, right := bigrams(l), bigrams(r)
	overlap := 0
	for b := range left {
		if right[b] {
			overlap++
		}
	}
	return float64(2*overlap) / float64(max(1, len(left)+len(right)))
}

// MatchSubtitles returns a one-to-one greedy assignment, including weak matches.
func MatchSubtitles(videos, subtitles []string) map[int]int {
	type candidate struct {
		v, s  int
		score float64
	}
	var candidates []candidate
	for v, name := range videos {
		for s, sub := range subtitles {
			candidates = append(candidates, candidate{v, s, SubtitleScore(name, sub)})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	out := map[int]int{}
	used := map[int]bool{}
	for _, c := range candidates {
		if _, ok := out[c.v]; ok || used[c.s] {
			continue
		}
		out[c.v] = c.s
		used[c.s] = true
	}
	return out
}
func MatchSubtitle(video Entry, paths []string) string {
	if video.Subtitle != "" {
		return video.Subtitle
	}
	best, score := "", -1.0
	for _, p := range paths {
		if filepath.Dir(p) == filepath.Dir(video.Path) {
			if candidate := SubtitleScore(video.Name, p); candidate > score {
				best, score = p, candidate
			}
		}
	}
	return best
}
func SubtitleSiblings(path string) ([]string, error) {
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, f := range files {
		p := filepath.Join(filepath.Dir(path), f.Name())
		if !f.IsDir() && IsSubtitle(p) && !IsAppleDouble(p) {
			paths = append(paths, p)
		}
	}
	return paths, nil
}
