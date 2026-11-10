//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"phptools/internal/model"
	"phptools/internal/services"
	"phptools/internal/sites"
)

func checkPathInfo(ctx context.Context, root string, manager *services.Manager, phpPkg, nginxPkg model.Package, nginx model.Instance, instances map[string]model.Instance, report *validationReport) error {
	port, err := availablePort()
	if err != nil {
		return err
	}
	php, err := manager.Create(ctx, phpPkg, model.InstanceInput{Name: "PATH_INFO fixture", PackageID: phpPkg.ID, Port: port})
	if err != nil {
		return err
	}
	instances[php.ID] = php
	project := filepath.Join(root, "projects", "path-info")
	if err = os.MkdirAll(project, 0700); err != nil {
		return err
	}
	fixture := `<?php
header('Content-Type: application/json');
echo json_encode(array(
    'script' => basename($_SERVER['SCRIPT_FILENAME']),
    'scriptName' => $_SERVER['SCRIPT_NAME'],
    'pathInfo' => isset($_SERVER['PATH_INFO']) ? $_SERVER['PATH_INFO'] : '',
    'translated' => isset($_SERVER['PATH_TRANSLATED']) ? $_SERVER['PATH_TRANSLATED'] : '',
    'query' => $_GET,
    'method' => $_SERVER['REQUEST_METHOD'],
    'body' => file_get_contents('php://input')
));`
	if err = os.WriteFile(filepath.Join(project, "index.php"), []byte(fixture), 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(project, "asset.txt"), []byte("UNCHANGED STATIC"), 0600); err != nil {
		return err
	}
	pathSite := model.Site{ID: "fixture_path", Domain: "path.test", Root: project, Port: nginx.Port, PHPInstanceID: php.ID, PHPPathInfo: true, Enabled: true,
		Rewrite: "location / { index index.php index.html; }"}
	ordinary := pathSite
	ordinary.ID, ordinary.Domain, ordinary.PHPPathInfo, ordinary.Rewrite = "fixture_ordinary", "ordinary.test", false, ""
	if _, err = sites.Apply(root, []model.Site{pathSite, ordinary}, []model.Instance{php}); err != nil {
		return err
	}
	php, err = manager.Start(ctx, phpPkg, php)
	instances[php.ID] = php
	if err != nil {
		return err
	}
	nginx, err = manager.Start(ctx, nginxPkg, nginx)
	instances[nginx.ID] = nginx
	if err != nil {
		return err
	}
	request := func(site model.Site, method, path, body string) (int, string, error) {
		client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
		req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", site.Port, path), strings.NewReader(body))
		if err != nil {
			return 0, "", err
		}
		req.Host = site.Domain
		response, err := client.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 32768))
		return response.StatusCode, string(data), err
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		body := ""
		if method == http.MethodPost {
			body = "preserved-body"
		}
		status, content, err := request(pathSite, method, "/index.php/alpha/beta?probe=kept", body)
		if err != nil {
			return err
		}
		var got struct {
			Script, ScriptName, PathInfo, Translated, Method, Body string
			Query                                                  map[string]string
		}
		if err := json.Unmarshal([]byte(content), &got); err != nil || status != 200 {
			return fmt.Errorf("PATH_INFO fixture HTTP %d: %s: %v", status, content, err)
		}
		if got.Script != "index.php" || got.ScriptName != "/index.php" || got.PathInfo != "/alpha/beta" || got.Query["probe"] != "kept" || got.Method != method || got.Body != body {
			return fmt.Errorf("PATH_INFO lost request semantics: %+v", got)
		}
		if filepath.Clean(filepath.FromSlash(got.Translated)) != filepath.Join(project, "alpha", "beta") {
			return fmt.Errorf("PATH_TRANSLATED mismatch: %s", got.Translated)
		}
		report.Checks = append(report.Checks, method+" /index.php/alpha/beta executes index.php with PATH_INFO, translated path, query and body preserved")
	}
	for _, site := range []model.Site{pathSite, ordinary} {
		status, content, err := request(site, http.MethodGet, "/asset.txt", "")
		if err != nil || status != 200 || content != "UNCHANGED STATIC" {
			return fmt.Errorf("static fixture %s HTTP %d: %s: %v", site.Domain, status, content, err)
		}
		status, content, err = request(site, http.MethodGet, "/missing.php/extra", "")
		if err != nil || status != 404 && site.PHPPathInfo {
			return fmt.Errorf("missing PHP fixture %s HTTP %d: %s: %v", site.Domain, status, content, err)
		}
	}
	report.Checks = append(report.Checks, "Static assets preserved for ordinary and PATH_INFO sites; nonexistent PATH_INFO PHP script returns 404")
	status, content, err := request(ordinary, http.MethodGet, "/route?probe=kept", "")
	if err != nil || status != 200 || !strings.Contains(content, `"pathInfo":""`) {
		return fmt.Errorf("ordinary site fallback changed: HTTP %d %s: %v", status, content, err)
	}
	report.Checks = append(report.Checks, "Ordinary PHP site's default front controller keeps PATH_INFO disabled")
	return nil
}
