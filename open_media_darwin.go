package main

import (
	"unsafe"

	"github.com/ebitengine/purego/objc"
)

const mediaOpenWidth float32 = 256

func (a *application) mediaOpenActions() []mediaPickerAction {
	return []mediaPickerAction{
		{icon: "file-video", label: "media.open", run: a.chooseMixedMedia},
		{icon: "library", label: "library.title", run: a.openCatalog},
	}
}

// MyGo's public dialog currently selects files or directories separately.
// NSOpenPanel supports both in the same native sheet.
func (a *application) chooseMixedMedia() {
	if a.dialogOpen {
		return
	}
	a.dialogOpen = true
	window, ctx := a.window, a.ctx
	title, message := a.t("media.open"), a.t("media.openDescription")
	window.Update(func() {
		if ctx.Err() != nil || a.shutdown {
			a.dialogOpen = false
			return
		}
		panel := objc.ID(objc.GetClass("NSOpenPanel")).Send(objc.RegisterName("openPanel"))
		if panel == 0 {
			a.dialogOpen = false
			a.notice = a.t("media.openFailed")
			return
		}
		panel.Send(objc.RegisterName("retain"))
		strings := objc.ID(objc.GetClass("NSString"))
		panel.Send(objc.RegisterName("setTitle:"), strings.Send(objc.RegisterName("stringWithUTF8String:"), title))
		panel.Send(objc.RegisterName("setMessage:"), strings.Send(objc.RegisterName("stringWithUTF8String:"), message))
		panel.Send(objc.RegisterName("setCanChooseFiles:"), uintptr(1))
		panel.Send(objc.RegisterName("setCanChooseDirectories:"), uintptr(1))
		panel.Send(objc.RegisterName("setAllowsMultipleSelection:"), uintptr(1))
		completion := objc.NewBlock(func(_ objc.Block, response int) {
			defer panel.Send(objc.RegisterName("release"))
			var paths []string
			if response == 1 {
				urls := panel.Send(objc.RegisterName("URLs"))
				count := objc.Send[uintptr](urls, objc.RegisterName("count"))
				for i := uintptr(0); i < count; i++ {
					url := urls.Send(objc.RegisterName("objectAtIndex:"), i)
					path := url.Send(objc.RegisterName("path"))
					length := objc.Send[uintptr](path, objc.RegisterName("lengthOfBytesUsingEncoding:"), uintptr(4)) // NSUTF8StringEncoding
					data := objc.Send[unsafe.Pointer](path, objc.RegisterName("UTF8String"))
					if data != nil && length > 0 {
						paths = append(paths, string(unsafe.Slice((*byte)(data), length)))
					}
				}
			}
			window.Update(func() {
				a.dialogOpen = false
				if ctx.Err() == nil && !a.shutdown && len(paths) > 0 {
					a.importPaths(paths, false)
				}
			})
		})
		panel.Send(objc.RegisterName("beginSheetModalForWindow:completionHandler:"), window.NativeHandle(), uintptr(completion))
		completion.Release()
	})
}
