package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefreshChecksum(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "player-1.0-arm64.dmg")
	checksums := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(image, []byte("abc"), 0644); err != nil {
		t.Fatal(err)
	}
	archive := strings.Repeat("a", 64) + "  player-1.0-darwin-arm64.tar.gz\n"
	old := strings.Repeat("b", 64) + "  player-1.0-arm64.dmg\n"
	if err := os.WriteFile(checksums, []byte(old+archive+old), 0644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := refreshChecksum(image, checksums); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(checksums)
		if err != nil {
			t.Fatal(err)
		}
		want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad  player-1.0-arm64.dmg\n" + archive
		if string(got) != want {
			t.Fatalf("checksum mismatch:\n%s", got)
		}
	}
	if err := os.Remove(checksums); err != nil {
		t.Fatal(err)
	}
	if err := refreshChecksum(image, checksums); err != nil {
		t.Fatal(err)
	}
}
