// Package dlna discovers UPnP media servers and browses ContentDirectory directly.
package dlna

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
)

const searchTarget = "urn:schemas-upnp-org:device:MediaServer:1"

var httpClient = &http.Client{Timeout: 8 * time.Second}

type Device struct {
	ID, Name, Location, ControlURL, ServiceType string
}

// DeviceID keeps saved sources stable across address changes and is also used
// to exclude this application's own server from discovery results.
func DeviceID(identity string) string {
	return fmt.Sprintf("dlna-%x", sha256.Sum256([]byte(identity)))
}

type Item struct {
	ID, Name, URL, Subtitle string
	Folder, SubtitleFile    bool
	Duration                float64
	Size, Modified          int64
}

type descriptionDevice struct {
	Name     string              `xml:"friendlyName"`
	UDN      string              `xml:"UDN"`
	Services []service           `xml:"serviceList>service"`
	Devices  []descriptionDevice `xml:"deviceList>device"`
}

type service struct {
	Type    string `xml:"serviceType"`
	Control string `xml:"controlURL"`
}

// Discover only scans on request. Description requests run concurrently and
// share the caller's cancellation; an unreachable device cannot block browsing.
func Discover(ctx context.Context) ([]Device, error) {
	locations, err := discoverLocations(ctx, "239.255.255.250:1900", 2500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	devices := map[string]Device{}
	var failures []error
	for _, location := range locations {
		wg.Go(func() {
			device, err := describe(ctx, location)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, err)
			} else {
				devices[device.ID] = device
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []Device
	for _, device := range devices {
		out = append(out, device)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) == 0 && len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return out, nil
}

func discoverLocations(ctx context.Context, address string, duration time.Duration) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	remote, err := net.ResolveUDPAddr("udp4", address)
	if err != nil {
		return nil, err
	}
	links := []discoveryLink{{name: "default", ip: net.IPv4zero}}
	if remote.IP.IsMulticast() {
		links, err = discoveryLinks()
		if err != nil {
			return nil, err
		}
		if len(links) == 0 {
			return nil, errors.New("no active IPv4 multicast network interface")
		}
	}
	return collectLocations(ctx, links, func(link discoveryLink) ([]string, error) {
		locations, err := discoverOnLink(ctx, remote, duration, link)
		log.Printf("[dlna] discovery interface=%s locations=%d error=%v", link.name, len(locations), err)
		return locations, err
	})
}

type discoveryLink struct {
	name  string
	iface *net.Interface
	ip    net.IP
}

// Each LAN gets its own source address and multicast socket. A VPN's default
// multicast route must not capture requests intended for Wi-Fi or Ethernet.
func discoveryLinks() ([]discoveryLink, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var links []discoveryLink
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil && !ip.IsUnspecified() && !ip.IsLoopback() {
				links = append(links, discoveryLink{name: iface.Name, iface: &iface, ip: ip.To4()})
				break
			}
		}
	}
	return links, nil
}

func collectLocations(ctx context.Context, links []discoveryLink, search func(discoveryLink) ([]string, error)) ([]string, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[string]bool{}
	var failures []error
	for _, link := range links {
		wg.Go(func() {
			locations, err := search(link)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", link.name, err))
			}
			for _, location := range locations {
				seen[location] = true
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var locations []string
	for location := range seen {
		locations = append(locations, location)
	}
	sort.Strings(locations)
	if len(locations) == 0 && len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return locations, nil
}

func discoverOnLink(ctx context.Context, remote *net.UDPAddr, duration time.Duration, link discoveryLink) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := net.ListenPacket("udp4", net.JoinHostPort(link.ip.String(), "0"))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if remote.IP.IsMulticast() && link.iface != nil {
		packet := ipv4.NewPacketConn(conn)
		if err := packet.SetMulticastInterface(link.iface); err != nil {
			return nil, err
		}
		if err := packet.SetMulticastTTL(2); err != nil {
			return nil, err
		}
	}
	request := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: " + searchTarget + "\r\n\r\n"
	if _, err = conn.WriteTo([]byte(request), remote); err != nil {
		return nil, err
	}
	// UDP search packets can be lost; repeat once within the discovery window.
	retry := time.AfterFunc(350*time.Millisecond, func() { _, _ = conn.WriteTo([]byte(request), remote) })
	defer retry.Stop()
	if err = conn.SetReadDeadline(time.Now().Add(duration)); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var locations []string
	buffer := make([]byte, 64<<10)
	for {
		n, _, err := conn.ReadFrom(buffer)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				return locations, nil
			}
			return nil, err
		}
		response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(buffer[:n])), nil)
		if err != nil {
			continue
		}
		response.Body.Close()
		location := response.Header.Get("Location")
		if response.StatusCode == http.StatusOK && !seen[location] && validURL(location) {
			seen[location] = true
			locations = append(locations, location)
		}
	}
}

func validURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

func resolveURL(base, reference string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	r, err := url.Parse(strings.TrimSpace(reference))
	if err != nil || strings.TrimSpace(reference) == "" {
		return ""
	}
	resolved := u.ResolveReference(r).String()
	if !validURL(resolved) {
		return ""
	}
	return resolved
}

func readResponse(ctx context.Context, method, address, body, soapAction string) ([]byte, error) {
	if !validURL(address) {
		return nil, fmt.Errorf("invalid DLNA URL")
	}
	req, err := http.NewRequestWithContext(ctx, method, address, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if soapAction != "" {
		req.Header.Set("Content-Type", "text/xml; charset=\"utf-8\"")
		req.Header.Set("SOAPAction", "\""+soapAction+"\"")
	}
	response, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("DLNA HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("DLNA response is too large")
	}
	return data, err
}

func describe(ctx context.Context, location string) (Device, error) {
	data, err := readResponse(ctx, http.MethodGet, location, "", "")
	if err != nil {
		return Device{}, err
	}
	var root struct {
		Base   string            `xml:"URLBase"`
		Device descriptionDevice `xml:"device"`
	}
	if err = xml.Unmarshal(data, &root); err != nil {
		return Device{}, err
	}
	base := location
	if validURL(strings.TrimSpace(root.Base)) {
		base = strings.TrimSpace(root.Base)
	}
	var find func(descriptionDevice) (Device, bool)
	find = func(d descriptionDevice) (Device, bool) {
		for _, s := range d.Services {
			if strings.HasPrefix(s.Type, "urn:schemas-upnp-org:service:ContentDirectory:") {
				control := resolveURL(base, s.Control)
				if control == "" {
					continue
				}
				identity := d.UDN
				if identity == "" {
					identity = location
				}
				name := d.Name
				if name == "" {
					name = "DLNA Media Server"
				}
				return Device{ID: DeviceID(identity), Name: name, Location: location, ControlURL: control, ServiceType: s.Type}, true
			}
		}
		for _, child := range d.Devices {
			if device, ok := find(child); ok {
				return device, true
			}
		}
		return Device{}, false
	}
	if device, ok := find(root.Device); ok {
		return device, nil
	}
	return Device{}, fmt.Errorf("DLNA ContentDirectory service not found")
}

type didlObject struct {
	ID        string `xml:"id,attr"`
	Name      string `xml:"title"`
	Class     string `xml:"class"`
	Caption   string `xml:"CaptionInfoEx"`
	Date      string `xml:"date"`
	Resources []struct {
		Protocol string `xml:"protocolInfo,attr"`
		URL      string `xml:",chardata"`
		Duration string `xml:"duration,attr"`
		Size     int64  `xml:"size,attr"`
	} `xml:"res"`
}

// Browse follows server pagination, including servers that cannot report a
// TotalMatches count. IDs are escaped as XML, never interpolated into markup.
func (d Device) Browse(ctx context.Context, objectID string) ([]Item, error) {
	var out []Item
	seen := map[string]bool{}
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(objectID)); err != nil {
		return nil, err
	}
	for start := 0; start < 100000; {
		body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:Browse xmlns:u="%s"><ObjectID>%s</ObjectID><BrowseFlag>BrowseDirectChildren</BrowseFlag><Filter>*</Filter><StartingIndex>%d</StartingIndex><RequestedCount>200</RequestedCount><SortCriteria></SortCriteria></u:Browse></s:Body></s:Envelope>`, d.ServiceType, escaped.String(), start)
		data, err := readResponse(ctx, http.MethodPost, d.ControlURL, body, d.ServiceType+"#Browse")
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Response *struct {
				Result   string `xml:"Result"`
				Returned int    `xml:"NumberReturned"`
				Total    int    `xml:"TotalMatches"`
			} `xml:"Body>BrowseResponse"`
		}
		if err = xml.Unmarshal(data, &envelope); err != nil {
			return nil, err
		}
		response := envelope.Response
		if response == nil {
			return nil, fmt.Errorf("invalid DLNA Browse response")
		}
		var didl struct {
			Folders []didlObject `xml:"container"`
			Items   []didlObject `xml:"item"`
		}
		if response.Result == "" && response.Returned == 0 {
			return out, nil
		}
		if response.Result == "" {
			return nil, fmt.Errorf("DLNA Browse response is missing Result")
		}
		if err = xml.Unmarshal([]byte(response.Result), &didl); err != nil {
			return nil, err
		}
		before := len(seen)
		for _, group := range []struct {
			objects []didlObject
			folder  bool
		}{{didl.Folders, true}, {didl.Items, false}} {
			for _, object := range group.objects {
				if seen[object.ID] {
					continue
				}
				seen[object.ID] = true
				item := Item{ID: object.ID, Name: object.Name, Folder: group.folder, Subtitle: resolveURL(d.ControlURL, object.Caption)}
				if at, err := time.Parse(time.RFC3339, object.Date); err == nil {
					item.Modified = at.UnixMilli()
				}
				if item.Folder {
					out = append(out, item)
					continue
				}
				// Prefer a declared video resource over cover art or thumbnails.
				for _, resource := range object.Resources {
					parts := strings.SplitN(resource.Protocol, ":", 4)
					if len(parts) != 4 || parts[0] != "http-get" {
						continue
					}
					address := resolveURL(d.ControlURL, resource.URL)
					if address == "" {
						continue
					}
					u, _ := url.Parse(address)
					switch strings.ToLower(path.Ext(u.Path)) {
					case ".srt", ".vtt", ".ass", ".ssa":
						item.URL, item.SubtitleFile = address, true
					}
					if strings.HasPrefix(parts[2], "video/") {
						item.URL, item.SubtitleFile = address, false
						item.Duration, item.Size = parseDuration(resource.Duration), max(0, resource.Size)
						break
					}
					if item.URL == "" && strings.Contains(object.Class, "videoItem") && !strings.HasPrefix(parts[2], "image/") && !strings.HasPrefix(parts[2], "audio/") {
						item.URL = address
						item.Duration, item.Size = parseDuration(resource.Duration), max(0, resource.Size)
					}
				}
				if item.URL != "" {
					out = append(out, item)
				}
			}
		}
		if response.Returned <= 0 || (response.Total > 0 && start+response.Returned >= response.Total) {
			return out, nil
		}
		if len(seen) == before {
			return nil, fmt.Errorf("DLNA server repeated a page without advancing")
		}
		start += response.Returned
	}
	return nil, fmt.Errorf("DLNA directory exceeds 100000 entries")
}

func parseDuration(value string) float64 {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0
	}
	hours, hErr := strconv.ParseUint(parts[0], 10, 32)
	minutes, mErr := strconv.ParseUint(parts[1], 10, 8)
	seconds, sErr := strconv.ParseFloat(parts[2], 64)
	if hErr != nil || mErr != nil || sErr != nil || minutes >= 60 || !(seconds >= 0 && seconds < 60) {
		return 0
	}
	return float64(hours)*3600 + float64(minutes)*60 + seconds
}

// Connect refreshes the description at a known location, rediscovering the
// same device identity if its address changed. It never substitutes another NAS.
func Connect(ctx context.Context, id, location string) (Device, error) {
	return connect(ctx, id, location, Discover)
}

func connect(ctx context.Context, id, location string, discover func(context.Context) ([]Device, error)) (Device, error) {
	if device, err := describe(ctx, location); err == nil && device.ID == id {
		return device, nil
	}
	if err := ctx.Err(); err != nil {
		return Device{}, err
	}
	devices, err := discover(ctx)
	if err != nil {
		return Device{}, err
	}
	for _, device := range devices {
		if device.ID == id {
			return device, nil
		}
	}
	return Device{}, fmt.Errorf("DLNA device is offline or unavailable")
}

func ReadSubtitle(ctx context.Context, address string) ([]byte, error) {
	return readResponse(ctx, http.MethodGet, address, "", "")
}
