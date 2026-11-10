//go:build windows

// Command port-smoke verifies Nginx port protection in a new isolated runtime.
// It copies an already accepted official package and never opens user sites.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"phptools/internal/acceptance"
	"phptools/internal/model"
	"phptools/internal/services"
)

type check struct {
	Name    string `json:"name"`
	Details string `json:"details"`
}

type report struct {
	Started   string         `json:"started"`
	Completed string         `json:"completed"`
	Root      string         `json:"root"`
	Checks    []check        `json:"checks"`
	Instance  model.Instance `json:"instance"`
	Error     string         `json:"error,omitempty"`
}

func main() {
	root := flag.String("root", "", "new disposable runtime (default: system temporary directory); must not already exist")
	source := flag.String("source", "", "required read-only source directory containing an installed official Nginx package")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, *root, *source); err != nil {
		fmt.Fprintln(os.Stderr, "PORT REGRESSION FAILED:", err)
		os.Exit(1)
	}
}

func run(parent context.Context, root, source string) (runErr error) {
	if !filepath.IsAbs(source) {
		return errors.New("-source must explicitly name an absolute read-only official Nginx package directory")
	}
	source, err := filepath.EvalSymlinks(filepath.Clean(source))
	if err != nil {
		return fmt.Errorf("resolve read-only source: %w", err)
	}
	reportPath, err := filepath.Abs(filepath.Join("build", "qa", "port-regression-report.json"))
	if err != nil {
		return err
	}
	temporary, err := acceptance.NewRoot(root, "port-regression")
	if err != nil {
		return err
	}
	root = temporary.Path
	reportAllowed := !pathWithin(root, reportPath)
	r := report{Started: model.Now(), Root: root, Checks: []check{}}
	record := func(name, details string) {
		r.Checks = append(r.Checks, check{Name: name, Details: details})
		fmt.Println("PASS", name, details)
	}
	defer func() {
		if !reportAllowed {
			return
		}
		r.Completed = model.Now()
		if runErr != nil {
			r.Error = runErr.Error()
		}
		data, err := json.MarshalIndent(r, "", "  ")
		if err == nil {
			err = os.MkdirAll(filepath.Dir(reportPath), 0700)
		}
		if err == nil {
			err = os.WriteFile(reportPath, data, 0600)
		}
		runErr = errors.Join(runErr, err)
	}()
	defer func() { runErr = errors.Join(runErr, temporary.Cleanup()) }()
	if !reportAllowed {
		return errors.New("acceptance report must be outside the disposable runtime")
	}
	if pathWithin(source, root) || pathWithin(root, source) {
		return errors.New("read-only source and disposable runtime must not overlap")
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	pkg := model.Package{ID: "nginx-1.31.7-windows", Kind: "nginx", Version: "1.31.7", Verified: true, Path: filepath.Join(root, "packages", "nginx-1.31.7")}
	if err := copyPackage(source, pkg.Path); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	record("isolated package", source+" -> "+pkg.Path)
	m := services.New(root, func(i model.Instance) error { r.Instance = i; return nil }, func(string) (string, error) { return "", errors.New("Nginx must not request secrets") })
	a, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	portA := a.Addr().(*net.TCPAddr).Port
	b, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		a.Close()
		return err
	}
	defer b.Close()
	portB := b.Addr().(*net.TCPAddr).Port
	if err := a.Close(); err != nil {
		return err
	}
	i, err := m.Create(ctx, pkg, model.InstanceInput{Name: "isolated port regression", PackageID: pkg.ID, Port: portA})
	if err != nil {
		return err
	}
	r.Instance = i
	defer func() {
		current := m.Inspect(i)
		if current.State != "stopped" {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer stopCancel()
			_, err := m.Stop(stopCtx, current)
			runErr = errors.Join(runErr, err)
		}
	}()
	siteB := filepath.Join(root, "sites", "existing.conf")
	if err := writeSite(siteB, portB, "original site"); err != nil {
		return err
	}
	i, err = m.Start(ctx, pkg, i)
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(portB)) || i.PID != 0 {
		return fmt.Errorf("occupied included site did not block Start: instance=%+v error=%v", i, err)
	}
	if pids, err := ownedProcesses(root); err != nil || len(pids) != 0 {
		return fmt.Errorf("rejected Start left owned processes: %v, %v", pids, err)
	}
	if err := assertExternal(b); err != nil {
		return err
	}
	record("included site rejects occupied port before launch", fmt.Sprintf("main=%d site=%d external PID=%d; %v", portA, portB, os.Getpid(), err))
	if err := b.Close(); err != nil {
		return err
	}
	// Preserve the same included file, now with its port released by the test.
	if err := writeSite(siteB, portB, "original site"); err != nil {
		return err
	}
	i, err = m.Start(ctx, pkg, i)
	if err != nil {
		return err
	}
	if err := expectHTTP(ctx, portA, "/__phptools", "PHPTools is running"); err != nil {
		return err
	}
	if err := expectHTTP(ctx, portB, "/", "original site"); err != nil {
		return err
	}
	owned, err := ownedProcesses(root)
	if err != nil || len(owned) < 2 {
		return fmt.Errorf("expected real master and worker: %v, %v", owned, err)
	}
	if err := m.ValidateConfig(ctx, pkg, i); err != nil {
		return fmt.Errorf("owned listener validation: %w", err)
	}
	if err := m.Reload(ctx, pkg, i); err != nil {
		return fmt.Errorf("owned listener reload: %w", err)
	}
	record("real master and worker permitted", fmt.Sprintf("main=%d site=%d master=%d owned=%v; ValidateConfig and Reload succeeded", portA, portB, i.PID, owned))
	c, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		return err
	}
	defer c.Close()
	portC := c.Addr().(*net.TCPAddr).Port
	siteC := filepath.Join(root, "sites", "new.conf")
	if err := writeSite(siteC, portC, "new site"); err != nil {
		return err
	}
	for name, operation := range map[string]func() error{
		"ValidateConfig": func() error { return m.ValidateConfig(ctx, pkg, i) },
		"Reload":         func() error { return m.Reload(ctx, pkg, i) },
	} {
		if err := operation(); err == nil || !strings.Contains(err.Error(), strconv.Itoa(portC)) {
			return fmt.Errorf("%s accepted occupied new site port %d: %v", name, portC, err)
		}
		if err := assertExternal(c); err != nil {
			return err
		}
		current := m.Inspect(i)
		if current.State != "running" || current.PID != i.PID {
			return fmt.Errorf("%s disturbed original process: %+v", name, current)
		}
		if err := expectHTTP(ctx, portA, "/__phptools", "PHPTools is running"); err != nil {
			return err
		}
		if err := expectHTTP(ctx, portB, "/", "original site"); err != nil {
			return err
		}
		record(name+" rejects new occupied site", fmt.Sprintf("new=%d external PID=%d accepts loopback proof; original master=%d and both HTTP pages healthy", portC, os.Getpid(), current.PID))
	}
	if err := c.Close(); err != nil {
		return err
	}
	// Do not change new.conf: freeing the independent socket is the only change.
	if err := m.Reload(ctx, pkg, i); err != nil {
		return err
	}
	if err := expectHTTP(ctx, portC, "/", "new site"); err != nil {
		return err
	}
	if err := expectHTTP(ctx, portA, "/__phptools", "PHPTools is running"); err != nil {
		return err
	}
	record("released external port permits same-file reload", fmt.Sprintf("new=%d returns new site; original health remains available", portC))
	stale := i
	i, err = m.Stop(ctx, i)
	if err != nil {
		return err
	}
	if err := waitNoOwned(ctx, root); err != nil {
		return err
	}
	if i.PID != 0 || i.State != "stopped" {
		return fmt.Errorf("unexpected stopped state: %+v", i)
	}
	record("normal Stop removes all owned processes", "master and workers exited; process scan contains no executable beneath isolated runtime")
	// Reproduce saved metadata outliving the ownership file after shutdown.
	stale.State = "error"
	stale.Error = "缺少可信进程记录，拒绝操作该进程"
	current := m.Inspect(stale)
	if current.State != "stopped" || current.PID != 0 || current.ProcessCreated != 0 || current.Error != "" {
		return fmt.Errorf("stale identity did not recover: %+v", current)
	}
	i, err = m.Start(ctx, pkg, stale)
	if err != nil {
		return fmt.Errorf("restart after missing ownership record: %w", err)
	}
	if err := expectHTTP(ctx, portA, "/__phptools", "PHPTools is running"); err != nil {
		return err
	}
	record("stale PID with missing record restarts", fmt.Sprintf("old master=%d new master=%d; HTTP healthy", stale.PID, i.PID))
	processPath := filepath.Join(filepath.Dir(filepath.Dir(i.ConfigPath)), "process.json")
	processData, err := os.ReadFile(processPath)
	if err != nil {
		return err
	}
	if err := os.Remove(processPath); err != nil {
		return err
	}
	// Restore the real record even if a live-process protection check fails.
	protectionErr := func() error {
		if current := m.Inspect(i); current.State != "error" || current.PID != i.PID {
			return fmt.Errorf("live Nginx without record was not protected: %+v", current)
		}
		if _, err := m.Start(ctx, pkg, i); err == nil {
			return errors.New("Start accepted a live Nginx without ownership record")
		}
		if _, err := m.Stop(ctx, i); err == nil {
			return errors.New("Stop accepted a live Nginx without ownership record")
		}
		return expectHTTP(ctx, portA, "/__phptools", "PHPTools is running")
	}()
	if err := os.WriteFile(processPath, processData, 0600); err != nil {
		return errors.Join(protectionErr, err)
	}
	if protectionErr != nil {
		return protectionErr
	}
	record("live Nginx without record remains protected", fmt.Sprintf("Inspect, Start and Stop rejected unverified master=%d; HTTP still healthy", i.PID))
	stale = i
	i, err = m.Stop(ctx, i)
	if err != nil {
		return err
	}
	if err := waitNoOwned(ctx, root); err != nil {
		return err
	}
	if err := os.WriteFile(processPath, []byte("{interrupted write"), 0600); err != nil {
		return err
	}
	i, err = m.Start(ctx, pkg, stale)
	if err != nil {
		return fmt.Errorf("restart after corrupt ownership record: %w", err)
	}
	if err := expectHTTP(ctx, portA, "/__phptools", "PHPTools is running"); err != nil {
		return err
	}
	record("stale PID with corrupt record restarts", fmt.Sprintf("new master=%d; HTTP healthy and ownership record replaced", i.PID))
	i, err = m.Stop(ctx, i)
	if err != nil {
		return err
	}
	if err := waitNoOwned(ctx, root); err != nil {
		return err
	}
	record("recovery checks leave no processes", "all isolated Nginx masters and workers stopped normally")
	return nil
}

