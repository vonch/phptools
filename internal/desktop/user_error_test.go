package desktop

import (
	"errors"
	"testing"
)

func TestTaskErrorsAreChinese(t *testing.T) {
	if got := presentError(errors.New("secret not found in keyring")); !hasChinese(got) || got == "secret not found in keyring" {
		t.Fatal(got)
	}
	if got := presentError(errors.New("exit status 1")); got != "操作失败。请稍后重试。" {
		t.Fatal(got)
	}
	const chinese = "端口 3307 已被占用"
	if got := presentError(errors.New(chinese)); got != chinese {
		t.Fatal(got)
	}
}
