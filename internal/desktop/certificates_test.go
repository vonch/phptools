package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestImportCertificateDialogAndInvalidFiles(t *testing.T) {
	a := testApp(t)
	a.data.Sites = []model.Site{{ID: "shop", Domain: "shop.com", Port: 80, Enabled: true}}
	if _, err := a.ImportPublicCertificate("missing"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatal(err)
	}
	if _, err := a.ImportPublicCertificate("shop"); err == nil || !strings.Contains(err.Error(), "选择器") {
		t.Fatal(err)
	}
	a.hooks.PickCertificate = func(string, string, string) (string, error) { return "", nil }
	if id, err := a.ImportPublicCertificate("shop"); id != "" || err != nil {
		t.Fatalf("cancel is not an error: %q %v", id, err)
	}
	dir := t.TempDir()
	a.hooks.PickCertificate = func(title, _, _ string) (string, error) {
		if strings.Contains(title, "私钥") {
			t.Fatal("key dialog opened after a bad certificate path")
		}
		return "relative.pem", nil
	}
	if _, err := a.ImportPublicCertificate("shop"); err == nil || !strings.Contains(err.Error(), "请选择") {
		t.Fatal(err)
	}
	cert := filepath.Join(dir, "site.crt")
	key := filepath.Join(dir, "site.key")
	if err := os.WriteFile(cert, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("not a key"), 0600); err != nil {
		t.Fatal(err)
	}
	a.hooks.PickCertificate = func(title, _, _ string) (string, error) {
		if strings.Contains(title, "私钥") {
			return key, nil
		}
		return cert, nil
	}
	if _, err := a.ImportPublicCertificate("shop"); err == nil || !strings.Contains(err.Error(), "证书文件无效") {
		t.Fatal(err)
	}
	if len(a.data.Tasks) != 0 {
		t.Fatal("invalid certificate started a task")
	}
}
