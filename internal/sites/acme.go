package sites

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/acme"
)

func ObtainPublicCertificate(ctx context.Context, root, email string, domains []string, progress func(string), ready func() error) ([]byte, []byte, error) {
	names := make([]string, 0, len(domains))
	seen := map[string]bool{}
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" || seen[domain] {
			continue
		}
		if !PublicDomain(domain) {
			return nil, nil, errors.New("请使用可以公网访问的域名")
		}
		seen[domain] = true
		names = append(names, domain)
	}
	if len(names) == 0 {
		return nil, nil, errors.New("请使用可以公网访问的域名")
	}
	if !ValidPublicEmail(email) {
		return nil, nil, errors.New("请填写有效的通知邮箱")
	}
	report := func(message string) {
		if progress != nil {
			progress(message)
		}
	}
	report("正在连接证书机构")
	account, err := loadOrCreateAccountKey(root)
	if err != nil {
		return nil, nil, err
	}
	client := &acme.Client{Key: account, DirectoryURL: acme.LetsEncryptURL, UserAgent: "PHPTools"}
	_, err = client.Register(ctx, &acme.Account{Contact: []string{"mailto:" + strings.TrimSpace(email)}}, acme.AcceptTOS)
	if errors.Is(err, acme.ErrAccountAlreadyExists) {
		_, err = client.GetReg(ctx, "")
	}
	if err != nil {
		return nil, nil, explainACME(err)
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(names...))
	if err != nil {
		return nil, nil, explainACME(err)
	}
	report("正在准备域名验证")
	var tokens []string
	type pending struct {
		authz *acme.Authorization
		chal  *acme.Challenge
	}
	var waiting []pending
	for _, url := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, url)
		if err != nil {
			return nil, nil, explainACME(err)
		}
		if authz.Status == acme.StatusValid {
			continue
		}
		var chal *acme.Challenge
		for _, candidate := range authz.Challenges {
			if candidate.Type == "http-01" {
				chal = candidate
				break
			}
		}
		if chal == nil {
			return nil, nil, errors.New("证书机构没有提供网页验证")
		}
		body, err := client.HTTP01ChallengeResponse(chal.Token)
		if err != nil {
			return nil, nil, errors.New("无法准备证书验证内容")
		}
		if err = WriteChallenge(root, chal.Token, body); err != nil {
			return nil, nil, err
		}
		tokens = append(tokens, chal.Token)
		waiting = append(waiting, pending{authz: authz, chal: chal})
	}
	defer func() {
		for _, token := range tokens {
			_ = RemoveChallenge(root, token)
		}
	}()
	if ready != nil {
		if err = ready(); err != nil {
			return nil, nil, err
		}
	}
	report("正在等待证书机构验证域名")
	for _, item := range waiting {
		if _, err = client.Accept(ctx, item.chal); err != nil {
			return nil, nil, explainACME(err)
		}
		if _, err = client.WaitAuthorization(ctx, item.authz.URI); err != nil {
			return nil, nil, explainACME(err)
		}
	}
	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return nil, nil, explainACME(err)
	}
	report("正在签发证书")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, errors.New("无法生成公网证书")
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: names[0]},
		DNSNames: names,
	}, key)
	if err != nil {
		return nil, nil, errors.New("无法生成公网证书")
	}
	ders, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return nil, nil, explainACME(err)
	}
	if len(ders) == 0 {
		return nil, nil, errors.New("证书机构没有返回证书")
	}
	var certPEM []byte
	for _, der := range ders {
		certPEM = append(certPEM, marshalCert(der)...)
	}
	keyPEM, err := marshalKey(key)
	if err != nil {
		return nil, nil, errors.New("无法生成公网证书")
	}
	return certPEM, keyPEM, nil
}

func loadOrCreateAccountKey(root string) (*ecdsa.PrivateKey, error) {
	path := filepath.Join(root, "certs", "acme-account.key")
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		key, err := parseKey(data)
		if err != nil {
			return nil, errors.New("公网证书账户已损坏。请删除当前环境目录下 certs 中的 acme-account.key 后重试。")
		}
		return key, nil
	case os.IsNotExist(err):
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, errors.New("无法创建公网证书账户")
		}
		encoded, err := marshalKey(key)
		if err != nil {
			return nil, errors.New("无法创建公网证书账户")
		}
		if err = writePEM(path, encoded); err != nil {
			return nil, err
		}
		return key, nil
	default:
		return nil, errors.New("无法读取公网证书账户")
	}
}

func explainACME(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "unauthorized") || strings.Contains(text, "connection") || strings.Contains(text, "timeout"):
		return errors.New("证书机构无法通过 80 端口访问这个域名。请确认域名已解析到这台电脑，并且公网可以访问 80 端口。")
	case strings.Contains(text, "rateLimited"):
		return errors.New("申请次数过多，请稍后再试。")
	case strings.Contains(text, "rejectedIdentifier") || strings.Contains(text, "unsupportedIdentifier"):
		return errors.New("这个域名不能申请公网证书。")
	case strings.Contains(text, "dns"):
		return errors.New("证书机构暂时无法查询这个域名，请稍后再试。")
	default:
		return fmt.Errorf("申请公网证书失败：%s", text)
	}
}
