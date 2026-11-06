package native

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestTargetRejectsHostLibrariesForAnotherPlatform(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	host := HostTarget()
	if err := host.CheckLibrary(executable); err != nil {
		t.Fatal(err)
	}
	for _, target := range []Target{{"windows", "amd64"}, {"windows", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}} {
		if target != host && target.CheckLibrary(executable) == nil {
			t.Fatalf("%s accepted host binary %s", target, host)
		}
	}
}

func TestTargetChecksForeignPEAndELFWithoutLoading(t *testing.T) {
	for _, tc := range []struct {
		target  Target
		machine uint16
		pe      bool
	}{
		{Target{"windows", "amd64"}, 0x8664, true}, {Target{"windows", "arm64"}, 0xaa64, true},
		{Target{"linux", "amd64"}, 62, false}, {Target{"linux", "arm64"}, 183, false},
	} {
		t.Run(tc.target.String(), func(t *testing.T) {
			data := make([]byte, 128)
			if tc.pe {
				binary.LittleEndian.PutUint16(data, tc.machine) // A COFF header is sufficient for debug/pe.
			} else {
				copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
				binary.LittleEndian.PutUint16(data[16:], 3)
				binary.LittleEndian.PutUint16(data[18:], tc.machine)
				binary.LittleEndian.PutUint32(data[20:], 1)
				binary.LittleEndian.PutUint16(data[52:], 64)
			}
			path := filepath.Join(t.TempDir(), "library")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			target := tc.target
			if err := target.CheckLibrary(path); err != nil {
				t.Fatal(err)
			}
			target.Arch = map[string]string{"amd64": "arm64", "arm64": "amd64"}[target.Arch]
			if err := target.CheckLibrary(path); err == nil {
				t.Fatal("accepted wrong architecture")
			}
		})
	}
}
