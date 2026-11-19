//go:build windows

package sites

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func TrustCA(root string) error {
	certPath, _ := caPaths(root)
	data, err := os.ReadFile(certPath)
	if err != nil {
		return errors.New("无法读取本地证书颁发机构")
	}
	cert, err := parseCertificate(data)
	if err != nil {
		return errors.New("本地证书颁发机构已损坏。请删除当前环境目录下的 certs 文件夹后重新保存。")
	}
	// Windows 证书存储用 SHA-1 指纹定位证书。
	sum := sha1.Sum(cert.Raw)
	thumb := strings.ToUpper(hex.EncodeToString(sum[:]))
	if certificateTrusted(thumb) {
		return nil
	}
	cmd := exec.Command("certutil", "-user", "-addstore", "Root", certPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err = cmd.Run(); err != nil && !certificateTrusted(thumb) {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("系统里没有找到证书工具，无法信任本地证书颁发机构")
		}
		return errors.New("没有信任本地证书颁发机构。请在 Windows 的安全提示中选择“是”，然后重新保存站点。")
	}
	return nil
}

func certificateTrusted(thumb string) bool {
	cmd := exec.Command("certutil", "-user", "-store", "Root", thumb)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run() == nil
}
