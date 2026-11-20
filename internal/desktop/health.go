package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"phptools/internal/model"
	"phptools/internal/services"
	"phptools/internal/sites"
)

// CheckEnvironment reports problems through the existing cancellable task queue.
// Manual checks leave repairs for each item's button.
func (a *App) CheckEnvironment() (string, error) {
	return a.task("environment-check", "environment", func(ctx context.Context, taskID string) error {
		a.opMu.Lock()
		defer a.opMu.Unlock()
		return a.inspectEnvironment(ctx, "manual", true, taskID)
	})
}

// RepairHealthItem revalidates the current report and the instance after taking
// the operation lock, then applies only the requested safe repair.
func (a *App) RepairHealthItem(id string) (string, error) {
	separator := strings.LastIndex(id, ":")
	if separator < 1 {
		return "", errors.New("此检测项不支持自动修复")
	}
	targetID, code := id[:separator], id[separator+1:]
	if code != "logs" && code != "temp" && code != "process" {
		return "", errors.New("此检测项不支持自动修复")
	}
	if !a.canRepairHealthItem(id, targetID) {
		return "", errors.New("该检测项当前没有可执行的安全修复，请重新检测")
	}
	return a.task("environment-repair", id, func(ctx context.Context, taskID string) error {
		a.opMu.Lock()
		defer a.opMu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if !a.canRepairHealthItem(id, targetID) {
			return errors.New("检测结果已变化，请重新检测后再修复")
		}
		a.mu.RLock()
		refreshFull := a.data.HealthReport != nil && a.data.HealthReport.Full
		a.mu.RUnlock()
		instance, err := a.instanceByID(targetID)
		if err != nil {
			return err
		}
		pkg, err := a.packageByID(instance.PackageID)
		if err != nil {
			return err
		}
		a.progress(taskID, 0, 1, "正在修复 "+instance.Name)
		result, repairErr := a.manager.RepairHealth(ctx, pkg, instance, code)
		// Refresh without repairing other items. If ownership changed since the
		// last report, the new report explains the blocking condition.
		refreshErr := a.inspectEnvironment(ctx, "manual", refreshFull, taskID)
		if repairErr == nil && result.Status == "repaired" {
			a.mu.Lock()
			if a.data.HealthReport != nil {
				for n, item := range a.data.HealthReport.Items {
					if item.ID == id && item.Status == "ok" {
						a.data.HealthReport.Items[n] = result
						break
					}
				}
			}
			a.mu.Unlock()
		}
		a.notify()
		return errors.Join(repairErr, refreshErr)
	})
}

func (a *App) canRepairHealthItem(id, targetID string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.data.HealthReport != nil {
		for _, item := range a.data.HealthReport.Items {
			if item.ID == id && item.TargetID == targetID && item.Repairable && item.RepairID == id && (item.Status == "warning" || item.Status == "error") {
				return true
			}
		}
	}
	return false
}

// The caller holds opMu, except during construction before App is published.
func (a *App) checkEnvironment(ctx context.Context, trigger string, full bool, taskID string) error {
	return a.environmentReport(ctx, trigger, full, taskID, true)
}

func (a *App) inspectEnvironment(ctx context.Context, trigger string, full bool, taskID string) error {
	return a.environmentReport(ctx, trigger, full, taskID, false)
}

