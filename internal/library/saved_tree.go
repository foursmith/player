package library

import "path/filepath"

// SavedTree is the media hierarchy shared by the library browser and DLNA
// publisher. Indexes refer to items; missing collection members stay in the tree.
func SavedTree(saved Saved, items []SavedItem) []Node {
	root := savedTreeRoot(saved, items)
	rootFolders := map[string]bool{}
	for _, item := range items {
		if item.Folder {
			rootFolders[item.Path] = true
		}
	}
	type folder struct {
		parent string
		node   Node
	}
	folders := map[string]folder{}
	children := map[string][]Node{}
	for i, item := range items {
		group, boundary := filepath.Dir(item.Path), root
		if item.Folder {
			group = item.Path
		}
		// A collection may span Windows volumes. Keep each volume's directory
		// chain bounded even though it has no common filesystem ancestor.
		if boundary != "" && !containsPath(boundary, group) {
			boundary = filepath.VolumeName(item.Path) + string(filepath.Separator)
		}
		if rootFolders[boundary] {
			boundary = ""
		}
		for path := group; path != boundary && path != "."; path = filepath.Dir(path) {
			if _, found := folders[path]; found {
				break
			}
			parent := filepath.Dir(path)
			if parent == boundary || parent == path {
				parent = ""
			}
			folders[path] = folder{parent: parent, node: Node{ID: path, Name: filepath.Base(path), Index: -1, Folder: true}}
			if parent == "" {
				break
			}
		}
		if item.Folder {
			f := folders[group]
			f.node.Index = i
			folders[group] = f
		} else {
			if group == boundary {
				group = ""
			}
			children[group] = append(children[group], Node{ID: item.Key(), Name: item.Name(), Index: i})
		}
	}
	for _, folder := range folders {
		children[folder.parent] = append(children[folder.parent], folder.node)
	}
	return buildTree(children)
}

func savedTreeRoot(saved Saved, items []SavedItem) string {
	root := saved.Folder
	if root == "" {
		for _, item := range items {
			dir := filepath.Dir(item.Path)
			if root == "" {
				root = dir
			}
			for !containsPath(root, dir) && filepath.Dir(root) != root {
				root = filepath.Dir(root)
			}
		}
	}
	return root
}

func containsPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}
