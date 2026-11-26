package desktop

import (
	"context"
	"errors"
	"fmt"

	"phptools/internal/model"
	"phptools/internal/packages"
)

func (a *App) task(action, target string, work func(context.Context, string) error) (string, error) {
	if err := a.needRoot(); err != nil {
		return "", err
	}
	return a.queueTask(action, target, work, nil)
}

// after runs outside wg, so a successful update can use the common Quit path
// without waiting on its own worker. Only updates use this exclusive phase.
func (a *App) queueTask(action, target string, work func(context.Context, string) error, after func(error) error) (string, error) {
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return "", errors.New("软件正在停止服务")
	}
	if a.updating {
		a.mu.Unlock()
		return "", errors.New("应用正在升级，请等待完成或取消升级任务")
	}
	for _, t := range a.data.Tasks {
		if action == "app-update" && t.State == "running" {
			a.mu.Unlock()
			return "", errors.New("请等待其他后台任务完成，再升级应用")
		}
		if t.State == "running" && t.Action == action && t.TargetID == target {
			a.mu.Unlock()
			return "", errors.New("相同任务正在执行")
		}
	}
	var updateDone chan struct{}
	if action == "app-update" {
		a.updating = true
		updateDone = make(chan struct{})
		a.updateDone = updateDone
	}
	id := newID()
	ctx, cancel := context.WithCancel(context.Background())
	a.jobs[id] = cancel
	a.data.Tasks = append([]model.Task{{ID: id, Action: action, TargetID: target, State: "running", StartedAt: model.Now()}}, a.data.Tasks...)
	if len(a.data.Tasks) > 100 {
		a.data.Tasks = a.data.Tasks[:100]
	}
	a.wg.Add(1)
	a.mu.Unlock()
	a.notify()
	go func() {
		if updateDone != nil {
			defer close(updateDone)
		}
		// Ordinary workers publish their final state before releasing wg.
		if after == nil {
			defer a.wg.Done()
		}
		err := work(ctx, id)
		a.mu.Lock()
		// Commit cancellation before removing the task from the cancellable
		// jobs map, so a successful CancelTask cannot race the update handoff.
		if err == nil {
			err = ctx.Err()
		}
		cancel()
		delete(a.jobs, id)
		a.mu.Unlock()
		if after != nil {
			a.wg.Done()
			err = after(err)
		}
		a.mu.Lock()
		if action == "app-update" {
			a.updating = false
		}
		for i := range a.data.Tasks {
			if a.data.Tasks[i].ID == id {
				t := &a.data.Tasks[i]
				if errors.Is(err, context.Canceled) {
					t.State = "cancelled"
					t.Message = "已取消"
					if action == "database-import" {
						t.Message = "导入已取消；部分 SQL 可能已经执行，请检查数据库后再决定是否重试"
					}
				} else if err != nil {
					t.State = "failed"
					t.Error = presentError(err)
				} else {
					t.State = "completed"
					t.Progress = 100
					t.Message = "已完成"
				}
				break
			}
		}
		a.mu.Unlock()
		a.notify()
	}()
	return id, nil
}

func (a *App) progress(id string, done, total int64, message string) {
	a.mu.Lock()
	for i := range a.data.Tasks {
		if a.data.Tasks[i].ID == id {
			t := &a.data.Tasks[i]
			t.BytesDone = done
			t.BytesTotal = total
			t.Message = message
			if total > 0 {
				t.Progress = float64(done) * 100 / float64(total)
			}
			break
		}
	}
	a.mu.Unlock()
	a.notify()
}

func (a *App) CancelTask(id string) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	cancel, ok := a.jobs[id]
	if !ok {
		return errors.New("任务已结束")
	}
	cancel()
	return nil
}

