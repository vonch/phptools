//go:build windows

package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"phptools/internal/model"
)

const updateFixtureSource = `package main
import (
 "encoding/json"
 "flag"
 "os"
 "path/filepath"
 "time"
)
var version string
func main() {
 profile := flag.String("config-dir", "", "test profile")
 flag.Parse()
 cwd, _ := os.Getwd()
 raw, _ := json.Marshal(map[string]any{"pid":os.Getpid(), "version":version,"args":os.Args[1:],"cwd":cwd})
 _ = os.WriteFile(filepath.Join(*profile,"started-"+version+".json"), raw, 0600)
 if _, err := os.Stat(filepath.Join(*profile,"fail-"+version)); err == nil {
  if _, err := os.Stat(filepath.Join(*profile,"block-rollback")); err == nil {
   exe, _ := os.Executable()
   _ = os.Chmod(exe, 0400)
  }
  os.Exit(3)
 }
 deadline := time.Now().Add(time.Minute)
 for time.Now().Before(deadline) {
  stop := filepath.Join(*profile,"stop-"+version)
  if _, err := os.Stat(stop); err == nil { _ = os.Remove(stop); return }
  time.Sleep(20*time.Millisecond)
 }
}
`

type updateStartedFixture struct {
	PID     int      `json:"pid"`
	Version string   `json:"version"`
	Args    []string `json:"args"`
	CWD     string   `json:"cwd"`
}

type updateScriptResult struct {
	Version     string `json:"version"`
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	BackupPath  string `json:"backupPath"`
	Warning     string `json:"warning"`
	StagingPath string `json:"stagingPath"`
	CompletedAt string `json:"completedAt"`
}

