package autostart

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const testExecutable = `D:\测试 程序\PHPTools.exe`
const testProfile = `E:\PHPTools 配置\当前用户`

func isolatedRegistration(t *testing.T) (registration, registry.Key) {
	t.Helper()
	path := fmt.Sprintf(`Software\PHPTools-Autostart-Test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := key.Close(); err != nil {
			t.Error(err)
		}
		if err := registry.DeleteKey(registry.CURRENT_USER, path); err != nil && !errors.Is(err, registry.ErrNotExist) {
			t.Errorf("清理测试注册表项 %s：%v", path, err)
		}
	})
	return registration{keyPath: path, approvedKeyPath: path + "-Approval"}, key
}

func TestCommandRoundTripPreservesPaths(t *testing.T) {
	for _, paths := range [][3]string{
		{testExecutable, testProfile, testProfile},
		{`D:\PHPTools\PHPTools.exe`, `E:\带空格 的配置\`, `E:\带空格 的配置`},
		{`\\host\中文 share\PHPTools.exe`, `\\host\配置 目录\`, `\\host\配置 目录\`},
		{`D:\PHPTools.exe`, `E:\`, `E:\`},
	} {
		cmd, err := command(paths[0], paths[1])
		if err != nil {
			t.Fatal(err)
		}
		args, err := windows.DecomposeCommandLine(cmd)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{paths[0], "--config-dir", paths[2]}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("命令丢失路径：%q => %#v，预期 %#v", cmd, args, want)
		}
	}
}

func TestInvalidPathsDoNotWriteRegistration(t *testing.T) {
	r, key := isolatedRegistration(t)
	for _, paths := range [][2]string{
		{"", testProfile}, {"PHPTools.exe", testProfile}, {`D:PHPTools.exe`, testProfile},
		{testExecutable, ""}, {testExecutable, "relative-profile"},
		{testExecutable, testProfile + "\x00bad"}, {testExecutable + `"`, testProfile},
	} {
		if _, err := r.status(paths[0], paths[1]); err == nil {
			t.Fatalf("读取接受无效路径：%q", paths)
		}
		for _, enabled := range []bool{false, true} {
			if err := r.set(paths[0], paths[1], enabled); err == nil {
				t.Fatalf("写入接受无效路径：%q", paths)
			}
		}
	}
	if _, _, err := key.GetStringValue("PHPTools"); !errors.Is(err, registry.ErrNotExist) {
		t.Fatalf("无效请求创建了启动项：%v", err)
	}
}

func TestRegistrationPersistsAndDisablesOnlyOwnValue(t *testing.T) {
	r, key := isolatedRegistration(t)
	if err := key.SetStringValue("OtherApplication", `C:\Other.exe`); err != nil {
		t.Fatal(err)
	}
	state, err := r.status(testExecutable, testProfile)
	if err != nil || state.Enabled || state.Conflict != "" {
		t.Fatalf("初始状态：%+v %v", state, err)
	}
	for range 2 {
		if err := r.set(testExecutable, testProfile, true); err != nil {
			t.Fatal(err)
		}
	}
	// A new reader must use persisted Windows state rather than cached settings.
	state, err = (registration{keyPath: r.keyPath, approvedKeyPath: r.approvedKeyPath}).status(testExecutable, testProfile)
	if err != nil || !state.Enabled || state.Conflict != "" {
		t.Fatalf("持久状态：%+v %v", state, err)
	}
	value, _, err := key.GetStringValue("PHPTools")
	if err != nil || !sameCommand(value, mustCommand(t, testExecutable, testProfile)) {
		t.Fatalf("注册表中的启动命令：%q %v", value, err)
	}
	for range 2 {
		if err := r.set(testExecutable, testProfile, false); err != nil {
			t.Fatal(err)
		}
	}
	state, err = r.status(testExecutable, testProfile)
	if err != nil || state.Enabled || state.Conflict != "" {
		t.Fatalf("关闭后状态：%+v %v", state, err)
	}
	other, _, err := key.GetStringValue("OtherApplication")
	if err != nil || other != `C:\Other.exe` {
		t.Fatalf("其他启动项受到影响：%q %v", other, err)
	}
}

