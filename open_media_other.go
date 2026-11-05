//go:build !darwin

package main

const mediaOpenWidth float32 = 420

// Native Windows/GTK choosers use separate file and folder modes.
func (a *application) mediaOpenActions() []mediaPickerAction {
	return []mediaPickerAction{
		{icon: "file-video", label: "media.openFiles", compact: "media.files", run: func() { a.choose(false, false) }},
		{icon: "library", label: "library.title", compact: "library.shortTitle", run: a.openCatalog},
		{icon: "folder", label: "media.openFolder", compact: "media.folder", run: func() { a.choose(true, false) }},
	}
}
