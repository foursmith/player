package dlnaserver

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSSDPCompatibilityAndBoundedResponseWindows(t *testing.T) {
	targets := []string{mediaServerType}
	for _, test := range []struct {
		name, line, host, mx string
		window               time.Duration
	}{
		{"standard multicast", "M-SEARCH * HTTP/1.1", multicast, "2", 2 * time.Second},
		{"trailing request whitespace", "M-SEARCH * HTTP/1.1 \t", multicast, "1", time.Second},
		{"unicast without MX", "M-SEARCH * HTTP/1.1", "192.0.2.1:1900", "", time.Second},
		{"legacy zero MX", "M-SEARCH * HTTP/1.1", multicast, "0", time.Second},
		{"bounded excessive MX", "M-SEARCH * HTTP/1.1", multicast, "999999", 5 * time.Second},
		{"negative MX", "M-SEARCH * HTTP/1.1", multicast, "-1", 0},
		{"malformed MX", "M-SEARCH * HTTP/1.1", multicast, "bad", 0},
		{"invalid HTTP version", "M-SEARCH * HTTP/nope", multicast, "1", 0},
		{"wrong method", "GET * HTTP/1.1", multicast, "1", 0},
		{"wrong target URI", "M-SEARCH / HTTP/1.1", multicast, "1", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			headers := "HOST: " + test.host + "\r\nMAN: \"ssdp:discover\"\r\nST: " + mediaServerType + "\r\n"
			if test.mx != "" {
				headers += "MX: " + test.mx + "\r\n"
			}
			matched, window := searchTargets([]byte(test.line+"\r\n"+headers+"\r\n"), targets)
			if window != test.window || (len(matched) == 1) != (test.window > 0) {
				t.Fatal("incorrect compatibility handling", matched, window)
			}
		})
	}
	packet := "M-SEARCH * HTTP/1.1\r\nMAN: \"ssdp:discover\"\r\nST: " + mediaServerType + "\r\nMX: 1\r\nBrokenHeader\r\n\r\n"
	if matched, _ := searchTargets([]byte(packet), targets); len(matched) != 0 {
		t.Fatal("malformed headers bypassed HTTP validation")
	}
	if matched, _ := searchTargets([]byte(strings.ReplaceAll(packet, "\r\n", "")), targets); len(matched) != 0 {
		t.Fatal("truncated request was accepted")
	}
}

func TestSSDPResponderUsesReceivingInterfaceAfterAnnouncementDeadline(t *testing.T) {
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 && iface.Flags&net.FlagUp != 0 {
			index = iface.Index
			break
		}
	}
	if index == 0 {
		t.Skip("no loopback interface")
	}
	listen := func() *net.UDPConn {
		t.Helper()
		socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { socket.Close() })
		return socket
	}
	listener, sender, peer := listen(), listen(), listen()
	// An earlier announcement leaves this deadline on the shared UDP socket.
	// Later searches must still receive replies, on the receiving interface.
	if err := sender.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	packet, err := newDiscoveryPacket(listener)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{ctx: ctx, cancel: cancel, udn: "uuid:responder-test", port: 12345}
	d := &discovery{server: s, listener: listener, packet: packet, links: []networkLink{{ip: net.IPv4(127, 0, 0, 1), index: index, socket: sender}}}
	d.wg.Go(d.listen)
	t.Cleanup(func() { cancel(); listener.Close(); d.wg.Wait() })
	request := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: ssdp:all\r\n\r\n"
	if _, err := peer.WriteToUDP([]byte(request), listener.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	seen := map[string]bool{}
	buffer := make([]byte, 8192)
	for range d.targets() {
		n, _, err := peer.ReadFromUDP(buffer)
		if err != nil {
			t.Fatal("SSDP responder did not answer", err)
		}
		response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(buffer[:n])), nil)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		target := response.Header.Get("ST")
		if response.StatusCode != 200 || response.Header.Get("LOCATION") != "http://127.0.0.1:12345/device.xml" || response.Header.Get("USN") != d.usn(target) {
			t.Fatalf("invalid discovery response: %+v", response)
		}
		seen[target] = true
	}
	if len(seen) != 5 {
		t.Fatal("some service types were not discoverable", seen)
	}
}
