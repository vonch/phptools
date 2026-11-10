package sites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"phptools/internal/model"
)

func accessLine(stamp string) string {
	return `127.0.0.1 - - [` + stamp + `] "GET / HTTP/1.1" 200 12 "-" "browser"` + "\r\n"
}

func TestActivityUsesRequestTimestampAndRefreshesChangedLogs(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	if err := os.Mkdir(logs, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logs, "one-access.log")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var reader ActivityReader
	all := []model.Site{{ID: "one", CreatedAt: "2026-10-01T00:00:00Z"}, {ID: "missing"}}
	first := accessLine("19/Oct/2026:23:45:12 +0800")
	write(first)
	// A copied log's modification date is not its most recent request.
	touch := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, touch, touch); err != nil {
		t.Fatal(err)
	}
	reader.Apply(root, all)
	if all[0].LastActiveAt != "2026-10-19T15:45:12Z" || all[1].LastActiveAt != "" {
		t.Fatalf("unexpected activity: %+v", all)
	}
	reader.Apply(root, all)
	if all[0].CreatedAt != "2026-10-01T00:00:00Z" {
		t.Fatal("activity changed the addition date")
	}
	write(first + accessLine("20/Oct/2026:08:00:00 +0800"))
	reader.Apply(root, all)
	if all[0].LastActiveAt != "2026-10-20T00:00:00Z" {
		t.Fatal("new requests did not update activity")
	}
	write(accessLine("21/Oct/2026:09:00:00 +0800"))
	reader.Apply(root, all)
	if all[0].LastActiveAt != "2026-10-21T01:00:00Z" {
		t.Fatal("truncated log was not reread")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reader.Apply(root, all)
	if all[0].LastActiveAt != "" {
		t.Fatal("removed log retained an unsupported activity date")
	}
}

func TestActivityReadsOnlyCompleteRequestLinesWithinBoundedTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	for name, input := range map[string]string{
		"normal":  accessLine("20/Oct/2026:09:15:00 +0800"),
		"large":   strings.Repeat("old data\n", activityTailSize) + accessLine("20/Oct/2026:09:15:00 +0800"),
		"partial": accessLine("20/Oct/2026:09:15:00 +0800") + strings.TrimSpace(accessLine("20/Oct/2026:12:00:00 +0800")),
		"invalid": accessLine("20/Oct/2026:09:15:00 +0800") + "broken [21/Oct/2026:12:00:00 +0800] log\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := lastRequestTime(path, int64(len(input)))
			if err != nil || got != "2026-10-20T01:15:00Z" {
				t.Fatalf("timestamp = %q, error = %v", got, err)
			}
		})
	}
}
