package native

import (
	"debug/elf"
	"debug/pe"
	"slices"
	"strings"
)

// LibraryDependencies inspects imports without executing a target library.
func (t Target) LibraryDependencies(path string) ([]string, error) {
	if t.OS == "linux" {
		file, err := elf.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		return file.ImportedLibraries()
	}
	file, err := pe.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	// debug/pe.ImportedLibraries is unimplemented. ImportedSymbols includes the
	// declaring DLL after each symbol name and also works for stripped images.
	symbols, err := file.ImportedSymbols()
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool)
	for _, symbol := range symbols {
		if at := strings.LastIndexByte(symbol, ':'); at >= 0 {
			set[strings.ToLower(symbol[at+1:])] = true
		}
	}
	var libraries []string
	for name := range set {
		libraries = append(libraries, name)
	}
	slices.Sort(libraries)
	return libraries, nil
}

func WindowsSystemLibrary(name string) bool {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "api-ms-win-") || strings.HasPrefix(name, "ext-ms-win-") {
		return true
	}
	return slices.Contains([]string{
		"advapi32.dll", "avrt.dll", "bcrypt.dll", "bcryptprimitives.dll", "cfgmgr32.dll", "comctl32.dll", "comdlg32.dll", "crypt32.dll", "d2d1.dll", "d3d11.dll", "dcomp.dll", "dnsapi.dll", "dwmapi.dll", "dwrite.dll", "dxgi.dll", "gdi32.dll", "imm32.dll", "iphlpapi.dll", "kernel32.dll", "kernelbase.dll", "mfplat.dll", "mpr.dll", "msimg32.dll", "msvcrt.dll", "ncrypt.dll", "normaliz.dll", "ntdll.dll", "ole32.dll", "oleaut32.dll", "opengl32.dll", "powrprof.dll", "propsys.dll", "rpcrt4.dll", "secur32.dll", "setupapi.dll", "shell32.dll", "shlwapi.dll", "ucrtbase.dll", "user32.dll", "userenv.dll", "usp10.dll", "uxtheme.dll", "version.dll", "winhttp.dll", "wininet.dll", "winmm.dll", "wintrust.dll", "ws2_32.dll", "wtsapi32.dll",
	}, name)
}
