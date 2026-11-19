//go:build !windows

package sites

import "errors"

func TrustCA(string) error {
	return errors.New("当前系统不能把本地证书加入信任")
}
