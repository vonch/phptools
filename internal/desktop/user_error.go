package desktop

import "strings"

func presentError(err error) string {
	if err == nil {
		return ""
	}
	if dialogCancel(err) {
		return "已取消。"
	}
	text := strings.ReplaceAll(err.Error(), "secret not found in keyring", "找不到这个 MySQL 实例的管理密码")
	if hasChinese(text) {
		return text
	}
	return "操作失败。请稍后重试。"
}

func withoutDialogCancel(path string, err error) (string, error) {
	if dialogCancel(err) {
		return "", nil
	}
	return path, err
}

func dialogCancel(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return text == "cancelled by user" || text == "canceled by user"
}

func hasChinese(text string) bool {
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}
