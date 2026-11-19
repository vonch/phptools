package desktop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"phptools/internal/model"
)

func TestCompareUpdateVersion(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.1.18", "0.1.17", 1}, {"0.1.18", "0.1.18", 0},
		{"1.0.0", "0.99.99", 1}, {"0.2.0", "0.10.0", -1},
	} {
		got, err := compareUpdateVersion(tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Fatalf("compare(%q,%q)=%d,%v", tc.a, tc.b, got, err)
		}
	}
	for _, value := range []string{"v1.0.0", "1.0", "1.0.0-beta", "01.0.0", "1.0.0.1", "9999999999999999.0.0", " 1.0.0"} {
		if _, err := compareUpdateVersion(value, "0.1.18"); err == nil {
			t.Fatalf("accepted invalid release version %q", value)
		}
	}
}

func updateTestPayload(t *testing.T) []byte {
	t.Helper()
	dos := make([]byte, 128)
	copy(dos, "MZ")
	binary.LittleEndian.PutUint32(dos[0x3c:], 128)
	var buf bytes.Buffer
	buf.Write(dos)
	buf.WriteString("PE\x00\x00")
	header := pe.FileHeader{Machine: pe.IMAGE_FILE_MACHINE_AMD64, SizeOfOptionalHeader: uint16(binary.Size(pe.OptionalHeader64{})), Characteristics: pe.IMAGE_FILE_EXECUTABLE_IMAGE}
	optional := pe.OptionalHeader64{Magic: 0x20b, NumberOfRvaAndSizes: 16}
	if err := binary.Write(&buf, binary.LittleEndian, header); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(&buf, binary.LittleEndian, optional); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func updateTestInfo(url string, data []byte) model.UpdateInfo {
	sum := sha256.Sum256(data)
	return model.UpdateInfo{CurrentVersion: appVersion, LatestVersion: "9.9.9", Configured: true, Available: true, DownloadURL: url, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
}

func TestCheckForUpdateCachesFailuresAndNeverTreatsThemAsLatest(t *testing.T) {
	a, err := New(t.TempDir(), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	var fail atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(updateManifest{Version: "9.9.9", DownloadURL: "https://updates.example/PHPTools.exe", SHA256: strings.Repeat("a", 64), Size: 1024, Notes: "更新说明"})
	}))
	t.Cleanup(server.Close)
	a.updateClient = server.Client()
	a.data.Settings.UpdateManifestURL = server.URL
	info, err := a.CheckForUpdate()
	if err != nil || !info.Available || info.LatestVersion != "9.9.9" || info.CheckedAt == "" {
		t.Fatalf("bad check: %+v %v", info, err)
	}
	if cached := a.GetUpdateInfo(); cached != info {
		t.Fatalf("lost update cache: %+v", cached)
	}
	fail.Store(true)
	info, err = a.CheckForUpdate()
	if err == nil || info.Available || !info.Configured || a.GetUpdateInfo().Error == "" {
		t.Fatalf("failed check claimed latest: %+v %v", info, err)
	}
	if strings.Contains(info.Error, "https://") || strings.Contains(info.Error, server.URL) || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("update error exposed the address: %v", err)
	}
}

func TestInstallUpdateRefreshesReplacedPackageBeforeDownload(t *testing.T) {
	a, err := New(t.TempDir(), Hooks{Exit: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	var downloads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/update.json" {
			downloads.Add(1)
			http.Error(w, "unexpected download", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(updateManifest{
			Version: "9.9.9", DownloadURL: "https://updates.example/PHPTools.exe",
			SHA256: strings.Repeat("ab", 32), Size: 19012608, Notes: "新版本",
		})
	}))
	t.Cleanup(server.Close)
	a.updateClient = server.Client()
	a.data.Settings.UpdateManifestURL = server.URL
	a.data.UpdateInfo = &model.UpdateInfo{
		CurrentVersion: appVersion, LatestVersion: "0.1.47", Available: true, Configured: true,
		DownloadURL: "https://updates.example/PHPTools.exe", SHA256: strings.Repeat("cd", 32), Size: 19001344,
	}
	if _, err = a.InstallUpdate(); err == nil || !strings.Contains(err.Error(), "官网安装包已更新") {
		t.Fatal(err)
	}
	if downloads.Load() != 0 {
		t.Fatal("replaced package was downloaded before confirmation")
	}
	info := a.GetUpdateInfo()
	if info.LatestVersion != "9.9.9" || info.Size != 19012608 || info.SHA256 != strings.Repeat("ab", 32) || !info.Available {
		t.Fatalf("%+v", info)
	}
}

