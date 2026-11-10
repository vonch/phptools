//go:build windows

package sites

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckHostsReportsProblemsWithoutChangingFile(t *testing.T) {
	domains := []string{"health.test"}
	synced, err := HostsContent([]byte("# unrelated entry\r\n127.0.0.1 localhost\r\n"), domains)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name    string
		content []byte
		wantErr bool
	}{
		{"synced", synced, false},
		{"missing", []byte("127.0.0.1 localhost\r\n"), true},
		{"conflict", []byte("192.0.2.1 health.test\r\n"), true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			systemRoot := t.TempDir()
			t.Setenv("SystemRoot", systemRoot)
			path := filepath.Join(systemRoot, "System32", "drivers", "etc", "hosts")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, fixture.content, 0600); err != nil {
				t.Fatal(err)
			}
			if err := CheckHosts(domains); (err != nil) != fixture.wantErr {
				t.Fatalf("CheckHosts error=%v, want error=%v", err, fixture.wantErr)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, fixture.content) {
				t.Fatalf("read-only check changed hosts: %v", err)
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(entries) != 1 {
				t.Fatalf("check created unexpected files: %v %v", entries, err)
			}
		})
	}
}
