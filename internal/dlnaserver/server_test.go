package dlnaserver

import (
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/foursmith/player/internal/dlna"
	"github.com/foursmith/player/internal/library"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T, sources []library.Saved) (*Server, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{ctx: ctx, cancel: cancel, name: "Foursmith & <Library>", udn: "uuid:" + NewUUID(), sources: SharedSources(sources), reload: make(chan struct{}, 1), events: make(chan struct{}, 1), subs: map[string]*subscription{}}
	var err error
	s.objects, err = readCatalog(ctx, s.name, s.sources)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s)
	s.wg.Go(s.refreshLoop)
	s.wg.Go(s.eventLoop)
	t.Cleanup(func() { cancel(); server.Close(); s.wg.Wait() })
	return s, server.URL
}

type soapResult struct {
	Result         string
	NumberReturned int
	TotalMatches   int
	UpdateID       uint32
	Id             uint32
	Source, Sink   string
	ErrorCode      int `xml:"detail>UPnPError>errorCode"`
}

func soap(t *testing.T, base, service, action, body string, status int) soapResult {
	t.Helper()
	path := "/content/control"
	if service == connectionType {
		path = "/connection/control"
	}
	request, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:`+action+` xmlns:u="`+service+`">`+body+`</u:`+action+`></s:Body></s:Envelope>`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("SOAPAction", `"`+service+`#`+action+`"`)
	request.Header.Set("Content-Type", "text/xml")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf("SOAP %s: status=%d err=%v body=%s", action, response.StatusCode, err, data)
	}
	var result struct {
		Body struct {
			Response soapResult `xml:",any"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(data, &result); err != nil {
		t.Fatalf("invalid SOAP XML: %v\n%s", err, data)
	}
	return result.Body.Response
}

func browseArgs(id, flag, filter, start, count, sort string) string {
	return tag("ObjectID", id) + tag("BrowseFlag", flag) + tag("Filter", filter) + tag("StartingIndex", start) + tag("RequestedCount", count) + tag("SortCriteria", sort)
}

func fetch(t *testing.T, method, address, byteRange string, status int) ([]byte, http.Header) {
	t.Helper()
	request, err := http.NewRequest(method, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	if byteRange != "" {
		request.Header.Set("Range", byteRange)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf("%s %s: status=%d err=%v body=%s", method, address, response.StatusCode, err, body)
	}
	return body, response.Header
}

func TestClientBrowseHierarchyPlaybackAndSubtitles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "Season & 2", "片段 #2 & <test>.mp4")
	writeFile(t, file, "0123456789")
	writeFile(t, filepath.Join(filepath.Dir(file), "片段 #2 & <test>.srt"), "1\n00:00:00,000 --> 00:00:01,000\nCaption")
	writeFile(t, filepath.Join(dir, "private.txt"), "not shared")
	s, base := fixture(t, []library.Saved{
		{ID: "folder", Name: "Movies & <TV>", Folder: dir, ShareDLNA: true},
		{ID: "collection", Name: "Favorites", Paths: []string{file, filepath.Join(dir, "missing.mp4")}, ShareDLNA: true},
		{ID: "remote", Name: "Other DLNA", DeviceID: "remote", Location: "http://127.0.0.1/remote"},
	})
	identity := fmt.Sprintf("dlna-%x", sha256.Sum256([]byte(s.udn)))
	device, err := dlna.Connect(context.Background(), identity, base+"/device.xml")
	if err != nil || device.Name != s.name {
		t.Fatalf("client cannot read server description: %+v %v", device, err)
	}
	roots, err := device.Browse(context.Background(), "0")
	if err != nil || len(roots) != 2 {
		t.Fatalf("local libraries not published: %+v %v", roots, err)
	}
	folders, err := device.Browse(context.Background(), objectID("folder"))
	if err != nil || len(folders) != 1 || !folders[0].Folder || folders[0].Name != "Season & 2" {
		t.Fatalf("directory structure lost: %+v %v", folders, err)
	}
	items, err := device.Browse(context.Background(), folders[0].ID)
	if err != nil || len(items) != 1 || items[0].Size != 10 || items[0].Subtitle == "" || !strings.HasPrefix(items[0].URL, base+"/media/") {
		t.Fatalf("invalid media metadata: %+v %v", items, err)
	}
	body, headers := fetch(t, "GET", items[0].URL, "bytes=2-5", 206)
	if string(body) != "2345" || headers.Get("Content-Range") != "bytes 2-5/10" || headers.Get("Content-Type") != "video/mp4" || headers.Get("CaptionInfo.sec") != items[0].Subtitle || headers.Get("contentFeatures.dlna.org") == "" {
		t.Fatalf("range or DLNA headers incorrect: %q %v", body, headers)
	}
	body, headers = fetch(t, "HEAD", items[0].URL, "", 200)
	if len(body) != 0 || headers.Get("Content-Length") != "10" {
		t.Fatalf("HEAD sent video bytes or wrong length: %q %v", body, headers)
	}
	fetch(t, "GET", items[0].URL, "bytes=100-200", 416)
	fetch(t, "POST", items[0].URL, "", 405)
	body, _ = fetch(t, "GET", items[0].Subtitle, "", 200)
	if !strings.Contains(string(body), "Caption") {
		t.Fatal("subtitle did not round trip")
	}
	collection, err := device.Browse(context.Background(), objectID("collection"))
	if err != nil || len(collection) != 1 || collection[0].Name != items[0].Name {
		t.Fatalf("missing collection members should be omitted: %+v %v", collection, err)
	}
}