func TestBuiltinUpdateSourceIgnoresSavedAddresses(t *testing.T) {
	for _, raw := range []string{
		"",
		"{\n  \"root\": \"\"\n}\n",
		"{\n  \"root\": \"\",\n  \"updateManifestUrl\": \"\"\n}\n",
		"{\n  \"root\": \"\",\n  \"updateManifestUrl\": \"https://updates.example/manifest.json\"\n}\n",
	} {
		dir := t.TempDir()
		if raw != "" {
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
		}
		a, err := New(dir, Hooks{})
		if err != nil {
			t.Fatal(err)
		}
		if a.data.Settings.UpdateManifestURL != defaultUpdateManifestURL {
			t.Fatalf("source %q became %s", raw, a.data.Settings.UpdateManifestURL)
		}
		if info := a.GetUpdateInfo(); !info.Configured {
			t.Fatal("built-in source was treated as unconfigured")
		}
		encoded, err := json.Marshal(a.data.Settings)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "http") || strings.Contains(string(encoded), "wuyouzhilian") || strings.Contains(string(encoded), "updateManifestUrl") {
			t.Fatalf("settings sent to the interface include an address: %s", encoded)
		}
		release, err := json.Marshal(model.UpdateInfo{DownloadURL: defaultUpdateManifestURL, Configured: true})
		if err != nil || strings.Contains(string(release), "http") || strings.Contains(string(release), "wuyouzhilian") || strings.Contains(string(release), "downloadUrl") {
			t.Fatalf("update info sent to the interface includes an address: %s %v", release, err)
		}
		saved, err := os.ReadFile(filepath.Join(dir, "settings.json"))
		if raw == "" {
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("new profile created settings: %s %v", saved, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(saved), "updateManifestUrl") || strings.Contains(string(saved), "http") || strings.Contains(string(saved), "wuyouzhilian") {
			t.Fatalf("settings file kept an update address: %s", saved)
		}
	}
	leaked := redactUpdateAddress(errors.New(`Get "https://phptools.wuyouzhilian.cn/update.json": dial tcp: lookup phptools.wuyouzhilian.cn: no such host`))
	if strings.Contains(leaked.Error(), "wuyouzhilian") || strings.Contains(leaked.Error(), "https://") {
		t.Fatalf("update error kept the address: %v", leaked)
	}
}

func TestRemovingSavedUpdateAddressKeepsEnvironmentRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(root, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "config")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"root": root, "updateManifestUrl": "https://updates.example/manifest.json"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config, "settings.json"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := New(config, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if a.data.Settings.Root != "" || a.data.Settings.UpdateManifestURL != defaultUpdateManifestURL {
		t.Fatalf("failed environment changed: %+v", a.data.Settings)
	}
	if len(a.data.Diagnostics) == 0 {
		t.Fatal("missing diagnostic for the unopenable environment")
	}
	saved, err := os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]string
	if err = json.Unmarshal(saved, &values); err != nil || values["root"] != root || values["updateManifestUrl"] != "" {
		t.Fatalf("environment root was not preserved without the address: %s %v", saved, err)
	}
	if strings.Contains(string(saved), "http") || strings.Contains(string(saved), "updateManifestUrl") {
		t.Fatalf("settings still contain an update address: %s", saved)
	}
}

