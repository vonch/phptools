//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"phptools/internal/acceptance"
	"phptools/internal/model"
)

func probeCGI(ctx context.Context, i model.Instance) (values map[string]string, runErr error) {
	root, err := acceptance.NewRoot("", "php-core-settings-probe")
	if err != nil {
		return nil, err
	}
	defer func() { runErr = errors.Join(runErr, root.Cleanup()) }()
	keys, err := json.Marshal(coreKeys)
	if err != nil {
		return nil, err
	}
	script := `<?php $keys=json_decode('` + string(keys) + `',true);$values=array();foreach($keys as $key){$values[$key]=(string)ini_get($key);} $values['error_reporting']=(string)error_reporting();$values['date.timezone']=date_default_timezone_get();echo json_encode(array('loadedINI'=>php_ini_loaded_file(),'sapi'=>PHP_SAPI,'version'=>PHP_VERSION,'values'=>$values),JSON_UNESCAPED_UNICODE|JSON_UNESCAPED_SLASHES);`
	path := filepath.Join(root.Path, "probe.php")
	if err = os.WriteFile(path, []byte(script), 0600); err != nil {
		return nil, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Do not set CGI request environment variables: they make PHP ignore -c.
	cmd := exec.CommandContext(probeCtx, i.Executable, "-q", "-c", i.ConfigPath, "-f", path)
	cmd.Dir = filepath.Dir(i.Executable)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("PHP core probe failed for %s: %w", i.Name, err)
	}
	if split := bytes.Index(raw, []byte("\r\n\r\n")); split >= 0 {
		raw = raw[split+4:]
	}
	var result struct {
		LoadedINI string            `json:"loadedINI"`
		SAPI      string            `json:"sapi"`
		Version   string            `json:"version"`
		Values    map[string]string `json:"values"`
	}
	if err = json.Unmarshal(bytes.TrimSpace(raw), &result); err != nil {
		return nil, errors.New("PHP CGI probe returned warnings or non-JSON output")
	}
	if result.SAPI != "cgi-fcgi" || result.LoadedINI == "" || !strings.EqualFold(filepath.Clean(result.LoadedINI), filepath.Clean(i.ConfigPath)) {
		return nil, fmt.Errorf("PHP CGI probe did not load the exact managed ini for %s", i.Name)
	}
	for _, key := range coreKeys {
		if _, ok := result.Values[key]; !ok {
			return nil, fmt.Errorf("PHP CGI probe omitted %s", key)
		}
	}
	return result.Values, nil
}