func pathWithin(parent, path string) bool {
	relative, err := filepath.Rel(parent, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func copyPackage(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("package contains a link: %s", path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("package contains a nonregular file: %s", path)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		return errors.Join(copyErr, output.Close())
	})
}

func writeSite(path string, port int, body string) error {
	contents := fmt.Sprintf("server { listen 127.0.0.1:%d; server_name port-regression.local; location / { return 200 '%s'; add_header Content-Type text/plain; } }\n", port, body)
	return os.WriteFile(path, []byte(contents), 0600)
}

func assertExternal(listener net.Listener) error {
	tcp := listener.(*net.TCPListener)
	if err := tcp.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	defer tcp.SetDeadline(time.Time{})
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(tcp.Addr().(*net.TCPAddr).Port))
	client, err := net.DialTimeout("tcp4", address, time.Second)
	if err != nil {
		return fmt.Errorf("external socket no longer accepts loopback: %w", err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	proof := fmt.Sprintf("external-socket-%d-%d", os.Getpid(), time.Now().UnixNano())
	if _, err := io.WriteString(client, proof); err != nil {
		return err
	}
	accepted, err := tcp.Accept()
	if err != nil {
		return fmt.Errorf("original external listener could not accept its own connection: %w", err)
	}
	defer accepted.Close()
	if err := accepted.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	buffer := make([]byte, len(proof))
	if _, err := io.ReadFull(accepted, buffer); err != nil {
		return err
	}
	if string(buffer) != proof {
		return errors.New("external listener received another payload")
	}
	if _, err := accepted.Write(buffer); err != nil {
		return err
	}
	if _, err := io.ReadFull(client, buffer); err != nil {
		return err
	}
	if string(buffer) != proof {
		return errors.New("external listener response changed")
	}
	return nil
}

func expectHTTP(ctx context.Context, port int, path, expected string) error {
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	url := "http://127.0.0.1:" + strconv.Itoa(port) + path
	deadline := time.Now().Add(8 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
			response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK && string(body) == expected {
				return nil
			}
			err = fmt.Errorf("HTTP %d body %q: %v", response.StatusCode, body, readErr)
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s did not serve %q: %w", url, expected, lastErr)
}

func ownedProcesses(root string) ([]int, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var owned []int
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "nginx.exe") {
			continue
		}
		handle, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if openErr != nil {
			continue
		}
		buffer := make([]uint16, 32768)
		size := uint32(len(buffer))
		queryErr := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size)
		windows.CloseHandle(handle)
		if queryErr != nil {
			continue
		}
		relative, pathErr := filepath.Rel(root, windows.UTF16ToString(buffer[:size]))
		if pathErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			owned = append(owned, int(entry.ProcessID))
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return owned, nil
}

func waitNoOwned(ctx context.Context, root string) error {
	deadline := time.Now().Add(8 * time.Second)
	for {
		pids, err := ownedProcesses(root)
		if err != nil {
			return err
		}
		if len(pids) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("owned processes remain after normal Stop: %v", pids)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
