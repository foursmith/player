package projection

import (
	"net/url"
	"path"
	"strings"
	"unicode"
)

// MetadataMode returns a conclusive metadata decision, including unsupported
// layouts. Some VR180 files carry generic full-sphere + SBS tags: a 2:1 packed
// frame contradicts the 4:1 full-sphere SBS layout, so inspect its images.
// Dimensions only invalidate that declaration; they never select a VR mode.
func MetadataMode(spherical, stereo string, aspect float64) (Mode, bool) {
	if spherical == "" || spherical == "equirectangular" && stereo == "sbs" && aspect >= 1.75 && aspect <= 2.25 {
		return Flat, false
	}
	return Detect("", spherical, stereo), true
}

// Detect prefers explicit spatial metadata to filename hints. Dimensions alone
// cannot distinguish a panorama from an ordinary wide video.
func Detect(name, spherical, stereo string) Mode {
	switch spherical {
	case "equirectangular":
		switch stereo {
		case "", "mono":
			return Mono360
		case "tb":
			return TB360
		}
	case "half-equirectangular", "fisheye":
		if stereo == "" || stereo == "mono" || stereo == "sbs" {
			if spherical == "fisheye" {
				if stereo == "sbs" {
					return SBSFisheye
				}
				return MonoFisheye
			}
			if stereo == "sbs" {
				return SBS180
			}
			return Mono180
		}
	}
	if spherical != "" {
		// A known but unsupported spatial layout must not be guessed as one
		// of the supported VR projections.
		return Flat
	}
	if u, err := url.Parse(name); err == nil && u.Scheme != "" {
		name = u.Path
	}
	name = path.Base(name)
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	tokens := strings.FieldsFunc(strings.ToLower(strings.TrimSuffix(name, path.Ext(name))), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	hints := map[string]bool{}
	for _, token := range tokens {
		hints[token] = true
	}
	if hints["2d"] || hints["flat"] || hints["flat2d"] {
		return Flat
	}
	sbs, tb := hints["sbs"] || hints["lr"], hints["tb"] || hints["ou"]
	half := hints["180"] || hints["vr180"]
	full := hints["360"] || hints["vr360"]
	mono := hints["mono"] || hints["monoscopic"] || hints["m"] && half
	fisheye := hints["fisheye"] || hints["fe"]
	vr := hints["vr"] || hints["vr180"] || hints["vr360"]
	if sbs && tb || half && full || full && fisheye {
		return Flat
	}
	if tb && !half && !fisheye {
		return TB360
	}
	if fisheye && (vr || half || sbs || mono) {
		if mono {
			return MonoFisheye
		}
		return SBSFisheye
	}
	if full && !sbs {
		return Mono360
	}
	if (half && (vr || mono || sbs || hints["eqr"])) || sbs && !full || vr && !full {
		if mono || hints["m"] {
			return Mono180
		}
		return SBS180
	}
	return Flat
}