func TestUpdateApplyScriptEndToEnd(t *testing.T) {
	buildDir := t.TempDir()
	sourcePath := filepath.Join(buildDir, "fixture.go")
	if err := os.WriteFile(sourcePath, []byte(updateFixtureSource), 0600); err != nil {
		t.Fatal(err)
	}
	binaries := map[string]string{}
	for _, version := range []string{"old", "new"} {
		path := filepath.Join(buildDir, version+".exe")
		command := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -H=windowsgui -X=main.version="+version, "-o", path, sourcePath)
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build update fixture: %v\n%s", err, output)
		}
		binaries[version] = path
	}
	for _, scenario := range []string{"success", "success-delayed-ready", "success-after-lock", "success-cleanup-warning", "cancelled", "changed-source", "rollback", "preserve-failed-rollback"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			installation := filepath.Join(directory, "应用 app ' quoted [test] $path")
			config := filepath.Join(directory, "配置 profile ' [test] $data")
			for _, path := range []string{installation, config} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(installation, "PHPTools.exe")
			copyUpdateFixture(t, binaries["old"], target)
			staging, err := os.MkdirTemp(installation, ".phptools-update-")
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(staging, "next.exe")
			copyUpdateFixture(t, binaries["new"], source)
			digest, err := updateApplyHash(source)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := prepareUpdate(staging, target, source, config, model.UpdateInfo{LatestVersion: "9.9.9", SHA256: digest})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "success-delayed-ready" {
				// Let the parent poll while the ready file exists but is still empty.
				// Only the staged test copy is slowed; the production helper is unchanged.
				beforeWrite := "$content = [Text.Encoding]::UTF8.GetBytes($value)"
				if strings.Count(string(updateApplyScript), beforeWrite) != 1 {
					t.Fatal("could not locate the ready-signal write for delay injection")
				}
				delayed := strings.Replace(string(updateApplyScript), beforeWrite, "Start-Sleep -Milliseconds 400\n        "+beforeWrite, 1)
				if err := os.WriteFile(filepath.Join(staging, "apply-update.ps1"), []byte(delayed), 0600); err != nil {
					t.Fatal(err)
				}
			}
			oldCommand := exec.Command(target, "--config-dir", config)
			oldCommand.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
			if err := oldCommand.Start(); err != nil {
				t.Fatal(err)
			}
			oldDone := make(chan error, 1)
			go func() { oldDone <- oldCommand.Wait(); close(oldDone) }()
			t.Cleanup(func() {
				if err := prepared.stopHelper(); err != nil {
					t.Error(err)
				}
				stopUpdateFixtures(t, config, target)
				select {
				case <-oldDone:
				case <-time.After(5 * time.Second):
					if err := oldCommand.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
						t.Errorf("stop original fixture: %v", err)
					}
					select {
					case <-oldDone:
					case <-time.After(5 * time.Second):
						t.Error("original fixture did not exit after termination")
					}
					t.Error("original update fixture required forced cleanup")
				}
				_ = os.Chmod(target, 0600)
				if _, err := os.Stat(prepared.plan.Backup); err == nil {
					// The retention assertion is complete. Remove only this test's
					// owned backup instead of rewriting a recently executed image.
					if err := prepared.checkOwnership(); err != nil {
						t.Error(err)
					} else if err := os.Remove(prepared.plan.Backup); err != nil {
						t.Errorf("remove owned test backup: %v", err)
					}
				} else if !os.IsNotExist(err) {
					t.Errorf("inspect owned test backup: %v", err)
				}
				if err := prepared.Cleanup(); err != nil {
					t.Error(err)
				}
				if _, err := os.Stat(staging); !os.IsNotExist(err) {
					t.Errorf("staging survived cleanup: %s: %v", staging, err)
				}
			})
			waitStartedFixture(t, config, "old", 0)
			prepared.plan.ParentPID = oldCommand.Process.Pid
			prepared.plan.ParentCreated, err = updateParentCreation(oldCommand.Process.Pid)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(staging, "plan.json")); err != nil {
				t.Fatal(err)
			}
			if err := prepared.writePlan(); err != nil {
				t.Fatal(err)
			}
			if scenario == "changed-source" {
				if err := os.WriteFile(source, []byte("tampered"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "rollback" || scenario == "preserve-failed-rollback" {
				if err := os.WriteFile(filepath.Join(config, "fail-new"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "preserve-failed-rollback" {
				if err := os.WriteFile(filepath.Join(config, "block-rollback"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "success-cleanup-warning" {
				if err := os.Mkdir(filepath.Join(staging, "cleanup-blocker"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			err = prepared.Start()
			if scenario == "changed-source" {
				if err == nil {
					t.Fatal("accepted changed source")
				}
				assertUpdateTarget(t, target, prepared.plan.PreviousHash)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "cancelled" {
				if err := prepared.Cleanup(); err != nil {
					t.Fatal(err)
				}
				assertUpdateTarget(t, target, prepared.plan.PreviousHash)
				if _, err := os.Stat(staging); !os.IsNotExist(err) {
					t.Fatalf("cancel did not clean staging: %v", err)
				}
				return
			}
			if scenario == "success-after-lock" {
				release := lockUpdateTarget(t, target)
				t.Cleanup(release)
				time.AfterFunc(2*time.Second, release)
			}
			if err := os.WriteFile(filepath.Join(config, "stop-old"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-oldDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("old fixture did not stop")
			}
			select {
			case <-prepared.done:
			case <-time.After(15 * time.Second):
				t.Fatal("update script did not finish")
			}
			result := readUpdateScriptResult(t, config)
			if strings.HasPrefix(scenario, "success") {
				if !result.Success || result.Version != "9.9.9" || result.CompletedAt == "" {
					t.Fatalf("unexpected success result: %+v", result)
				}
				assertUpdateTarget(t, target, digest)
				started := waitStartedFixture(t, config, "new", 0)
				if len(started.Args) != 2 || started.Args[0] != "--config-dir" || started.Args[1] != config || started.CWD != installation {
					t.Fatalf("restart lost quoting/profile/directory: %+v", started)
				}
				if scenario == "success-cleanup-warning" {
					if result.Warning == "" || result.StagingPath != staging || result.BackupPath != "" {
						t.Fatalf("cleanup warning or retained directory not reported: %+v", result)
					}
					if _, err := os.Stat(filepath.Join(staging, "cleanup-blocker")); err != nil {
						t.Fatalf("cleanup did not preserve the unexpected directory: %v", err)
					}
				} else if result.Warning != "" || result.StagingPath != "" {
					t.Fatalf("clean upgrade reported a cleanup warning: %+v", result)
				}
				if scenario == "success" {
					handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(started.PID))
					if err != nil {
						t.Fatal(err)
					}
					defer windows.CloseHandle(handle)
					if err := os.Remove(filepath.Join(config, "started-new.json")); err != nil {
						t.Fatal(err)
					}
					stopUpdateFixtures(t, config, target)
					if state, err := windows.WaitForSingleObject(handle, 0); err != nil || state != windows.WAIT_OBJECT_0 {
						t.Fatalf("fixture without a startup manifest survived cleanup: state=%d: %v", state, err)
					}
				}
			} else {
				if result.Success {
					t.Fatalf("failed upgrade reported success: %+v", result)
				}
				if scenario == "rollback" {
					assertUpdateTarget(t, target, prepared.plan.PreviousHash)
					waitStartedFixture(t, config, "old", oldCommand.Process.Pid)
				} else {
					if result.BackupPath != prepared.plan.Backup {
						t.Fatalf("recovery backup not reported: %+v", result)
					}
					assertUpdateTarget(t, prepared.plan.Backup, prepared.plan.PreviousHash)
					if err := prepared.Cleanup(); err == nil {
						t.Fatal("discarded an unrecovered previous executable")
					}
				}
			}
			if scenario != "preserve-failed-rollback" && scenario != "success-cleanup-warning" {
				if _, err := os.Stat(staging); !os.IsNotExist(err) {
					t.Fatalf("script left staging: %v; result: %+v", err, result)
				}
			}
		})
	}
}

func lockUpdateTarget(t *testing.T, path string) func() {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	// Read sharing still lets the helper hash the file, and it is enough to make
	// ReplaceFile return a sharing violation until the handle is closed.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	return func() { once.Do(func() { _ = windows.CloseHandle(handle) }) }
}

func copyUpdateFixture(t *testing.T, source, destination string) {
	t.Helper()
	raw, err := os.ReadFile(source)
	if err == nil {
		err = os.WriteFile(destination, raw, 0600)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func waitStartedFixture(t *testing.T, config, version string, previousPID int) updateStartedFixture {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(filepath.Join(config, "started-"+version+".json"))
		var started updateStartedFixture
		if err == nil && json.Unmarshal(raw, &started) == nil && started.PID != 0 && started.PID != previousPID {
			return started
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s fixture did not start in %s", version, config)
	return updateStartedFixture{}
}

func readUpdateScriptResult(t *testing.T, config string) updateScriptResult {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(config, "update-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result updateScriptResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("read helper result: %v: %s", err, raw)
	}
	return result
}

func assertUpdateTarget(t *testing.T, path, expectedHash string) {
	t.Helper()
	hash, err := updateApplyHash(path)
	if err != nil || hash != expectedHash {
		t.Fatalf("unexpected executable contents at %s: %s != %s (%v)", path, hash, expectedHash, err)
	}
}

func stopUpdateFixtures(t *testing.T, config, target string) {
	t.Helper()
	// A helper can be cancelled before the started JSON has been written.
	// Match the unique executable path and keep its handle until it has exited.
	for _, version := range []string{"old", "new"} {
		if err := os.WriteFile(filepath.Join(config, "stop-"+version), nil, 0600); err != nil {
			t.Errorf("request fixture stop: %v", err)
		}
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		t.Errorf("scan owned fixtures: %v", err)
		return
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), filepath.Base(target)) {
			continue
		}
		handle, openErr := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, entry.ProcessID)
		if errors.Is(openErr, windows.ERROR_INVALID_PARAMETER) || errors.Is(openErr, windows.ERROR_NOT_FOUND) {
			continue
		}
		if openErr != nil {
			t.Errorf("could not inspect possible fixture %d: %v", entry.ProcessID, openErr)
			continue
		}
		func() {
			defer windows.CloseHandle(handle)
			buffer := make([]uint16, 32768)
			size := uint32(len(buffer))
			if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
				// Termination can make image queries fail before the process
				// object signals. Waiting is safe without assuming ownership.
				if state, waitErr := windows.WaitForSingleObject(handle, 5000); waitErr == nil && state == windows.WAIT_OBJECT_0 {
					return
				}
				t.Errorf("could not inspect possible fixture %d: %v", entry.ProcessID, err)
				return
			}
			if !strings.EqualFold(windows.UTF16ToString(buffer[:size]), target) {
				return
			}
			state, err := windows.WaitForSingleObject(handle, 5000)
			if err != nil || state != windows.WAIT_OBJECT_0 {
				if stopErr := windows.TerminateProcess(handle, 1); stopErr != nil {
					t.Errorf("terminate owned fixture %d: %v", entry.ProcessID, stopErr)
				}
				state, waitErr := windows.WaitForSingleObject(handle, 5000)
				if waitErr != nil || state != windows.WAIT_OBJECT_0 {
					t.Errorf("owned fixture %d did not exit after termination: state=%d: %v", entry.ProcessID, state, waitErr)
				}
				t.Errorf("owned fixture %d required forced cleanup: state=%d: %v", entry.ProcessID, state, err)
			}
		}()
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		t.Errorf("scan owned fixtures: %v", err)
	}
}

func TestPrepareUpdateRejectsInvalidDigestAndLinkedDirectories(t *testing.T) {
	installation := t.TempDir()
	config := t.TempDir()
	target := filepath.Join(installation, "PHPTools.exe")
	if err := os.WriteFile(target, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	staging, err := os.MkdirTemp(installation, ".phptools-update-")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(staging, "next.exe")
	if err := os.WriteFile(source, []byte("next"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareUpdate(staging, target, source, config, model.UpdateInfo{SHA256: strings.Repeat("0", 64)}); err == nil {
		t.Fatal("accepted an invalid update digest")
	}
	entries, err := os.ReadDir(staging)
	if err != nil || len(entries) != 1 {
		t.Fatalf("failed preparation changed staging: %v, %v", entries, err)
	}
	link := filepath.Join(installation, "linked-staging")
	if err := os.Symlink(staging, link); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	digest, err := updateApplyHash(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareUpdate(link, target, filepath.Join(link, "next.exe"), config, model.UpdateInfo{SHA256: digest}); err == nil {
		t.Fatal("accepted a linked staging directory")
	}
}

func TestPreparedUpdateCleanupRefusesReplacedOwnership(t *testing.T) {
	installation := t.TempDir()
	config := t.TempDir()
	target := filepath.Join(installation, "PHPTools.exe")
	if err := os.WriteFile(target, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	staging, err := os.MkdirTemp(installation, ".phptools-update-")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(staging, "next.exe")
	if err := os.WriteFile(source, []byte("next"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := updateApplyHash(source)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareUpdate(staging, target, source, config, model.UpdateInfo{SHA256: digest})
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(staging, ".update-owner")
	if err := os.WriteFile(marker, []byte("foreign ownership"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Cleanup(); err == nil {
		t.Fatal("deleted a foreign staging directory")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal(fmt.Errorf("foreign file not preserved: %w", err))
	}
	if err := os.WriteFile(marker, []byte(prepared.plan.Token), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateCommitIsCompleteAndExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commit")
	token := []byte("complete-test-ownership-token")
	if err := writeUpdateCommit(path, token); err != nil {
		t.Fatal(err)
	}
	if err := writeUpdateCommit(path, []byte("replacement-token")); err == nil {
		t.Fatal("overwrote an existing commit credential")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != string(token) {
		t.Fatalf("published commit did not preserve the complete token: %q: %v", raw, err)
	}
	if _, err := os.Stat(path + ".pending"); !os.IsNotExist(err) {
		t.Fatalf("commit left a pending signal: %v", err)
	}
}
