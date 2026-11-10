package dbtools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if marker := os.Getenv("PHPTOOLS_CLIENT_LAUNCH_TEST"); marker != "" {
		cwd, _ := os.Getwd()
		data, _ := json.Marshal(map[string]any{"args": os.Args, "cwd": cwd})
		if os.WriteFile(marker, data, 0600) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestLaunchClientUsesExactPathAndNoArguments(t *testing.T) {
	dir := t.TempDir()
	exe, _ := os.Executable()
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(dir, "中文 GUI client.exe")
	if err = os.WriteFile(client, data, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "launched.json")
	t.Setenv("PHPTOOLS_CLIENT_LAUNCH_TEST", marker)
	if err = LaunchClient(client); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var launched struct {
		Args []string `json:"args"`
		Cwd  string   `json:"cwd"`
	}
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(marker); err == nil && json.Unmarshal(raw, &launched) == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(launched.Args) != 1 || launched.Args[0] != client || launched.Cwd != dir {
		t.Fatalf("client received unexpected path, arguments, or working directory: %+v", launched)
	}
}
