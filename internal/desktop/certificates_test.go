package desktop

import (
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestImportCertificateRejectsMissingAndInvalidText(t *testing.T) {
	a := testApp(t)
	a.data.Sites = []model.Site{{ID: "shop", Domain: "shop.com", Port: 80, Enabled: true}}
	if _, err := a.ImportPublicCertificate("missing", "cert", "key"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatal(err)
	}
	if _, err := a.ImportPublicCertificate("shop", "  ", "key"); err == nil || !strings.Contains(err.Error(), "请填写公钥") {
		t.Fatal(err)
	}
	if _, err := a.ImportPublicCertificate("shop", "cert", " "); err == nil || !strings.Contains(err.Error(), "请填写私钥") {
		t.Fatal(err)
	}
	if _, err := a.ImportPublicCertificate("shop", "not a certificate", "not a key"); err == nil || !strings.Contains(err.Error(), "公钥无效") {
		t.Fatal(err)
	}
	huge := strings.Repeat("a", 256*1024+1)
	if _, err := a.ImportPublicCertificate("shop", huge, "key"); err == nil || !strings.Contains(err.Error(), "过长") {
		t.Fatal(err)
	}
	if len(a.data.Tasks) != 0 {
		t.Fatal("invalid certificate started a task")
	}
}
