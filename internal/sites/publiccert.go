package sites

import (
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"phptools/internal/model"
)

var challengeTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

func PublicDomain(name string) bool {
	if !ValidDomain(name) || name == "localhost" {
		return false
	}
	for _, suffix := range []string{".test", ".local", ".localhost", ".invalid", ".example", ".example.com", ".example.net", ".example.org", ".home", ".lan", ".internal"} {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	return true
}

func ValidPublicEmail(email string) bool {
	email = strings.TrimSpace(email)
	if len(email) < 6 || len(email) > 254 || strings.ContainsAny(email, " \t\r\n") {
		return false
	}
	at := strings.Index(email, "@")
	if at <= 0 || at != strings.LastIndex(email, "@") || at == len(email)-1 {
		return false
	}
	host := email[at+1:]
	return strings.Contains(host, ".") && !strings.HasPrefix(host, ".") && !strings.HasSuffix(host, ".")
}

func PublicCertificateEmail(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "certs", "acme-email.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func SavePublicCertificateEmail(root, email string) error {
	email = strings.TrimSpace(email)
	if !ValidPublicEmail(email) {
		return errors.New("请填写有效的通知邮箱")
	}
	dir := filepath.Join(root, "certs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("无法保存通知邮箱")
	}
	tmp := filepath.Join(dir, "acme-email.txt.tmp")
	defer os.Remove(tmp)
	if err := os.WriteFile(tmp, []byte(email+"\n"), 0600); err != nil {
		return errors.New("无法保存通知邮箱")
	}
	if err := os.Rename(tmp, filepath.Join(dir, "acme-email.txt")); err != nil {
		return errors.New("无法保存通知邮箱")
	}
	return nil
}

func DescribeCertificate(root string, site model.Site) model.SiteCertificate {
	names := siteDomains(site)
	if names == nil {
		names = []string{}
	}
	info := model.SiteCertificate{
		SiteID:   site.ID,
		SiteName: site.Name,
		Domains:  names,
		Kind:     "none",
	}
	if info.SiteName == "" && len(names) > 0 {
		info.SiteName = names[0]
	}
	info.Eligible, info.Reason = certificateEligible(site, names)
	certPath, keyPath := certificatePaths(root, site.ID)
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		if !os.IsNotExist(err) {
			info.Kind = "invalid"
			info.Reason = "无法读取已有的 HTTPS 证书"
			info.Eligible = false
		}
		return info
	}
	cert, err := parseCertificate(certPEM)
	if err != nil {
		info.Kind = "invalid"
		info.Reason = "已有的 HTTPS 证书无法读取"
		info.Eligible = false
		return info
	}
	info.Issuer = cert.Issuer.CommonName
	info.NotAfter = cert.NotAfter.UTC().Format(time.RFC3339)
	info.Kind = "public"
	if ca, caErr := readCACertificate(root); caErr == nil && cert.CheckSignatureFrom(ca) == nil {
		info.Kind = "local"
	}
	if _, err = os.ReadFile(keyPath); err != nil {
		info.Kind = "invalid"
		info.Reason = "已有 HTTPS 证书的私钥无法读取"
		info.Eligible = false
	}
	return info
}

func siteDomains(site model.Site) []string {
	return certificateNames([]string{site.Domain, site.SecondDomain})
}

func certificateEligible(site model.Site, names []string) (bool, string) {
	if !site.Enabled {
		return false, "站点未启用，请先在站点管理中启用"
	}
	if len(names) == 0 {
		return false, "请先填写有效域名"
	}
	for _, name := range names {
		if !PublicDomain(name) {
			return false, "本地或保留域名不能申请公网证书"
		}
	}
	return true, ""
}

func readCACertificate(root string) (*x509.Certificate, error) {
	certPEM, err := os.ReadFile(filepath.Join(root, "certs", "ca.crt"))
	if err != nil {
		return nil, err
	}
	return parseCertificate(certPEM)
}

func retainPublicCertificate(root, certPath, keyPath string, names []string) (bool, error) {
	certPEM, err := os.ReadFile(certPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("无法读取已有的 HTTPS 证书")
	}
	cert, err := parseCertificate(certPEM)
	if err != nil {
		return false, errors.New("已有的 HTTPS 证书无法读取")
	}
	if ca, caErr := readCACertificate(root); caErr == nil && cert.CheckSignatureFrom(ca) == nil {
		return false, nil
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return false, errors.New("已有公网证书的私钥无法读取。请到证书菜单重新申请，或改回本地证书。")
	}
	key, err := parsePrivateKey(keyPEM)
	if err != nil || !publicKeyMatches(cert, key) {
		return false, errors.New("已有公网证书和私钥不匹配。请到证书菜单重新添加，或改回本地证书。")
	}
	if !coversNames(cert, names) {
		return false, errors.New("已有公网证书不包含当前域名。请到证书菜单重新添加，或改回本地证书。")
	}
	if time.Until(cert.NotAfter) < 30*24*time.Hour {
		return false, errors.New("公网证书即将到期。请到证书菜单重新添加或重新申请。")
	}
	return true, nil
}

