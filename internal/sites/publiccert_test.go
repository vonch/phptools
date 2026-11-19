package sites

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"phptools/internal/model"
)

func TestPublicCertificateStaysWhenSavingSite(t *testing.T) {
	root := t.TempDir()
	key, certPEM := issueSelfSigned(t, "shop.com", time.Now().Add(80*24*time.Hour))
	keyPEM, err := marshalKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err = InstallPublicCertificate(root, "shop", certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "certs", "sites", "shop.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if err = EnsureCertificate(root, "shop", "shop.com"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(root, "certs", "sites", "shop.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("saving the site replaced the public certificate")
	}
	info := DescribeCertificate(root, model.Site{ID: "shop", Name: "商店", Domain: "shop.com", Port: 80, Enabled: true})
	if info.Kind != "public" || !info.Eligible || info.Issuer == "PHPTools Local CA" {
		t.Fatalf("%+v", info)
	}
}

func TestPublicCertificateMustCoverTheCurrentNames(t *testing.T) {
	root := t.TempDir()
	key, certPEM := issueSelfSigned(t, "shop.com", time.Now().Add(80*24*time.Hour))
	keyPEM, err := marshalKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err = InstallPublicCertificate(root, "shop", certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	if err = EnsureCertificate(root, "shop", "other.com"); err == nil || !strings.Contains(err.Error(), "不包含当前域名") {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(filepath.Join(root, "certs", "sites", "shop.crt"))
	if err != nil || string(kept) != string(certPEM) {
		t.Fatal("mismatched public certificate was replaced")
	}
}

func TestLocalNamesCannotRequestPublicCertificate(t *testing.T) {
	info := DescribeCertificate(t.TempDir(), model.Site{ID: "local", Domain: "demo.test", Port: 80, Enabled: true})
	if info.Eligible || !strings.Contains(info.Reason, "保留域名") {
		t.Fatal(info)
	}
	if _, err := ACMEHTTPConfig(t.TempDir(), []string{"demo.test"}); err == nil {
		t.Fatal("reserved domain was accepted")
	}
}

func TestChallengeFileIsServedFromTheWebRoot(t *testing.T) {
	root := t.TempDir()
	if err := WriteChallenge(root, "abcDEF12_token", "response-body"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "certs", "acme-web", "abcDEF12_token"))
	if err != nil || string(got) != "response-body" {
		t.Fatalf("%q %v", got, err)
	}
	rendered := Render(root, model.Site{ID: "shop", Domain: "shop.com", Port: 80, Root: root}, nil)
	alias := strings.Index(rendered, "location ^~ /.well-known/acme-challenge/")
	hidden := strings.Index(rendered, "location ~ /\\.")
	if alias < 0 || hidden < 0 || alias > hidden || !strings.Contains(rendered, "certs/acme-web/") {
		t.Fatal(rendered)
	}
	if err = WriteChallenge(root, "../escape", "nope"); err == nil {
		t.Fatal("challenge token escaped the web root")
	}
	if err = RemoveChallenge(root, "abcDEF12_token"); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPChallengeServerUsesPort80(t *testing.T) {
	config, err := ACMEHTTPConfig(`D:\env`, []string{"www.shop.com", "shop.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"listen 0.0.0.0:80;", "server_name www.shop.com shop.com;", "alias \"D:/env/certs/acme-web/\";"} {
		if !strings.Contains(config, want) {
			t.Fatal(config)
		}
	}
	site := model.Site{Enabled: true, Port: 80}
	if NeedsHTTPChallengeServer(site) {
		t.Fatal("a site already listening on port 80 does not need another server")
	}
	site.Port = 8080
	if !NeedsHTTPChallengeServer(site) {
		t.Fatal("another HTTP port still needs a port 80 challenge server")
	}
}

func issueSelfSigned(t *testing.T, name string, expires time.Time) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := certificateSerial()
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     expires,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{name},
	}, &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Example CA"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     expires,
	}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, marshalCert(der)
}
