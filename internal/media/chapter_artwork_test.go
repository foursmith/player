package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"
)

// Add three independent JPEG samples referenced as QuickTime chapters. The
// first picture uses an empty edit, matching movies whose opening has no cover.
func chapterArtworkFixture(t *testing.T) (string, [][]byte) {
	t.Helper()
	file := chapterPreviewFixture(t, "mp4")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var pictures [][]byte
	for _, c := range []color.RGBA{{0, 255, 0, 255}, {255, 0, 255, 255}, {255, 255, 0, 255}} {
		img := image.NewRGBA(image.Rect(0, 0, 64, 36))
		for y := 0; y < 36; y++ {
			for x := 0; x < 64; x++ {
				img.SetRGBA(x, y, c)
			}
		}
		var encoded bytes.Buffer
		if err := jpeg.Encode(&encoded, img, nil); err != nil {
			t.Fatal(err)
		}
		pictures = append(pictures, encoded.Bytes())
	}
	var moov []byte
	mp4Children(t, data, func(kind string, payload []byte) []byte {
		if kind == "moov" {
			moov = bytes.Clone(payload)
		}
		return nil
	})
	var scale, maxID uint32
	mp4Children(t, moov, func(kind string, payload []byte) []byte {
		if kind == "mvhd" {
			scale = binary.BigEndian.Uint32(payload[12:])
		}
		if kind == "trak" {
			mp4Children(t, payload, func(k string, p []byte) []byte {
				if k == "tkhd" {
					maxID = max(maxID, binary.BigEndian.Uint32(p[12:]))
				}
				return nil
			})
		}
		return nil
	})
	imageID := maxID + 1
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd, 6)
	binary.BigEndian.PutUint32(tkhd[12:], imageID)
	binary.BigEndian.PutUint32(tkhd[20:], scale*12/10)
	copy(tkhd[40:], mp4Words(0x10000, 0, 0, 0, 0x10000, 0, 0, 0, 0x40000000))
	binary.BigEndian.PutUint32(tkhd[76:], 64<<16)
	binary.BigEndian.PutUint32(tkhd[80:], 36<<16)
	mdhd := append(mp4Words(0, 0, 0, 1000, 1200), 0x55, 0xc4, 0, 0)
	hdlr := append(mp4Words(0, 0), []byte("vide")...)
	hdlr = append(hdlr, make([]byte, 12)...)
	hdlr = append(hdlr, []byte("Chapter Images\x00")...)
	entry := make([]byte, 78)
	binary.BigEndian.PutUint16(entry[6:], 1)
	binary.BigEndian.PutUint16(entry[24:], 64)
	binary.BigEndian.PutUint16(entry[26:], 36)
	binary.BigEndian.PutUint32(entry[28:], 72<<16)
	binary.BigEndian.PutUint32(entry[32:], 72<<16)
	binary.BigEndian.PutUint16(entry[40:], 1)
	binary.BigEndian.PutUint16(entry[74:], 24)
	binary.BigEndian.PutUint16(entry[76:], 0xffff)
	stsd := mp4Atom("stsd", append(mp4Words(0, 1), mp4Atom("jpeg", entry)...))
	stts := mp4Atom("stts", mp4Words(0, 3, 1, 320, 1, 400, 1, 400))
	stsc := mp4Atom("stsc", mp4Words(0, 1, 1, 1, 1))
	stsz := mp4Atom("stsz", mp4Words(0, 0, 3, uint32(len(pictures[0])), uint32(len(pictures[1])), uint32(len(pictures[2]))))
	co64 := mp4Words(0, 3)
	position := uint64(len(data) + 8)
	for _, picture := range pictures {
		co64 = binary.BigEndian.AppendUint64(co64, position)
		position += uint64(len(picture))
	}
	stbl := mp4Atom("stbl", bytes.Join([][]byte{stsd, stts, stsc, stsz, mp4Atom("co64", co64), mp4Atom("stss", mp4Words(0, 3, 1, 2, 3))}, nil))
	dinf := mp4Atom("dinf", mp4Atom("dref", append(mp4Words(0, 1), mp4Atom("url ", mp4Words(1))...)))
	minf := mp4Atom("minf", bytes.Join([][]byte{mp4Atom("vmhd", mp4Words(1, 0, 0)), dinf, stbl}, nil))
	elst := mp4Words(0x01000000, 2)
	for _, edit := range [][2]uint64{{uint64(scale) * 8 / 100, ^uint64(0)}, {uint64(scale) * 112 / 100, 0}} {
		elst = binary.BigEndian.AppendUint64(elst, edit[0])
		elst = binary.BigEndian.AppendUint64(elst, edit[1])
		elst = append(elst, 0, 1, 0, 0)
	}
	track := mp4Atom("trak", bytes.Join([][]byte{mp4Atom("tkhd", tkhd), mp4Atom("edts", mp4Atom("elst", elst)), mp4Atom("mdia", bytes.Join([][]byte{mp4Atom("mdhd", mdhd), mp4Atom("hdlr", hdlr), minf}, nil))}, nil))
	moov = mp4Children(t, moov, func(kind string, payload []byte) []byte {
		if kind == "mvhd" {
			binary.BigEndian.PutUint32(payload[len(payload)-4:], imageID+1)
		}
		if kind == "trak" {
			payload = mp4Children(t, payload, func(k string, p []byte) []byte {
				if k == "tref" {
					p = mp4Children(t, p, func(ref string, b []byte) []byte {
						if ref == "chap" {
							b = append(bytes.Clone(b), mp4Words(imageID)...)
						}
						return mp4Atom(ref, b)
					})
				}
				return mp4Atom(k, p)
			})
		}
		return mp4Atom(kind, payload)
	})
	data = mp4Children(t, data, func(kind string, payload []byte) []byte {
		if kind == "moov" {
			kind = "free"
		}
		return mp4Atom(kind, payload)
	})
	data = append(data, mp4Atom("mdat", bytes.Join(pictures, nil))...)
	data = append(data, mp4Atom("moov", append(moov, track...))...)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	return file, pictures
}

