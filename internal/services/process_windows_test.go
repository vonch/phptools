//go:build windows

package services

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"phptools/internal/model"
)

func TestProcessFixture(t *testing.T) {
	if os.Getenv("PHPTOOLS_PROCESS_FIXTURE") == "1" {
		time.Sleep(60 * time.Second)
		os.Exit(0)
	}
}

func TestWindowsProcessIdentityAndSafeTermination(t *testing.T) {
	config := filepath.Join(t.TempDir(), "中文 空格", "php.ini")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestProcessFixture$", "--", config)
	cmd.Env = append(os.Environ(), "PHPTOOLS_PROCESS_FIXTURE=1")
	hideConsole(cmd)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	created, actual, err := processIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if created == 0 || !samePath(actual, exe) {
		t.Fatalf("unexpected identity: %d %s", created, actual)
	}
	p := processRecord{PID: cmd.Process.Pid, Created: created, Executable: exe, Config: config, ConfigArgument: config}
	if err = verifyProcess(p); err != nil {
		t.Fatal(err)
	}
	p.Created++
	if err = verifyProcess(p); !errors.Is(err, errProcessGone) {
		t.Fatalf("reused PID was accepted: %v", err)
	}
	p.Created--
	p.ConfigArgument = filepath.Join(t.TempDir(), "wrong.ini")
	if err = verifyProcess(p); err == nil {
		t.Fatal("wrong config accepted")
	}
	i := model.Instance{Kind: "php", PID: cmd.Process.Pid, ProcessCreated: created + 1, Executable: exe, ConfigPath: config}
	if err = terminateOwned(i); err == nil {
		t.Fatal("terminated a process with a different creation time")
	}
	if _, _, err = processIdentity(cmd.Process.Pid); err != nil {
		t.Fatalf("fixture unexpectedly stopped: %v", err)
	}
	i.ProcessCreated = created
	i.Kind = "mysql"
	if err = terminateOwned(i); err == nil {
		t.Fatal("database allowed force termination")
	}
	i.Kind = "php"
	if err = terminateOwned(i); err != nil {
		t.Fatal(err)
	}
	if err = waitStopped(context.Background(), i, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialsAreRestrictedAndRemoved(t *testing.T) {
	root := filepath.Join(t.TempDir(), "中文 空格")
	m := New(root, nil, nil)
	pkg := fixturePackage(t, root, "mysql", "8.4.8")
	i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "MySQL", PackageID: pkg.ID, Port: 19991})
	if err != nil {
		t.Fatal(err)
	}
	name, cleanup, err := m.clientFile(i, false, `strong'"\password`)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "protocol=TCP") || !strings.Contains(string(content), "port=19991") {
		t.Fatal("missing client connection settings")
	}
	cleanup()
	if _, err = os.Stat(name); !os.IsNotExist(err) {
		t.Fatal("credential file was not removed")
	}
}

// The helper is the test executable copied into an isolated instance package.
// It accepts this instance's config argument without starting a real service.
func startOwnedProcessFixture(t *testing.T, i model.Instance) model.Instance {
	t.Helper()
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(testExecutable)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(i.Executable, contents, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(i.Executable, "-test.run=^TestProcessFixture$", "--", i.ConfigPath)
	cmd.Env = append(os.Environ(), "PHPTOOLS_PROCESS_FIXTURE=1")
	hideConsole(cmd)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	i.PID = cmd.Process.Pid
	i.ProcessCreated, _, err = processIdentity(i.PID)
	if err != nil {
		t.Fatal(err)
	}
	i.State = "running"
	return i
}

func assertClearedProcessIdentity(t *testing.T, i model.Instance) {
	t.Helper()
	if i.PID != 0 || i.ProcessCreated != 0 || i.State != "stopped" || i.Error != "" {
		t.Fatalf("stale identity was not cleared: %+v", i)
	}
}

func TestWindowsLiveIdentityWithoutRecordIsNeverControlled(t *testing.T) {
	created, executable, err := processIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []string{"missing", "corrupt"} {
		t.Run(record, func(t *testing.T) {
			root := t.TempDir()
			m := New(root, nil, nil)
			pkg := fixturePackage(t, root, "php", "8.4.8")
			i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "php", PackageID: pkg.ID, Port: 19991})
			if err != nil {
				t.Fatal(err)
			}
			i.PID, i.ProcessCreated, i.Executable = os.Getpid(), created, executable
			i.State = "running"
			if record == "corrupt" {
				path := filepath.Join(filepath.Dir(filepath.Dir(i.ConfigPath)), "process.json")
				if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got := m.Inspect(i)
			if got.State != "error" || got.PID != i.PID || got.ProcessCreated != created {
				t.Fatalf("unrecorded live identity was discarded: %+v", got)
			}
			if _, err := m.Start(context.Background(), pkg, i); err == nil {
				t.Fatal("Start accepted an unrecorded live identity")
			}
			if _, err := m.Stop(context.Background(), i); err == nil {
				t.Fatal("Stop accepted an unrecorded live identity")
			}
			if now, actual, err := processIdentity(i.PID); err != nil || now != created || !samePath(actual, executable) {
				t.Fatalf("live identity changed after rejected operations: %d %s %v", now, actual, err)
			}
		})
	}
}

