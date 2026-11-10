package sites

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"phptools/internal/model"
)

const activityTailSize = 64 << 10

var accessTimestamp = regexp.MustCompile(`^\S+ - \S+ \[([^\]]+)\] "`)

type activityEntry struct {
	size     int64
	modified time.Time
	value    string
}

// ActivityReader reuses parsed timestamps until a site's access log changes.
// It never writes logs, metadata or server configuration.
type ActivityReader struct {
	mu      sync.Mutex
	root    string
	entries map[string]activityEntry
}

func (r *ActivityReader) Apply(root string, all []model.Site) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if root != r.root {
		r.root, r.entries = root, nil
	}
	next := make(map[string]activityEntry, len(all))
	for i := range all {
		all[i].LastActiveAt = ""
		if root == "" || !idPattern.MatchString(all[i].ID) {
			continue
		}
		path := filepath.Join(root, "logs", all[i].ID+"-access.log")
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			continue
		}
		entry, cached := r.entries[all[i].ID]
		if !cached || entry.size != info.Size() || !entry.modified.Equal(info.ModTime()) {
			value, err := lastRequestTime(path, info.Size())
			if err != nil {
				continue
			}
			entry = activityEntry{size: info.Size(), modified: info.ModTime(), value: value}
		}
		next[all[i].ID] = entry
		all[i].LastActiveAt = entry.value
	}
	r.entries = next
}

func lastRequestTime(path string, size int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	offset := max(int64(0), size-activityTailSize)
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	raw, err := io.ReadAll(io.LimitReader(file, activityTailSize))
	if err != nil {
		return "", err
	}
	lines := bytes.Split(raw, []byte{'\n'})
	// Skip a partial first line and the final line until its newline is written.
	start := 0
	if offset > 0 {
		start = 1
	}
	for i := len(lines) - 2; i >= start; i-- {
		match := accessTimestamp.FindSubmatch(lines[i])
		if match == nil {
			continue
		}
		stamp, err := time.Parse("02/Jan/2006:15:04:05 -0700", string(match[1]))
		if err == nil {
			return stamp.UTC().Format(time.RFC3339), nil
		}
	}
	return "", nil
}
