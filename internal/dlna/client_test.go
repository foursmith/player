package dlna

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDescribeEmbeddedMediaServerAndURLBase(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<root xmlns="urn:schemas-upnp-org:device-1-0"><URLBase>http://192.0.2.1:8200/base/</URLBase><device><friendlyName>Root</friendlyName><deviceList><device><friendlyName>My NAS</friendlyName><UDN>uuid:nas</UDN><serviceList><service><serviceType>urn:schemas-upnp-org:service:ContentDirectory:2</serviceType><controlURL>browse</controlURL></service></serviceList></device></deviceList></device></root>`)
	}))
	defer server.Close()
	d, err := describe(context.Background(), server.URL+"/device.xml")
	if err != nil || d.Name != "My NAS" || d.ControlURL != "http://192.0.2.1:8200/base/browse" || !strings.HasPrefix(d.ID, "dlna-") || !strings.HasSuffix(d.ServiceType, ":2") {
		t.Fatalf("device description: %+v, %v", d, err)
	}
	// Rediscovery through a new address must retain the device identity.
	other, err := describe(context.Background(), server.URL+"/new-location.xml")
	if err != nil || other.ID != d.ID {
		t.Fatalf("device identity changed: %+v, %v", other, err)
	}
}

func writeBrowse(w http.ResponseWriter, didl string, returned, total int) {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(didl))
	fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><Result>%s</Result><NumberReturned>%d</NumberReturned><TotalMatches>%d</TotalMatches></u:BrowseResponse></s:Body></s:Envelope>`, escaped.String(), returned, total)
}

func TestBrowsePaginationResourcesAndEscapedObjectID(t *testing.T) {
	var starts []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("SOAPAction") != `"urn:schemas-upnp-org:service:ContentDirectory:1#Browse"` {
			t.Errorf("wrong SOAP request: %s %v", r.Method, r.Header)
		}
		var request struct {
			ID    string `xml:"Body>Browse>ObjectID"`
			Start int    `xml:"Body>Browse>StartingIndex"`
			Flag  string `xml:"Body>Browse>BrowseFlag"`
		}
		if err := xml.NewDecoder(r.Body).Decode(&request); err != nil || request.ID != `folder<&"` || request.Flag != "BrowseDirectChildren" {
			t.Errorf("invalid request: %+v, %v", request, err)
		}
		starts = append(starts, request.Start)
		switch request.Start {
		case 0:
			writeBrowse(w, `<DIDL-Lite xmlns:dc="urn:title" xmlns:upnp="urn:upnp" xmlns:sec="urn:sec"><container id="folder"><dc:title>Season 2</dc:title></container><item id="video"><dc:title>Episode 2.mp4</dc:title><upnp:class>object.item.videoItem</upnp:class><res protocolInfo="http-get:*:image/jpeg:*">/cover.jpg</res><res protocolInfo="http-get:*:video/mp4:*">/movie.mp4?a=1&amp;b=2</res><sec:CaptionInfoEx>/movie.srt</sec:CaptionInfoEx></item></DIDL-Lite>`, 2, 0)
		case 2:
			writeBrowse(w, `<DIDL-Lite xmlns:dc="urn:title" xmlns:upnp="urn:upnp"><item id="audio"><dc:title>Music</dc:title><upnp:class>object.item.audioItem</upnp:class><res protocolInfo="http-get:*:audio/mpeg:*">/audio.mp3</res></item><item id="subtitle"><dc:title>Episode 2.srt</dc:title><res protocolInfo="http-get:*:text/plain:*">/episode.srt?token=1</res></item></DIDL-Lite>`, 2, 0)
		default:
			writeBrowse(w, `<DIDL-Lite/>`, 0, 0)
		}
	}))
	defer server.Close()
	d := Device{ControlURL: server.URL + "/control", ServiceType: "urn:schemas-upnp-org:service:ContentDirectory:1"}
	items, err := d.Browse(context.Background(), `folder<&"`)
	if err != nil || !reflect.DeepEqual(starts, []int{0, 2, 4}) || len(items) != 3 {
		t.Fatalf("browse: %+v, pages %v, %v", items, starts, err)
	}
	if !items[0].Folder || items[1].URL != server.URL+"/movie.mp4?a=1&b=2" || items[1].Subtitle != server.URL+"/movie.srt" || !items[2].SubtitleFile {
		t.Fatalf("resources or subtitle lost: %+v", items)
	}
}

func TestBrowseStopsAtTotalAndRejectsRepeatedPages(t *testing.T) {
	for _, total := range []int{1, 0} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				writeBrowse(w, `<DIDL-Lite><container id="one"><title>Folder</title></container></DIDL-Lite>`, 1, total)
			}))
			defer server.Close()
			d := Device{ControlURL: server.URL, ServiceType: "urn:schemas-upnp-org:service:ContentDirectory:1"}
			items, err := d.Browse(context.Background(), "0")
			if total == 1 && (err != nil || calls != 1 || len(items) != 1) {
				t.Fatalf("ignored TotalMatches: %v, %d calls, %v", items, calls, err)
			}
			if total == 0 && (err == nil || calls != 2) {
				t.Fatalf("repeated page didn't stop: %d calls, %v", calls, err)
			}
		})
	}
}

func TestDiscoveryDeduplicatesLocationsAndCancellation(t *testing.T) {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	served := make(chan struct{})
	go func() {
		defer close(served)
		buffer := make([]byte, 4096)
		n, peer, err := conn.ReadFrom(buffer)
		if err != nil {
			return
		}
		if !strings.Contains(string(buffer[:n]), "ST: "+searchTarget+"\r\n") {
			t.Error("wrong SSDP search target")
		}
		for _, response := range []string{
			"HTTP/1.1 200 OK\r\nLOCATION: http://127.0.0.1/device.xml\r\n\r\n",
			"HTTP/1.1 200 OK\r\nlocation: http://127.0.0.1/device.xml\r\n\r\n",
			"HTTP/1.1 200 OK\r\nLOCATION: file:///etc/passwd\r\n\r\n",
		} {
			_, _ = conn.WriteTo([]byte(response), peer)
		}
	}()
	locations, err := discoverLocations(context.Background(), conn.LocalAddr().String(), 100*time.Millisecond)
	<-served
	if err != nil || !reflect.DeepEqual(locations, []string{"http://127.0.0.1/device.xml"}) {
		t.Fatalf("SSDP responses: %v, %v", locations, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = discoverLocations(ctx, conn.LocalAddr().String(), time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("discovery ignored cancellation: %v", err)
	}
}

func TestBrowseCancelsBlockedHTTP(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		d := Device{ControlURL: server.URL, ServiceType: "urn:schemas-upnp-org:service:ContentDirectory:1"}
		_, err := d.Browse(ctx, "0")
		done <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("DLNA request did not cancel")
	}
}

func TestBrowseRejectsFaultInsteadOfTreatingItAsEmptyFolder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultstring>No such object</faultstring></s:Fault></s:Body></s:Envelope>`)
	}))
	defer server.Close()
	d := Device{ControlURL: server.URL, ServiceType: "urn:schemas-upnp-org:service:ContentDirectory:1"}
	if _, err := d.Browse(context.Background(), "missing"); err == nil {
		t.Fatal("SOAP fault was accepted as an empty folder")
	}
}
