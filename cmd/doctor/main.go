package main

import (
	"fmt"
	"github.com/foursmith/vr/app/internal/media"
	"github.com/foursmith/vr/app/internal/video"
	"log"
	"os"
	"os/exec"
)

func main() {
	fmt.Println("FFmpeg:", media.Version())
	if path, err := exec.LookPath("ffmpeg"); err != nil {
		log.Fatal("FFmpeg encoder executable is missing")
	} else {
		fmt.Println("Encoder:", path)
	}
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
