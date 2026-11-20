package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"phptools/internal/model"
)

func defaultCertificateView() model.CertificateViewPreferences {
	return model.CertificateViewPreferences{
		HiddenColumns: []string{},
		SortBy:        "manual",
		SortDirection: "asc",
	}
}

func normalizeCertificateView(preferences model.CertificateViewPreferences) (model.CertificateViewPreferences, error) {
	result := make([]string, 0, len(preferences.HiddenColumns))
	seen := make(map[string]bool, len(preferences.HiddenColumns))
	for _, column := range preferences.HiddenColumns {
		if seen[column] {
			continue
		}
		switch column {
		case "siteName", "domains", "kind", "notAfter", "actions":
		default:
			return model.CertificateViewPreferences{}, errors.New("证书列表包含未知的列")
		}
		result = append(result, column)
		seen[column] = true
	}
	if len(result) > 4 {
		return model.CertificateViewPreferences{}, errors.New("证书列表至少保留一列显示")
	}
	preferences.HiddenColumns = result
	switch preferences.SortBy {
	case "manual", "siteName", "domains", "kind", "notAfter":
	default:
		return model.CertificateViewPreferences{}, errors.New("证书排序字段无效")
	}
	if preferences.SortDirection != "asc" && preferences.SortDirection != "desc" {
		return model.CertificateViewPreferences{}, errors.New("证书排序方向无效")
	}
	return preferences, nil
}

func (a *App) loadCertificateView() {
	a.data.CertificateView = defaultCertificateView()
	raw, err := os.ReadFile(filepath.Join(a.configDir, "certificate-view.json"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	preferences := defaultCertificateView()
	if err == nil {
		err = json.Unmarshal(raw, &preferences)
	}
	if err == nil {
		preferences, err = normalizeCertificateView(preferences)
	}
	if err != nil {
		a.data.Diagnostics = append(a.data.Diagnostics, model.Diagnostic{
			Level: "warning", Message: "证书列表配置无法读取，已使用默认显示和排序：" + err.Error(),
		})
		return
	}
	a.data.CertificateView = preferences
}

func (a *App) SaveCertificateView(preferences model.CertificateViewPreferences) error {
	preferences, err := normalizeCertificateView(preferences)
	if err != nil {
		return err
	}
	if err = a.saveCertificateView(preferences); err != nil {
		return fmt.Errorf("保存证书列表配置失败：%w", err)
	}
	a.notify()
	return nil
}

func (a *App) saveCertificateView(preferences model.CertificateViewPreferences) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return errors.New("软件正在停止服务")
	}
	if err := os.MkdirAll(a.configDir, 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(preferences, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(a.configDir, "certificate-view.json.tmp")
	defer os.Remove(tmp)
	if err = os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(a.configDir, "certificate-view.json")); err != nil {
		return err
	}
	a.data.CertificateView = preferences
	return nil
}
