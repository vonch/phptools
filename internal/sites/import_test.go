package sites

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImportedRSACertificateKeepsTheChainAndSurvivesSave(t *testing.T) {
	leafPEM, intermediatePEM, keyPEM, roots := issueRSAChain(t, []string{"www.shop.com"}, time.Now().Add(80*24*time.Hour))
	supplied := append(append([]byte{}, intermediatePEM...), leafPEM...)
	normalized, gotKey, err := NormalizeImportedCertificate([]string{"www.shop.com"}, supplied, keyPEM, roots)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(normalized), string(leafPEM)) || !strings.Contains(string(normalized), string(intermediatePEM)) {
		t.Fatal("leaf certificate was not placed ahead of the intermediate")
	}
	if string(gotKey) != string(keyPEM) {
		t.Fatal("private key changed")
	}
	root := t.TempDir()
	if err = InstallPublicCertificate(root, "shop", normalized, gotKey); err != nil {
		t.Fatal(err)
	}
	if err = EnsureCertificate(root, "shop", "www.shop.com"); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(root, "certs", "sites", "shop.crt"))
	if err != nil || string(stored) != string(normalized) {
		t.Fatal("imported certificate was replaced")
	}
}

func TestImportedCertificateRejectsMismatchExpiryAndTrust(t *testing.T) {
	leafPEM, intermediatePEM, keyPEM, roots := issueRSAChain(t, []string{"*.shop.com", "shop.com"}, time.Now().Add(80*24*time.Hour))
	chainPEM := append(append([]byte{}, leafPEM...), intermediatePEM...)
	otherPEM := rsaKeyPEM(t, mustRSA(t))
	if _, _, err := NormalizeImportedCertificate([]string{"www.shop.com"}, chainPEM, otherPEM, roots); err == nil || !strings.Contains(err.Error(), "不匹配") {
		t.Fatal(err)
	}
	if _, _, err := NormalizeImportedCertificate([]string{"other.com"}, chainPEM, keyPEM, roots); err == nil || !strings.Contains(err.Error(), "不包含站点域名") {
		t.Fatal(err)
	}
	if _, _, err := NormalizeImportedCertificate([]string{"shop.com"}, chainPEM, keyPEM, roots); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NormalizeImportedCertificate([]string{"www.shop.com"}, chainPEM, keyPEM, roots); err != nil {
		t.Fatal(err)
	}
	expiredPEM, _, expiredKey, expiredRoots := issueRSAChain(t, []string{"shop.com"}, time.Now().Add(-time.Hour))
	if _, _, err := NormalizeImportedCertificate([]string{"shop.com"}, expiredPEM, expiredKey, expiredRoots); err == nil || !strings.Contains(err.Error(), "已过期") {
		t.Fatal(err)
	}
	empty := x509.NewCertPool()
	if _, _, err := NormalizeImportedCertificate([]string{"shop.com"}, chainPEM, keyPEM, empty); err == nil || !strings.Contains(err.Error(), "不信任") {
		t.Fatal(err)
	}
	encrypted := pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte("locked")})
	if _, _, err := NormalizeImportedCertificate([]string{"shop.com"}, chainPEM, encrypted, roots); err == nil || !strings.Contains(err.Error(), "密码") {
		t.Fatal(err)
	}
	pkcs8 := rsaPKCS8PEM(t, mustParseRSA(t, keyPEM))
	if _, _, err := NormalizeImportedCertificate([]string{"www.shop.com"}, chainPEM, pkcs8, roots); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := InstallPublicCertificate(dir, "shop", chainPEM, otherPEM); err == nil {
		t.Fatal("mismatched key was installed")
	}
	if _, err := os.Stat(filepath.Join(dir, "certs", "sites", "shop.crt")); !os.IsNotExist(err) {
		t.Fatal("rejected import created a certificate file")
	}
}

func issueRSAChain(t *testing.T, names []string, expires time.Time) (leafPEM, intermediatePEM, keyPEM []byte, roots *x509.CertPool) {
	t.Helper()
	caKey := mustRSA(t)
	ca := mustCert(t, &x509.Certificate{
		SerialNumber:          mustSerial(t),
		Subject:               pkix.Name{CommonName: "Example CA"},
		NotBefore:             time.Now().Add(-2 * time.Hour),
		NotAfter:              time.Now().Add(90 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}, nil, &caKey.PublicKey, caKey)
	midKey := mustRSA(t)
	mid := mustCert(t, &x509.Certificate{
		SerialNumber:          mustSerial(t),
		Subject:               pkix.Name{CommonName: "Example Intermediate"},
		NotBefore:             time.Now().Add(-2 * time.Hour),
		NotAfter:              time.Now().Add(90 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}, ca, &midKey.PublicKey, caKey)
	leafKey := mustRSA(t)
	notBefore := time.Now().Add(-2 * time.Hour)
	if !expires.After(notBefore) {
		notBefore = expires.Add(-2 * time.Hour)
	}
	leaf := mustCert(t, &x509.Certificate{
		SerialNumber: mustSerial(t),
		Subject:      pkix.Name{CommonName: names[0]},
		NotBefore:    notBefore,
		NotAfter:     expires,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     names,
	}, mid, &leafKey.PublicKey, midKey)
	roots = x509.NewCertPool()
	roots.AddCert(ca)
	return marshalCert(leaf.Raw), marshalCert(mid.Raw), rsaKeyPEM(t, leafKey), roots
}

func mustRSA(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustSerial(t *testing.T) *big.Int {
	t.Helper()
	serial, err := certificateSerial()
	if err != nil {
		t.Fatal(err)
	}
	return serial
}

func mustCert(t *testing.T, template, parent *x509.Certificate, pub any, signer crypto.Signer) *x509.Certificate {
	t.Helper()
	if parent == nil {
		parent = template
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, pub, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func rsaKeyPEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func rsaPKCS8PEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func mustParseRSA(t *testing.T, keyPEM []byte) *rsa.PrivateKey {
	t.Helper()
	key, err := parsePrivateKey(keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("expected an RSA key")
	}
	return rsaKey
}
