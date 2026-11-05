package dlnaserver

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
)

const (
	mediaServerType = "urn:schemas-upnp-org:device:MediaServer:1"
	contentType     = "urn:schemas-upnp-org:service:ContentDirectory:1"
	connectionType  = "urn:schemas-upnp-org:service:ConnectionManager:1"
	multicast       = "239.255.255.250:1900"
	serverHeader    = runtime.GOOS + "/1.0 UPnP/1.0 Foursmith/1.0"
)

type networkLink struct {
	ip     net.IP
	index  int
	socket *net.UDPConn
}

func (link networkLink) send(data []byte, remote net.Addr) error {
	// UDP deadlines apply to future writes too. Renew it for every packet,
	// including search replies sent long after the last alive announcement.
	if err := link.socket.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	_, err := link.socket.WriteTo(data, remote)
	return err
}

type discovery struct {
	server   *Server
	listener *net.UDPConn
	packet   *ipv4.PacketConn
	links    []networkLink
	wg       sync.WaitGroup
}

func startDiscovery(s *Server) (*discovery, error) {
	group, _ := net.ResolveUDPAddr("udp4", multicast)
	d := &discovery{server: s}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		addresses, _ := iface.Addrs()
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
			socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip.To4()})
			if err != nil {
				continue
			}
			packet := ipv4.NewPacketConn(socket)
			if err = packet.SetMulticastInterface(&iface); err == nil {
				err = packet.SetMulticastTTL(2)
			}
			if err != nil {
				socket.Close()
				continue
			}
			if d.listener == nil {
				d.listener, err = net.ListenMulticastUDP("udp4", &iface, group)
				if err == nil {
					d.packet = ipv4.NewPacketConn(d.listener)
					err = d.packet.SetControlMessage(ipv4.FlagInterface, true)
					if err != nil {
						d.listener.Close()
						d.listener = nil
					}
				}
			} else {
				err = d.packet.JoinGroup(&iface, group)
			}
			if err != nil {
				log.Printf("[dlna-server] SSDP interface=%s unavailable: %v", iface.Name, err)
				socket.Close()
				continue
			}
			d.links = append(d.links, networkLink{ip: ip.To4(), index: iface.Index, socket: socket})
			break
		}
	}
	if len(d.links) == 0 {
		return nil, fmt.Errorf("no active IPv4 multicast network interface")
	}
	return d, nil
}

func (d *discovery) targets() []string {
	return []string{"upnp:rootdevice", d.server.udn, mediaServerType, contentType, connectionType}
}

func (d *discovery) usn(target string) string {
	if target == d.server.udn {
		return target
	}
	return d.server.udn + "::" + target
}

func (d *discovery) run() {
	d.wg.Go(func() {
		ticker := time.NewTicker(3 * time.Minute)
		defer ticker.Stop()
		d.announce("ssdp:alive")
		for {
			select {
			case <-d.server.ctx.Done():
				return
			case <-ticker.C:
				d.announce("ssdp:alive")
			}
		}
	})
	d.wg.Go(d.listen)
}

func searchTargets(data []byte, targets []string) ([]string, time.Duration) {
	line, headers, ok := bytes.Cut(data, []byte("\n"))
	if !ok {
		return nil, 0
	}
	// Some UPnP clients append whitespace to the request line. Normalize only
	// that line, retaining net/http's header and request validation.
	input := io.MultiReader(strings.NewReader(strings.TrimSpace(string(line))+"\r\n"), bytes.NewReader(headers))
	request, err := http.ReadRequest(bufio.NewReader(input))
	if err != nil {
		return nil, 0
	}
	request.Body.Close()
	if request.Method != "M-SEARCH" || request.RequestURI != "*" || !strings.EqualFold(request.Header.Get("MAN"), `"ssdp:discover"`) {
		return nil, 0
	}
	// Like dms, give zero-MX clients a bounded response window. Unicast
	// searches also commonly omit MX entirely.
	mx := 1
	if value := strings.TrimSpace(request.Header.Get("MX")); value != "" {
		mx, err = strconv.Atoi(value)
		if err != nil || mx < 0 {
			return nil, 0
		}
	}
	var matched []string
	for _, target := range targets {
		if request.Header.Get("ST") == "ssdp:all" || request.Header.Get("ST") == target {
			matched = append(matched, target)
		}
	}
	return matched, time.Duration(max(1, min(mx, 5))) * time.Second
}

func (d *discovery) listen() {
	buffer := make([]byte, 8192)
	// Bound pending replies even when another device floods discovery packets.
	jobs := make(chan struct{}, 32)
	for {
		n, control, remote, err := d.packet.ReadFrom(buffer)
		if err != nil {
			if d.server.ctx.Err() == nil {
				log.Printf("[dlna-server] SSDP stopped: %v", err)
				d.server.cancel()
			}
			return
		}
		targets, delay := searchTargets(buffer[:n], d.targets())
		if len(targets) == 0 || control == nil {
			continue
		}
		for _, link := range d.links {
			if link.index != control.IfIndex {
				continue
			}
			select {
			case jobs <- struct{}{}:
			default:
				continue
			}
			d.wg.Go(func() {
				defer func() { <-jobs }()
				timer := time.NewTimer(time.Duration(rand.Int64N(int64(delay))))
				defer timer.Stop()
				select {
				case <-d.server.ctx.Done():
					return
				case <-timer.C:
				}
				for _, target := range targets {
					response := fmt.Sprintf("HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=600\r\nDATE: %s\r\nEXT:\r\nLOCATION: %s/device.xml\r\nSERVER: %s\r\nST: %s\r\nUSN: %s\r\n\r\n", time.Now().UTC().Format(http.TimeFormat), d.server.baseURL(link.ip), serverHeader, target, d.usn(target))
					if err := link.send([]byte(response), remote); err != nil && d.server.ctx.Err() == nil {
						log.Printf("[dlna-server] SSDP reply interface=%d peer=%s failed: %v", link.index, remote, err)
					}
				}
			})
		}
	}
}

func (d *discovery) announce(kind string) {
	group, _ := net.ResolveUDPAddr("udp4", multicast)
	for _, link := range d.links {
		for _, target := range d.targets() {
			message := fmt.Sprintf("NOTIFY * HTTP/1.1\r\nHOST: %s\r\nNT: %s\r\nNTS: %s\r\nUSN: %s\r\n", multicast, target, kind, d.usn(target))
			if kind == "ssdp:alive" {
				message += fmt.Sprintf("CACHE-CONTROL: max-age=600\r\nLOCATION: %s/device.xml\r\nSERVER: %s\r\n", d.server.baseURL(link.ip), serverHeader)
			}
			if err := link.send([]byte(message+"\r\n"), group); err != nil && kind == "ssdp:alive" {
				log.Printf("[dlna-server] SSDP announce interface=%d failed: %v", link.index, err)
			}
		}
	}
}

func (d *discovery) close() {
	d.listener.Close()
	d.wg.Wait()
	d.announce("ssdp:byebye")
	for _, link := range d.links {
		link.socket.Close()
	}
}
