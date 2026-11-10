package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestartedAppStartsInstancesRecordedBeforeUpdate(t *testing.T) {
	a := testApp(t)
	_, instance := desktopHealthFixture(t, a, "php")
	if err := a.writeUpdateResume([]string{instance.ID, "gone-instance"}); err != nil {
		t.Fatal(err)
	}
	configDir := a.configDir
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	a.db = nil
	restarted, err := New(configDir, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		restarted.wg.Wait()
		if restarted.db != nil {
			restarted.db.Close()
		}
	})
	if _, err = restarted.ResumeUpdatedInstances(); err != nil {
		t.Fatal(err)
	}
	restarted.wg.Wait()
	if _, err = os.Stat(filepath.Join(configDir, "update-resume.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resume record was kept: %v", err)
	}
	state, err := restarted.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Tasks) != 1 || state.Tasks[0].Action != "resume-instances" || state.Tasks[0].State != "failed" {
		t.Fatalf("resume task = %+v", state.Tasks)
	}
	if !strings.Contains(state.Tasks[0].Error, instance.Name) || !strings.Contains(state.Tasks[0].Error, "gone-instance") {
		t.Fatalf("resume error = %s", state.Tasks[0].Error)
	}
}

func TestFailedUpdateRemovesTheResumeRecord(t *testing.T) {
	a := testApp(t)
	if err := a.writeUpdateResume([]string{"stale-instance"}); err != nil {
		t.Fatal(err)
	}
	exited := false
	a.hooks.Exit = func() { exited = true }
	err := a.quit(func() error { return errors.New("helper unavailable") })
	if err == nil || exited || a.closing {
		t.Fatalf("failed update exited: %v exited=%v closing=%v", err, exited, a.closing)
	}
	if _, err = os.Stat(a.updateResumePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed update kept resume record: %v", err)
	}
}

func TestSuccessfulUpdateWithoutRunningInstancesLeavesNoResumeRecord(t *testing.T) {
	a := testApp(t)
	if err := a.writeUpdateResume([]string{"stale-instance"}); err != nil {
		t.Fatal(err)
	}
	exited := false
	a.hooks.Exit = func() { exited = true }
	if err := a.quit(func() error { return nil }); err != nil || !exited {
		t.Fatalf("update quit = %v exited=%v", err, exited)
	}
	a.db = nil
	if _, err := os.Stat(a.updateResumePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty update kept resume record: %v", err)
	}
}
