package scriptplugin

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/foursmith/player/library"
)

//go:embed builtin/*/* builtin/*/locales/*
var bundled embed.FS

func Bundled() ([]*Plugin, error) {
	entries, err := fs.ReadDir(bundled, "builtin")
	if err != nil {
		return nil, err
	}
	var plugins []*Plugin
	for _, entry := range entries {
		root := "builtin/" + entry.Name()
		p, err := load(func(name string) ([]byte, error) { return fs.ReadFile(bundled, root+"/"+name) })
		if err != nil {
			return nil, err
		}
		plugins = append(plugins, p)
	}
	return plugins, nil
}

type Catalog struct {
	Dir      string
	Registry *library.Registry
	mu       sync.Mutex
}

func (c *Catalog) Load() []error {
	entries, err := os.ReadDir(c.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []error{err}
	}
	var failures []error
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name()[0] == '.' {
			continue
		}
		p, err := LoadDir(filepath.Join(c.Dir, entry.Name()))
		if err == nil {
			err = c.Registry.Register(p)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return failures
}

// Install stages a checked, single-entry plugin. No plugin code runs on import.
// Reimports are rejected so active code cannot be replaced behind a playback.
func (c *Catalog) Install(dir string) (*Plugin, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := LoadDir(dir)
	if err != nil {
		return nil, err
	}
	if _, err = c.Registry.Plugin(p.descriptor.ID); err == nil {
		return nil, errors.New("plugin ID is already installed")
	}
	if err = os.MkdirAll(c.Dir, 0700); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(c.Dir, ".install-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	for _, name := range []string{"plugin.json", p.descriptor.Main, "locales/en.json", "locales/zh-CN.json", "locales/ja.json"} {
		data, err := readFile(filepath.Join(dir, filepath.FromSlash(name)))
		if errors.Is(err, os.ErrNotExist) && name != "plugin.json" && name != p.descriptor.Main {
			continue
		}
		if err != nil {
			return nil, err
		}
		destination := filepath.Join(tmp, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return nil, err
		}
		if err = os.WriteFile(destination, data, 0600); err != nil {
			return nil, err
		}
	}
	checked, err := LoadDir(tmp)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(c.Dir, checked.descriptor.ID)
	if _, err = os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("plugin directory already exists")
	}
	if err = os.Rename(tmp, target); err != nil {
		return nil, err
	}
	if err = c.Registry.Register(checked); err != nil {
		_ = os.RemoveAll(target)
		return nil, err
	}
	return checked, nil
}
