package dlnaserver

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/ipv4"
)

func TestSSDPResponderUsesReceivingInterfaceAddress(t *testing.T) {
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
	packet := ipv4.NewPacketConn(listener)
	if err := packet.SetControlMessage(ipv4.FlagInterface, true); err != nil {
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
