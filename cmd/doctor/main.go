package main

import (
	"fmt"
	"github.com/foursmith/player/internal/media"
	"github.com/foursmith/player/internal/video"
	"log"
	"os"
)

func main() {
	fmt.Println("FFmpeg:", media.Version())
	surface, err := video.New(0)
	if err != nil {
		log.Fatal("GPU renderer: ", err)
	}
	surface.Close()
	fmt.Println("GPU renderer: ready")
	path := os.Getenv("FSVR_TFLITE_LIBRARY")
	if path == "" {
		log.Fatal("LiteRT runtime is missing; run just deps")
	}
	if _, err = os.Stat(path); err != nil {
		log.Fatal(err)
	}
	fmt.Println("LiteRT:", path)
}