func mustCommand(t *testing.T, exe, profile string) string {
	t.Helper()
	value, err := command(exe, profile)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestConflictingRegistrationsAreNeverOverwrittenOrDeleted(t *testing.T) {
	for _, test := range []struct {
		name  string
		write func(registry.Key) error
	}{
		{"other-program", func(key registry.Key) error {
			return key.SetStringValue("PHPTools", mustCommand(t, `C:\Other\app.exe`, testProfile))
		}},
		{"other-profile", func(key registry.Key) error {
			return key.SetStringValue("PHPTools", mustCommand(t, testExecutable, `E:\OtherProfile`))
		}},
		{"extra-argument", func(key registry.Key) error {
			return key.SetStringValue("PHPTools", mustCommand(t, testExecutable, testProfile)+" --unknown")
		}},
		{"missing-profile", func(key registry.Key) error {
			return key.SetStringValue("PHPTools", windows.ComposeCommandLine([]string{testExecutable}))
		}},
		{"wrong-type", func(key registry.Key) error {
			return key.SetDWordValue("PHPTools", 1)
		}},
		{"expandable-command", func(key registry.Key) error {
			return key.SetExpandStringValue("PHPTools", mustCommand(t, testExecutable, testProfile))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, key := isolatedRegistration(t)
			if err := test.write(key); err != nil {
				t.Fatal(err)
			}
			before, beforeType := readRawValue(t, key)
			state, err := r.status(testExecutable, testProfile)
			if err != nil || state.Enabled || state.Conflict == "" {
				t.Fatalf("冲突未正确显示：%+v %v", state, err)
			}
			for _, enabled := range []bool{true, false} {
				if err := r.set(testExecutable, testProfile, enabled); err == nil {
					t.Fatalf("错误允许修改冲突项：enabled=%v", enabled)
				}
				after, afterType := readRawValue(t, key)
				if beforeType != afterType || !bytes.Equal(before, after) {
					t.Fatal("冲突项被覆盖或删除")
				}
			}
		})
	}
}

func readRawValue(t *testing.T, key registry.Key) ([]byte, uint32) {
	t.Helper()
	n, _, err := key.GetValue("PHPTools", nil)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, n)
	_, valueType, err := key.GetValue("PHPTools", buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf, valueType
}

func TestExternalChangesAndWindowsPathCasingAreRespected(t *testing.T) {
	r, key := isolatedRegistration(t)
	value := mustCommand(t, strings.ToUpper(testExecutable), strings.ToUpper(testProfile))
	if err := key.SetStringValue("PHPTools", value); err != nil {
		t.Fatal(err)
	}
	state, err := r.status(testExecutable, testProfile)
	if err != nil || !state.Enabled || state.Conflict != "" {
		t.Fatalf("Windows 路径大小写：%+v %v", state, err)
	}
	if err := key.DeleteValue("PHPTools"); err != nil {
		t.Fatal(err)
	}
	state, err = r.status(testExecutable, testProfile)
	if err != nil || state.Enabled || state.Conflict != "" {
		t.Fatalf("未读取外部关闭结果：%+v %v", state, err)
	}
}

func TestMissingKeyIsDisabledAndInvalidRegistryLocationFails(t *testing.T) {
	r, key := isolatedRegistration(t)
	missing := registration{keyPath: r.keyPath + `\Missing`}
	state, err := missing.status(testExecutable, testProfile)
	if err != nil || state.Enabled || state.Conflict != "" {
		t.Fatalf("不存在的注册表键：%+v %v", state, err)
	}
	if err := missing.set(testExecutable, testProfile, false); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.OpenKey(key, "Missing", registry.QUERY_VALUE); !errors.Is(err, registry.ErrNotExist) {
		t.Fatalf("关闭操作意外创建了注册表键：%v", err)
	}
	invalid := registration{keyPath: "Software\\invalid\x00key"}
	if _, err := invalid.status(testExecutable, testProfile); err == nil {
		t.Fatal("读取错误未返回")
	}
	for _, enabled := range []bool{true, false} {
		if err := invalid.set(testExecutable, testProfile, enabled); err == nil {
			t.Fatal("写入错误未返回")
		}
	}
}

func isolatedApprovalKey(t *testing.T, r registration) registry.Key {
	t.Helper()
	key, _, err := registry.CreateKey(registry.CURRENT_USER, r.approvedKeyPath, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := key.Close(); err != nil {
			t.Error(err)
		}
		if err := registry.DeleteKey(registry.CURRENT_USER, r.approvedKeyPath); err != nil && !errors.Is(err, registry.ErrNotExist) {
			t.Errorf("清理测试审批项 %s：%v", r.approvedKeyPath, err)
		}
	})
	return key
}

func approvalRecord(state uint32) []byte {
	value := make([]byte, 12)
	binary.LittleEndian.PutUint32(value, state)
	return value
}