func coversNames(cert *x509.Certificate, names []string) bool {
	if cert == nil || len(names) == 0 {
		return false
	}
	for _, name := range names {
		if !certificateMatches(cert, name) {
			return false
		}
	}
	return true
}

func certificateMatches(cert *x509.Certificate, name string) bool {
	name = strings.ToLower(name)
	candidates := cert.DNSNames
	if len(candidates) == 0 && cert.Subject.CommonName != "" {
		candidates = []string{cert.Subject.CommonName}
	}
	for _, candidate := range candidates {
		candidate = strings.ToLower(candidate)
		if candidate == name {
			return true
		}
		if strings.HasPrefix(candidate, "*.") && strings.Count(name, ".") == strings.Count(candidate, ".") && strings.HasSuffix(name, candidate[1:]) {
			return true
		}
	}
	return false
}

func NeedsHTTPChallengeServer(site model.Site) bool {
	return !site.Enabled || site.Port != 80
}

func ACMEHTTPConfig(root string, domains []string) (string, error) {
	names := make([]string, 0, len(domains))
	seen := map[string]bool{}
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" || seen[domain] {
			continue
		}
		if !PublicDomain(domain) {
			return "", errors.New("请使用可以公网访问的域名")
		}
		seen[domain] = true
		names = append(names, domain)
	}
	if len(names) == 0 {
		return "", errors.New("请使用可以公网访问的域名")
	}
	var b strings.Builder
	b.WriteString("# Generated by PHPTools: acme-http\nserver {\n    listen 0.0.0.0:80;\n    server_name ")
	b.WriteString(strings.Join(names, " "))
	b.WriteString(";\n    location ^~ /.well-known/acme-challenge/ {\n        default_type text/plain;\n        alias ")
	b.WriteString(acmeAlias(root))
	b.WriteString(";\n    }\n    location / { return 404; }\n}\n")
	return b.String(), nil
}

func WriteACMEHTTPConfig(root string, domains []string) (string, error) {
	body, err := ACMEHTTPConfig(root, domains)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "sites", "acme-http.conf")
	tmp := path + ".tmp"
	defer os.Remove(tmp)
	if err = os.WriteFile(tmp, []byte(body), 0600); err != nil {
		return "", errors.New("无法写入证书验证配置")
	}
	if err = os.Rename(tmp, path); err != nil {
		return "", errors.New("无法写入证书验证配置")
	}
	return path, nil
}

func acmeAlias(root string) string {
	alias := filepath.ToSlash(filepath.Join(root, "certs", "acme-web"))
	alias = strings.ReplaceAll(alias, "$", `\$`)
	if !strings.HasSuffix(alias, "/") {
		alias += "/"
	}
	return `"` + alias + `"`
}

func WriteChallenge(root, token, body string) error {
	if !challengeTokenPattern.MatchString(token) || body == "" || len(body) > 512 || strings.ContainsAny(body, "\r\n") {
		return errors.New("证书验证内容无效")
	}
	dir := filepath.Join(root, "certs", "acme-web")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("无法准备证书验证目录")
	}
	path := filepath.Join(dir, token)
	tmp := path + ".tmp"
	defer os.Remove(tmp)
	if err := os.WriteFile(tmp, []byte(body), 0644); err != nil {
		return errors.New("无法写入证书验证文件")
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.New("无法写入证书验证文件")
	}
	return nil
}

func RemoveChallenge(root, token string) error {
	if !challengeTokenPattern.MatchString(token) {
		return errors.New("证书验证内容无效")
	}
	err := os.Remove(filepath.Join(root, "certs", "acme-web", token))
	if err != nil && !os.IsNotExist(err) {
		return errors.New("无法删除证书验证文件")
	}
	return nil
}

func InstallPublicCertificate(root, id string, certPEM, keyPEM []byte) error {
	if !idPattern.MatchString(id) {
		return errors.New("站点 ID 无效")
	}
	cert, err := parseCertificate(certPEM)
	if err != nil || cert.NotAfter.Before(time.Now()) {
		return errors.New("公网证书无效")
	}
	key, err := parsePrivateKey(keyPEM)
	if err != nil || !publicKeyMatches(cert, key) {
		return errors.New("公网证书和私钥不匹配")
	}
	certPath, keyPath := certificatePaths(root, id)
	if err = writePEM(keyPath, keyPEM); err != nil {
		return err
	}
	return writePEM(certPath, certPEM)
}
