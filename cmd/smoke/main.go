//go:build windows

// Command smoke runs the real Windows acceptance flow in an isolated runtime.
// It never edits hosts or adopts existing phpStudy processes or data directories.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zalando/go-keyring"
	"phptools/internal/acceptance"
	"phptools/internal/model"
	"phptools/internal/packages"
	"phptools/internal/services"
	"phptools/internal/sites"
	"phptools/internal/store"
)

type check struct {
	Name    string `json:"name"`
	Details string `json:"details"`
	Passed  bool   `json:"passed"`
}
type report struct {
	Started     string   `json:"started"`
	Completed   string   `json:"completed"`
	Root        string   `json:"root"`
	Checks      []check  `json:"checks"`
	Error       string   `json:"error,omitempty"`
	Limitations []string `json:"limitations,omitempty"`
}

func main() {
	root := flag.String("root", "", "new isolated runtime; defaults to a system temporary directory and is removed after the run")
	reportPath := flag.String("report", `build\qa\smoke-acceptance.json`, "JSON report output path outside the temporary runtime")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, *root, *reportPath); err != nil {
		fmt.Fprintln(os.Stderr, "ACCEPTANCE FAILED:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, root, reportPath string) (runErr error) {
	reportPath, err := filepath.Abs(reportPath)
	if err != nil {
		return err
	}
	testRoot, err := acceptance.NewRoot(root, "smoke")
	if err != nil {
		return err
	}
	root = testRoot.Path
	relativeReport, err := filepath.Rel(root, reportPath)
	if err == nil && relativeReport != ".." && !strings.HasPrefix(relativeReport, ".."+string(filepath.Separator)) && !filepath.IsAbs(relativeReport) {
		return errors.Join(errors.New("acceptance report must be outside the temporary runtime"), testRoot.Cleanup())
	}
	r := report{Started: model.Now(), Root: root, Checks: []check{}}
	record := func(name, details string) {
		r.Checks = append(r.Checks, check{Name: name, Details: details, Passed: true})
		fmt.Println("PASS", name, details)
	}
	defer func() {
		r.Completed = model.Now()
		if runErr != nil {
			r.Error = runErr.Error()
		}
		b, err := json.MarshalIndent(r, "", "  ")
		if err == nil {
			err = os.MkdirAll(filepath.Dir(reportPath), 0700)
		}
		if err == nil {
			err = os.WriteFile(reportPath, b, 0600)
		}
		runErr = errors.Join(runErr, err)
	}()
	defer func() { runErr = errors.Join(runErr, testRoot.Cleanup()) }()
	db, err := store.Open(root)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, db.Close()) }()
	pkgs, err := store.List[model.Package](db, "package")
	if err != nil {
		return err
	}
	byPackage := map[string]model.Package{}
	for _, p := range packages.SeedCatalog() {
		byPackage[p.ID] = p
	}
	for _, p := range pkgs {
		byPackage[p.ID] = p
	}
	previous := make([]model.Package, 0, len(byPackage))
	for _, p := range byPackage {
		previous = append(previous, p)
	}
	fresh, diags, refreshErr := packages.Refresh(ctx, previous)
	for _, d := range diags {
		fmt.Println("SOURCE", d.Level, d.Message)
	}
	if refreshErr != nil {
		fmt.Println("SOURCE refresh:", refreshErr)
	}
	for _, p := range fresh {
		byPackage[p.ID] = p
	}
	record("catalog", "seed catalog loaded and online refresh attempted")
	wanted := []struct{ kind, branch string }{{"php", "5.6."}, {"php", "8.0."}, {"mysql", "5.7."}, {"mysql", "8.4."}, {"redis", ""}, {"nginx", ""}}
	selected := []model.Package{}
	for _, want := range wanted {
		var choices []model.Package
		for _, p := range byPackage {
			if want.kind == "nginx" && !strings.HasSuffix(p.ID, "-windows") {
				continue
			}
			if p.Kind == want.kind && strings.HasPrefix(p.Version, want.branch) && p.Verified {
				choices = append(choices, p)
			}
		}
		sort.Slice(choices, func(i, j int) bool {
			if (choices[i].Path != "") != (choices[j].Path != "") {
				return choices[i].Path != ""
			}
			return versionValue(choices[i].Version) > versionValue(choices[j].Version)
		})
		if len(choices) == 0 {
			return fmt.Errorf("no verified %s %s package", want.kind, want.branch)
		}
		p := choices[0]
		if p.Path == "" {
			lastPrint := time.Time{}
			p, err = packages.Install(ctx, root, p, func(done, total int64, message string) {
				if time.Since(lastPrint) > 3*time.Second {
					fmt.Printf("INSTALL %s %s %d/%d %s\n", p.Kind, p.Version, done, total, message)
					lastPrint = time.Now()
				}
			})
			if err != nil {
				return err
			}
			if err = db.Put("package", p.ID, p); err != nil {
				return err
			}
		}
		selected = append(selected, p)
		record("install "+p.ID, p.Path)
	}
	instances := map[string]model.Instance{}
	h := sha256.Sum256([]byte(strings.ToLower(root)))
	credentialService := "PHPTools/" + hex.EncodeToString(h[:16])
	getSecret := func(id string) (string, error) { return keyring.Get(credentialService, id) }
	save := func(i model.Instance) error { instances[i.ID] = i; return db.Put("instance", i.ID, i) }
	m := services.New(root, save, getSecret)
	var credentialIDs []string
	defer func() {
		order := []model.Instance{}
		for _, i := range instances {
			order = append(order, i)
		}
		priority := map[string]int{"nginx": 0, "php": 1, "mysql": 2, "redis": 3}
		sort.Slice(order, func(a, b int) bool { return priority[order[a].Kind] < priority[order[b].Kind] })
		for _, i := range order {
			stopctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			next, err := m.Stop(stopctx, i)
			cancel()
			current := m.Inspect(next)
			instances[i.ID] = current
			if err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("cleanup %s: %w", i.Name, err))
			}
			if current.PID != 0 || current.State != "stopped" {
				runErr = errors.Join(runErr, fmt.Errorf("cleanup %s: service is not confirmed stopped (PID %d, state %s)", i.Name, current.PID, current.State))
				continue
			}
			record("stop "+i.PackageID, "normally stopped; persisted PID cleared; Inspect=stopped")
		}
		for _, id := range credentialIDs {
			i, exists := instances[id]
			if !exists {
				runErr = errors.Join(runErr, fmt.Errorf("acceptance credential %s retained: no instance available to verify shutdown", id))
				continue
			}
			current := m.Inspect(i)
			if current.PID != 0 || current.State != "stopped" {
				runErr = errors.Join(runErr, fmt.Errorf("acceptance credential %s retained: service is not confirmed stopped (PID %d, state %s)", id, current.PID, current.State))
				continue
			}
			if err := keyring.Delete(credentialService, id); err != nil && !errors.Is(err, keyring.ErrNotFound) {
				runErr = errors.Join(runErr, fmt.Errorf("delete acceptance credential %s: %w", id, err))
			}
		}
	}()
	selectedInstances := []model.Instance{}
	port := 22000
	for _, p := range selected {
		for ; port < 60000; port++ {
			if portFree(port) {
				break
			}
		}
		if port == 60000 {
			return errors.New("no free local acceptance port")
		}
		i, err := m.Create(ctx, p, model.InstanceInput{Name: "acceptance " + p.ID, PackageID: p.ID, Port: port})
		port++
		if err != nil {
			return err
		}
		if p.Kind == "mysql" {
			if err = keyring.Set(credentialService, i.ID, "PHPTools!"+rand.Text()); err != nil {
				return err
			}
			credentialIDs = append(credentialIDs, i.ID)
		}
		if err = save(i); err != nil {
			return err
		}
		i, err = m.Start(ctx, p, i)
		instances[i.ID] = i
		if err != nil {
			return fmt.Errorf("start %s: %w", p.ID, err)
		}
		selectedInstances = append(selectedInstances, i)
		record("start "+p.ID, fmt.Sprintf("127.0.0.1:%d PID %d", i.Port, i.PID))
	}
	var nginx model.Instance
	var nginxPkg model.Package
	phpInstances := []model.Instance{}
	for _, i := range selectedInstances {
		switch i.Kind {
		case "nginx":
			nginx = i
			for _, p := range selected {
				if p.ID == i.PackageID {
					nginxPkg = p
				}
			}
		case "php":
			phpInstances = append(phpInstances, i)
		case "mysql":
			password, err := getSecret(i.ID)
			if err != nil {
				return err
			}
			if _, err = mysqlQuery(ctx, i, password, "CREATE DATABASE IF NOT EXISTS phptools_acceptance; CREATE TABLE IF NOT EXISTS phptools_acceptance.persistence (id INT PRIMARY KEY, value VARCHAR(64)); REPLACE INTO phptools_acceptance.persistence VALUES(1,'survives-restart');\n"); err != nil {
				return err
			}
			var pkg model.Package
			for _, p := range selected {
				if p.ID == i.PackageID {
					pkg = p
				}
			}
			i, err = m.Stop(ctx, i)
			instances[i.ID] = i
			if err != nil {
				return err
			}
			i, err = m.Start(ctx, pkg, i)
			instances[i.ID] = i
			if err != nil {
				return err
			}
			value, err := mysqlQuery(ctx, i, password, "SELECT CONCAT(@@version,':',value) FROM phptools_acceptance.persistence WHERE id=1;\n")
			if err != nil || !strings.Contains(value, "survives-restart") {
				return fmt.Errorf("MySQL persistence: %s %v", value, err)
			}
			record("persistence "+pkg.ID, strings.TrimSpace(value))
		case "redis":
			if value, err := redisCommand(i, "SET", "phptools:acceptance", "survives-restart"); err != nil || value != "+OK" {
				return fmt.Errorf("Redis SET: %s %v", value, err)
			}
			var pkg model.Package
			for _, p := range selected {
				if p.ID == i.PackageID {
					pkg = p
				}
			}
			i, err = m.Stop(ctx, i)
			instances[i.ID] = i
			if err != nil {
				return err
			}
			i, err = m.Start(ctx, pkg, i)
			instances[i.ID] = i
			if err != nil {
				return err
			}
			value, err := redisCommand(i, "GET", "phptools:acceptance")
			if err != nil || value != "survives-restart" {
				return fmt.Errorf("Redis persistence: %s %v", value, err)
			}
			record("persistence "+pkg.ID, value)
		}
	}
	websites := []model.Site{}
	for n, php := range phpInstances {
		projectName := fmt.Sprintf("中文 站点 %d", n)
		if strings.HasPrefix(php.PackageID, "php-5.6.") {
			projectName = fmt.Sprintf("PHP56 Site %d", n)
			r.Limitations = append(r.Limitations, "Official PHP 5.6 Windows CGI cannot open UTF-8 FastCGI script paths containing Chinese characters; PHP 5.6 was tested with an ASCII path containing spaces, PHP 8.0 with a Chinese path containing spaces.")
		}
		project := filepath.Join(root, "projects", projectName)
		if err = os.MkdirAll(project, 0700); err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(project, "index.php"), []byte("<?php header('Content-Type: text/plain'); echo PHP_VERSION;"), 0600); err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(project, "extensions.php"), []byte("<?php header('Content-Type: application/json'); $r = array(); foreach (array('mysqli','pdo_mysql','mbstring','openssl','curl','fileinfo','zip') as $e) { $r[$e] = extension_loaded($e); } echo json_encode($r);"), 0600); err != nil {
			return err
		}
		site := model.Site{ID: fmt.Sprintf("acceptance%d", n), Name: fmt.Sprintf("PHP %d", n), Domain: fmt.Sprintf("acceptance%d.test", n), Root: project, Port: nginx.Port, PHPInstanceID: php.ID, Enabled: true}
		site, err = sites.Validate(site, websites, selectedInstances)
		if err != nil {
			return err
		}
		websites = append(websites, site)
	}
	rollback, err := sites.Apply(root, websites, selectedInstances)
	if err != nil {
		return err
	}
	if err = m.ValidateConfig(ctx, nginxPkg, nginx); err != nil {
		_ = rollback()
		return err
	}
	if err = m.Reload(ctx, nginxPkg, nginx); err != nil {
		_ = rollback()
		_ = m.Reload(ctx, nginxPkg, nginx)
		return err
	}
	for n, site := range websites {
		value, err := httpPHP(ctx, site)
		if err != nil {
			return err
		}
		var version string
		for _, p := range selected {
			if p.ID == phpInstances[n].PackageID {
				version = p.Version
			}
		}
		if strings.TrimSpace(value) != version {
			return fmt.Errorf("PHP HTTP expected %s, received %q", version, value)
		}
		if err = db.Put("site", site.ID, site); err != nil {
			return err
		}
		record("HTTP "+site.Domain, "PHP "+value+" via Host header; no hosts modification")
		extensions, err := httpPHPPath(ctx, site, "/extensions.php")
		if err != nil {
			return err
		}
		loaded := map[string]bool{}
		if err = json.Unmarshal([]byte(extensions), &loaded); err != nil {
			return err
		}
		for _, name := range []string{"mysqli", "pdo_mysql", "mbstring", "openssl"} {
			if !loaded[name] {
				return fmt.Errorf("PHP %s required extension %s is not loaded", version, name)
			}
		}
		record("extensions PHP "+version, extensions)
	}
	// Exercise the same write/validate/rollback path with a genuinely invalid directive.
	bad := append([]model.Site{}, websites...)
	bad[0].Rewrite = "phptools_invalid_directive on;"
	rollback, err = sites.Apply(root, bad, selectedInstances)
	if err != nil {
		return err
	}
	if m.ValidateConfig(ctx, nginxPkg, nginx) == nil {
		_ = rollback()
		return errors.New("invalid Nginx config was accepted")
	}
	if err = rollback(); err != nil {
		return err
	}
	if err = m.Reload(ctx, nginxPkg, nginx); err != nil {
		return err
	}
	if _, err = httpPHP(ctx, websites[0]); err != nil {
		return err
	}
	record("Nginx rollback", "invalid config rejected and previous PHP site still serves")
	return nil
}

