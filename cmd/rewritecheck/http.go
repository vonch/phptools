//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"phptools/internal/model"
)

var safeID = regexp.MustCompile(`^[a-z0-9_-]+$`)

func fixtureSite(root string, preset model.RewritePreset, php model.Instance, port, ordinal int) (model.Site, error) {
	if !safeID.MatchString(preset.ID) {
		return model.Site{}, fmt.Errorf("unsafe preset ID: %q", preset.ID)
	}
	project := filepath.Join(root, "projects", "中文 Project "+preset.ID)
	entry := preset.SuggestedEntry
	if filepath.IsAbs(entry) || !within(project, filepath.Join(project, entry)) {
		return model.Site{}, errors.New("preset suggestedEntry escapes project")
	}
	webRoot := filepath.Join(project, entry)
	if err := os.MkdirAll(webRoot, 0700); err != nil {
		return model.Site{}, err
	}
	source := `<?php
header('Content-Type: application/json; charset=utf-8');
echo json_encode(array(
  'fixture' => ` + strconv.Quote(preset.ID) + `,
  'script' => basename($_SERVER['SCRIPT_FILENAME']),
  'method' => $_SERVER['REQUEST_METHOD'],
  'uri' => $_SERVER['REQUEST_URI'],
  'query' => (object) $_GET,
  'queryString' => $_SERVER['QUERY_STRING'],
  'post' => (object) $_POST,
  'body' => file_get_contents('php://input')
), JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES);
`
	if err := os.WriteFile(filepath.Join(webRoot, "index.php"), []byte(source), 0600); err != nil {
		return model.Site{}, err
	}
	if err := os.WriteFile(filepath.Join(webRoot, "asset.txt"), []byte("STATIC:"+preset.ID), 0600); err != nil {
		return model.Site{}, err
	}
	for _, path := range deniedFixturePaths(preset.ID) {
		target := filepath.Join(webRoot, filepath.FromSlash(strings.TrimPrefix(path, "/")))
		if !within(webRoot, target) {
			return model.Site{}, errors.New("denied fixture escaped web root")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return model.Site{}, err
		}
		body := "SENSITIVE-FIXTURE"
		if strings.HasSuffix(path, ".php") {
			body = "<?php echo 'UNEXPECTED-PHP-EXECUTION';"
		}
		if err := os.WriteFile(target, []byte(body), 0600); err != nil {
			return model.Site{}, err
		}
	}
	return model.Site{ID: "rewrite_" + preset.ID, Name: preset.Name, Domain: fmt.Sprintf("r%d.test", ordinal), Root: project, Entry: entry, Port: port, PHPInstanceID: php.ID, Enabled: true, Rewrite: preset.Rules}, nil
}

type entryResponse struct {
	Fixture     string            `json:"fixture"`
	Script      string            `json:"script"`
	Method      string            `json:"method"`
	URI         string            `json:"uri"`
	Query       map[string]string `json:"query"`
	QueryString string            `json:"queryString"`
	Post        map[string]string `json:"post"`
	Body        string            `json:"body"`
}

func checkHTTP(ctx context.Context, preset model.RewritePreset, site model.Site, record func(string, string)) error {
	query := url.Values{"alpha": {"one"}, "note": {"中文 a+b"}}.Encode()
	route := "/rewrite/check"
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		body := ""
		if method == http.MethodPost {
			body = url.Values{"posted": {"kept"}, "message": {"中文 body & plus+"}}.Encode()
		}
		status, content, err := request(ctx, site, method, route+"?"+query, body)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("preset %s %s route returned HTTP %d: %s", preset.ID, method, status, content)
		}
		var got entryResponse
		if err = json.Unmarshal([]byte(content), &got); err != nil {
			return fmt.Errorf("preset %s did not execute PHP JSON fixture: %w; body=%q", preset.ID, err, content)
		}
		if got.Fixture != preset.ID || got.Script != "index.php" || got.Method != method {
			return fmt.Errorf("preset %s wrong entry or method: %+v", preset.ID, got)
		}
		if got.Query["alpha"] != "one" || got.Query["note"] != "中文 a+b" {
			return fmt.Errorf("preset %s lost query parameters: %+v", preset.ID, got.Query)
		}
		rawQuery, err := url.ParseQuery(got.QueryString)
		if err != nil || len(rawQuery["alpha"]) != 1 || len(rawQuery["note"]) != 1 {
			return fmt.Errorf("preset %s query values duplicated or invalid: %q", preset.ID, got.QueryString)
		}
		if method == http.MethodPost && (got.Post["posted"] != "kept" || got.Post["message"] != "中文 body & plus+" || got.Body != body) {
			return fmt.Errorf("preset %s lost POST body or method: %+v", preset.ID, got)
		}
		if strings.HasPrefix(preset.ID, "thinkphp") {
			if got.Query["s"] != route || len(rawQuery["s"]) != 1 {
				return fmt.Errorf("ThinkPHP preset %s expected s=%q, got %q", preset.ID, route, got.Query["s"])
			}
			record(preset.ID+" "+method+" s routing", "s=/rewrite/check and original query parameters appended exactly once")
		}
		record(preset.ID+" "+method+" front controller", "index.php hit; original UTF-8 GET values retained; request method/body retained")
	}
	status, content, err := request(ctx, site, http.MethodGet, "/asset.txt?cache=one", "")
	if err != nil {
		return err
	}
	if status != http.StatusOK || content != "STATIC:"+preset.ID {
		return fmt.Errorf("preset %s static file changed: HTTP %d %q", preset.ID, status, content)
	}
	record(preset.ID+" static", "existing asset.txt served verbatim")
	status, content, err = request(ctx, site, http.MethodGet, "/missing.php?alpha=one", "")
	if err != nil {
		return err
	}
	if status != http.StatusNotFound {
		return fmt.Errorf("preset %s missing PHP path must be 404, got HTTP %d: %s", preset.ID, status, content)
	}
	record(preset.ID+" missing PHP", "nonexistent missing.php returns HTTP 404")
	deniedPaths := deniedFixturePaths(preset.ID)
	if preset.ID == "typecho" {
		deniedPaths = append(deniedPaths, "/usr/uploads/payload.php/extra")
	}
	for _, path := range deniedPaths {
		status, content, err = request(ctx, site, http.MethodGet, path, "")
		if err != nil {
			return err
		}
		if status != http.StatusForbidden {
			return fmt.Errorf("preset %s protected fixture %s expected 403, got HTTP %d: %q", preset.ID, path, status, content)
		}
		record(preset.ID+" deny "+path, "existing PHP/sensitive fixture denied with HTTP 403")
	}
	return nil
}

func deniedFixturePaths(id string) []string {
	switch id {
	case "wordpress":
		return []string{"/wp-content/uploads/payload.php", "/wp-content/files/payload.php"}
	case "yii2":
		return []string{"/assets/payload.php"}
	case "typecho":
		return []string{"/usr/uploads/payload.php", "/usr/private.db", "/usr/backups/secret.txt"}
	}
	return nil
}

func request(ctx context.Context, site model.Site, method, path, body string) (int, string, error) {
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", site.Port, path), strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Host = site.Domain
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return resp.StatusCode, string(b), err
}
