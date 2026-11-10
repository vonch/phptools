package services

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

func TestApplicationMemoryOnlyCountsOwnedWebViewBranch(t *testing.T) {
	table := map[int]processTableEntry{
		2:  {ParentID: 1, Name: "msedgewebview2.exe"},
		3:  {ParentID: 2, Name: "msedgewebview2.exe"},
		4:  {ParentID: 1, Name: "mysqld.exe"},
		5:  {ParentID: 4, Name: "msedgewebview2.exe"},
		6:  {ParentID: 1, Name: "navicat.exe"},
		7:  {ParentID: 6, Name: "msedgewebview2.exe"},
		8:  {ParentID: 1, Name: "msedgewebview2.exe"},
		9:  {ParentID: 1, Name: "msedgewebview2.exe"},
		10: {ParentID: 99, Name: "msedgewebview2.exe"},
		11: {ParentID: 1, Name: "msedgewebview2.exe"},
		12: {ParentID: 2, Name: "msedgewebview2.exe"},
		13: {ParentID: 12, Name: "msedgewebview2.exe"},
	}
	processes := map[int]memoryProcess{
		1: {Created: 100, Path: `C:\PHPTools.exe`, WorkingSet: 10, PrivateBytes: 7},
		2: {Created: 101, Path: `C:\WebView\msedgewebview2.exe`, WorkingSet: 20, PrivateBytes: 11},
		3: {Created: 102, Path: `C:\WebView\MSEDGEWEBVIEW2.EXE`, WorkingSet: 30, PrivateBytes: 13},
		8: {Created: 105, Path: `C:\other.exe`, WorkingSet: 1000},
		9: {Created: 99, Path: `C:\WebView\msedgewebview2.exe`, WorkingSet: 1000},
	}
	usage := collectApplicationMemory(1, table, func(pid int) (memoryProcess, error) {
		if pid == 11 {
			return memoryProcess{}, errProcessGone
		}
		if pid == 12 {
			return memoryProcess{}, errors.New("access denied")
		}
		value, exists := processes[pid]
		if !exists {
			t.Fatalf("queried unrelated process %d", pid)
		}
		return value, nil
	})
	if usage.WorkingSetBytes != 60 || usage.ManagerBytes != 10 || usage.WebViewBytes != 50 || usage.PrivateBytes != 31 || usage.ProcessCount != 3 {
		t.Fatalf("incorrect process scope: %+v", usage)
	}
	if !usage.Partial || usage.Error != "" {
		t.Fatalf("missing partial-result indication: %+v", usage)
	}
}

func TestApplicationMemoryUnavailableAndExitedChildren(t *testing.T) {
	usage := collectApplicationMemory(1, nil, func(int) (memoryProcess, error) {
		return memoryProcess{}, errors.New("access denied")
	})
	if usage.Error == "" || usage.ProcessCount != 0 {
		t.Fatal("unavailable root was reported as zero memory")
	}
	usage = collectApplicationMemory(1, map[int]processTableEntry{2: {ParentID: 1, Name: "msedgewebview2.exe"}}, func(pid int) (memoryProcess, error) {
		if pid == 1 {
			return memoryProcess{Created: 100, WorkingSet: 42}, nil
		}
		return memoryProcess{}, errProcessGone
	})
	if usage.Partial || usage.WorkingSetBytes != 42 || usage.Error != "" {
		t.Fatalf("exited child should not make a successful sample incomplete: %+v", usage)
	}
}

func TestNativeMemoryCountersAndCachedSample(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 && unsafe.Sizeof(processMemoryCounters{}) != 80 {
		t.Fatal("PROCESS_MEMORY_COUNTERS_EX layout is incorrect")
	}
	process, err := queryMemoryProcess(os.Getpid())
	if err != nil || process.WorkingSet == 0 || process.PrivateBytes == 0 || process.Created == 0 {
		t.Fatalf("native process query failed: %+v %v", process, err)
	}
	table, err := processTable()
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	if table[os.Getpid()].Name != filepath.Base(exe) {
		t.Fatal("process snapshot did not identify the current executable")
	}
	first, second := ApplicationMemory(), ApplicationMemory()
	if first.Error != "" || first.WorkingSetBytes == 0 || first != second {
		t.Fatalf("immediate reads did not reuse the native sample: %+v %+v", first, second)
	}
	first.WorkingSetBytes = 0
	if ApplicationMemory().WorkingSetBytes == 0 {
		t.Fatal("caller mutated the shared cached sample")
	}
}
