package projection

import "testing"

func TestMetadataRequiresFramesForAmbiguousPackedVR180(t *testing.T) {
	for _, tc := range []struct {
		spherical, stereo string
		aspect            float64
		mode              Mode
		conclusive        bool
	}{
		{"", "", 2, Flat, false},
		{"", "sbs", 2, Flat, false},
		{"equirectangular", "sbs", 2, Flat, false}, // 7680x3840 AVC regression.
		{"equirectangular", "sbs", 1.9, Flat, false},
		{"equirectangular", "sbs", 4, Flat, true}, // Genuine unsupported SBS 360.
		{"equirectangular", "mono", 2, Mono360, true},
		{"equirectangular", "tb", 1, TB360, true},
		{"half-equirectangular", "sbs", 2, SBS180, true},
		{"fisheye", "sbs", 2, SBSFisheye, true},
		{"unsupported", "sbs", 2, Flat, true},
	} {
		mode, conclusive := MetadataMode(tc.spherical, tc.stereo, tc.aspect)
		if mode != tc.mode || conclusive != tc.conclusive {
			t.Fatalf("%+v: mode=%v conclusive=%v", tc, mode, conclusive)
		}
	}
}

func TestDetectVideoType(t *testing.T) {
	for _, tc := range []struct {
		name, spherical, stereo string
		want                    Mode
	}{
		{"ordinary.mp4", "", "", Flat},
		{"Interstellar.2014.2160p.mkv", "", "", Flat},
		{"/VR180/ordinary.mp4", "", "", Flat},
		{"https://host/ordinary.mp4?title=VR180", "", "", Flat},
		{"180-days.mp4", "", "", Flat},
		{"concert_VR180.mp4", "", "", SBS180},
		{"concert_SBS_180_EQR.mp4", "", "", SBS180},
		{"concert_SBS_180_FE.mp4", "", "", SBSFisheye},
		{"concert_M_180_EQR.mp4", "", "", Mono180},
		{"concert_M_180_FE.mp4", "", "", MonoFisheye},
		{"concert_MONO_180_FISHEYE.mp4", "", "", MonoFisheye},
		{"trip_VR360.mp4", "", "", Mono360},
		{"trip_TB_360.mp4", "", "", TB360},
		{"trip_SBS_360.mp4", "", "", Flat}, // No SBS 360 renderer.
		{"trip_VR360_FISHEYE.mp4", "", "", Flat},
		{"trip_180_360_SBS.mp4", "", "", Flat},
		{"trip_VR180_Flat_2D.mp4", "", "", Flat},
		{"https://host/concert%20VR180.mp4", "", "", SBS180},
		{"ordinary.mp4", "equirectangular", "", Mono360},
		{"ordinary.mp4", "equirectangular", "tb", TB360},
		{"ordinary.mp4", "half-equirectangular", "sbs", SBS180},
		{"ordinary.mp4", "half-equirectangular", "mono", Mono180},
		{"ordinary.mp4", "fisheye", "sbs", SBSFisheye},
		{"ordinary.mp4", "fisheye", "", MonoFisheye},
		{"trip_VR180.mp4", "equirectangular", "mono", Mono360},
		{"trip_VR180.mp4", "unsupported", "", Flat},
		{"ordinary.mp4", "", "sbs", Flat}, // Stereo alone may be a 3D movie.
		{"trip_VR360.mp4", "equirectangular", "sbs", Flat},
	} {
		t.Run(tc.name+"/"+tc.spherical+"/"+tc.stereo, func(t *testing.T) {
			if got := Detect(tc.name, tc.spherical, tc.stereo); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