func TestBrowsePaginationMetadataFilterAndFaults(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for _, name := range []string{"Episode 10.mp4", "Episode 2.mp4", "Episode 1.mp4"} {
		path := filepath.Join(dir, name)
		writeFile(t, path, name)
		paths = append(paths, path)
	}
	_, base := fixture(t, []library.Saved{{ID: "shows", Name: "Shows", Paths: paths, ShareDLNA: true}})
	result := soap(t, base, contentType, "Browse", browseArgs(objectID("shows"), "BrowseDirectChildren", "*", "1", "1", "+dc:title"), 200)
	if result.TotalMatches != 3 || result.NumberReturned != 1 || !strings.Contains(result.Result, "Episode 2.mp4") || strings.Contains(result.Result, dir) {
		t.Fatalf("pagination, ordering or private paths incorrect: %+v", result)
	}
	result = soap(t, base, contentType, "Browse", browseArgs(objectID("shows"), "BrowseDirectChildren", "*", "0", "0", "-dc:title"), 200)
	if result.NumberReturned != 3 || strings.Index(result.Result, "Episode 10") > strings.Index(result.Result, "Episode 2") {
		t.Fatalf("unbounded descending page incorrect: %+v", result)
	}
	itemID := objectID("shows", "file", paths[0])
	result = soap(t, base, contentType, "Browse", browseArgs(itemID, "BrowseMetadata", "", "0", "0", ""), 200)
	if result.NumberReturned != 1 || result.TotalMatches != 1 || !strings.Contains(result.Result, "dc:title") || strings.Contains(result.Result, "<res") || strings.Contains(result.Result, "dc:date") {
		t.Fatalf("metadata must honor empty filter: %+v", result)
	}
	for _, test := range []struct {
		id, flag, start, sort string
		code                  int
	}{
		{"unknown", "BrowseDirectChildren", "0", "", 701},
		{itemID, "BrowseDirectChildren", "0", "", 710},
		{"0", "invalid", "0", "", 402}, {"0", "BrowseMetadata", "1", "", 402},
		{"0", "BrowseDirectChildren", "-1", "", 402}, {"0", "BrowseDirectChildren", "0", "+res@size", 709},
	} {
		got := soap(t, base, contentType, "Browse", browseArgs(test.id, test.flag, "*", test.start, "20", test.sort), 500)
		if got.ErrorCode != test.code {
			t.Fatalf("fault %+v: %+v", test, got)
		}
	}
	if got := soap(t, base, contentType, "Browse", "<ObjectID>0</ObjectID>", 500); got.ErrorCode != 402 {
		t.Fatalf("missing arguments accepted: %+v", got)
	}
	if got := soap(t, base, contentType, "DeleteObject", "", 500); got.ErrorCode != 401 {
		t.Fatal("mutating or unknown actions must fail")
	}
	if got := soap(t, base, connectionType, "GetProtocolInfo", "", 200); !strings.Contains(got.Source, "video/mp4") || got.Sink != "" {
		t.Fatalf("invalid ConnectionManager: %+v", got)
	}
	for _, path := range []string{"/content/scpd.xml", "/connection/scpd.xml"} {
		data, _ := fetch(t, "GET", base+path, "", 200)
		var description struct {
			Actions []struct{ Name string } `xml:"actionList>action"`
		}
		if err := xml.Unmarshal(data, &description); err != nil || len(description.Actions) < 3 {
			t.Fatalf("invalid service description: %v %s", err, data)
		}
	}
}

