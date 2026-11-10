package packages

import (
	"bytes"
	"context"
	"crypto"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"phptools/internal/model"
)

func verifyPGP(ctx context.Context, archive string, pkg model.Package) error {
	signature, err := fetchBytes(ctx, pkg.SignatureURL, 128<<10)
	if err != nil {
		return fmt.Errorf("下载 PGP 签名：%w", err)
	}
	keyData, err := fetchBytes(ctx, pkg.SigningKeyURL, 1<<20)
	if err != nil {
		return fmt.Errorf("下载签名公钥：%w", err)
	}
	input, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer input.Close()
	return verifyDetached(ctx, input, signature, keyData, pkg.SigningFingerprint)
}
func verifyDetached(ctx context.Context, signed io.Reader, signature, keyData []byte, fingerprint string) error {
	keys, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(keyData))
	if err != nil {
		return fmt.Errorf("读取 PGP 公钥：%w", err)
	}
	trusted := openpgp.EntityList{}
	for _, key := range keys {
		if strings.EqualFold(hex.EncodeToString(key.PrimaryKey.Fingerprint), fingerprint) {
			trusted = append(trusted, key)
		}
	}
	if len(trusted) != 1 {
		return errors.New("PGP 公钥完整指纹不匹配固定信任指纹")
	}
	block, err := armor.Decode(bytes.NewReader(signature))
	if err != nil || block.Type != openpgp.SignatureType {
		return errors.New("PGP 签名格式无效")
	}
	raw, err := io.ReadAll(io.LimitReader(block.Body, 128<<10))
	if err != nil {
		return err
	}
	parsed, err := packet.Read(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	sig, ok := parsed.(*packet.Signature)
	if !ok {
		return errors.New("PGP 数据不是签名")
	}
	if sig.SigType != packet.SigTypeBinary {
		return errors.New("组件 ZIP 必须使用二进制 PGP 签名")
	}
	if sig.IssuerKeyId == nil {
		return errors.New("PGP 签名缺少签名者标识")
	}
	if sig.CreationTime.Before(trusted[0].PrimaryKey.CreationTime) || sig.CreationTime.After(time.Now().Add(24*time.Hour)) {
		return errors.New("PGP 签名时间无效")
	}
	if trusted[0].Revoked(time.Now()) {
		return errors.New("发布者签名公钥已吊销")
	}
	for _, key := range trusted.KeysByIdUsage(*sig.IssuerKeyId, packet.KeyFlagSign) {
		if key.Revoked(time.Now()) {
			return errors.New("发布者签名子密钥已吊销")
		}
	}
	// Historical releases remain verifiable after their release key expires. The
	// signature and key must have been valid at signing time; revocations above
	// are still checked against the current time.
	config := &packet.Config{Time: func() time.Time { return sig.CreationTime }}
	_, err = openpgp.CheckDetachedSignatureAndHash(trusted, &contextReader{ctx: ctx, r: signed}, bytes.NewReader(raw), []crypto.Hash{crypto.SHA256, crypto.SHA384, crypto.SHA512}, config)
	if err != nil {
		return fmt.Errorf("PGP 签名验证失败：%w", err)
	}
	return ctx.Err()
}
