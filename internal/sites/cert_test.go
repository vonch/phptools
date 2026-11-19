package sites

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"phptools/internal/model"
)

func TestCertificateMatchesDomainAndIsReused(t *testing.T) {
	root := t.TempDir()
	if err := EnsureCertificate(root, "shop", "shop.test"); err != nil {
		t.Fatal(err)
	}
	cert := readTestCertificate(t, root, "shop")
	if err := cert.CheckSignatureFrom(readTestCA(t, root)); err != nil {
		t.Fatal(err)
	}
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "shop.test" || len(cert.IPAddresses) != 0 {
		t.Fatalf("names: %+v %+v", cert.DNSNames, cert.IPAddresses)
	}
	if cert.NotAfter.Sub(cert.NotBefore) < 800*24*time.Hour {
		t.Fatal("certificate expires too soon")
	}
	certPath, _ := certificatePaths(root, "shop")
	before, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = EnsureCertificate(root, "shop", "shop.test"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(certPath)
	if err != nil || string(after) != string(before) {
		t.Fatal("same domain replaced the certificate")
	}
	if err = EnsureCertificate(root, "shop", "other.test"); err != nil {
		t.Fatal(err)
	}
	renewed := readTestCertificate(t, root, "shop")
	if renewed.DNSNames[0] != "other.test" || string(renewed.Raw) == string(cert.Raw) {
		t.Fatal("domain change kept the old certificate")
	}
}

func TestCertificateCoversSecondDomain(t *testing.T) {
	root := t.TempDir()
	if err := EnsureCertificate(root, "shop", "shop.test", "api.shop.test"); err != nil {
		t.Fatal(err)
	}
	cert := readTestCertificate(t, root, "shop")
	if len(cert.DNSNames) != 2 || cert.DNSNames[0] != "shop.test" || cert.DNSNames[1] != "api.shop.test" {
		t.Fatal(cert.DNSNames)
	}
	certPath, _ := certificatePaths(root, "shop")
	before, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = EnsureCertificate(root, "shop", "shop.test", "api.shop.test"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(certPath)
	if err != nil || string(after) != string(before) {
		t.Fatal("same names replaced the certificate")
	}
	if err = EnsureCertificate(root, "shop", "shop.test"); err != nil {
		t.Fatal(err)
	}
	renewed := readTestCertificate(t, root, "shop")
	if len(renewed.DNSNames) != 1 || renewed.DNSNames[0] != "shop.test" {
		t.Fatal(renewed.DNSNames)
	}
}

func TestLocalhostCertificateIncludesLoopback(t *testing.T) {
	root := t.TempDir()
	if err := EnsureCertificate(root, "local", "localhost"); err != nil {
		t.Fatal(err)
	}
	cert := readTestCertificate(t, root, "local")
	if len(cert.IPAddresses) != 1 || cert.IPAddresses[0].String() != "127.0.0.1" {
		t.Fatal(cert.IPAddresses)
	}
}

func TestCertificateBackupRestoresPreviousFiles(t *testing.T) {
	root := t.TempDir()
	if err := EnsureCertificate(root, "shop", "shop.test"); err != nil {
		t.Fatal(err)
	}
	backup, err := BackupCertificate(root, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if err = EnsureCertificate(root, "shop", "other.test"); err != nil {
		t.Fatal(err)
	}
	if err = RestoreCertificate(root, "shop", backup); err != nil {
		t.Fatal(err)
	}
	if got := readTestCertificate(t, root, "shop"); got.DNSNames[0] != "shop.test" {
		t.Fatal(got.DNSNames)
	}
	if err = RemoveCertificate(root, "shop"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "certs", "sites", "shop.crt")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestRenderHTTPSUsesGeneratedCertificate(t *testing.T) {
	root := t.TempDir()
	site := model.Site{ID: "shop", Domain: "shop.test", Root: root, Port: 8080, HTTPS: true, Enabled: true}
	if err := EnsureCertificate(root, site.ID, site.Domain); err != nil {
		t.Fatal(err)
	}
	rendered := Render(root, site, &model.Instance{Port: 9100})
	cert, key := certificatePaths(root, site.ID)
	for _, want := range []string{
		"listen 127.0.0.1:8080;",
		"listen 127.0.0.1:443 ssl;",
		"ssl_certificate \"" + filepath.ToSlash(cert) + "\"",
		"ssl_certificate_key \"" + filepath.ToSlash(key) + "\"",
		"fastcgi_param HTTPS $https if_not_empty;",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %s\n%s", want, rendered)
		}
	}
	site.Port = 443
	secureOnly := Render(root, site, nil)
	if strings.Contains(secureOnly, "listen 127.0.0.1:80;") || strings.Count(secureOnly, "listen ") != 1 {
		t.Fatal(secureOnly)
	}
	site.HTTPS = false
	site.Port = 8080
	plain := Render(root, site, &model.Instance{Port: 9100})
	if strings.Contains(plain, "ssl_certificate") || strings.Contains(plain, "fastcgi_param HTTPS") {
		t.Fatal(plain)
	}
}

func TestHTTPSRejectsAnotherListenerOnSameDomain(t *testing.T) {
	root := t.TempDir()
	site := model.Site{ID: "shop", Domain: "shop.test", Root: root, Port: 80, HTTPS: true, Enabled: true}
	other := model.Site{ID: "other", Domain: "shop.test", Root: root, Port: 443, Enabled: true}
	if _, err := Validate(site, []model.Site{other}, nil); err == nil {
		t.Fatal("accepted two listeners on 443 for one domain")
	}
	other.Domain = "other.test"
	if _, err := Validate(site, []model.Site{other}, nil); err != nil {
		t.Fatal(err)
	}
}

func readTestCertificate(t *testing.T, root, id string) *x509.Certificate {
	t.Helper()
	certPath, keyPath := certificatePaths(root, id)
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := parseCertificate(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	key, err := parseKey(keyPEM)
	if err != nil || !publicKeyEqual(cert.PublicKey, key) {
		t.Fatal(err)
	}
	return cert
}

func readTestCA(t *testing.T, root string) *x509.Certificate {
	t.Helper()
	certPath, _ := caPaths(root)
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := parseCertificate(data)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