func awaitCatalog(t *testing.T, s *Server, match func(catalog) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.RLock()
		matched := match(s.objects)
		s.mu.RUnlock()
		if matched {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("catalog refresh did not complete")
}

func TestLiveLibraryChangesRevokeURLsAndRefresh(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "clip.mp4")
	writeFile(t, file, "video")
	source := library.Saved{ID: "folder", Name: "Movies", Folder: dir, ShareDLNA: true}
	s, base := fixture(t, []library.Saved{source})
	id := objectID(source.ID, "file", file)
	s.mu.RLock()
	item := s.objects[id]
	s.mu.RUnlock()
	address := resourceURL(base, "media", item)
	fetch(t, "GET", address, "", 200)
	s.SetSources(nil)
	fetch(t, "GET", address, "", 404)
	source.Name = "Renamed"
	s.SetSources([]library.Saved{source})
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	awaitCatalog(t, s, func(c catalog) bool { return c[id].File == resolved && c[objectID(source.ID)].Name == "Renamed" })
	fetch(t, "GET", address, "", 200)
	writeFile(t, filepath.Join(dir, "new.mkv"), "new video")
	s.Refresh()
	awaitCatalog(t, s, func(c catalog) bool { return len(c[objectID(source.ID)].Children) == 2 })
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	s.Refresh()
	awaitCatalog(t, s, func(c catalog) bool { _, exists := c[id]; return !exists })
	fetch(t, "GET", address, "", 404)
}

func TestCatalogCannotServeUnsharedFilesOrEscapingSymlinks(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	file := filepath.Join(dir, "clip.mp4")
	secret := filepath.Join(outside, "secret.mp4")
	writeFile(t, file, "video")
	writeFile(t, secret, "private")
	if err := os.Symlink(secret, filepath.Join(dir, "escaped.mp4")); err != nil {
		t.Skip(err)
	}
	s, base := fixture(t, []library.Saved{{ID: "folder", Name: "Movies", Folder: dir, ShareDLNA: true}})
	s.mu.RLock()
	items := s.objects
	s.mu.RUnlock()
	if len(items[objectID("folder")].Children) != 1 {
		t.Fatal("symlink escaped the shared folder")
	}
	id := objectID("folder", "file", file)
	address := resourceURL(base, "media", items[id])
	fetch(t, "GET", base+"/media/unknown/secret.mp4", "", 404)
	fetch(t, "GET", base+"/media/"+id+"/secret.mp4", "", 404)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, file); err != nil {
		t.Fatal(err)
	}
	fetch(t, "GET", address, "", 404)
}

func TestSSDPRequestsAndServerClose(t *testing.T) {
	targets := []string{"upnp:rootdevice", "uuid:test", mediaServerType, contentType, connectionType}
	for _, test := range []struct {
		method, man, target, mx string
		count                   int
	}{
		{"M-SEARCH", `"ssdp:discover"`, "ssdp:all", "1", 5},
		{"M-SEARCH", `"ssdp:discover"`, mediaServerType, "30", 1},
		{"NOTIFY", `"ssdp:discover"`, mediaServerType, "1", 0},
		{"M-SEARCH", "invalid", mediaServerType, "1", 0},
		{"M-SEARCH", `"ssdp:discover"`, mediaServerType, "0", 0},
		{"M-SEARCH", `"ssdp:discover"`, "other", "1", 0},
	} {
		data := fmt.Sprintf("%s * HTTP/1.1\r\nMAN: %s\r\nST: %s\r\nMX: %s\r\n\r\n", test.method, test.man, test.target, test.mx)
		matched, delay := searchTargets([]byte(data), targets)
		if len(matched) != test.count || delay > 5*time.Second {
			t.Fatalf("SSDP %+v: %v %s", test, matched, delay)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{ctx: ctx, cancel: cancel, http: &http.Server{}, reload: make(chan struct{}, 1), events: make(chan struct{}, 1)}
	s.ssdp = &discovery{server: s, listener: listener}
	s.wg.Go(s.refreshLoop)
	s.wg.Go(s.eventLoop)
	s.Close()
	s.Close()
	select {
	case <-s.Done():
	default:
		t.Fatal("server did not cancel workers")
	}
}
