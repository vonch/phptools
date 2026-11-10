//go:build windows

package services

import (
	"encoding/binary"
	"net"
	"net/netip"
	"os"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsNginxTCPTableDecodesIPv4IPv6Owners(t *testing.T) {
	v4 := make([]byte, 4+24)
	binary.LittleEndian.PutUint32(v4[:4], 1)
	row := v4[4:]
	binary.LittleEndian.PutUint32(row[:4], 2)
	copy(row[4:8], []byte{127, 0, 0, 1})
	binary.BigEndian.PutUint16(row[8:10], 8081)
	binary.LittleEndian.PutUint32(row[20:24], 1234)
	v6 := make([]byte, 4+56)
	binary.LittleEndian.PutUint32(v6[:4], 1)
	row = v6[4:]
	address := netip.MustParseAddr("::").As16()
	copy(row[:16], address[:])
	binary.BigEndian.PutUint16(row[20:22], 80)
	binary.LittleEndian.PutUint32(row[48:52], 2)
	binary.LittleEndian.PutUint32(row[52:56], 5678)
	for _, tc := range []struct {
		family   uint32
		data     []byte
		expected nginxListener
	}{
		{windows.AF_INET, v4, nginxListener{nginxListen: nginxListen{address: netip.MustParseAddr("127.0.0.1"), port: 8081}, pid: 1234}},
		{windows.AF_INET6, v6, nginxListener{nginxListen: nginxListen{address: netip.MustParseAddr("::"), port: 80}, pid: 5678}},
	} {
		got, err := parseNginxTCPTable(tc.data, tc.family)
		if err != nil || !reflect.DeepEqual(got, []nginxListener{tc.expected}) {
			t.Fatalf("family %d: %+v %v", tc.family, got, err)
		}
		for _, malformed := range [][]byte{nil, tc.data[:3], tc.data[:len(tc.data)-1]} {
			if _, err := parseNginxTCPTable(malformed, tc.family); err == nil {
				t.Fatalf("accepted truncated family %d table", tc.family)
			}
		}
	}
}

func TestWindowsNginxTCPListenersFindRealLocalSocketOwner(t *testing.T) {
	for _, tc := range []struct{ network, address string }{
		{"tcp4", "127.0.0.1:0"},
		{"tcp6", "[::1]:0"},
	} {
		t.Run(tc.network, func(t *testing.T) {
			listener, err := net.Listen(tc.network, tc.address)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			endpoint := listener.Addr().(*net.TCPAddr).AddrPort()
			rows, err := nginxTCPListeners()
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.address == endpoint.Addr() && row.port == endpoint.Port() && row.pid == os.Getpid() {
					requested := nginxListen{address: endpoint.Addr(), port: endpoint.Port()}
					if err := checkNginxListeners([]nginxListen{requested}, rows, func(int) bool { return false }); err == nil {
						t.Fatal("real external listener accepted")
					}
					return
				}
			}
			t.Fatalf("socket %s PID %d absent from Windows TCP owner table", endpoint, os.Getpid())
		})
	}
}