func TestSettingsFileDoesNotStoreUpdateAddress(t *testing.T) {
	a, err := New(filepath.Join(t.TempDir(), "config"), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	a.data.Settings.UpdateManifestURL = "https://updates.example/manifest.json"
	if err = a.SetRoot(filepath.Join(t.TempDir(), "runtime")); err != nil {
		t.Fatal(err)
	}
	if err = a.db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(a.configDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Root string `json:"root"`
	}
	if err = json.Unmarshal(data, &saved); err != nil || saved.Root != a.data.Settings.Root {
		t.Fatalf("settings lost the environment root: %s %v", data, err)
	}
	if strings.Contains(string(data), "updateManifestUrl") || strings.Contains(string(data), "https://") || strings.Contains(string(data), "wuyouzhilian") {
		t.Fatalf("settings stored an update address: %s", data)
	}
	b, err := New(a.configDir, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.db.Close() })
	if b.data.Settings.UpdateManifestURL != defaultUpdateManifestURL || b.data.Settings.Root != a.data.Settings.Root {
		t.Fatalf("reload changed the built-in source or root: %+v", b.data.Settings)
	}
}

func TestUpdateRejectsUnsafeOrMalformedManifests(t *testing.T) {
	for _, value := range []string{"http://updates.example/a", "https://name:secret@updates.example/a", "file:///a", "https://updates.example/a#part", "https:///a"} {
		if err := validateUpdateURL(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	var requests atomic.Int32
	base := updateManifest{Version: "0.1.19", DownloadURL: "https://updates.example/PHPTools.exe", SHA256: strings.Repeat("b", 64)}
	for _, tc := range []struct {
		name   string
		change func(*updateManifest)
	}{
		{"invalid-version", func(m *updateManifest) { m.Version = "1.2" }},
		{"invalid-hash", func(m *updateManifest) { m.SHA256 = strings.Repeat("x", 64) }},
		{"http-payload", func(m *updateManifest) { m.DownloadURL = "http://updates.example/PHPTools.exe" }},
		{"oversized-payload", func(m *updateManifest) { m.Size = maxUpdateSize + 1 }},
		{"negative-size", func(m *updateManifest) { m.Size = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			tc.change(&m)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _ = json.NewEncoder(w).Encode(m) }))
			defer server.Close()
			info, err := fetchUpdate(t.Context(), server.Client(), server.URL, appVersion)
			if err == nil || info.Available {
				t.Fatalf("accepted invalid manifest: %+v %v", info, err)
			}
		})
	}
	info, err := fetchUpdate(t.Context(), &http.Client{}, "", appVersion)
	if err != nil || info.Configured || info.CheckedAt != "" {
		t.Fatalf("unconfigured check: %+v %v", info, err)
	}
	if requests.Load() != 5 {
		t.Fatalf("unexpected requests: %d", requests.Load())
	}
}

func TestUpdateRejectsHTTPSDowngradeRedirect(t *testing.T) {
	var insecureRequests atomic.Int32
	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { insecureRequests.Add(1) }))
	defer insecure.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, insecure.URL, http.StatusFound) }))
	defer secure.Close()
	a := &App{updateClient: secure.Client()}
	if _, err := fetchUpdate(t.Context(), a.updateTransport(time.Second), secure.URL, appVersion); err == nil {
		t.Fatal("accepted HTTPS downgrade")
	}
	if insecureRequests.Load() != 0 {
		t.Fatal("contacted insecure redirect")
	}
}