func TestWindowsExitedIdentityIsClearedBeforeFailedStart(t *testing.T) {
	root := t.TempDir()
	m := New(root, nil, nil)
	pkg := fixturePackage(t, root, "php", "8.4.8")
	base, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "helper", PackageID: pkg.ID, Port: 19991})
	if err != nil {
		t.Fatal(err)
	}
	base = startOwnedProcessFixture(t, base)
	if err = terminateOwned(base); err != nil {
		t.Fatal(err)
	}
	if err = waitStopped(context.Background(), base, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	for _, record := range []string{"missing", "corrupt", "valid"} {
		t.Run(record, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			port := listener.Addr().(*net.TCPAddr).Port
			i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: record, PackageID: pkg.ID, Port: port})
			if err != nil {
				t.Fatal(err)
			}
			i.PID, i.ProcessCreated = base.PID, base.ProcessCreated
			i.State, i.Error = "error", "stale failure"
			switch record {
			case "corrupt":
				path := filepath.Join(filepath.Dir(filepath.Dir(i.ConfigPath)), "process.json")
				if err = os.WriteFile(path, []byte("{broken"), 0600); err != nil {
					t.Fatal(err)
				}
			case "valid":
				p := processRecord{PID: i.PID, Created: i.ProcessCreated, Executable: i.Executable, Config: i.ConfigPath, ConfigArgument: configArgument(i)}
				if err = m.writeRecord(i, p); err != nil {
					t.Fatal(err)
				}
			}
			assertClearedProcessIdentity(t, m.Inspect(i))
			got, err := m.Start(context.Background(), pkg, i)
			if err == nil || !strings.Contains(err.Error(), "端口") {
				t.Fatalf("expected an occupied-port error, got %+v %v", got, err)
			}
			// The desktop persists Start's return value even when Start fails.
			// The occupied port stays visible, and the exited process identity does not.
			if got.PID != 0 || got.ProcessCreated != 0 || got.State != "error" || !strings.Contains(got.Error, "端口") {
				t.Fatalf("failed start did not keep a cleared error: %+v", got)
			}
			if i.State != "error" || i.PID == 0 {
				t.Fatal("test lost the original stale input")
			}
		})
	}
}

func TestWindowsReusedPIDIsClearedWithoutControllingNewProcess(t *testing.T) {
	created, executable, err := processIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{"creation-time", "executable"} {
		t.Run(changed, func(t *testing.T) {
			root := t.TempDir()
			m := New(root, nil, nil)
			pkg := fixturePackage(t, root, "php", "8.4.8")
			i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "php", PackageID: pkg.ID, Port: 19991})
			if err != nil {
				t.Fatal(err)
			}
			i.PID, i.ProcessCreated = os.Getpid(), created
			i.State, i.Error = "error", "stale failure"
			if changed == "creation-time" {
				i.Executable = executable
				i.ProcessCreated--
			}
			assertClearedProcessIdentity(t, m.Inspect(i))
			got, err := m.Stop(context.Background(), i)
			if err != nil {
				t.Fatal(err)
			}
			assertClearedProcessIdentity(t, got)
			if now, actual, err := processIdentity(os.Getpid()); err != nil || now != created || !samePath(actual, executable) {
				t.Fatalf("reused PID was controlled: %d %s %v", now, actual, err)
			}
		})
	}
}

func TestWindowsRecordIdentityMismatchNeverControlsLiveProcess(t *testing.T) {
	root := t.TempDir()
	m := New(root, nil, nil)
	pkg := fixturePackage(t, root, "php", "8.4.8")
	i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "php", PackageID: pkg.ID, Port: 19991})
	if err != nil {
		t.Fatal(err)
	}
	i = startOwnedProcessFixture(t, i)
	p := processRecord{PID: i.PID, Created: i.ProcessCreated + 1, Executable: i.Executable, Config: i.ConfigPath, ConfigArgument: configArgument(i)}
	if err = m.writeRecord(i, p); err != nil {
		t.Fatal(err)
	}
	got := m.Inspect(i)
	if got.State != "error" || got.PID != i.PID || !strings.Contains(got.Error, "不一致") {
		t.Fatalf("conflicting record was accepted: %+v", got)
	}
	if _, err = m.Start(context.Background(), pkg, i); err == nil {
		t.Fatal("Start accepted a conflicting record")
	}
	if _, err = m.Stop(context.Background(), i); err == nil {
		t.Fatal("Stop accepted a conflicting record")
	}
	if created, executable, err := processIdentity(i.PID); err != nil || created != i.ProcessCreated || !samePath(executable, i.Executable) {
		t.Fatalf("live helper was controlled: %d %s %v", created, executable, err)
	}
}

