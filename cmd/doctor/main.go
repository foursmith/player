package main

import (
	"fmt"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/native"
	"github.com/foursmith/player/internal/video"
	"log"
)

func main() {
	fmt.Println("FFmpeg:", media.Version())
	surface, err := video.New(0)
	if err != nil {
		log.Fatal("GPU renderer: ", err)
	}
	surface.Close()
	fmt.Println("GPU renderer: ready")
	path, err := native.FindLibrary(native.InferenceLibrary())
	if err != nil {
		log.Fatal(err)
	}
	if err = native.HostTarget().CheckLibrary(path); err != nil {
		log.Fatal(err)
	}
	fmt.Println("LiteRT:", path)
}
