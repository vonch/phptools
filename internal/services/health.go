package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"phptools/internal/model"
)

// CheckHealth only repairs verified metadata and disposable runtime directories.
// It never starts or stops a service, recreates configuration, or initializes data.
func (m *Manager) CheckHealth(ctx context.Context, pkg model.Package, original model.Instance, full bool) (model.Instance, []model.HealthItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkHealth(ctx, pkg, original, full, "all")
}

// InspectHealth reports conditions without applying directory or metadata
// repairs. Full configuration checks can write the component's diagnostic logs.
func (m *Manager) InspectHealth(ctx context.Context, pkg model.Package, original model.Instance, full bool) (model.Instance, []model.HealthItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkHealth(ctx, pkg, original, full, "")
}

// RepairHealth repeats all ownership checks and repairs only the selected item.
func (m *Manager) RepairHealth(ctx context.Context, pkg model.Package, original model.Instance, code string) (model.HealthItem, error) {
	if err := ctx.Err(); err != nil {
		return model.HealthItem{}, err
	}
	if code != "logs" && code != "temp" && code != "process" {
		return model.HealthItem{}, errors.New("此检测项不支持自动修复")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, items := m.checkHealth(ctx, pkg, original, false, code)
	if err := ctx.Err(); err != nil {
		return model.HealthItem{}, err
	}
	for _, item := range items {
		if item.ID == original.ID+":"+code {
			if item.Status == "ok" || item.Status == "repaired" {
				return item, nil
			}
			return item, errors.New(item.Message)
		}
	}
	for _, item := range items {
		if item.Status == "error" {
			return item, errors.New(item.Message)
		}
	}
	return model.HealthItem{}, errors.New("当前实例没有此修复项，请重新检测")
}

// repair selects a single safe repair, all automatic repairs, or inspection only.
func (m *Manager) checkHealth(ctx context.Context, pkg model.Package, original model.Instance, full bool, repair string) (model.Instance, []model.HealthItem) {
	i := original
	var items []model.HealthItem
	add := func(code, check, status, message, suggestion string) {
		items = append(items, model.HealthItem{ID: i.ID + ":" + code, TargetID: i.ID, TargetName: i.Name, Check: check, Status: status, Message: message, Suggestion: suggestion})
	}
	markRepairable := func() {
		item := &items[len(items)-1]
		item.Repairable = true
		item.RepairID = item.ID
	}
	if err := ctx.Err(); err != nil {
		add("cancelled", "环境检查", "error", err.Error(), "请重新执行检测。")
		return original, items
	}
	if err := healthSafePath(m.root, filepath.Join(m.root, ".phptools-runtime")); err != nil {
		add("root", "环境目录", "error", err.Error(), "请检查当前环境目录和目录链接，未自动修改。")
		return original, items
	}
	marker, markerErr := os.ReadFile(filepath.Join(m.root, ".phptools-runtime"))
	if markerErr != nil || string(marker) != "PHPTools runtime v1\n" {
		add("root", "环境目录", "error", "环境根目录缺少有效的 PHPTools 标记。", "请在设置中确认原环境位置；检查不会重建或接管未知目录。")
		return original, items
	}
	if err := m.validateInstance(i); err != nil {
		add("instance-path", "运行目录", "error", err.Error(), "请核对实例路径和端口，检查不会重建实例或数据。")
		return original, items
	}
	dir, _ := m.instanceDir(i.ID)
	if err := healthDirectory(dir); err != nil {
		add("instance-path", "运行目录", "error", "实例目录不可用："+err.Error(), "请恢复原实例目录或备份，检查不会创建新的实例树。")
		return original, items
	}
	for _, path := range []string{dir, i.ConfigPath, i.DataDir, i.LogPath, filepath.Join(dir, "process.json")} {
		if err := healthSafePath(m.root, path); err != nil {
			add("instance-path", "运行目录", "error", err.Error(), "请检查目录链接和实例路径；为避免操作其他目录，未自动修改。")
			return original, items
		}
	}
	add("instance-path", "运行目录", "ok", "实例路径位于当前环境内，未发现目录链接重定向。", "")
	if err := m.healthPackage(pkg, i); err != nil {
		add("package", "组件文件", "error", err.Error(), "在组件中心核对组件版本和路径。目录丢失时，先停止引用它的实例，再从相同版本的原始安装包或备份恢复组件目录，保留实例与数据目录。")
		return original, items
	}
	componentOK := true
	if err := healthRegularFile(i.Executable); err != nil {
		componentOK = false
		add("package", "组件文件", "error", "组件程序不可用："+err.Error(), "先在服务实例中停止引用此组件的实例，再从相同版本、相同架构的原始发行包或备份恢复组件程序及随附 DLL 到原安装位置，完成后重新检测。")
	} else {
		add("package", "组件文件", "ok", "组件程序存在，路径与实例安装包一致。", "")
	}
	// A record must authorize this exact component and config. Inspect performs
	// the Windows process identity and command-line verification afterwards.
	if b, err := os.ReadFile(filepath.Join(dir, "process.json")); err == nil {
		var p processRecord
		if json.Unmarshal(b, &p) == nil && (!samePath(p.Executable, i.Executable) || !samePath(p.Config, i.ConfigPath) || p.ConfigArgument != configArgument(i)) {
			add("process", "进程状态", "error", "进程归属记录与此实例的组件或配置不一致。", "请核对日志和进程命令行，保留 process.json 供排查；确认归属前不会自动修改或控制进程。")
			return original, items
		}
	}
	i = m.inspect(i)
	if i.State == "error" {
		add("process", "进程状态", "error", i.Error, "请核对进程可执行文件、命令行及 process.json；确认归属前不会修复目录或启停服务。")
		return i, items
	}
	if i != original {
		if repair != "all" && repair != "process" {
			add("process", "进程状态", "warning", "已保存的状态与核实后的进程状态不同。", "点击修复以保存核实后的状态；不会启动或停止进程。")
			markRepairable()
		} else if err := m.persist(i); err != nil {
			add("process", "进程状态", "error", "已核实进程状态，但保存修复结果失败："+err.Error(), "请检查环境目录写入权限和磁盘空间后重试，原状态已保留。")
			markRepairable()
			return original, items
		} else {
			add("process", "进程状态", "repaired", "已按实际进程身份修正并保存过期状态；进程归属文件已保留。", "")
		}
	} else {
		message := "实例已停止，未启动服务。"
		if i.State == "running" {
			message = "受管进程身份和配置归属正常。"
		}
		add("process", "进程状态", "ok", message, "")
	}
	filesOK := componentOK
	for _, required := range []struct{ code, check, path, suggestion string }{
		{"config", "配置文件", i.ConfigPath, "请从备份恢复原配置或手动核对，检查不会覆盖配置。"},
		{"data", "数据目录", i.DataDir, "请确认磁盘与原数据位置，必要时从备份恢复；检查不会创建或初始化数据。"},
	} {
		var err error
		if required.code == "data" {
			err = healthDirectory(required.path)
		} else {
			err = healthRegularFile(required.path)
		}
		if err != nil {
			filesOK = false
			add(required.code, required.check, "error", err.Error(), required.suggestion)
		} else {
			add(required.code, required.check, "ok", required.path+" 可用。", "")
		}
	}
	for _, runtimeDir := range []struct{ code, check, path string }{
		{"logs", "日志目录", filepath.Dir(i.LogPath)},
		{"temp", "临时目录", filepath.Join(dir, "temp")},
	} {
		if runtimeDir.code == "temp" && i.Kind != "nginx" {
			continue
		}
		if runtimeDir.code == "logs" && !samePath(runtimeDir.path, filepath.Join(dir, "logs")) {
			filesOK = false
			add(runtimeDir.code, runtimeDir.check, "warning", "日志目录使用了自定义路径，未自动重建。", "请手动确认日志目录，或恢复此实例 logs 目录下的日志路径。")
			continue
		}
		status, repairable, err := healthRuntimeDirectory(dir, runtimeDir.path, repair == "all" || repair == runtimeDir.code)
		if err != nil {
			filesOK = false
			add(runtimeDir.code, runtimeDir.check, "error", err.Error(), "请检查目录链接、文件占用、磁盘空间和写入权限。")
		} else if status == "warning" {
			filesOK = false
			add(runtimeDir.code, runtimeDir.check, status, "缺少运行目录："+runtimeDir.path, "点击修复可重建此运行目录，现有配置和数据将保留。")
		} else if status == "repaired" {
			add(runtimeDir.code, runtimeDir.check, status, "已重建缺失目录："+runtimeDir.path, "")
		} else {
			add(runtimeDir.code, runtimeDir.check, status, runtimeDir.path+" 可用。", "")
		}
		if repairable {
			markRepairable()
		}
	}
	if !full || !filesOK {
		return i, items
	}
	m.healthFull(ctx, pkg, i, add)
	return i, items
}

func (m *Manager) healthPackage(pkg model.Package, i model.Instance) error {
	filename := map[string]string{"php": "php-cgi.exe", "mysql": "mysqld.exe", "nginx": "nginx.exe", "redis": "redis-server.exe"}[i.Kind]
	if filename == "" || pkg.Kind != i.Kind || pkg.ID != i.PackageID || pkg.ID == "" {
		return errors.New("安装包类型或编号与实例不匹配")
	}
	if !within(m.root, pkg.Path) || samePath(m.root, pkg.Path) || !within(pkg.Path, i.Executable) || !strings.EqualFold(filepath.Base(i.Executable), filename) {
		return errors.New("组件程序不属于当前环境中关联的安装包")
	}
	if err := healthDirectory(pkg.Path); err != nil {
		return fmt.Errorf("组件目录不可用：%w", err)
	}
	for _, path := range []string{pkg.Path, i.Executable} {
		if err := healthSafePath(m.root, path); err != nil {
			return err
		}
	}
	return ValidatePHPPath(pkg, m.root)
}

// healthSafePath validates the existing ancestor of a potentially missing path.
// Ancestors are checked individually: Windows junctions can be ModeIrregular
// rather than ModeSymlink and EvalSymlinks may leave those names unchanged.
func healthSafePath(root, target string) error {
	if !filepath.IsAbs(root) || !filepath.IsAbs(target) || !within(root, target) {
		return errors.New("路径不属于当前受管目录")
	}
	if err := healthDirectory(root); err != nil {
		return fmt.Errorf("受管根目录不可用：%w", err)
	}
	resolvedChecked := false
	for path := filepath.Clean(target); ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err == nil {
			if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				return fmt.Errorf("路径包含目录链接或特殊文件，拒绝自动操作：%s", path)
			}
			if !resolvedChecked {
				resolved, resolveErr := filepath.EvalSymlinks(path)
				if resolveErr != nil {
					return fmt.Errorf("无法确认路径归属 %s：%w", path, resolveErr)
				}
				if !samePath(resolved, path) {
					return fmt.Errorf("路径包含目录链接重定向，拒绝自动操作：%s", path)
				}
				resolvedChecked = true
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if samePath(path, filepath.Dir(path)) {
			return nil
		}
	}
}

func healthDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("路径不是目录：%s", path)
	}
	return nil
}

func healthRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("路径不是普通文件：%s", path)
	}
	return nil
}

func healthRuntimeDirectory(root, path string, repair bool) (string, bool, error) {
	if !samePath(filepath.Dir(path), root) {
		return "error", false, errors.New("只允许重建实例下的日志或临时目录")
	}
	if err := healthSafePath(root, path); err != nil {
		return "error", false, err
	}
	if err := healthDirectory(path); err == nil {
		return "ok", false, nil
	} else if !os.IsNotExist(err) {
		return "error", false, err
	}
	if !repair {
		return "warning", true, nil
	}
	// These are direct children. Mkdir must fail if the instance root vanished
	// during inspection rather than reconstructing an entire instance tree.
	if err := os.Mkdir(path, 0700); err != nil {
		retryable := healthSafePath(root, path) == nil && os.IsNotExist(healthDirectory(path))
		return "error", retryable, err
	}
	if err := healthSafePath(root, path); err != nil {
		return "error", false, err
	}
	return "repaired", false, nil
}

func (m *Manager) healthFull(ctx context.Context, pkg model.Package, i model.Instance, add func(string, string, string, string, string)) {
	exe, err := findExecutable(pkg)
	if err != nil || !samePath(exe, i.Executable) {
		message := "安装包中的唯一程序与实例记录不一致。"
		if err != nil {
			message = err.Error()
		}
		add("config-test", "配置校验", "error", message, "请核对安装包和组件文件；未执行未知程序。")
		return
	}
	if err := healthSafePath(m.root, exe); err != nil {
		add("config-test", "配置校验", "error", err.Error(), "请核对组件目录链接；未执行程序。")
		return
	}
	if i.Kind != "nginx" && i.State == "stopped" {
		if err := availablePort(i.Port); err != nil {
			add("port", "监听端口", "error", err.Error(), "请通过占用端口的软件停止冲突服务；也可在服务实例中创建使用其他端口的新实例，并调整站点关联。")
			return
		}
		add("port", "监听端口", "ok", fmt.Sprintf("本机端口 %d 当前可用。", i.Port), "")
	}
	if i.Kind == "nginx" {
		// nginx -t can open logs and temporary paths. Verify configured paths
		// physically as well as using the existing managed-config restrictions.
		err := m.inspectNginxConfig(i, func(d nginxDirective) error {
			if len(d.args) < 2 {
				return nil
			}
			key, path := d.args[0], filepath.FromSlash(d.args[1])
			if key == "include" && path == "fastcgi_params" {
				path = filepath.Join(filepath.Dir(i.ConfigPath), path)
			}
			if key == "include" && samePath(path, m.siteConfigGlob()) {
				path = filepath.Dir(path)
			}
			if key == "include" || key == "error_log" || key == "access_log" || strings.HasSuffix(key, "_temp_path") {
				if key == "access_log" && path == "off" {
					return nil
				}
				return healthSafePath(m.root, path)
			}
			return nil
		})
		if err != nil {
			add("config-test", "配置校验", "error", err.Error(), "请手动修正配置中的路径或指令；检查不会覆盖配置。")
			return
		}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = m.validateConfig(checkCtx, pkg, i)
	cancel()
	if err != nil {
		add("config-test", "配置校验", "error", err.Error(), "请检查配置、Nginx 站点端口和日志；检查不会恢复默认配置或启停服务。")
		return
	}
	message := "配置校验命令通过。"
	if i.Kind == "redis" {
		message = "Redis 受管配置规则校验通过。"
	}
	add("config-test", "配置校验", "ok", message, "")
	if i.Kind == "nginx" {
		add("port", "监听端口", "ok", "主配置和站点配置的全部监听端口未发现其他进程冲突。", "")
	}
	if i.State != "running" {
		return
	}
	if i.Kind == "mysql" {
		dir, _ := m.instanceDir(i.ID)
		cli := filepath.Join(filepath.Dir(i.Executable), "mysql.exe")
		for _, path := range []string{filepath.Join(dir, "private"), cli} {
			if err := healthSafePath(m.root, path); err != nil {
				add("ready", "服务响应", "error", err.Error(), "请检查实例 private 目录和 MySQL 客户端路径；未运行数据库查询。")
				return
			}
		}
		if err := healthRegularFile(cli); err != nil {
			add("ready", "服务响应", "error", err.Error(), "请修复对应安装包中缺失的 MySQL 客户端。")
			return
		}
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
	defer probeCancel()
	if i.Kind == "php" {
		dialer := net.Dialer{Timeout: time.Second}
		conn, dialErr := dialer.DialContext(probeCtx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(i.Port)))
		err = dialErr
		if conn != nil {
			_ = conn.Close()
		}
	} else {
		err = m.probeReady(probeCtx, i)
	}
	if err != nil {
		add("ready", "服务响应", "warning", err.Error(), "进程仍在运行，请检查日志、认证和服务就绪状态；检查不会自动重启。")
	} else {
		message := "本机服务响应检查通过。"
		if i.Kind == "php" {
			message = "本机 FastCGI 端口可连接，未执行站点脚本。"
		}
		add("ready", "服务响应", "ok", message, "")
	}
}
