// package bundles native dependencies and creates the macOS installer.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/foursmith/player/internal/native"
)

var output = flag.String("out", "build", "MyGo output directory")
var prepare = flag.Bool("prepare", false, "prepare local libraries before MyGo creates archives and installers")

type packageConfig struct {
	Name, Slug, Version string
}

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func command(name string, args ...string) error {
	c := exec.Command(name, args...)
	b, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, b)
	}
	return nil
}
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	e := out.Close()
	if err != nil {
		return err
	}
	return e
}
func deps(path string) ([]string, error) {
	b, err := exec.Command("otool", "-L", path).Output()
	if err != nil {
		return nil, err
	}
	var result []string
	for _, line := range strings.Split(string(b), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			result = append(result, fields[0])
		}
	}
	return result, nil
}
func run() error {
	target := native.HostTarget()
	if *prepare {
		if target.OS == "darwin" {
			return target.CheckLibrary(target.InferencePath())
		}
		return preparePortable(target)
	}
	manifest, err := os.ReadFile("mygo.json")
	if err != nil {
		return err
	}
	var config packageConfig
	if err := json.Unmarshal(manifest, &config); err != nil {
		return err
	}
	if config.Name == "" {
		return fmt.Errorf("mygo.json is missing the application name")
	}
	if target.OS != "darwin" {
		return portable(config, target)
	}
	bundle := filepath.Join(*output, target.Directory(), config.Name+".app")
	resource := filepath.Join(bundle, "Contents", "Resources")
	lib := filepath.Join(resource, "lib")
	if err := os.MkdirAll(lib, 0755); err != nil {
		return err
	}
	main := filepath.Join(bundle, "Contents", "MacOS", config.Name)
	if _, err := os.Stat(main); err != nil {
		return err
	}
	rt := target.InferencePath()
	runtimeDest := filepath.Join(lib, target.InferenceLibrary())
	if err = copyFile(rt, runtimeDest); err != nil {
		return err
	}
	queue := []string{main, runtimeDest}
	seen := map[string]string{target.InferenceLibrary(): rt}
	var libraries []string
	// Purego loads these at runtime, so otool on the executable cannot discover
	// SDL, filters and scaling. Seed every direct dependency explicitly.
	for _, name := range target.MediaLibraries() {
		path, err := native.FindLibrary(name)
		if err != nil {
			return err
		}
		if err := target.CheckLibrary(path); err != nil {
			return err
		}
		dest := filepath.Join(lib, name)
		if err := copyFile(path, dest); err != nil {
			return err
		}
		seen[name] = path
		queue = append(queue, dest)
		libraries = append(libraries, dest)
	}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		if filepath.Dir(path) == lib {
			if err := command("install_name_tool", "-id", "@loader_path/"+filepath.Base(path), path); err != nil {
				return err
			}
		}
		dependencies, err := deps(path)
		if err != nil {
			return err
		}
		for _, dep := range dependencies {
			if strings.HasPrefix(dep, "/System/") || strings.HasPrefix(dep, "/usr/lib/") {
				continue
			}
			if strings.HasPrefix(dep, "@") { // Includes each library's rewritten install name.
				if strings.HasPrefix(dep, "@loader_path/") || strings.HasPrefix(dep, "@executable_path/") {
					continue
				}
				return fmt.Errorf("unresolved dynamic dependency %s in %s", dep, path)
			}
			base := filepath.Base(dep)
			dest := filepath.Join(lib, base)
			if old, ok := seen[base]; ok {
				if old != dep {
					oldReal, _ := filepath.EvalSymlinks(old)
					newReal, _ := filepath.EvalSymlinks(dep)
					if oldReal != newReal {
						return fmt.Errorf("conflicting libraries: %s and %s", old, dep)
					}
				}
			} else {
				seen[base] = dep
				if err = copyFile(dep, dest); err != nil {
					return err
				}
				queue = append(queue, dest)
				libraries = append(libraries, dest)
			}
			replacement := "@loader_path/" + base
			if path == main {
				replacement = "@executable_path/../Resources/lib/" + base
			}
			if err = command("install_name_tool", "-change", dep, replacement, path); err != nil {
				return err
			}
		}
	}
	for _, path := range append(libraries, runtimeDest, main) {
		if err = command("codesign", "--force", "--sign", "-", path); err != nil {
			return err
		}
	}
	if err = command("codesign", "--force", "--deep", "--sign", "-", bundle); err != nil {
		return err
	}
	if err = command("codesign", "--verify", "--deep", "--strict", bundle); err != nil {
		return err
	}
	fmt.Printf("Packaged %d native libraries: %s\n", len(seen), bundle)
	return buildDMG(bundle, config)
}