func TestDownloadUpdateChecksContentAndRemovesFailedFiles(t *testing.T) {
	payload := updateTestPayload(t)
	for _, tc := range []struct {
		name   string
		data   []byte
		change func(*model.UpdateInfo)
	}{
		{"valid", payload, nil},
		{"wrong-hash", payload, func(i *model.UpdateInfo) { i.SHA256 = strings.Repeat("0", 64) }},
		{"wrong-size", payload, func(i *model.UpdateInfo) { i.Size++ }},
		{"not-exe", []byte("not an executable"), nil},
		{"empty", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(tc.data) }))
			defer server.Close()
			info := updateTestInfo(server.URL, tc.data)
			if tc.change != nil {
				tc.change(&info)
			}
			target := filepath.Join(t.TempDir(), "download.exe")
			var progress int64
			err := downloadUpdate(t.Context(), server.Client(), info, target, func(done, total int64) { progress = done })
			if tc.name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(target)
				if err != nil || !bytes.Equal(data, payload) || progress != int64(len(payload)) {
					t.Fatalf("bad payload or progress: %d %v", progress, err)
				}
			} else {
				if err == nil {
					t.Fatal("accepted invalid update")
				}
				if _, err = os.Stat(target); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed download left file: %v", err)
				}
			}
		})
	}
}

func TestDownloadCancellationAndOversizedHeaderLeaveNoFile(t *testing.T) {
	payload := updateTestPayload(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large" {
			w.Header().Set("Content-Length", "536870913")
			return
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	target := filepath.Join(t.TempDir(), "cancelled.exe")
	err := downloadUpdate(ctx, server.Client(), updateTestInfo(server.URL, payload), target, func(int64, int64) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("not cancelled: %v", err)
	}
	if _, err = os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancel left file: %v", err)
	}
	info := updateTestInfo(server.URL+"/large", payload)
	if err = downloadUpdate(t.Context(), server.Client(), info, target, nil); err == nil {
		t.Fatal("accepted oversized content-length")
	}
	if _, err = os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("large response left file: %v", err)
	}
}

func TestUpdateExecutableRejectsDLLAndWrongArchitecture(t *testing.T) {
	for _, tc := range []struct {
		name           string
		machine, flags uint16
	}{
		{"x86", pe.IMAGE_FILE_MACHINE_I386, pe.IMAGE_FILE_EXECUTABLE_IMAGE},
		{"dll", pe.IMAGE_FILE_MACHINE_AMD64, pe.IMAGE_FILE_EXECUTABLE_IMAGE | pe.IMAGE_FILE_DLL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := updateTestPayload(t)
			binary.LittleEndian.PutUint16(payload[132:], tc.machine)
			binary.LittleEndian.PutUint16(payload[150:], tc.flags)
			path := filepath.Join(t.TempDir(), "invalid.exe")
			if err := os.WriteFile(path, payload, 0600); err != nil {
				t.Fatal(err)
			}
			if err := validateUpdateExecutable(path); err == nil {
				t.Fatal("accepted wrong executable kind")
			}
		})
	}
}

