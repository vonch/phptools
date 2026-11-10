package services

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"phptools/internal/model"
)

type processTableEntry struct {
	ParentID int
	Name     string
}

// Shared by Nginx ownership checks and the desktop memory reader.
func processTable() (map[int]processTableEntry, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	result := map[int]processTableEntry{}
	err = windows.Process32First(snapshot, &entry)
	for err == nil {
		result[int(entry.ProcessID)] = processTableEntry{
			ParentID: int(entry.ParentProcessID), Name: windows.UTF16ToString(entry.ExeFile[:]),
		}
		err = windows.Process32Next(snapshot, &entry)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return result, nil
}

// PROCESS_MEMORY_COUNTERS_EX, with SIZE_T fields matching the native pointer size.
type processMemoryCounters struct {
	Size, PageFaultCount                               uint32
	PeakWorkingSetSize, WorkingSetSize                 uintptr
	QuotaPeakPagedPoolUsage, QuotaPagedPoolUsage       uintptr
	QuotaPeakNonPagedPoolUsage, QuotaNonPagedPoolUsage uintptr
	PagefileUsage, PeakPagefileUsage, PrivateUsage     uintptr
}

type memoryProcess struct {
	Created                  uint64
	Path                     string
	WorkingSet, PrivateBytes uint64
}

var getProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

func queryMemoryProcess(pid int) (memoryProcess, error) {
	// Query-limited-information + VM-read are the documented rights for
	// GetProcessMemoryInfo. The handle is also used for identity verification.
	handle, err := syscall.OpenProcess(0x1000|0x0010, false, uint32(pid))
	if err != nil {
		if errors.Is(err, syscall.Errno(87)) {
			return memoryProcess{}, errProcessGone
		}
		return memoryProcess{}, err
	}
	defer syscall.CloseHandle(handle)
	created, path, err := identityHandle(handle)
	if err != nil {
		return memoryProcess{}, err
	}
	var counters processMemoryCounters
	counters.Size = uint32(unsafe.Sizeof(counters))
	ok, _, callErr := getProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size))
	if ok == 0 {
		return memoryProcess{}, fmt.Errorf("GetProcessMemoryInfo: %w", callErr)
	}
	return memoryProcess{Created: created, Path: path, WorkingSet: uint64(counters.WorkingSetSize), PrivateBytes: uint64(counters.PrivateUsage)}, nil
}

func collectApplicationMemory(rootPID int, table map[int]processTableEntry, query func(int) (memoryProcess, error)) model.MemoryUsage {
	usage := model.MemoryUsage{SampledAt: model.Now()}
	root, err := query(rootPID)
	if err != nil {
		usage.Error = "无法读取主程序内存：" + err.Error()
		return usage
	}
	usage.ManagerBytes, usage.WorkingSetBytes = root.WorkingSet, root.WorkingSet
	usage.PrivateBytes, usage.ProcessCount = root.PrivateBytes, 1
	accepted := map[int]uint64{rootPID: root.Created}
	queue := []int{rootPID}
	children := map[int][]int{}
	for pid, process := range table {
		if strings.EqualFold(process.Name, "msedgewebview2.exe") {
			children[process.ParentID] = append(children[process.ParentID], pid)
		}
	}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, pid := range children[parent] {
			if _, visited := accepted[pid]; visited {
				continue
			}
			process, err := query(pid)
			if err != nil {
				if !errors.Is(err, errProcessGone) {
					usage.Partial = true
				}
				continue
			}
			// Ignore stale/reused PIDs and unrelated clients, including WebViews
			// belonging to a database client launched from PHPTools.
			if process.Created < accepted[parent] || !strings.EqualFold(filepath.Base(process.Path), "msedgewebview2.exe") {
				continue
			}
			accepted[pid] = process.Created
			queue = append(queue, pid)
			usage.WebViewBytes += process.WorkingSet
			usage.WorkingSetBytes += process.WorkingSet
			usage.PrivateBytes += process.PrivateBytes
			usage.ProcessCount++
		}
	}
	return usage
}

var appMemoryCache struct {
	sync.Mutex
	sampled time.Time
	value   model.MemoryUsage
}

// ApplicationMemory is read-only and demand-driven. Repeated UI state events
// reuse a five-second sample; this adds no timer or tray background work.
func ApplicationMemory() model.MemoryUsage {
	appMemoryCache.Lock()
	defer appMemoryCache.Unlock()
	if !appMemoryCache.sampled.IsZero() && time.Since(appMemoryCache.sampled) < 5*time.Second {
		return appMemoryCache.value
	}
	table, err := processTable()
	if err != nil {
		appMemoryCache.value = model.MemoryUsage{SampledAt: model.Now(), Error: "无法读取界面进程列表：" + err.Error()}
	} else {
		appMemoryCache.value = collectApplicationMemory(os.Getpid(), table, queryMemoryProcess)
	}
	appMemoryCache.sampled = time.Now()
	return appMemoryCache.value
}