func versionValue(s string) int {
	var a, b, c int
	_, _ = fmt.Sscanf(s, "%d.%d.%d", &a, &b, &c)
	return a*1000000 + b*1000 + c
}
func portFree(port int) bool {
	l, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

func mysqlQuery(ctx context.Context, i model.Instance, password, sql string) (output string, queryErr error) {
	// Startup has already applied an owner-only inheritable DACL to this directory.
	dir := filepath.Join(filepath.Dir(filepath.Dir(i.ConfigPath)), "private")
	f, err := os.CreateTemp(dir, "acceptance-client-*.cnf")
	if err != nil {
		return "", err
	}
	name := f.Name()
	defer func() {
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			queryErr = errors.Join(queryErr, err)
		}
	}()
	quoted := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(password) + `"`
	_, err = fmt.Fprintf(f, "[client]\nuser=root\npassword=%s\nprotocol=TCP\nhost=127.0.0.1\nport=%d\n", quoted, i.Port)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	commandCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, filepath.Join(filepath.Dir(i.Executable), "mysql.exe"), "--defaults-file="+name, "--batch", "--skip-column-names")
	cmd.Env = append(os.Environ(), "MYSQL_TEST_LOGIN_FILE="+filepath.Join(dir, "unused.mylogin.cnf"))
	cmd.Dir = filepath.Dir(i.Executable)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	cmd.Stdin = strings.NewReader(sql)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("MySQL acceptance query failed: %w: %s", err, strings.ReplaceAll(strings.TrimSpace(stderr.String()), password, "[redacted]"))
	}
	return string(b), nil
}

func redisCommand(i model.Instance, args ...string) (string, error) {
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(i.Port)), time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var req strings.Builder
	fmt.Fprintf(&req, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(&req, "$%d\r\n%s\r\n", len(arg), arg)
	}
	if _, err = io.WriteString(conn, req.String()); err != nil {
		return "", err
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if strings.HasPrefix(line, "$") {
		n, err := strconv.Atoi(line[1:])
		if err != nil || n < 0 {
			return "", errors.New("Redis missing bulk value")
		}
		b := make([]byte, n+2)
		if _, err = io.ReadFull(reader, b); err != nil {
			return "", err
		}
		return string(b[:n]), nil
	}
	return line, nil
}

func httpPHP(ctx context.Context, s model.Site) (string, error) {
	return httpPHPPath(ctx, s, "/")
}

func httpPHPPath(ctx context.Context, s model.Site, path string) (string, error) {
	client := http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", s.Port, path), nil)
	if err != nil {
		return "", err
	}
	req.Host = s.Domain
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 10000))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, b)
	}
	return string(b), err
}
