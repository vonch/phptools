//go:build windows

package services

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
	"phptools/internal/model"
)

var getExtendedTCPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

func nginxTCPTable(family uint32) ([]byte, error) {
	const ownerPIDListeners = 3
	var size uint32
	code, _, _ := getExtendedTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), ownerPIDListeners, 0)
	if code != 0 && code != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
		return nil, windows.Errno(code)
	}
	for attempt := 0; attempt < 5; attempt++ {
		if size < 4 || size > 64<<20 {
			return nil, errors.New("Windows TCP 监听表大小无效")
		}
		data := make([]byte, size)
		code, _, _ = getExtendedTCPTable.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(family), ownerPIDListeners, 0)
		if code == 0 {
			if size > uint32(len(data)) {
				return nil, errors.New("Windows TCP 监听表返回大小无效")
			}
			return data[:size], nil
		}
		if code != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
			return nil, windows.Errno(code)
		}
	}
	return nil, errors.New("Windows TCP 监听表持续变化，请重试")
}

func parseNginxTCPTable(data []byte, family uint32) ([]nginxListener, error) {
	rowSize := 24
	if family == windows.AF_INET6 {
		rowSize = 56
	} else if family != windows.AF_INET {
		return nil, errors.New("未知 Windows TCP 地址族")
	}
	if len(data) < 4 {
		return nil, errors.New("Windows TCP 监听表不完整")
	}
	count := binary.LittleEndian.Uint32(data[:4])
	if uint64(count)*uint64(rowSize) > uint64(len(data)-4) {
		return nil, errors.New("Windows TCP 监听表条目不完整")
	}
	listeners := make([]nginxListener, 0, count)
	for offset := 4; len(listeners) < int(count); offset += rowSize {
		row := data[offset : offset+rowSize]
		var address netip.Addr
		var port uint16
		var pid, state uint32
		if family == windows.AF_INET {
			state = binary.LittleEndian.Uint32(row[:4])
			address = netip.AddrFrom4([4]byte(row[4:8]))
			port = binary.BigEndian.Uint16(row[8:10])
			pid = binary.LittleEndian.Uint32(row[20:24])
		} else {
			address = netip.AddrFrom16([16]byte(row[:16])).Unmap()
			port = binary.BigEndian.Uint16(row[20:22])
			state = binary.LittleEndian.Uint32(row[48:52])
			pid = binary.LittleEndian.Uint32(row[52:56])
		}
		if state != 2 { // MIB_TCP_STATE_LISTEN
			return nil, fmt.Errorf("Windows TCP 监听表包含非监听状态 %d", state)
		}
		listeners = append(listeners, nginxListener{nginxListen: nginxListen{address: address, port: port}, pid: int(pid)})
	}
	return listeners, nil
}

func nginxTCPListeners() ([]nginxListener, error) {
	var listeners []nginxListener
	for _, family := range []uint32{windows.AF_INET, windows.AF_INET6} {
		data, err := nginxTCPTable(family)
		if err != nil {
			return nil, err
		}
		rows, err := parseNginxTCPTable(data, family)
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, rows...)
	}
	return listeners, nil
}

func nginxOwnedListenerProcesses(i model.Instance) (map[int]bool, error) {
	if i.State != "running" || i.PID <= 0 {
		return map[int]bool{}, nil
	}
	table, err := processTable()
	if err != nil {
		return nil, err
	}
	parents := map[int]int{}
	for pid, process := range table {
		parents[pid] = process.ParentID
	}
	return nginxOwnedProcessIDs(i, parents, processIdentity, verifyProcess)
}