func TestWindowsStartupApprovalAllowsOnlyKnownEnabledStates(t *testing.T) {
	for _, code := range []uint32{2, 6, 3, 7, 0, 1, 8, 258} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			r, _ := isolatedRegistration(t)
			approval := isolatedApprovalKey(t, r)
			if err := approval.SetBinaryValue("PHPTools", approvalRecord(code)); err != nil {
				t.Fatal(err)
			}
			before, beforeType := readRawValue(t, approval)
			if err := r.set(testExecutable, testProfile, true); err != nil {
				t.Fatal(err)
			}
			state, err := r.status(testExecutable, testProfile)
			wantEnabled := code == 2 || code == 6
			if err != nil || state.Enabled != wantEnabled || (state.Conflict == "") != wantEnabled {
				t.Fatalf("审批状态 %d：%+v %v", code, state, err)
			}
			if code == 3 || code == 7 {
				if !strings.Contains(state.Conflict, "Windows 禁用") {
					t.Fatalf("未说明系统禁用原因：%q", state.Conflict)
				}
			}
			// A Windows block must not prevent removing our own Run registration.
			if err := r.set(testExecutable, testProfile, false); err != nil {
				t.Fatal(err)
			}
			state, err = r.status(testExecutable, testProfile)
			if err != nil || state.Enabled || state.Conflict != "" {
				t.Fatalf("关闭后：%+v %v", state, err)
			}
			after, afterType := readRawValue(t, approval)
			if beforeType != afterType || !bytes.Equal(before, after) {
				t.Fatal("应用修改了 Windows 的启动审批值")
			}
		})
	}
}

func TestMissingWindowsStartupApprovalIsNormalForNewRegistrations(t *testing.T) {
	r, _ := isolatedRegistration(t)
	if err := r.set(testExecutable, testProfile, true); err != nil {
		t.Fatal(err)
	}
	for _, createEmptyKey := range []bool{false, true} {
		if createEmptyKey {
			isolatedApprovalKey(t, r)
		}
		state, err := r.status(testExecutable, testProfile)
		if err != nil || !state.Enabled || state.Conflict != "" {
			t.Fatalf("无审批记录的新注册项：%+v %v", state, err)
		}
	}
}

func TestUnrecognizedWindowsApprovalNeverReportsEnabled(t *testing.T) {
	for _, length := range []int{0, 4, 11, 13} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			r, _ := isolatedRegistration(t)
			approval := isolatedApprovalKey(t, r)
			value := make([]byte, length)
			if length > 0 {
				value[0] = 2
			}
			if err := approval.SetBinaryValue("PHPTools", value); err != nil {
				t.Fatal(err)
			}
			if err := r.set(testExecutable, testProfile, true); err != nil {
				t.Fatal(err)
			}
			state, err := r.status(testExecutable, testProfile)
			if err != nil || state.Enabled || !strings.Contains(state.Conflict, "无法识别") {
				t.Fatalf("格式长度 %d：%+v %v", length, state, err)
			}
		})
	}
	t.Run("wrong-type", func(t *testing.T) {
		r, _ := isolatedRegistration(t)
		approval := isolatedApprovalKey(t, r)
		if err := approval.SetStringValue("PHPTools", "2"); err != nil {
			t.Fatal(err)
		}
		if err := r.set(testExecutable, testProfile, true); err != nil {
			t.Fatal(err)
		}
		state, err := r.status(testExecutable, testProfile)
		if err != nil || state.Enabled || !strings.Contains(state.Conflict, "无法读取") {
			t.Fatalf("非二进制审批值：%+v %v", state, err)
		}
	})
	t.Run("read-error", func(t *testing.T) {
		r, _ := isolatedRegistration(t)
		if err := r.set(testExecutable, testProfile, true); err != nil {
			t.Fatal(err)
		}
		r.approvedKeyPath += "\x00invalid"
		state, err := r.status(testExecutable, testProfile)
		if err != nil || state.Enabled || !strings.Contains(state.Conflict, "无法读取") {
			t.Fatalf("读取审批状态失败：%+v %v", state, err)
		}
	})
}

func TestWindowsStartupApprovalExternalChangeIsReflected(t *testing.T) {
	r, _ := isolatedRegistration(t)
	approval := isolatedApprovalKey(t, r)
	if err := r.set(testExecutable, testProfile, true); err != nil {
		t.Fatal(err)
	}
	for _, code := range []uint32{2, 3, 2} {
		if err := approval.SetBinaryValue("PHPTools", approvalRecord(code)); err != nil {
			t.Fatal(err)
		}
		state, err := r.status(testExecutable, testProfile)
		if err != nil || state.Enabled != (code == 2) {
			t.Fatalf("未读取外部审批变化 %d：%+v %v", code, state, err)
		}
	}
}
