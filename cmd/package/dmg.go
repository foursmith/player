package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

func buildDMG(bundle string, config packageConfig) (err error) {
	if !regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(config.Slug) ||
		config.Version == "" || strings.ContainsAny(config.Version, `/\`+"\x00") {
		return fmt.Errorf("DMG packaging requires a slug and version in mygo.json")
	}
	bundle, err = filepath.Abs(bundle)
	if err != nil {
		return err
	}
	dir := filepath.Dir(bundle)
	work, err := os.MkdirTemp(dir, ".dmg-")
	if err != nil {
		return err
	}
	var disk string
	defer func() {
		if disk != "" {
			if detachErr := command("hdiutil", "detach", "-force", disk); detachErr != nil {
				err = errors.Join(err, detachErr)
				return // Never remove a directory that still contains a mounted image.
			}
		}
		err = errors.Join(err, os.RemoveAll(work))
	}()

	writable := filepath.Join(work, "writable.dmg")
	if err = command("hdiutil", "convert", "assets/dmg/layout.dmg", "-format", "UDRW", "-o", writable); err != nil {
		return err
	}
	usage, err := exec.Command("du", "-Ask", bundle).Output()
	if err != nil {
		return err
	}
	fields := strings.Fields(string(usage))
	if len(fields) == 0 {
		return fmt.Errorf("could not measure %s", bundle)
	}
	kilobytes, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || kilobytes <= 0 {
		return fmt.Errorf("invalid bundle size: %q", fields[0])
	}
	info, err := os.Stat(writable)
	if err != nil {
		return err
	}
	megabytes := (info.Size()+kilobytes*1024)/(1024*1024) + 16
	if err = command("hdiutil", "resize", "-size", fmt.Sprintf("%dm", megabytes), writable); err != nil {
		return err
	}
	mount := filepath.Join(work, "mount")
	if err = os.Mkdir(mount, 0755); err != nil {
		return err
	}
	attached, err := exec.Command("hdiutil", "attach", "-readwrite", "-noverify", "-noautoopen", "-nobrowse", "-mountpoint", mount, writable).CombinedOutput()
	if err != nil {
		return fmt.Errorf("attach DMG: %w: %s", err, attached)
	}
	for _, line := range strings.Split(string(attached), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && strings.HasPrefix(fields[0], "/dev/disk") {
			disk = fields[0]
			break
		}
	}
	if disk == "" {
		disk = mount // Still detach on an unexpected hdiutil output format.
		return fmt.Errorf("could not identify attached DMG: %s", attached)
	}
	if err = command("diskutil", "renameVolume", mount, config.Name); err != nil {
		return err
	}
	app := filepath.Join(mount, filepath.Base(bundle))
	if err = command("ditto", "--noclone", bundle, app); err != nil {
		return err
	}
	if err = copyFile(filepath.Join(bundle, "Contents/Resources/AppIcon.icns"), filepath.Join(mount, ".VolumeIcon.icns")); err != nil {
		return err
	}
	if err = command("xattr", "-wx", "com.apple.FinderInfo", "0000000000000000040000000000000000000000000000000000000000000000", mount); err != nil {
		return err
	}
	artwork := filepath.Join(work, "artwork")
	preview := filepath.Join(*output, "dmg-preview.png")
	if err = command("swift", "-O", "scripts/dmg-artwork.swift", artwork, preview); err != nil {
		return err
	}
	background := filepath.Join(work, "background.tiff")
	if err = command("tiffutil", "-cathidpicheck", filepath.Join(artwork, "background.png"), filepath.Join(artwork, "background@2x.png"), "-out", background); err != nil {
		return err
	}
	pixels, err := os.ReadFile(background)
	if err != nil {
		return err
	}
	// Write in place: the Finder alias refers to this file's HFS ID.
	if err = os.WriteFile(filepath.Join(mount, ".background.tiff"), pixels, 0644); err != nil {
		return err
	}
	if err = command("codesign", "--verify", "--deep", "--strict", app); err != nil {
		return err
	}
	for _, name := range []string{".fseventsd", ".Trashes"} {
		if err = os.RemoveAll(filepath.Join(mount, name)); err != nil {
			return err
		}
	}
	if err = command("hdiutil", "detach", disk); err != nil {
		return err
	}
	disk = ""
	name := fmt.Sprintf("%s-%s-%s.dmg", config.Slug, config.Version, runtime.GOARCH)
	image := filepath.Join(work, name)
	if exec.Command("diskutil", "image", "create", "from", "-h").Run() == nil {
		err = command("diskutil", "image", "create", "from", "--format", "ULMO", writable, image)
	} else {
		err = command("hdiutil", "convert", writable, "-format", "ULMO", "-o", image)
	}
	if err != nil {
		return err
	}
	target := filepath.Join(dir, name)
	if err = os.Rename(image, target); err != nil {
		return err
	}
	if err = refreshChecksum(target, filepath.Join(*output, "SHA256SUMS")); err != nil {
		return err
	}
	fmt.Printf("Created installer: %s\nDMG preview: %s\n", target, preview)
	return nil
}

func refreshChecksum(image, checksums string) error {
	file, err := os.Open(image)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return err
	}
	data, err := os.ReadFile(checksums)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	name := filepath.Base(image)
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		if len(line) < 67 || line[64:66] != "  " {
			return fmt.Errorf("invalid checksum entry: %q", line)
		}
		if line[66:] != name {
			lines = append(lines, line)
		}
	}
	lines = append(lines, fmt.Sprintf("%x  %s", hash.Sum(nil), name))
	slices.SortFunc(lines, func(a, b string) int { return strings.Compare(a[66:], b[66:]) })
	return os.WriteFile(checksums, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}