func TestUpdateTaskCanQuitWithoutWaitingOnItselfOrRequiringRoot(t *testing.T) {
	exited := make(chan struct{})
	a, err := New(t.TempDir(), Hooks{Exit: func() { close(exited) }})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	_, err = a.queueTask("app-update", "0.1.19", func(context.Context, string) error { return nil }, func(workErr error) error {
		if workErr != nil {
			finished <- workErr
			return workErr
		}
		err := a.quit(func() error { return nil })
		finished <- err
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("update waited on its own worker")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("exit hook not called")
	}
	a.mu.RLock()
	done := a.updateDone
	a.mu.RUnlock()
	<-done
}

func TestUpdateHandoffWaitsForAcceptedCancellation(t *testing.T) {
	a, err := New(t.TempDir(), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	returnWork := make(chan struct{})
	workReturned := make(chan struct{})
	cancelEntered := make(chan struct{})
	releaseCancel := make(chan struct{})
	afterResult := make(chan error, 1)
	id, err := a.queueTask("app-update", "0.1.19", func(context.Context, string) error {
		<-returnWork
		close(workReturned)
		return nil
	}, func(err error) error {
		afterResult <- err
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	originalCancel := a.jobs[id]
	done := a.updateDone
	a.jobs[id] = func() {
		close(cancelEntered)
		<-releaseCancel
		originalCancel()
	}
	a.mu.Unlock()
	release := func(ch chan struct{}) {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
	t.Cleanup(func() {
		release(returnWork)
		release(releaseCancel)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("update worker survived cancellation cleanup")
		}
	})
	cancelResult := make(chan error, 1)
	go func() { cancelResult <- a.CancelTask(id) }()
	select {
	case <-cancelEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("CancelTask never entered the accepted cancellation")
	}
	release(returnWork)
	select {
	case <-workReturned:
	case <-time.After(3 * time.Second):
		t.Fatal("download work did not finish")
	}
	select {
	case err := <-afterResult:
		t.Fatalf("update handoff passed an unfinished accepted cancellation: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release(releaseCancel)
	select {
	case err := <-cancelResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CancelTask did not finish")
	}
	select {
	case err := <-afterResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("accepted cancellation was lost at update handoff: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled update did not reach cleanup")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled update did not finish")
	}
	if task := a.data.Tasks[0]; task.ID != id || task.State != "cancelled" {
		t.Fatalf("accepted cancellation was not reported: %+v", task)
	}
	if err := a.CancelTask(id); err == nil {
		t.Fatal("cancellation was accepted after task handoff")
	}
}

func TestCancelledUpdateReportsCleanupOwnershipFailure(t *testing.T) {
	var exited atomic.Bool
	a, err := New(t.TempDir(), Hooks{Exit: func() { exited.Store(true) }})
	if err != nil {
		t.Fatal(err)
	}
	installation := t.TempDir()
	executable := filepath.Join(installation, "PHPTools.exe")
	payload := updateTestPayload(t)
	if err := os.WriteFile(executable, payload, 0600); err != nil {
		t.Fatal(err)
	}
	requestStarted := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	a.updateClient = server.Client()
	id, err := a.installUpdateAt(updateTestInfo(server.URL, payload), executable)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.RLock()
	done := a.updateDone
	a.mu.RUnlock()
	t.Cleanup(func() {
		_ = a.CancelTask(id)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("cancelled download worker survived cleanup")
		}
	})
	select {
	case <-requestStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("update did not reach the blocked download")
	}
	paths, err := filepath.Glob(filepath.Join(installation, ".phptools-update-*"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("expected one owned staging directory: %v %v", paths, err)
	}
	staging := paths[0]
	if err := os.Rename(staging, staging+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(staging, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(staging, "replacement-owner.txt")
	const content = "replacement directory must be preserved"
	if err := os.WriteFile(sentinel, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.CancelTask(id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled update did not finish cleanup")
	}
	if task := a.data.Tasks[0]; task.ID != id || task.State != "failed" || !strings.Contains(task.Error, staging) || !strings.Contains(task.Error, "清理") {
		t.Fatalf("cleanup failure was hidden as successful cancellation: %+v", task)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != content {
		t.Fatalf("cleanup changed a replacement directory: %q %v", data, err)
	}
	if data, err := os.ReadFile(executable); err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("cancelled update changed the installed executable: %v", err)
	}
	if exited.Load() || a.closing || a.updating {
		t.Fatalf("failed cleanup left an exit or upgrade in progress: exit=%v closing=%v updating=%v", exited.Load(), a.closing, a.updating)
	}
	if _, err := a.queueTask("app-update", "retry", func(context.Context, string) error { return nil }, nil); err != nil {
		t.Fatalf("cleanup failure prevented retry: %v", err)
	}
	a.mu.RLock()
	retryDone := a.updateDone
	a.mu.RUnlock()
	select {
	case <-retryDone:
	case <-time.After(3 * time.Second):
		t.Fatal("retry task did not finish")
	}
}

func TestNormalQuitWaitsForUpdateCancellationCleanup(t *testing.T) {
	exited := make(chan struct{})
	a, err := New(t.TempDir(), Hooks{Exit: func() { close(exited) }})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	cleaning := make(chan struct{})
	release := make(chan struct{})
	_, err = a.queueTask("app-update", "0.1.19", func(ctx context.Context, _ string) error { close(started); <-ctx.Done(); return ctx.Err() }, func(err error) error { close(cleaning); <-release; return err })
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err = a.queueTask("app-update", "0.1.19", func(context.Context, string) error { return nil }, nil); err == nil {
		t.Fatal("duplicate update accepted")
	}
	if _, err = a.queueTask("refresh", "catalog", func(context.Context, string) error { return nil }, nil); err == nil {
		t.Fatal("concurrent mutation accepted")
	}
	quitDone := make(chan error, 1)
	go func() { quitDone <- a.Quit() }()
	select {
	case <-cleaning:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("update was not cancelled")
	}
	select {
	case <-exited:
		close(release)
		t.Fatal("exited before temporary-file cleanup")
	default:
	}
	close(release)
	select {
	case err = <-quitDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("normal quit deadlocked")
	}
}

func TestFailedUpdateShutdownKeepsManagerOpenAndSkipsHelper(t *testing.T) {
	a := testApp(t)
	_, instance := desktopHealthFixture(t, a, "php")
	instance.PID, instance.State = os.Getpid(), "running"
	if err := a.saveInstance(instance); err != nil {
		t.Fatal(err)
	}
	helperStarted := false
	exited := false
	a.hooks.Exit = func() { exited = true }
	err := a.quit(func() error { helperStarted = true; return nil })
	if err == nil || helperStarted || exited || a.closing {
		t.Fatalf("unsafe shutdown: %v helper=%v exit=%v closing=%v", err, helperStarted, exited, a.closing)
	}
	if _, err = a.GetState(); err != nil {
		t.Fatalf("manager not available after failed stop: %v", err)
	}
}

func TestUpdateHelperPreparationFailureAllowsRetry(t *testing.T) {
	a := testApp(t)
	exited := false
	a.hooks.Exit = func() { exited = true }
	err := a.quit(func() error { return errors.New("helper unavailable") })
	if err == nil || exited || a.closing {
		t.Fatalf("helper failure closed application: %v", err)
	}
	if _, err = a.CheckEnvironment(); err != nil {
		t.Fatalf("application cannot retry: %v", err)
	}
	a.wg.Wait()
}

func TestUpdateResultReportsRecoveryPathAndIsConsumed(t *testing.T) {
	config := t.TempDir()
	path := filepath.Join(config, "update-result.json")
	raw := []byte(`{"version":"0.1.19","success":false,"message":"replacement failed","backupPath":"saved/previous.exe"}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := New(config, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.data.Diagnostics) != 1 || !strings.Contains(a.data.Diagnostics[0].Message, "saved/previous.exe") {
		t.Fatalf("missing rollback report: %+v", a.data.Diagnostics)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("result not consumed: %v", err)
	}
}

func TestSuccessfulUpdateResultRetainsCleanupWarning(t *testing.T) {
	config := t.TempDir()
	path := filepath.Join(config, "update-result.json")
	raw := []byte(`{"version":"0.1.19","success":true,"message":"installed","warning":"backup is locked","backupPath":"saved/previous.exe","stagingPath":"saved"}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := New(config, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.data.Diagnostics) != 1 {
		t.Fatalf("missing successful-update cleanup warning: %+v", a.data.Diagnostics)
	}
	diagnostic := a.data.Diagnostics[0]
	if diagnostic.Level != "warning" || !strings.Contains(diagnostic.Message, "backup is locked") || !strings.Contains(diagnostic.Message, "saved/previous.exe") || !strings.Contains(diagnostic.Message, "保留目录：saved") {
		t.Fatalf("cleanup warning lost its recovery details: %+v", diagnostic)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("result not consumed: %v", err)
	}
}
