//go:build windows

package services

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestWindowsHealthUnknownLiveProcessDoesNotRepairOrControl(t *testing.T) {
	m, pkg, i := healthFixture(t, "nginx")
	i = startOwnedProcessFixture(t, i)
	dir, _ := m.instanceDir(i.ID)
	for _, path := range []string{filepath.Dir(i.LogPath), filepath.Join(dir, "temp")} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	_, items := m.CheckHealth(context.Background(), pkg, i, true)
	if item := healthResult(t, items, "process"); item.Status != "error" {
		t.Fatalf("missing record was accepted for live process: %+v", item)
	}
	for _, path := range []string{filepath.Dir(i.LogPath), filepath.Join(dir, "temp"), filepath.Join(dir, "process.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unknown live process caused filesystem repair: %s (%v)", path, err)
		}
	}
	if created, _, err := processIdentity(i.PID); err != nil || created != i.ProcessCreated {
		t.Fatalf("unknown process was controlled: %v", err)
	}
	for _, item := range items {
		if item.Status == "repaired" || item.Repairable || strings.HasSuffix(item.ID, ":config-test") {
			t.Fatalf("unknown identity allowed mutation or execution: %+v", item)
		}
	}
	if _, err := m.RepairHealth(context.Background(), pkg, i, "process"); err == nil {
		t.Fatal("manual repair accepted unknown live process")
	}
}

func TestWindowsHealthStaleStateOnlyReportsPersistedRepairs(t *testing.T) {
	for _, saveFails := range []bool{false, true} {
		name := "saved"
		if saveFails {
			name = "failed-save"
		}
		t.Run(name, func(t *testing.T) {
			m, pkg, i := healthFixture(t, "php")
			i = startOwnedProcessFixture(t, i)
			actualCreated := i.ProcessCreated
			// A recycled identity is stale metadata even though the numeric PID
			// currently belongs to a live process. Never terminate that process.
			i.ProcessCreated++
			i.State = "error"
			i.Error = "stale failure"
			record := processRecord{PID: i.PID, Created: i.ProcessCreated, Executable: i.Executable, Config: i.ConfigPath, ConfigArgument: configArgument(i)}
			if err := m.writeRecord(i, record); err != nil {
				t.Fatal(err)
			}
			dir, _ := m.instanceDir(i.ID)
			recordPath := filepath.Join(dir, "process.json")
			before, err := os.ReadFile(recordPath)
			if err != nil {
				t.Fatal(err)
			}
			saveCount := 0
			m.save = func(current model.Instance) error {
				saveCount++
				assertClearedProcessIdentity(t, current)
				if saveFails {
					return errors.New("fixture disk write failed")
				}
				return nil
			}
			got, items := m.CheckHealth(context.Background(), pkg, i, false)
			if saveCount != 1 {
				t.Fatalf("unexpected save count: %d", saveCount)
			}
			status := "repaired"
			if saveFails {
				status = "error"
				if got != i {
					t.Fatalf("failed save discarded original state: %+v", got)
				}
			} else {
				assertClearedProcessIdentity(t, got)
			}
			if item := healthResult(t, items, "process"); item.Status != status {
				t.Fatalf("misreported metadata repair: %+v", item)
			}
			after, err := os.ReadFile(recordPath)
			if err != nil || string(after) != string(before) {
				t.Fatalf("diagnostic process record changed: %v", err)
			}
			if created, _, err := processIdentity(i.PID); err != nil || created != actualCreated {
				t.Fatalf("reused PID was controlled: %v", err)
			}
		})
	}
}

func TestWindowsHealthRejectsJunctionBeforeRepair(t *testing.T) {
	m, pkg, i := healthFixture(t, "nginx")
	outside := t.TempDir()
	logs := filepath.Dir(i.LogPath)
	if err := os.Remove(logs); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cmd.exe", "/c", "mklink", "/J", logs, outside)
	hideConsole(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("junction creation unavailable: %v %s", err, output)
	}
	t.Cleanup(func() { _ = os.Remove(logs) })
	dir, _ := m.instanceDir(i.ID)
	if err := os.Remove(filepath.Join(dir, "temp")); err != nil {
		t.Fatal(err)
	}
	_, items := m.CheckHealth(context.Background(), pkg, i, true)
	if item := healthResult(t, items, "instance-path"); item.Status != "error" {
		t.Fatalf("junction was accepted: %+v", item)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("junction target changed: %v %+v", err, entries)
	}
	if _, err := os.Stat(filepath.Join(dir, "temp")); !os.IsNotExist(err) {
		t.Fatalf("unsafe instance had runtime directories repaired: %v", err)
	}
	for _, item := range items {
		if item.Repairable {
			t.Fatalf("junction offered a repair: %+v", item)
		}
	}
	if _, err := m.RepairHealth(context.Background(), pkg, i, "logs"); err == nil {
		t.Fatal("manual repair accepted junction")
	}
}

func TestWindowsFullHealthDetectsIncludedNginxPortWithoutStarting(t *testing.T) {
	m, pkg, i := healthFixture(t, "nginx")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	site := "server { listen 127.0.0.1:" + strconv.Itoa(port) + "; server_name conflict.test; }\n"
	if err := os.WriteFile(filepath.Join(m.root, "conf", "conflict.conf"), []byte(site), 0600); err != nil {
		t.Fatal(err)
	}
	got, items := m.CheckHealth(context.Background(), pkg, i, true)
	if got.PID != 0 || got.State != "stopped" {
		t.Fatalf("port check started or changed stopped service: %+v", got)
	}
	item := healthResult(t, items, "config-test")
	if item.Status != "error" || !strings.Contains(item.Message, strconv.Itoa(port)) {
		t.Fatalf("included site port conflict was missed: %+v", item)
	}
	dir, _ := m.instanceDir(i.ID)
	if _, err := os.Stat(filepath.Join(dir, "process.json")); !os.IsNotExist(err) {
		t.Fatalf("health check created process record: %v", err)
	}
	if _, err := os.Stat(i.LogPath); !os.IsNotExist(err) {
		t.Fatalf("external executable ran before detecting conflict: %v", err)
	}
}
