package sites

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"time"
)

func NormalizeImportedCertificate(domains []string, certPEM, keyPEM []byte, roots *x509.CertPool) ([]byte, []byte, error) {
	if len(domains) == 0 {
		return nil, nil, errors.New("请先填写有效域名")
	}
	_, chain, blocks, err := orderedCertificates(certPEM)
	if err != nil {
		return nil, nil, err
	}
	key, err := parsePrivateKey(keyPEM)
	if err != nil {
		return nil, nil, err
	}
	leaf, ordered := certificateMatchingKey(chain, blocks, key)
	if leaf == nil {
		return nil, nil, errors.New("证书和私钥不匹配")
	}
	if leaf.NotAfter.Before(time.Now()) {
		return nil, nil, errors.New("证书已过期")
	}
	if !coversNames(leaf, domains) {
		return nil, nil, errors.New("证书不包含站点域名")
	}
	if roots == nil {
		roots, err = x509.SystemCertPool()
		if err != nil {
			return nil, nil, errors.New("无法读取系统信任的证书颁发机构")
		}
	}
	intermediates := x509.NewCertPool()
	for _, extra := range chain {
		if extra != leaf {
			intermediates.AddCert(extra)
		}
	}
	for _, domain := range domains {
		opts := x509.VerifyOptions{DNSName: domain, Roots: roots, Intermediates: intermediates, CurrentTime: time.Now()}
		if _, err = leaf.Verify(opts); err != nil {
			var unknown x509.UnknownAuthorityError
			if errors.As(err, &unknown) {
				return nil, nil, errors.New("系统不信任这张证书的颁发机构，请提供包含中间证书的完整证书文件")
			}
			return nil, nil, errors.New("这张证书不能用于站点域名 " + domain)
		}
	}
	return ordered, keyPEM, nil
}

func orderedCertificates(data []byte) (*x509.Certificate, []*x509.Certificate, [][]byte, error) {
	var certs []*x509.Certificate
	var blocks [][]byte
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, nil, nil, errors.New("证书文件无效")
		}
		certs = append(certs, cert)
		blocks = append(blocks, pem.EncodeToMemory(block))
	}
	if len(certs) == 0 {
		return nil, nil, nil, errors.New("证书文件无效")
	}
	return certs[0], certs, blocks, nil
}

func certificateMatchingKey(certs []*x509.Certificate, blocks [][]byte, key crypto.Signer) (*x509.Certificate, []byte) {
	for index, cert := range certs {
		if publicKeyMatches(cert, key) {
			ordered := make([][]byte, 0, len(blocks))
			ordered = append(ordered, blocks[index])
			ordered = append(ordered, blocks[:index]...)
			ordered = append(ordered, blocks[index+1:]...)
			return cert, bytesJoin(ordered)
		}
	}
	return nil, nil
}

func parsePrivateKey(data []byte) (crypto.Signer, error) {
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "ENCRYPTED PRIVATE KEY" || x509.IsEncryptedPEMBlock(block) {
			return nil, errors.New("私钥有密码，请先导出为未加密的 PEM")
		}
		switch block.Type {
		case "PRIVATE KEY":
			parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, errors.New("私钥无效")
			}
			signer, ok := parsed.(crypto.Signer)
			if !ok {
				return nil, errors.New("私钥无效")
			}
			return signer, nil
		case "RSA PRIVATE KEY":
			key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				return nil, errors.New("私钥无效")
			}
			return key, nil
		case "EC PRIVATE KEY":
			key, err := x509.ParseECPrivateKey(block.Bytes)
			if err != nil {
				return nil, errors.New("私钥无效")
			}
			return key, nil
		}
	}
	return nil, errors.New("私钥无效")
}

func publicKeyMatches(cert *x509.Certificate, key crypto.Signer) bool {
	if cert == nil || key == nil {
		return false
	}
	switch pub := cert.PublicKey.(type) {
	case *ecdsa.PublicKey:
		priv, ok := key.(*ecdsa.PrivateKey)
		return ok && priv.PublicKey.Equal(pub)
	case *rsa.PublicKey:
		priv, ok := key.(*rsa.PrivateKey)
		return ok && priv.PublicKey.Equal(pub)
	case ed25519.PublicKey:
		priv, ok := key.(ed25519.PrivateKey)
		if !ok {
			return false
		}
		derived, ok := priv.Public().(ed25519.PublicKey)
		return ok && derived.Equal(pub)
	default:
		return false
	}
}

func bytesJoin(parts [][]byte) []byte {
	var out []byte
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}
