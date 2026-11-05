// Package fsvr implements the existing /api/v1 protocol, including cookie auth.
package fsvr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/foursmith/player/internal/library"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	base *url.URL
	http *http.Client
}
type source struct{ ID, Name, Kind string }
type entry struct{ ID, Name, Kind string }

func New(endpoint string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("invalid FSVR endpoint")
	}
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""
	jar, _ := cookiejar.New(nil)
	return &Client{u, &http.Client{Jar: jar, Timeout: 25 * time.Second}}, nil
}
func (c *Client) request(ctx context.Context, method, path string, body any, out any) error {
	var b bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&b).Encode(body); err != nil {
			return err
		}
	}
	u, err := url.Parse(c.base.String() + path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), &b)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	r, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return fmt.Errorf("FSVR HTTP %d", r.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(out)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(r.Body, 16<<20))
	return err
}
func (c *Client) Connect(ctx context.Context) (bool, error) {
	var status struct{ Name string }
	if err := c.request(ctx, "GET", "/api/v1/status", nil, &status); err != nil {
		return false, err
	}
	if status.Name != "fsvr" {
		return false, fmt.Errorf("not an FSVR server")
	}
	var auth struct{ Authenticated bool }
	if err := c.request(ctx, "GET", "/api/v1/auth", nil, &auth); err != nil {
		return false, err
	}
	return auth.Authenticated, nil
}
func (c *Client) Authenticate(ctx context.Context, password string) error {
	return c.request(ctx, "POST", "/api/v1/auth", map[string]string{"password": password}, nil)
}
func (c *Client) Sources(ctx context.Context) ([]library.Folder, error) {
	var sources []source
	if err := c.request(ctx, "GET", "/api/v1/sources", nil, &sources); err != nil {
		return nil, err
	}
	var out []library.Folder
	for _, s := range sources {
		out = append(out, library.Folder{ID: "source:" + s.ID, Name: s.Name, SourceID: s.ID, Kind: s.Kind})
	}
	return out, nil
}
func (c *Client) MediaURL(sourceID, id string) string {
	return c.base.String() + "/api/v1/media/" + url.PathEscape(sourceID) + "/" + url.PathEscape(id)
}
func (c *Client) Cookies(mediaURL string) string {
	u, err := url.Parse(mediaURL)
	if err != nil {
		return ""
	}
	var lines []string
	for _, cookie := range c.http.Jar.Cookies(u) {
		lines = append(lines, fmt.Sprintf("%s=%s; path=/; domain=%s;", cookie.Name, cookie.Value, u.Hostname()))
	}
	return strings.Join(lines, "\n")
}
func (c *Client) Entries(ctx context.Context, folder library.Folder) ([]library.Entry, []library.Folder, error) {
	path := "/api/v1/sources/" + url.PathEscape(folder.SourceID) + "/entries"
	if folder.RemotePath != "" {
		path += "?path=" + url.QueryEscape(folder.RemotePath)
	}
	var entries []entry
	if err := c.request(ctx, "GET", path, nil, &entries); err != nil {
		return nil, nil, err
	}
	var videos []library.Entry
	var folders []library.Folder
	var subtitles []string
	for _, e := range entries {
		if library.IsAppleDouble(e.Name) {
			continue
		}
		switch e.Kind {
		case "folder":
			folders = append(folders, library.Folder{ID: folder.SourceID + ":" + e.ID, Parent: folder.ID, Name: e.Name, SourceID: folder.SourceID, RemotePath: e.ID, Kind: folder.Kind})
		case "video":
			u := c.MediaURL(folder.SourceID, e.ID)
			videos = append(videos, library.Entry{Name: e.Name, Path: u, Group: folder.ID, SourceID: folder.SourceID, Cookies: c.Cookies(u)})
		case "subtitle":
			subtitles = append(subtitles, e.Name)
		}
	}
	names := make([]string, len(videos))
	for i, v := range videos {
		names[i] = v.Name
	}
	for vi, si := range library.MatchSubtitles(names, subtitles) {
		for _, e := range entries {
			if e.Kind == "subtitle" && e.Name == subtitles[si] {
				videos[vi].Subtitle = c.MediaURL(folder.SourceID, e.ID)
				videos[vi].SubtitleName = e.Name
				break
			}
		}
	}
	return videos, folders, nil
}
func (c *Client) Scan(ctx context.Context) error {
	return c.request(ctx, "POST", "/api/v1/dlna/discover", nil, nil)
}
func (c *Client) ReadSubtitle(ctx context.Context, path string) ([]byte, error) {
	u, err := url.Parse(path)
	if err != nil || u.Scheme != c.base.Scheme || u.Host != c.base.Host {
		return nil, fmt.Errorf("invalid subtitle origin")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	r, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("subtitle HTTP %d", r.StatusCode)
	}
	return io.ReadAll(io.LimitReader(r.Body, 8<<20))
}
