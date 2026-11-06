// Package scriptplugin adapts installable JS/TS modules to library.Plugin.
package scriptplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/foursmith/player/library"
)

const maxScriptSize = 4 << 20

var validID = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,79}$`)

type Descriptor struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Version    string            `json:"version"`
	APIVersion int               `json:"apiVersion"`
	Main       string            `json:"main"`
	Network    []string          `json:"network"`
	Settings   []library.Setting `json:"settings"`
}
type Plugin struct {
	descriptor Descriptor
	module     *moejs.Module
	exports    map[string]bool
	locales    map[string]map[string]string
}

func Load(manifest, script []byte, locales map[string]map[string]string) (*Plugin, error) {
	var d Descriptor
	if err := json.Unmarshal(manifest, &d); err != nil {
		return nil, err
	}
	if !validID.MatchString(d.ID) || d.ID == library.LocalID || d.ID == "dlna" || d.Name == "" || d.APIVersion != 1 {
		return nil, errors.New("invalid plugin manifest or unsupported apiVersion")
	}
	if !filepath.IsLocal(d.Main) || filepath.Base(d.Main) != d.Main || (filepath.Ext(d.Main) != ".js" && filepath.Ext(d.Main) != ".ts") || len(script) > maxScriptSize {
		return nil, errors.New("invalid plugin entry")
	}
	keys := map[string]bool{}
	urlKeys := map[string]bool{}
	for _, setting := range d.Settings {
		if setting.Key == "" || keys[setting.Key] || setting.Label == "" || (setting.Kind != "text" && setting.Kind != "url" && setting.Kind != "password") {
			return nil, errors.New("invalid plugin settings")
		}
		if setting.Kind == "password" && !setting.Secret {
			return nil, errors.New("password setting must be secret")
		}
		keys[setting.Key] = true
		urlKeys[setting.Key] = setting.Kind == "url" && !setting.Secret
	}
	for _, host := range d.Network {
		if host == "" || host == "*" {
			return nil, errors.New("network must name hosts or configured URL fields")
		}
		if strings.HasPrefix(host, "$") && !urlKeys[host[1:]] {
			return nil, errors.New("unknown network URL setting")
		}
	}
	compile := moejs.Compile
	if strings.HasSuffix(d.Main, ".ts") {
		compile = moejs.CompileTS
	}
	module, err := compile(d.Main, string(script))
	if err != nil {
		return nil, err
	}
	p := &Plugin{descriptor: d, module: module, exports: map[string]bool{}, locales: locales}
	for _, name := range module.Exports() {
		p.exports[name] = true
	}
	if !p.exports["browse"] || !p.exports["resolve"] {
		return nil, errors.New("plugin must export browse and resolve")
	}
	return p, nil
}
func LoadDir(dir string) (*Plugin, error) {
	return load(func(name string) ([]byte, error) { return readFile(filepath.Join(dir, filepath.FromSlash(name))) })
}

func load(read func(string) ([]byte, error)) (*Plugin, error) {
	manifest, err := read("plugin.json")
	if err != nil {
		return nil, err
	}
	var d Descriptor
	if err = json.Unmarshal(manifest, &d); err != nil {
		return nil, err
	}
	if !filepath.IsLocal(d.Main) || filepath.Base(d.Main) != d.Main {
		return nil, errors.New("invalid plugin entry")
	}
	script, err := read(d.Main)
	if err != nil {
		return nil, err
	}
	locales := map[string]map[string]string{}
	for _, locale := range []string{"en", "zh-CN", "ja"} {
		data, err := read("locales/" + locale + ".json")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var values map[string]string
		if err = json.Unmarshal(data, &values); err != nil {
			return nil, err
		}
		locales[locale] = values
	}
	return Load(manifest, script, locales)
}
func readFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxScriptSize {
		return nil, errors.New("plugin file must be a regular file below 4 MiB")
	}
	return os.ReadFile(path)
}
func (p *Plugin) Manifest() library.Manifest {
	return library.Manifest{ID: p.descriptor.ID, Name: p.descriptor.Name, Icon: "library"}
}
func (p *Plugin) Settings() []library.Setting {
	return append([]library.Setting(nil), p.descriptor.Settings...)
}
func (p *Plugin) Text(text, locale string) string {
	if value := p.locales[locale][text]; value != "" {
		return value
	}
	return text
}
func (p *Plugin) NetworkPermissions() []string { return append([]string(nil), p.descriptor.Network...) }
func (p *Plugin) Normalize(source library.Source) (library.Source, error) {
	var input map[string]string
	if err := json.Unmarshal(source.Config, &input); err != nil {
		return source, err
	}
	config := map[string]string{}
	for _, setting := range p.descriptor.Settings {
		value := strings.TrimSpace(input[setting.Key])
		if setting.Secret {
			if value != "" {
				return source, errors.New("secret must be stored in the credential store")
			}
			continue
		}
		if setting.Required && value == "" {
			return source, fmt.Errorf("%w: %s", library.ErrSetup, setting.Label)
		}
		if setting.Kind == "url" && value != "" {
			u, err := url.Parse(value)
			if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" {
				return source, fmt.Errorf("%w: invalid server URL", library.ErrSetup)
			}
			value = strings.TrimRight(value, "/")
		}
		config[setting.Key] = value
	}
	source.Config = library.EncodeConfig(config)
	source.ShareDLNA = false
	return source, nil
}
func (p *Plugin) Open(ctx context.Context, source library.Source, host library.Host) (library.Session, error) {
	var config map[string]string
	if err := json.Unmarshal(source.Config, &config); err != nil {
		return nil, err
	}
	credentials := map[string]string{}
	if source.CredentialRef != "" && host.Credentials != nil {
		var err error
		credentials, err = host.Credentials.Lookup(ctx, source.CredentialRef)
		if err != nil {
			return nil, err
		}
	}
	if credentials == nil {
		credentials = map[string]string{}
	}
	engine, err := p.start(ctx, source, config)
	if err != nil {
		return nil, err
	}
	return &scriptSession{source: source, plugin: p, engine: engine, config: config, credentials: credentials}, nil
}

type scriptSession struct {
	source              library.Source
	plugin              *Plugin
	engine              *engine
	config, credentials map[string]string
}

func (s *scriptSession) Source() library.Source { return s.source }
func (s *scriptSession) Close() error           { s.engine.close(); return nil }
func (s *scriptSession) call(ctx context.Context, method string, params map[string]any, out any) error {
	params["config"] = s.config
	params["credentials"] = s.credentials
	return s.engine.call(ctx, method, params, out)
}
func (s *scriptSession) Browse(ctx context.Context, request library.BrowseRequest) (library.Page, error) {
	var page library.Page
	err := s.call(ctx, "browse", map[string]any{"parentId": request.ParentID, "cursor": request.Cursor, "limit": request.Limit}, &page)
	return page, err
}

type playbackResult struct {
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Subtitle struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	} `json:"subtitle"`
	Live      bool            `json:"live"`
	Seekable  bool            `json:"seekable"`
	ExpiresAt string          `json:"expiresAt"`
	Session   json.RawMessage `json:"session"`
}

func (s *scriptSession) resolve(ctx context.Context, method string, request library.PlayRequest) (library.Playback, error) {
	var result playbackResult
	if err := s.call(ctx, method, map[string]any{"id": request.ItemID, "parentId": request.ParentID, "position": request.Position}, &result); err != nil {
		return library.Playback{}, err
	}
	playback := library.Playback{Resource: library.Resource{URL: result.URL, Headers: toHeaders(result.Headers)}, Subtitle: library.Resource{URL: result.Subtitle.URL, Headers: toHeaders(result.Subtitle.Headers)}, Live: result.Live, Seekable: result.Seekable, SessionData: result.Session}
	for _, address := range []string{result.URL, result.Subtitle.URL} {
		if address == "" {
			continue
		}
		req, err := http.NewRequest(http.MethodGet, address, nil)
		if err != nil {
			return playback, err
		}
		if err = s.engine.allow(req); err != nil {
			return playback, err
		}
	}
	if result.ExpiresAt != "" {
		var err error
		playback.ExpiresAt, err = time.Parse(time.RFC3339, result.ExpiresAt)
		if err != nil {
			return playback, err
		}
	}
	return playback, nil
}
func (s *scriptSession) Resolve(ctx context.Context, ref library.Reference) (library.Playback, error) {
	return s.resolve(ctx, "resolve", library.PlayRequest{Reference: ref})
}
func (s *scriptSession) Prepare(ctx context.Context, request library.PlayRequest) (library.Playback, error) {
	method := "resolve"
	if s.plugin.exports["prepare"] {
		method = "prepare"
	}
	return s.resolve(ctx, method, request)
}
func (p *Plugin) Authenticate(ctx context.Context, source library.Source, credentials map[string]string) (map[string]string, error) {
	if !p.exports["authenticate"] {
		return credentials, nil
	}
	var config map[string]string
	if err := json.Unmarshal(source.Config, &config); err != nil {
		return nil, err
	}
	engine, err := p.start(ctx, source, config)
	if err != nil {
		return nil, err
	}
	defer engine.close()
	var result map[string]string
	err = engine.call(ctx, "authenticate", map[string]any{"config": config, "credentials": credentials}, &result)
	return result, err
}
func (p *Plugin) Report(ctx context.Context, source library.Source, ref library.Reference, playback library.Playback, event library.PlaybackEvent, host library.Host) error {
	if !p.exports["report"] {
		return nil
	}
	opened, err := p.Open(ctx, source, host)
	if err != nil {
		return err
	}
	defer opened.Close()
	return opened.(*scriptSession).call(ctx, "report", map[string]any{"id": ref.ItemID, "session": playback.SessionData, "event": event}, nil)
}
func toHeaders(values map[string]string) http.Header {
	headers := http.Header{}
	for key, value := range values {
		headers.Set(key, value)
	}
	return headers
}