func TestWindowsDivergedExitedIdentitiesCanStop(t *testing.T) {
	root := t.TempDir()
	m := New(root, nil, nil)
	pkg := fixturePackage(t, root, "php", "8.4.8")
	i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "php", PackageID: pkg.ID, Port: 19991})
	if err != nil {
		t.Fatal(err)
	}
	live := startOwnedProcessFixture(t, i)
	i.PID = 2_000_000_001
	i.ProcessCreated = live.ProcessCreated
	i.Executable = live.Executable
	i.State = "error"
	p := processRecord{PID: 2_000_000_002, Created: live.ProcessCreated + 1, Executable: i.Executable, Config: i.ConfigPath, ConfigArgument: configArgument(i)}
	if err = m.writeRecord(i, p); err != nil {
		t.Fatal(err)
	}
	assertClearedProcessIdentity(t, m.Inspect(i))
	got, err := m.Stop(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	assertClearedProcessIdentity(t, got)

	// A still-living record must not be adopted just because the saved row is stale.
	i.PID = 2_000_000_001
	i.ProcessCreated = live.ProcessCreated
	i.State = "running"
	p = processRecord{PID: live.PID, Created: live.ProcessCreated, Executable: live.Executable, Config: i.ConfigPath, ConfigArgument: configArgument(i)}
	if err = m.writeRecord(i, p); err != nil {
		t.Fatal(err)
	}
	conflict := m.Inspect(i)
	if conflict.State != "error" || conflict.PID != i.PID || !strings.Contains(conflict.Error, "不一致") {
		t.Fatalf("live divergent record was adopted: %+v", conflict)
	}
	if _, err = m.Stop(context.Background(), i); err == nil {
		t.Fatal("Stop adopted a divergent live record")
	}
	if created, executable, err := processIdentity(live.PID); err != nil || created != live.ProcessCreated || !samePath(executable, live.Executable) {
		t.Fatalf("divergent live process was controlled: %d %s %v", created, executable, err)
	}
}

func TestWindowsStopSavesClearedStateBeforeRemovingRecord(t *testing.T) {
	for _, saveFails := range []bool{false, true} {
		name := "successful-save"
		if saveFails {
			name = "failed-save"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			m := New(root, nil, nil)
			pkg := fixturePackage(t, root, "php", "8.4.8")
			i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "php", PackageID: pkg.ID, Port: 19991})
			if err != nil {
				t.Fatal(err)
			}
			i = startOwnedProcessFixture(t, i)
			p := processRecord{PID: i.PID, Created: i.ProcessCreated, Executable: i.Executable, Config: i.ConfigPath, ConfigArgument: configArgument(i)}
			if err = m.writeRecord(i, p); err != nil {
				t.Fatal(err)
			}
			recordPath := filepath.Join(filepath.Dir(filepath.Dir(i.ConfigPath)), "process.json")
			saveErr := errors.New("fixture metadata save failed")
			saved := false
			m.save = func(current model.Instance) error {
				saved = true
				assertClearedProcessIdentity(t, current)
				if _, err := os.Stat(recordPath); err != nil {
					t.Fatalf("record was removed before state was saved: %v", err)
				}
				if saveFails {
					return saveErr
				}
				return nil
			}
			got, err := m.Stop(context.Background(), i)
			if !saved {
				t.Fatal("cleared state was not saved")
			}
			assertClearedProcessIdentity(t, got)
			if saveFails {
				if !errors.Is(err, saveErr) {
					t.Fatalf("metadata save failure was lost: %v", err)
				}
				if _, err = os.Stat(recordPath); err != nil {
					t.Fatalf("record was removed after a failed save: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = os.Stat(recordPath); !os.IsNotExist(err) {
					t.Fatalf("record remains after successful stop: %v", err)
				}
			}
			if _, _, err = processIdentity(i.PID); !errors.Is(err, errProcessGone) {
				t.Fatalf("test helper is still alive after Stop: %v", err)
			}
			// A failed metadata save can leave the old row; inspection must still
			// recognize the exited helper using the retained trusted record.
			assertClearedProcessIdentity(t, m.Inspect(i))
		})
	}
}
