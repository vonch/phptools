//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceFixture(t *testing.T) sourceSite {
	t.Helper()
	root := t.TempDir()
	rulesPath := filepath.Join(root, "nginx.htaccess")
	rules := "location /mobile/ { try_files $uri $uri/ /mobile/index.html; }\nlocation / { if (!-e $request_filename) { rewrite ^(.*)$ /index.php?s=$1 last; } }\n"
	if err := os.WriteFile(rulesPath, []byte(rules), 0600); err != nil {
		t.Fatal(err)
	}
	config := "server {\n listen 80;\n server_name fixture.test;\n root \"D:/project/public\";\n location / {\n  index index.php index.html error/index.html;\n  include " + filepath.ToSlash(rulesPath) + ";\n  autoindex off;\n }\n location ~ \\.php(.*)$ {\n  fastcgi_pass 127.0.0.1:9007;\n  include fastcgi_params;\n }\n error_log D:/phpstudy/logs/source.log crit;\n}\n"
	configPath := filepath.Join(root, "fixture.conf")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	configHash := sha256.Sum256([]byte(config))
	rulesHash := sha256.Sum256([]byte(rules))
	item := sourceSite{SourceConfig: configPath, SourceConfigSHA256: hex.EncodeToString(configHash[:])}
	item.RewriteFiles = append(item.RewriteFiles, struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}{rulesPath, hex.EncodeToString(rulesHash[:])})
	return item
}

func TestSourceRulesPreservesNestedScopeAndRemovesProcessSettings(t *testing.T) {
	item := sourceFixture(t)
	got, err := sourceRules(item)
	if err != nil {
		t.Fatal(err)
	}
	for _, unexpected := range []string{"listen", "server_name", "root ", "fastcgi_", "include ", "error_log", "D:/phpstudy"} {
		if strings.Contains(got, unexpected) {
			t.Fatalf("source process/path directive leaked: %q in %s", unexpected, got)
		}
	}
	parent := strings.Index(got, "location / {")
	spa := strings.Index(got, "location /mobile/")
	closing := strings.LastIndex(got, "autoindex off;")
	if parent < 0 || spa <= parent || closing <= spa || strings.Count(got, "location / {") != 2 || !strings.Contains(got, "error/index.html") {
		t.Fatalf("source nested locations, index, or directive order changed:\n%s", got)
	}
	if err := os.WriteFile(item.RewriteFiles[0].Path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceRules(item); err == nil {
		t.Fatal("accepted an include changed since the inventory")
	}
}

func TestSourceHTTPSettingsPreservesOwnershipAndIsIdempotent(t *testing.T) {
	original := []byte("worker_processes 1;\npid \"E:/PHPTools/instances/id/nginx.pid\";\nhttp {\n include \"E:/PHPTools/sites/*.conf\";\n}\n")
	first, err := sourceHTTPSettings(original)
	if err != nil {
		t.Fatal(err)
	}
	second, err := sourceHTTPSettings(first)
	if err != nil || string(second) != string(first) {
		t.Fatalf("repeated application changed source request settings: %v", err)
	}
	if !strings.Contains(string(first), "client_max_body_size 50m;") || !strings.Contains(string(first), "server_names_hash_bucket_size 256;") || !strings.Contains(string(first), "worker_processes 1;") || !strings.Contains(string(first), `pid "E:/PHPTools/instances/id/nginx.pid";`) || !strings.Contains(string(first), `include "E:/PHPTools/sites/*.conf";`) {
		t.Fatal("request settings changed ownership directives")
	}
	conflict := strings.Replace(string(original), "http {", "http {\n client_max_body_size 200m;", 1)
	if _, err := sourceHTTPSettings([]byte(conflict)); err == nil {
		t.Fatal("overwrote an existing destination request-size choice")
	}
}
