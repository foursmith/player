package library

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

const LocalID = "local"

// Source is a configured plugin instance. Config belongs to the plugin, while
// CredentialRef names a host-managed secret; passwords and access tokens do not
// belong in Config or playlist snapshots.
type Source struct {
	ID, Name      string
	Plugin        string
	Config        json.RawMessage
	CredentialRef string `json:",omitempty"`
	ShareDLNA     bool   `json:",omitempty"`
}

// LocalConfig describes the built-in filesystem plugin, including offline paths.
type LocalConfig struct {
	Folder         string   `json:",omitempty"`
	Paths, Folders []string `json:",omitempty"`
}

func EncodeConfig(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func (s Source) Local() LocalConfig {
	var config LocalConfig
	if s.IsLocal() {
		_ = json.Unmarshal(s.Config, &config)
	}
	return config
}

// LocalFolder reads only the root, without allocating large collection member lists.
func (s Source) LocalFolder() string {
	if !s.IsLocal() {
		return ""
	}
	var config struct{ Folder string }
	_ = json.Unmarshal(s.Config, &config)
	return config.Folder
}
func (s *Source) SetLocal(config LocalConfig) { s.Config = EncodeConfig(config) }
func (s Source) IsLocal() bool                { return s.Plugin == LocalID }
func (s Source) Collection() bool             { return s.IsLocal() && s.LocalFolder() == "" }

func CloneSources(sources []Source) []Source {
	out := slices.Clone(sources)
	for i := range out {
		out[i].Config = slices.Clone(out[i].Config)
	}
	return out
}

// ValidateSource does no IO and does not require an installed plugin. Keeping
// unavailable plugins allows users to reconnect them without losing libraries.
func ValidateSource(s Source) error {
	if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Plugin) == "" || !json.Valid(s.Config) || !bytes.HasPrefix(bytes.TrimSpace(s.Config), []byte("{")) {
		return errors.New("invalid library source")
	}
	return nil
}

// Raw plugin JSON has a canonical representation across indented state writes.
func (s *Source) UnmarshalJSON(data []byte) error {
	type plain Source
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if len(value.Config) > 0 {
		var buffer bytes.Buffer
		if err := json.Compact(&buffer, value.Config); err != nil {
			return err
		}
		value.Config = buffer.Bytes()
	}
	*s = Source(value)
	return nil
}
