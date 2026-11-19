package sites

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const certificateLifetime = 825 * 24 * time.Hour

type CertificateBackup struct {
	cert []byte
	key  []byte
}

func certificatePaths(root, id string) (string, string) {
	dir := filepath.Join(root, "certs", "sites")
	return filepath.Join(dir, id+".crt"), filepath.Join(dir, id+".key")
}

func caPaths(root string) (string, string) {
	dir := filepath.Join(root, "certs")
	return filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
}

func BackupCertificate(root, id string) (CertificateBackup, error) {
	if !idPattern.MatchString(id) {
		return CertificateBackup{}, errors.New("站点 ID 无效")
	}
	certPath, keyPath := certificatePaths(root, id)
	cert, err := os.ReadFile(certPath)
	if err != nil && !os.IsNotExist(err) {
		return CertificateBackup{}, errors.New("无法读取已有的 HTTPS 证书")
	}
	key, err := os.ReadFile(keyPath)
	if err != nil && !os.IsNotExist(err) {
		return CertificateBackup{}, errors.New("无法读取已有的 HTTPS 证书")
	}
	return CertificateBackup{cert: cert, key: key}, nil
}

func RestoreCertificate(root, id string, backup CertificateBackup) error {
	if !idPattern.MatchString(id) {
		return errors.New("站点 ID 无效")
	}
	certPath, keyPath := certificatePaths(root, id)
	if len(backup.cert) == 0 && len(backup.key) == 0 {
		return RemoveCertificate(root, id)
	}
	if err := writePEM(certPath, backup.cert); err != nil {
		return err
	}
	return writePEM(keyPath, backup.key)
}

func RemoveCertificate(root, id string) error {
	if !idPattern.MatchString(id) {
		return errors.New("站点 ID 无效")
	}
	certPath, keyPath := certificatePaths(root, id)
	for _, path := range []string{certPath, keyPath, certPath + ".tmp", keyPath + ".tmp"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return errors.New("无法删除 HTTPS 证书文件")
		}
	}
	return nil
}

func EnsureCertificate(root, id string, domains ...string) error {
	if !idPattern.MatchString(id) {
		return errors.New("站点 ID 无效")
	}
	names := certificateNames(domains)
	if len(names) == 0 {
		return errors.New("请输入有效域名，例如 demo.test")
	}
	certPath, keyPath := certificatePaths(root, id)
	keep, err := retainPublicCertificate(root, certPath, keyPath, names)
	if err != nil {
		return err
	}
	if keep {
		return nil
	}
	caCert, caKey, err := loadOrCreateCA(root)
	if err != nil {
		return err
	}
	if reusable(certPath, keyPath, caCert, names) {
		return nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return errors.New("无法生成 HTTPS 证书")
	}
	serial, err := certificateSerial()
	if err != nil {
		return errors.New("无法生成 HTTPS 证书")
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: names[0]},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(certificateLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     names,
	}
	for _, name := range names {
		if name == "localhost" {
			template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
			break
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return errors.New("无法生成 HTTPS 证书")
	}
	keyPEM, err := marshalKey(key)
	if err != nil {
		return errors.New("无法生成 HTTPS 证书")
	}
	if err = writePEM(keyPath, keyPEM); err != nil {
		return err
	}
	if err = writePEM(certPath, marshalCert(der)); err != nil {
		return err
	}
	return nil
}

func loadOrCreateCA(root string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPath, keyPath := caPaths(root)
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		cert, err := parseCertificate(certPEM)
		if err != nil {
			return nil, nil, errors.New("本地证书颁发机构已损坏。请删除当前环境目录下的 certs 文件夹后重新保存。")
		}
		key, err := parseKey(keyPEM)
		if err != nil || !publicKeyEqual(cert.PublicKey, key) {
			return nil, nil, errors.New("本地证书颁发机构已损坏。请删除当前环境目录下的 certs 文件夹后重新保存。")
		}
		return cert, key, nil
	case os.IsNotExist(certErr) && os.IsNotExist(keyErr):
		return createCA(certPath, keyPath)
	default:
		if certErr != nil && !os.IsNotExist(certErr) || keyErr != nil && !os.IsNotExist(keyErr) {
			return nil, nil, errors.New("无法读取本地证书颁发机构")
		}
		return nil, nil, errors.New("本地证书颁发机构已损坏。请删除当前环境目录下的 certs 文件夹后重新保存。")
	}
}

func createCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, errors.New("无法生成 HTTPS 证书")
	}
	serial, err := certificateSerial()
	if err != nil {
		return nil, nil, errors.New("无法生成 HTTPS 证书")
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "PHPTools Local CA", Organization: []string{"PHPTools"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certificateLifetime),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, errors.New("无法生成 HTTPS 证书")
	}
	keyPEM, err := marshalKey(key)
	if err != nil {
		return nil, nil, errors.New("无法生成 HTTPS 证书")
	}
	if err = writePEM(keyPath, keyPEM); err != nil {
		return nil, nil, err
	}
	certPEM := marshalCert(der)
	if err = writePEM(certPath, certPEM); err != nil {
		return nil, nil, err
	}
	cert, err := parseCertificate(certPEM)
	if err != nil {
		return nil, nil, errors.New("无法生成 HTTPS 证书")
	}
	return cert, key, nil
}

func certificateNames(domains []string) []string {
	names := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		if !ValidDomain(domain) {
			return nil
		}
		names = append(names, domain)
	}
	return names
}

func reusable(certPath, keyPath string, ca *x509.Certificate, domains []string) bool {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return false
	}
	cert, err := parseCertificate(certPEM)
	if err != nil || cert.CheckSignatureFrom(ca) != nil {
		return false
	}
	key, err := parseKey(keyPEM)
	if err != nil || !publicKeyEqual(cert.PublicKey, key) {
		return false
	}
	if !namesMatch(cert, domains) || time.Until(cert.NotAfter) < 30*24*time.Hour {
		return false
	}
	return true
}

func namesMatch(cert *x509.Certificate, domains []string) bool {
	if len(cert.DNSNames) != len(domains) {
		return false
	}
	wantsLoopback := false
	for i, domain := range domains {
		if cert.DNSNames[i] != domain {
			return false
		}
		if domain == "localhost" {
			wantsLoopback = true
		}
	}
	if wantsLoopback {
		return len(cert.IPAddresses) == 1 && cert.IPAddresses[0].Equal(net.ParseIP("127.0.0.1"))
	}
	return len(cert.IPAddresses) == 0
}

func publicKeyEqual(public any, key *ecdsa.PrivateKey) bool {
	candidate, ok := public.(*ecdsa.PublicKey)
	return ok && candidate.Equal(&key.PublicKey)
}

func certificateSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}

func marshalKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func marshalCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func parseCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("证书无效")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("私钥无效")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("私钥无效")
	}
	return key, nil
}

func writePEM(path string, data []byte) error {
	if len(data) == 0 {
		return errors.New("无法写入 HTTPS 证书")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("无法写入 HTTPS 证书")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return errors.New("无法写入 HTTPS 证书")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tmp)
		return errors.New("无法写入 HTTPS 证书")
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return errors.New("无法写入 HTTPS 证书")
	}
	return nil
}