func (a *App) environmentReport(ctx context.Context, trigger string, full bool, taskID string, autoRepair bool) error {
	a.mu.RLock()
	root := a.data.Settings.Root
	instances := append([]model.Instance{}, a.data.Instances...)
	allSites := append([]model.Site{}, a.data.Sites...)
	pkgs := append([]model.Package{}, a.data.Packages...)
	manager := a.manager
	a.mu.RUnlock()
	report := &model.HealthReport{StartedAt: model.Now(), Trigger: trigger, Full: full, Items: []model.HealthItem{}}
	add := func(id, targetID, targetName, check, status, message, suggestion string) {
		report.Items = append(report.Items, model.HealthItem{ID: id, TargetID: targetID, TargetName: targetName, Check: check, Status: status, Message: message, Suggestion: suggestion})
	}
	defer func() {
		if ctx.Err() != nil {
			message := "检测未完成，仅显示已检查的项目"
			if autoRepair {
				message = "检测未完成，已完成的安全修复予以保留"
			}
			add("environment:cancelled", "environment", "检测", "检测进度", "warning", message, "重新点击检测可继续检查剩余项目。")
		}
		report.CompletedAt = model.Now()
		a.mu.Lock()
		a.data.HealthReport = report
		a.mu.Unlock()
		a.notify()
	}()
	if manager == nil || root == "" {
		return errors.New("请先选择独立环境目录")
	}
	marker, err := os.ReadFile(filepath.Join(root, ".phptools-runtime"))
	resolved, resolveErr := filepath.EvalSymlinks(root)
	if err != nil || string(marker) != "PHPTools runtime v1\n" || resolveErr != nil || !strings.EqualFold(filepath.Clean(root), filepath.Clean(resolved)) {
		add("environment:root", "environment", "环境目录", "目录归属", "error", "环境目录的归属标记或实际路径异常，已停止修复", "检查环境目录是否被移动、替换或设为目录联接，再重新打开应用。")
		return nil
	}
	add("environment:root", "environment", "环境目录", "目录归属", "ok", root, "")
	byPackage := make(map[string]model.Package, len(pkgs))
	for _, p := range pkgs {
		byPackage[p.ID] = p
	}
	for n, current := range instances {
		if err := ctx.Err(); err != nil {
			return err
		}
		if taskID != "" {
			a.progress(taskID, int64(n), int64(len(instances)+len(allSites)+1), "正在检查 "+current.Name)
		}
		pkg, found := byPackage[current.PackageID]
		if !found {
			add(current.ID+":package", current.ID, current.Name, "组件文件", "error", "实例引用的组件不存在", "在组件中心安装原版本，或在服务实例中核对组件引用。")
			continue
		}
		var next model.Instance
		var checks []model.HealthItem
		if autoRepair {
			next, checks = manager.CheckHealth(ctx, pkg, current, full)
		} else {
			next, checks = manager.InspectHealth(ctx, pkg, current, full)
		}
		instances[n] = next
		report.Items = append(report.Items, checks...)
	}
	for n, site := range allSites {
		if err := ctx.Err(); err != nil {
			return err
		}
		if taskID != "" {
			a.progress(taskID, int64(len(instances)+n), int64(len(instances)+len(allSites)+1), "正在检查站点 "+site.Name)
		}
		report.Items = append(report.Items, checkSiteHealth(root, site, allSites, instances, byPackage)...)
	}
	if full && len(sites.Domains(allSites)) > 0 {
		if err := sites.CheckHosts(sites.Domains(allSites)); err != nil {
			add("environment:hosts", "environment", "本地域名", "域名映射", "warning", err.Error(), "在站点管理中核对域名后保存站点，以备份并同步 hosts；有冲突的其他条目需要手动处理。")
		} else {
			add("environment:hosts", "environment", "本地域名", "域名映射", "ok", "hosts 的 PHPTools 域名映射已同步", "")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if taskID != "" {
		a.progress(taskID, int64(len(instances)+len(allSites)+1), int64(len(instances)+len(allSites)+1), "检测完成，请查看检测结果")
	}
	return nil
}

func checkSiteHealth(root string, site model.Site, all []model.Site, instances []model.Instance, pkgs map[string]model.Package) []model.HealthItem {
	items := []model.HealthItem{}
	add := func(code, check, status, message, suggestion string) {
		items = append(items, model.HealthItem{ID: "site:" + site.ID + ":" + code, TargetID: site.ID, TargetName: site.Name, Check: check, Status: status, Message: message, Suggestion: suggestion})
	}
	if !site.Enabled {
		add("disabled", "站点状态", "ok", "站点未启用，无需加载配置", "")
		return items
	}
	validated, err := sites.Validate(site, all, instances)
	if err != nil {
		add("directory", "站点目录", "error", err.Error(), "在站点管理中核对项目目录、入口目录、域名和 PHP 实例后重新保存。")
		return items
	}
	add("directory", "站点目录", "ok", filepath.Join(validated.Root, validated.Entry), "")
	var php *model.Instance
	nginxRunning := false
	nginxExists := false
	for n := range instances {
		i := &instances[n]
		if i.ID == site.PHPInstanceID {
			php = i
		}
		if i.Kind == "nginx" {
			nginxExists = true
			nginxRunning = nginxRunning || i.State == "running"
		}
	}
	if !nginxExists {
		add("nginx", "Nginx 依赖", "warning", "尚未创建 Nginx 实例，站点暂时无法访问", "在组件中心安装 Nginx 并创建服务实例。")
	}
	if php != nil {
		pkg, found := pkgs[php.PackageID]
		if !found {
			add("php", "PHP 依赖", "error", "PHP 实例引用的组件不存在", "在组件中心核对 PHP 组件，或为站点选择可用的 PHP 实例。")
		} else if err := services.ValidatePHPPath(pkg, filepath.Join(validated.Root, validated.Entry)); err != nil {
			add("php", "PHP 依赖", "error", err.Error(), "调整项目路径或在站点管理中选择支持该路径的 PHP 版本。")
		} else if nginxRunning && php.State != "running" {
			add("php", "PHP 依赖", "warning", "Nginx 正在运行，但此站点的 PHP 实例尚未运行", "在服务实例中启动对应 PHP，或查看其实例日志。")
		} else {
			add("php", "PHP 依赖", "ok", "已关联 "+php.Name, "")
		}
	}
	path := filepath.Join(sites.ConfigDirectory(root), site.ID+".conf")
	data, err := os.ReadFile(path)
	if err != nil {
		add("config", "站点配置", "error", fmt.Sprintf("无法读取站点配置：%v", err), "在站点管理中保存该站点，以重新生成配置并完成 Nginx 校验。")
		return items
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !strings.EqualFold(filepath.Clean(path), filepath.Clean(resolved)) {
		add("config", "站点配置", "error", "站点配置使用了链接或路径无法核实", "检查环境 conf 目录，恢复受管文件后保存站点。")
		return items
	}
	if !bytes.Equal(data, []byte(sites.Render(root, validated, php))) {
		add("config", "站点配置", "warning", "配置文件与已保存的站点设置不同，已保留现有内容", "核对手动修改后，在站点管理中保存站点，以重新生成并校验配置。")
	} else {
		add("config", "站点配置", "ok", "配置文件与已保存的站点设置一致", "")
	}
	return items
}

func (a *App) checkAfterServiceFailure() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.checkEnvironment(ctx, "operation", false, "")
}