func (a *App) RefreshCatalog() (string, error) {
	return a.task("refresh", "catalog", func(ctx context.Context, id string) error {
		a.mu.RLock()
		old := append([]model.Package{}, a.data.Packages...)
		a.mu.RUnlock()
		list, diagnostics, err := packages.Refresh(ctx, old)
		if err != nil {
			return err
		}
		a.opMu.Lock()
		defer a.opMu.Unlock()
		a.mu.Lock()
		defer a.mu.Unlock()
		list = mergeInstalledCatalog(list, a.data.Packages)
		values := map[string]any{}
		for _, p := range list {
			values[p.ID] = p
		}
		if err := a.db.Replace("package", values); err != nil {
			return err
		}
		a.data.Packages = list
		a.data.Diagnostics = diagnostics
		return nil
	})
}

// Refresh can overlap install/uninstall. Current local state, including an empty
// path after uninstall, always wins over the snapshot used for the network call.
func mergeInstalledCatalog(incoming, current []model.Package) []model.Package {
	result := append([]model.Package{}, incoming...)
	byID := map[string]model.Package{}
	for _, p := range current {
		byID[p.ID] = p
	}
	for i := range result {
		if p, ok := byID[result[i].ID]; ok {
			result[i].Path = p.Path
			result[i].InstalledAt = p.InstalledAt
			if p.Path != "" {
				result[i].Arch = p.Arch
			}
			delete(byID, p.ID)
		}
	}
	for _, p := range byID {
		if p.Path != "" {
			result = append(result, p)
		}
	}
	return result
}

func (a *App) InstallPackage(packageID string) (string, error) {
	p, err := a.packageByID(packageID)
	if err != nil {
		return "", err
	}
	if p.Path != "" {
		return "", errors.New("此版本已经安装")
	}
	return a.task("install", packageID, func(ctx context.Context, id string) error {
		p, err := packages.Install(ctx, a.data.Settings.Root, p, func(done, total int64, message string) { a.progress(id, done, total, message) })
		if err != nil {
			return err
		}
		if err := a.storeInstalledPackage(p); err != nil {
			return err
		}
		if _, ok := map[string]bool{"php": true, "mysql": true, "nginx": true, "redis": true}[p.Kind]; !ok {
			return nil
		}
		a.opMu.Lock()
		defer a.opMu.Unlock()
		if err := a.ensureServiceInstances(ctx); err != nil {
			return err
		}
		var failures []error
		for _, v := range a.orderedInstances(false) {
			if v.PackageID == p.ID {
				if err := a.start(ctx, v.ID); err != nil {
					failures = append(failures, fmt.Errorf("%s：%w", v.Name, err))
				}
			}
		}
		return errors.Join(failures...)
	})
}

func (a *App) storeInstalledPackage(p model.Package) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.db.Put("package", p.ID, p); err != nil {
		return err
	}
	for i := range a.data.Packages {
		if a.data.Packages[i].ID == p.ID {
			a.data.Packages[i] = p
			return nil
		}
	}
	a.data.Packages = append(a.data.Packages, p)
	return nil
}

func (a *App) UninstallPackage(id string) error {
	if err := a.needRoot(); err != nil {
		return err
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	p, err := a.packageByID(id)
	if err != nil {
		return err
	}
	a.mu.RLock()
	for _, i := range a.data.Instances {
		if i.PackageID == id {
			a.mu.RUnlock()
			return errors.New("此版本被实例引用，请先删除对应实例（数据默认保留）")
		}
	}
	a.mu.RUnlock()
	removed := p
	removed.Path = ""
	removed.InstalledAt = ""
	// Commit the reversible metadata change first. A crash before file removal is
	// recoverable by Install's ownership-verified adoption of the existing package.
	if err := a.db.Put("package", id, removed); err != nil {
		return err
	}
	if err := packages.Uninstall(a.data.Settings.Root, p); err != nil {
		return errors.Join(err, a.db.Put("package", id, p))
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.data.Packages {
		if a.data.Packages[i].ID == id {
			a.data.Packages[i] = removed
		}
	}
	return nil
}
