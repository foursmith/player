//go:build !windows

package dlnaserver

import (
	"net"

	"golang.org/x/net/ipv4"
)

func newDiscoveryPacket(conn *net.UDPConn) (discoveryPacket, error) {
	packet := ipv4.NewPacketConn(conn)
	if err := packet.SetControlMessage(ipv4.FlagInterface, true); err != nil {
		return nil, err
	}
	return packet, nil
}
