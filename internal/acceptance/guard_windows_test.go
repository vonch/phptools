package acceptance

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCleanupRefusesRunningProcess(t *testing.T) {
	if os.Getenv("PHPTOOLS_ACCEPTANCE_PROCESS_HELPER") == "1" {
		_, _ = io.WriteString(os.Stdout, "ready\n")
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	root, err := NewRoot(filepath.Join(t.TempDir(), "runtime"), "process-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Cleanup(); err != nil {
			t.Error(err)
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root.Path, "acceptance-process-helper.exe")
	if err := os.WriteFile(helper, data, 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(helper, "-test.run=^TestCleanupRefusesRunningProcess$")
	command.Env = append(os.Environ(), "PHPTOOLS_ACCEPTANCE_PROCESS_HELPER=1")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = input.Close() // The helper exits normally when its stdin closes.
		if err := command.Wait(); err != nil {
			t.Error(err)
		}
	}()
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("helper startup: %q, %v", line, err)
	}
	if err := root.Cleanup(); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("cleanup did not protect the running process: %v", err)
	}
	if _, err := os.Stat(helper); err != nil {
		t.Fatalf("running helper files changed: %v", err)
	}
}
