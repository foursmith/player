package exporter

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestMotionPhotoKeepsJpegAndVideo(t *testing.T) {
	cover := []byte{0xff, 0xd8, 0xff, 0xe0, 0, 4, 1, 2, 0xff, 0xd9}
	video := []byte("video-payload")
	out, err := MotionPhoto(cover, video)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out[:8], cover[:8]) || !bytes.Equal(out[len(out)-len(video):], video) {
		t.Fatal("media payload changed")
	}
	if out[8] != 0xff || out[9] != 0xe1 {
		t.Fatal("XMP not after APP0")
	}
	length := int(binary.BigEndian.Uint16(out[10:12]))
	if !bytes.Contains(out[12:10+length], []byte(`GCamera:MicroVideoOffset="13"`)) {
		t.Fatal("incorrect video offset")
	}
	if _, err = MotionPhoto([]byte("not-jpeg"), video); err == nil {
		t.Fatal("invalid cover accepted")
	}
}
