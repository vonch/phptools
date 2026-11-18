package services

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestFailedStartStaysVisibleAndCanBeRetried(t *testing.T) {
	root := t.TempDir()
	m := New(root, nil, nil)
	pkg := fixturePackage(t, root, "php", "8.3.1")
	instance, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "php", PackageID: pkg.ID, Port: 19991})
	if err != nil {
		t.Fatal(err)
	}
	instance.State = "error"
	instance.Error = "旧错误"
	instance.PID = 0
	got, err := m.Start(context.Background(), pkg, instance)
	if err == nil || strings.Contains(err.Error(), "旧错误") {
		t.Fatalf("start stayed on the old error: %v", err)
	}
	if got.PID != 0 || got.State != "error" || got.Error == "" || strings.Contains(got.Error, "旧错误") {
		t.Fatalf("failed start was not recorded for retry: %+v", got)
	}
}

func TestMissingRuntimeLibraryExplainsTheExit(t *testing.T) {
	if !missingRuntimeLibrary(errString("exit status 0xc0000135")) {
		t.Fatal("0xc0000135 was not recognized as a missing runtime library")
	}
	if missingRuntimeLibrary(errString("exit status 1")) {
		t.Fatal("ordinary failure treated as a missing runtime library")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func fixturePackage(t *testing.T, root, kind, version string) model.Package {
	t.Helper()
	pkg := model.Package{ID: kind + "-fixture", Kind: kind, Version: version, Path: filepath.Join(root, "packages", kind)}
	name := map[string]string{"php": "php-cgi.exe", "mysql": "mysqld.exe", "nginx": "nginx.exe", "redis": "redis-server.exe"}[kind]
	dir := pkg.Path
	if kind == "mysql" {
		dir = filepath.Join(dir, "bin")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture: never execute"), 0600); err != nil {
		t.Fatal(err)
	}
	if kind == "php" {
		if err := os.WriteFile(filepath.Join(dir, "php.ini-development"), []byte("; untouched package\n[PHP]\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "nginx" {
		if err := os.MkdirAll(filepath.Join(dir, "conf"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "conf", "fastcgi_params"), []byte("fastcgi_param REQUEST_METHOD $request_method;\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return pkg
}

func TestCreateUsesIsolatedDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), "中文 环境")
	m := New(root, nil, nil)
	for _, kind := range []string{"php", "nginx", "mysql", "redis"} {
		t.Run(kind, func(t *testing.T) {
			pkg := fixturePackage(t, root, kind, "8.4.8")
			i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "测试 实例", PackageID: pkg.ID, Port: 19990})
			if err != nil {
				t.Fatal(err)
			}
			if i.ID == "" || i.PID != 0 || i.State != "stopped" {
				t.Fatalf("bad initial instance: %+v", i)
			}
			dir := filepath.Join(root, "instances", i.ID)
			for _, p := range []string{i.ConfigPath, i.DataDir, i.LogPath} {
				if !within(dir, p) {
					t.Fatalf("path escaped instance: %s", p)
				}
			}
			contents, err := os.ReadFile(i.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(contents) == 0 {
				t.Fatal("empty config")
			}
			if kind == "mysql" {
				if err = validateMySQLConfig(i); err != nil {
					t.Fatal(err)
				}
				entries, _ := os.ReadDir(i.DataDir)
				if len(entries) != 0 {
					t.Fatal("Create initialized database before credentials were persisted")
				}
			}
			if kind == "redis" {
				if err = validateRedisConfig(i); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "php" {
				b, _ := os.ReadFile(filepath.Join(pkg.Path, "php.ini-development"))
				if string(b) != "; untouched package\n[PHP]\n" {
					t.Fatal("package config was modified")
				}
			}
		})
	}
}

func TestInstancePathTraversalIsRejected(t *testing.T) {
	root := t.TempDir()
	m := New(root, nil, nil)
	for _, id := range []string{"../other", "a/b", `a\b`, "..", "x:y", ""} {
		if _, err := m.instanceDir(id); err == nil {
			t.Fatalf("accepted unsafe ID %q", id)
		}
	}
	if within(filepath.Join(root, "a"), filepath.Join(root, "ab", "config")) {
		t.Fatal("prefix was confused with directory containment")
	}
	pkg := fixturePackage(t, root, "php", "8.4.8")
	i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "php", PackageID: pkg.ID, Port: 19991})
	if err != nil {
		t.Fatal(err)
	}
	i.ConfigPath = filepath.Join(root, "phpstudy", "php.ini")
	if _, err = m.Start(context.Background(), pkg, i); err == nil {
		t.Fatal("accepted external config")
	}
}

func TestCreateRejectsExternalPackageAndDuplicateBinaries(t *testing.T) {
	root := t.TempDir()
	m := New(root, nil, nil)
	pkg := fixturePackage(t, t.TempDir(), "php", "8.4.8")
	if _, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "php", PackageID: pkg.ID, Port: 19991}); err == nil {
		t.Fatal("accepted external package")
	}
	pkg = fixturePackage(t, root, "php", "8.4.8")
	if err := os.MkdirAll(filepath.Join(pkg.Path, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg.Path, "nested", "php-cgi.exe"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := findExecutable(pkg); err == nil {
		t.Fatal("ambiguous executable accepted")
	}
}

func TestPortConflict(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err = availablePort(listener.Addr().(*net.TCPAddr).Port); err == nil {
		t.Fatal("occupied port accepted")
	}
}

func TestWindowsPathAndPasswordQuoting(t *testing.T) {
	if got := cygwinPath(`D:\中文 目录\Redis\data`); got != "/cygdrive/d/中文 目录/Redis/data" {
		t.Fatal(got)
	}
	if got := cygwinPath(`\\server\share\data`); got != "//server/share/data" {
		t.Fatal(got)
	}
	password := `secret'"\tail`
	if got := sqlPassword(password); got != `'secret''"\\tail'` {
		t.Fatalf("SQL quote: %s", got)
	}
	if got := mysqlPassword(password); got != `"secret'\"\\tail"` {
		t.Fatalf("option quote: %s", got)
	}
	fields, err := redisFields(`dir "/cygdrive/d/中文 目录" # comment`)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields[1] != "/cygdrive/d/中文 目录" {
		t.Fatal(fields)
	}
	if _, err = redisFields(`dir "open`); err == nil {
		t.Fatal("unclosed quote accepted")
	}
}

func TestPHPOnlyEnablesPresentExtensionsOnce(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"php_mysqli.dll", "php_pdo_mysql.dll", "php_openssl.dll"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ini := ";extension=php_mysqli.dll\nextension=php_openssl.dll\n"
	result := enablePHPExtensions(ini, dir)
	if strings.Count(result, "extension=php_openssl.dll") != 1 {
		t.Fatal("duplicate enabled extension")
	}
	if !strings.Contains(result, "\nextension=php_mysqli.dll\n") || !strings.Contains(result, "\nextension=php_pdo_mysql.dll\n") {
		t.Fatal(result)
	}
	if strings.Contains(result, "extension=php_curl.dll") || strings.Contains(result, "extension=php_zip.dll") {
		t.Fatal("nonexistent extension was enabled")
	}
	if again := enablePHPExtensions(result, dir); again != result {
		t.Fatal("extension generation was not idempotent")
	}
}

func TestDatabaseConfigCannotEscapeInstance(t *testing.T) {
	root := t.TempDir()
	m := New(root, nil, nil)
	for _, kind := range []string{"mysql", "redis"} {
		t.Run(kind, func(t *testing.T) {
			pkg := fixturePackage(t, root, kind, "8.4.8")
			i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: kind, PackageID: pkg.ID, Port: 19991})
			if err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(i.ConfigPath, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "mysql" {
				_, err = f.WriteString("datadir=\"D:/phpstudy_pro/Extensions/MySQL/data\"\n")
			} else {
				_, err = f.WriteString("include /cygdrive/d/phpstudy/redis.conf\n")
			}
			_ = f.Close()
			if err != nil {
				t.Fatal(err)
			}
			if kind == "mysql" {
				err = validateMySQLConfig(i)
			} else {
				err = validateRedisConfig(i)
			}
			if err == nil {
				t.Fatal("external configuration accepted")
			}
		})
	}
}

func TestMissingOwnershipRecordWithIncompleteIdentityNeverStopsPID(t *testing.T) {
	root := t.TempDir()
	m := New(root, nil, nil)
	pkg := fixturePackage(t, root, "php", "8.4.8")
	i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "php", PackageID: pkg.ID, Port: 19991})
	if err != nil {
		t.Fatal(err)
	}
	i.PID = os.Getpid()
	// Without a recorded creation time the live PID cannot safely be classified
	// as a reused process, even when its executable differs from this fixture.
	i.ProcessCreated = 0
	i.State = "running"
	inspected := m.Inspect(i)
	if inspected.State != "error" || inspected.PID != os.Getpid() {
		t.Fatalf("lost unknown process identity: %+v", inspected)
	}
	if _, err = m.Stop(context.Background(), i); err == nil {
		t.Fatal("unknown process could be stopped")
	}
}

func TestTamperedRecordIsRejected(t *testing.T) {
	root := t.TempDir()
	m := New(root, nil, nil)
	pkg := fixturePackage(t, root, "php", "8.4.8")
	i, err := m.Create(context.Background(), pkg, model.InstanceInput{Name: "php", PackageID: pkg.ID, Port: 19991})
	if err != nil {
		t.Fatal(err)
	}
	p := processRecord{PID: os.Getpid(), Created: 1, Executable: i.Executable, Config: filepath.Join(root, "unowned.ini"), ConfigArgument: i.ConfigPath}
	b, _ := json.Marshal(p)
	if err = os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(i.ConfigPath)), "process.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	current := m.Inspect(i)
	if current.State != "error" {
		t.Fatalf("accepted tampered record %+v", current)
	}
	if _, err = m.Start(context.Background(), pkg, i); err == nil || !strings.Contains(err.Error(), "归属") {
		t.Fatalf("Start did not reject tampered record: %v", err)
	}
	if _, err = m.Stop(context.Background(), i); err == nil {
		t.Fatal("Stop did not reject tampered record")
	}
}