func TestEmbeddedChapterArtworkWithEmptyEditAndHTTP(t *testing.T) {
	file, pictures := chapterArtworkFixture(t)
	server := httptest.NewServer(http.FileServer(http.Dir(filepath.Dir(file))))
	defer server.Close()
	for _, source := range []string{file, server.URL + "/" + filepath.Base(file)} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		p, err := OpenChapterArtwork(ctx, source)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		for _, i := range []int{2, 0, 1, 0} {
			at := []float64{.08, .4, .8}[i]
			img, err := p.Image(at)
			if err != nil {
				t.Fatal(at, err)
			}
			if img.Bounds().Dx() != 240 || img.Bounds().Dy() != 135 {
				t.Fatal("incorrect artwork dimensions", img.Bounds())
			}
			packet := p.decoder.packet
			if !bytes.Equal(unsafe.Slice(packet.Data, int(packet.Size)), pictures[i]) {
				t.Fatal("selected a main-video frame or another chapter's picture", i)
			}
		}
		cancel()
		if _, err := p.Image(.4); !errors.Is(err, context.Canceled) {
			t.Fatal("artwork ignored cancellation", err)
		}
		p.Close()
	}
	d, err := Open(context.Background(), Source{URL: file}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if !d.Metadata().HasChapterArtwork {
		t.Fatal("playback metadata omitted embedded artwork")
	}
}

func TestChapterArtworkDoesNotSubstituteMovieFrames(t *testing.T) {
	if p, err := OpenChapterArtwork(context.Background(), chapterPreviewFixture(t, "mp4")); !errors.Is(err, ErrNoChapterArtwork) {
		if p != nil {
			p.Close()
		}
		t.Fatal("movie without artwork was treated as illustrated chapters", err)
	}
}
