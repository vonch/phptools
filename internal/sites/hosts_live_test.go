package sites

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Explicit opt-in: ordinary go test never modifies system hosts.
func TestLiveHostsRoundTrip(t *testing.T) {
	if os.Getenv("PHPTOOLS_HOSTS_TEST") != "1" {
		t.Skip("opt-in system hosts acceptance")
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("requires an already elevated acceptance process")
	}
	root := t.TempDir()
	for _, dir := range []string{"backups", "cache"} {
		os.Mkdir(filepath.Join(root, dir), 0700)
	}
	original, err := os.ReadFile(hostsPath())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(original, []byte(hostsStart)) {
		_, managed, ok := strings.Cut(string(original), hostsStart)
		managed, _, ended := strings.Cut(managed, hostsEnd)
		owned := ok && ended
		for _, line := range strings.Split(managed, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if len(fields) != 2 || fields[0] != "127.0.0.1" || !strings.HasPrefix(fields[1], "phptools-acceptance-") || !strings.HasSuffix(fields[1], ".test") {
				owned = false
			}
		}
		if !owned {
			t.Skip("another PHPTools hosts block already exists")
		}
		if err = SyncHosts(root, nil); err != nil {
			t.Fatal(err)
		}
		original, err = os.ReadFile(hostsPath())
		if err != nil {
			t.Fatal(err)
		}
		t.Log("recovered previous acceptance-only hosts entry")
	}
	domains := []string{"phptools-acceptance-" + strings.ToLower(randomID()) + ".test"}
	expected, err := HostsContent(original, domains)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		current, err := os.ReadFile(hostsPath())
		if err != nil {
			t.Error(err)
			return
		}
		if !bytes.Equal(current, expected) {
			t.Error("hosts changed externally; leaving its current content intact")
			return
		}
		if err = SyncHosts(root, nil); err != nil {
			t.Error(err)
			return
		}
		restored, err := os.ReadFile(hostsPath())
		if err != nil || !bytes.Equal(restored, original) {
			t.Error("hosts restoration failed")
		}
	}()
	if err = SyncHosts(root, domains); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(hostsPath())
	if err != nil || !bytes.Equal(actual, expected) {
		t.Fatal("managed hosts update failed")
	}
}
