package dlnaserver

import (
	"errors"
	"net"
	"unsafe"

	"golang.org/x/net/ipv4"
	"golang.org/x/sys/windows"
)

// x/net/ipv4 does not implement interface control messages on Windows. Winsock
// exposes the receiving interface through IP_PKTINFO and WSARecvMsg, which
// net.UDPConn.ReadMsgUDP integrates with Go's network poller and cancellation.
type windowsDiscoveryPacket struct {
	*ipv4.PacketConn
	conn *net.UDPConn
	oob  [128]byte
}

func newDiscoveryPacket(conn *net.UDPConn) (discoveryPacket, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}
	var optionErr error
	if err := raw.Control(func(fd uintptr) {
		optionErr = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, windows.IP_PKTINFO, 1)
	}); err != nil {
		return nil, err
	}
	if optionErr != nil {
		return nil, optionErr
	}
	return &windowsDiscoveryPacket{PacketConn: ipv4.NewPacketConn(conn), conn: conn}, nil
}

func (p *windowsDiscoveryPacket) ReadFrom(data []byte) (int, *ipv4.ControlMessage, net.Addr, error) {
	n, nn, flags, remote, err := p.conn.ReadMsgUDP(data, p.oob[:])
	if err != nil {
		return n, nil, remote, err
	}
	if flags&windows.MSG_CTRUNC != 0 {
		return n, nil, remote, errors.New("truncated SSDP interface information")
	}
	const headerSize = int(unsafe.Sizeof(windows.WSACMSGHDR{}))
	const alignment = int(unsafe.Sizeof(uintptr(0)))
	for offset := 0; offset+headerSize <= nn; {
		header := (*windows.WSACMSGHDR)(unsafe.Pointer(&p.oob[offset]))
		length := int(header.Len)
		if length < headerSize || length > nn-offset {
			return n, nil, remote, errors.New("invalid SSDP interface information")
		}
		if header.Level == windows.IPPROTO_IP && header.Type == windows.IP_PKTINFO && length >= headerSize+int(unsafe.Sizeof(windows.IN_PKTINFO{})) {
			info := (*windows.IN_PKTINFO)(unsafe.Pointer(&p.oob[offset+headerSize]))
			return n, &ipv4.ControlMessage{IfIndex: int(info.Ifindex), Dst: net.IPv4(info.Addr[0], info.Addr[1], info.Addr[2], info.Addr[3])}, remote, nil
		}
		offset += (length + alignment - 1) &^ (alignment - 1)
	}
	return n, nil, remote, nil
}
