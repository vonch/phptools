package desktop

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/zalando/go-keyring"
	"phptools/internal/model"
)

func (a *App) CreateInstance(input model.InstanceInput) (model.Instance, error) {
	if err := a.needRoot(); err != nil {
		return model.Instance{}, err
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	p, err := a.packageByID(input.PackageID)
	if err != nil {
		return model.Instance{}, err
	}
	if p.Path == "" {
		return model.Instance{}, errors.New("请先安装所选组件")
	}
	a.mu.RLock()
	for _, v := range a.data.Instances {
		if v.Port == input.Port {
			a.mu.RUnlock()
			return model.Instance{}, errors.New("端口已分配给其他实例")
		}
	}
	a.mu.RUnlock()
	if p.Kind == "mysql" && len(input.Password) < 8 {
		return model.Instance{}, errors.New("MySQL 管理密码至少需要 8 个字符")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	instance, err := a.manager.Create(ctx, p, input)
	if err != nil {
		return instance, err
	}
	if p.Kind == "mysql" {
		if err := keyring.Set(a.credentialService(), instance.ID, input.Password); err != nil {
			return instance, errors.New("无法把 MySQL 管理密码写入 Windows 凭据管理器")
		}
	}
	if err := a.saveInstance(instance); err != nil {
		if p.Kind == "mysql" {
			_ = keyring.Delete(a.credentialService(), instance.ID)
		}
		return instance, err
	}
	a.notify()
	return instance, nil
}

func (a *App) DeleteInstance(id string) error {
	if err := a.needRoot(); err != nil {
		return err
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	v, err := a.instanceByID(id)
	if err != nil {
		return err
	}
	v = a.manager.Inspect(v)
	if v.PID != 0 || v.State != "stopped" {
		return errors.New("只有确认已停止且归属正常的实例才能移除")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.data.Sites {
		if s.PHPInstanceID == id {
			return errors.New("站点仍引用此 PHP 实例")
		}
	}
	if err := a.db.Put("retained-instance", id, v); err != nil {
		return err
	}
	if err := a.db.Delete("instance", id); err != nil {
		return err
	}
	for i, item := range a.data.Instances {
		if item.ID == id {
			a.data.Instances = append(a.data.Instances[:i], a.data.Instances[i+1:]...)
			break
		}
	}
	return nil
}

func (a *App) start(ctx context.Context, id string) error {
	v, err := a.instanceByID(id)
	if err != nil {
		return err
	}
	p, err := a.packageByID(v.PackageID)
	if err != nil {
		return err
	}
	if v.Kind == "nginx" {
		a.mu.RLock()
		all := append([]model.Instance{}, a.data.Instances...)
		siteList := append([]model.Site{}, a.data.Sites...)
		a.mu.RUnlock()
		for _, other := range all {
			if other.Kind == "nginx" && other.ID != v.ID && a.manager.Inspect(other).PID != 0 {
				return errors.New("已有 Nginx 实例运行，请先停止后再切换版本")
			}
		}
		for _, s := range siteList {
			if s.Enabled && s.PHPInstanceID != "" {
				if err := a.start(ctx, s.PHPInstanceID); err != nil {
					return fmt.Errorf("启动站点 PHP：%w", err)
				}
			}
		}
	}
	v, err = a.manager.Start(ctx, p, v)
	if saveErr := a.saveInstance(v); err == nil {
		err = saveErr
	}
	return err
}

func (a *App) stop(ctx context.Context, id string) error {
	v, err := a.instanceByID(id)
	if err != nil {
		return err
	}
	v, err = a.manager.Stop(ctx, v)
	if saveErr := a.saveInstance(v); err == nil {
		err = saveErr
	}
	return err
}

func (a *App) instanceTask(action, id string) (string, error) {
	if _, err := a.instanceByID(id); err != nil {
		return "", err
	}
	return a.task(action, id, func(ctx context.Context, taskID string) error {
		a.opMu.Lock()
		defer a.opMu.Unlock()
		var err error
		switch action {
		case "start":
			err = a.start(ctx, id)
		case "stop":
			err = a.stop(ctx, id)
		case "restart":
			err = a.stop(ctx, id)
			if err == nil {
				err = a.start(ctx, id)
			}
		default:
			err = errors.New("未知操作")
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			a.checkAfterServiceFailure()
		}
		return err
	})
}

// StartInstalledPHP opens every installed PHP version. Different versions are
// separate programs, so each still has its own process, but the user does not
// start them one by one.
func (a *App) StartInstalledPHP() (string, error) {
	if err := a.needRoot(); err != nil {
		return "", err
	}
	return a.task("start-php", "php", func(ctx context.Context, id string) error {
		a.opMu.Lock()
		defer a.opMu.Unlock()
		if err := a.ensureServiceInstances(ctx); err != nil {
			return err
		}
		var failures []error
		for _, v := range a.orderedInstances(false) {
			if v.Kind != "php" {
				continue
			}
			if err := a.start(ctx, v.ID); err != nil {
				failures = append(failures, fmt.Errorf("%s：%w", v.Name, err))
			}
		}
		if len(failures) > 0 && !errors.Is(errors.Join(failures...), context.Canceled) {
			a.checkAfterServiceFailure()
		}
		return errors.Join(failures...)
	})
}

func (a *App) ensureInstalledPHPInstances(ctx context.Context) error {
	return a.ensureServiceInstances(ctx)
}

func (a *App) ensureServiceInstances(ctx context.Context) error {
	a.mu.RLock()
	pkgs := append([]model.Package{}, a.data.Packages...)
	instances := append([]model.Instance{}, a.data.Instances...)
	a.mu.RUnlock()
	used := map[int]bool{}
	has := map[string]bool{}
	for _, inst := range instances {
		used[inst.Port] = true
		has[inst.PackageID] = true
	}
	next := map[string]int{"php": 9100, "mysql": 3307, "nginx": 8081, "redis": 6380}
	label := map[string]string{"php": "PHP", "mysql": "MySQL", "nginx": "Nginx", "redis": "Redis"}
	for _, pkg := range pkgs {
		port, ok := next[pkg.Kind]
		if !ok || pkg.Path == "" || has[pkg.ID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		for used[port] {
			port++
		}
		if port > 65535 {
			return errors.New("没有可用端口来创建 " + label[pkg.Kind] + " 实例")
		}
		instance, err := a.manager.Create(ctx, pkg, model.InstanceInput{Name: label[pkg.Kind] + " " + pkg.Version, PackageID: pkg.ID, Port: port})
		if err != nil {
			return err
		}
		if pkg.Kind == "mysql" {
			password, err := newMySQLPassword()
			if err != nil {
				return err
			}
			if err = keyring.Set(a.credentialService(), instance.ID, password); err != nil {
				return errors.New("无法把 MySQL 管理密码写入 Windows 凭据管理器")
			}
		}
		if err := a.saveInstance(instance); err != nil {
			if pkg.Kind == "mysql" {
				_ = keyring.Delete(a.credentialService(), instance.ID)
			}
			return err
		}
		used[port] = true
		has[pkg.ID] = true
		next[pkg.Kind] = port + 1
	}
	return nil
}

func newMySQLPassword() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("无法生成 MySQL 管理密码")
	}
	for i, b := range raw {
		raw[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(raw), nil
}

func (a *App) StartInstance(id string) (string, error)   { return a.instanceTask("start", id) }
func (a *App) StopInstance(id string) (string, error)    { return a.instanceTask("stop", id) }
func (a *App) RestartInstance(id string) (string, error) { return a.instanceTask("restart", id) }

func (a *App) orderedInstances(stopping bool) []model.Instance {
	a.mu.RLock()
	all := append([]model.Instance{}, a.data.Instances...)
	a.mu.RUnlock()
	priority := map[string]int{"php": 0, "mysql": 1, "redis": 2, "nginx": 3}
	if stopping {
		priority = map[string]int{"nginx": 0, "php": 1, "mysql": 2, "redis": 3}
	}
	sort.SliceStable(all, func(i, j int) bool { return priority[all[i].Kind] < priority[all[j].Kind] })
	return all
}

func (a *App) all(ctx context.Context, stopping bool) error {
	var failures []error
	nginxStarted := false
	for _, v := range a.orderedInstances(stopping) {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		if stopping {
			err = a.stop(ctx, v.ID)
		} else {
			if v.Kind == "nginx" && nginxStarted {
				continue
			}
			err = a.start(ctx, v.ID)
			if v.Kind == "nginx" && err == nil {
				nginxStarted = true
			}
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s：%w", v.Name, err))
		}
	}
	return errors.Join(failures...)
}

func (a *App) StartAll() (string, error) {
	return a.task("start-all", "all", func(ctx context.Context, id string) error {
		a.opMu.Lock()
		defer a.opMu.Unlock()
		err := a.all(ctx, false)
		if err != nil && !errors.Is(err, context.Canceled) {
			a.checkAfterServiceFailure()
		}
		return err
	})
}
func (a *App) StopAll() (string, error) {
	return a.task("stop-all", "all", func(ctx context.Context, id string) error {
		a.opMu.Lock()
		defer a.opMu.Unlock()
		err := a.all(ctx, true)
		if err != nil && !errors.Is(err, context.Canceled) {
			a.checkAfterServiceFailure()
		}
		return err
	})
}

func (a *App) Quit() error {
	return a.quit(nil)
}

// beforeExit is used by the updater after every owned service has stopped.
// On preparation failure the manager and database remain available for retry.
func (a *App) quit(beforeExit func() error) error {
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return errors.New("正在停止服务")
	}
	a.closing = true
	updateDone := a.updateDone
	for _, cancel := range a.jobs {
		cancel()
	}
	a.mu.Unlock()
	a.wg.Wait()
	// A normal exit also waits for update cancellation cleanup. The updater's
	// own post-task shutdown must not wait for its own completion channel.
	if beforeExit == nil && updateDone != nil {
		<-updateDone
	}
	a.opMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var err error
	resumeNoted := false
	if beforeExit != nil && a.manager != nil {
		err = a.writeUpdateResume(a.runningInstanceIDs())
		resumeNoted = err == nil
	}
	if err == nil && a.manager != nil {
		err = a.all(ctx, true)
	}
	if err == nil && beforeExit != nil {
		err = beforeExit()
	}
	if err != nil {
		if resumeNoted {
			if clearErr := a.writeUpdateResume(nil); clearErr != nil {
				err = fmt.Errorf("%w；升级前的运行记录未能清除：%v", err, clearErr)
			}
		}
		a.opMu.Unlock()
		a.mu.Lock()
		a.closing = false
		a.data.Diagnostics = append(a.data.Diagnostics, model.Diagnostic{Level: "error", Message: "退出未完成：" + err.Error()})
		a.mu.Unlock()
		a.notify()
		return err
	}
	if a.db != nil {
		_ = a.db.Close()
	}
	a.opMu.Unlock()
	if a.hooks.Exit != nil {
		a.hooks.Exit()
	}
	return nil
}
