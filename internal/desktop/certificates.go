package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"phptools/internal/model"
	"phptools/internal/sites"
)

func (a *App) GetCertificates() (model.CertificatePage, error) {
	page := model.CertificatePage{Sites: []model.SiteCertificate{}}
	if err := a.needRoot(); err != nil {
		return page, err
	}
	a.mu.RLock()
	root := a.data.Settings.Root
	list := append([]model.Site{}, a.data.Sites...)
	a.mu.RUnlock()
	page.Email = sites.PublicCertificateEmail(root)
	for _, site := range list {
		page.Sites = append(page.Sites, sites.DescribeCertificate(root, site))
	}
	return page, nil
}

func (a *App) ApplyPublicCertificate(siteID, email string) (string, error) {
	if !sites.ValidPublicEmail(email) {
		return "", errors.New("请填写有效的通知邮箱")
	}
	if err := a.needRoot(); err != nil {
		return "", err
	}
	if _, err := a.certificateSite(siteID); err != nil {
		return "", err
	}
	return a.task("public-certificate", siteID, func(ctx context.Context, taskID string) error {
		a.opMu.Lock()
		defer a.opMu.Unlock()
		return a.issuePublicCertificate(ctx, taskID, siteID, email)
	})
}

func (a *App) ImportPublicCertificate(siteID, certificate, privateKey string) (string, error) {
	if err := a.needRoot(); err != nil {
		return "", err
	}
	site, err := a.certificateSite(siteID)
	if err != nil {
		return "", err
	}
	a.mu.RLock()
	root := a.data.Settings.Root
	a.mu.RUnlock()
	info := sites.DescribeCertificate(root, site)
	if len(info.Domains) == 0 {
		if info.Reason != "" {
			return "", errors.New(info.Reason)
		}
		return "", errors.New("请先填写有效域名")
	}
	certPEM, keyPEM, err := pastedCertificate(certificate, privateKey)
	if err != nil {
		return "", err
	}
	certPEM, keyPEM, err = sites.NormalizeImportedCertificate(info.Domains, certPEM, keyPEM, nil)
	if err != nil {
		return "", err
	}
	return a.task("import-certificate", siteID, func(ctx context.Context, taskID string) error {
		a.opMu.Lock()
		defer a.opMu.Unlock()
		return a.installImportedCertificate(ctx, taskID, siteID, certPEM, keyPEM)
	})
}

func (a *App) UseLocalCertificate(siteID string) (string, error) {
	if err := a.needRoot(); err != nil {
		return "", err
	}
	if _, err := a.certificateSite(siteID); err != nil {
		return "", err
	}
	return a.task("local-certificate", siteID, func(ctx context.Context, taskID string) error {
		a.opMu.Lock()
		defer a.opMu.Unlock()
		return a.useLocalCertificate(ctx, siteID)
	})
}

func (a *App) installImportedCertificate(ctx context.Context, taskID, siteID string, certPEM, keyPEM []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.RLock()
	root := a.data.Settings.Root
	old := append([]model.Site{}, a.data.Sites...)
	instances := append([]model.Instance{}, a.data.Instances...)
	a.mu.RUnlock()
	var site model.Site
	found := false
	for _, item := range old {
		if item.ID == siteID {
			site = item
			found = true
			break
		}
	}
	if !found {
		return errors.New("站点不存在")
	}
	info := sites.DescribeCertificate(root, site)
	if len(info.Domains) == 0 {
		if info.Reason != "" {
			return errors.New(info.Reason)
		}
		return errors.New("请先填写有效域名")
	}
	if _, _, err := sites.NormalizeImportedCertificate(info.Domains, certPEM, keyPEM, nil); err != nil {
		return err
	}
	backup, err := sites.BackupCertificate(root, site.ID)
	if err != nil {
		return err
	}
	restore := func() {
		_ = sites.RestoreCertificate(root, site.ID, backup)
	}
	a.progress(taskID, 0, 0, "正在安装公网证书")
	if err = sites.InstallPublicCertificate(root, site.ID, certPEM, keyPEM); err != nil {
		restore()
		return err
	}
	site.HTTPS = true
	site.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	updated := make([]model.Site, 0, len(old))
	for _, item := range old {
		if item.ID == site.ID {
			updated = append(updated, site)
		} else {
			updated = append(updated, item)
		}
	}
	if err = a.changeSites(old, updated, instances); err != nil {
		restore()
		return err
	}
	a.progress(taskID, 1, 1, "公网证书已安装")
	return nil
}

func pastedCertificate(certificate, privateKey string) ([]byte, []byte, error) {
	certificate = strings.TrimSpace(certificate)
	privateKey = strings.TrimSpace(privateKey)
	if certificate == "" {
		return nil, nil, errors.New("请填写公钥")
	}
	if privateKey == "" {
		return nil, nil, errors.New("请填写私钥")
	}
	if len(certificate) > 256*1024 || len(privateKey) > 256*1024 {
		return nil, nil, errors.New("公钥或私钥内容过长")
	}
	if strings.ContainsRune(certificate, 0) || strings.ContainsRune(privateKey, 0) {
		return nil, nil, errors.New("公钥或私钥内容无效")
	}
	return []byte(certificate), []byte(privateKey), nil
}

func (a *App) certificateSite(siteID string) (model.Site, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, site := range a.data.Sites {
		if site.ID == siteID {
			return site, nil
		}
	}
	return model.Site{}, errors.New("站点不存在")
}

func (a *App) issuePublicCertificate(ctx context.Context, taskID, siteID, email string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	a.mu.RLock()
	root := a.data.Settings.Root
	old := append([]model.Site{}, a.data.Sites...)
	instances := append([]model.Instance{}, a.data.Instances...)
	a.mu.RUnlock()
	var site model.Site
	found := false
	for _, item := range old {
		if item.ID == siteID {
			site = item
			found = true
			break
		}
	}
	if !found {
		return errors.New("站点不存在")
	}
	info := sites.DescribeCertificate(root, site)
	if !info.Eligible {
		if info.Reason == "" {
			return errors.New("这个站点不能申请公网证书")
		}
		return errors.New(info.Reason)
	}
	if err := sites.SavePublicCertificateEmail(root, email); err != nil {
		return err
	}
	a.progress(taskID, 0, 0, "正在准备站点配置")
	if _, err := sites.Apply(root, old, instances); err != nil {
		return fmt.Errorf("准备站点配置失败：%w", err)
	}
	var temporary string
	defer func() {
		if temporary != "" {
			_ = os.Remove(temporary)
			_ = a.reloadRunningNginx(context.Background())
		}
	}()
	if sites.NeedsHTTPChallengeServer(site) {
		path, err := sites.WriteACMEHTTPConfig(root, info.Domains)
		if err != nil {
			return err
		}
		temporary = path
	}
	if err := a.ensureNginxRunning(ctx); err != nil {
		return err
	}
	certPEM, keyPEM, err := sites.ObtainPublicCertificate(ctx, root, email, info.Domains, func(message string) {
		a.progress(taskID, 0, 0, message)
	}, func() error {
		return a.reloadRunningNginx(ctx)
	})
	if err != nil {
		return err
	}
	if err = sites.InstallPublicCertificate(root, site.ID, certPEM, keyPEM); err != nil {
		return err
	}
	if temporary != "" {
		if err = os.Remove(temporary); err != nil && !os.IsNotExist(err) {
			return errors.New("无法移除证书验证配置")
		}
		temporary = ""
	}
	site.HTTPS = true
	site.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	updated := make([]model.Site, 0, len(old))
	for _, item := range old {
		if item.ID == site.ID {
			updated = append(updated, site)
		} else {
			updated = append(updated, item)
		}
	}
	if err = a.changeSites(old, updated, instances); err != nil {
		return err
	}
	a.progress(taskID, 1, 1, "公网证书已安装")
	return nil
}

func (a *App) useLocalCertificate(ctx context.Context, siteID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.RLock()
	root := a.data.Settings.Root
	old := append([]model.Site{}, a.data.Sites...)
	instances := append([]model.Instance{}, a.data.Instances...)
	a.mu.RUnlock()
	var site model.Site
	found := false
	for _, item := range old {
		if item.ID == siteID {
			site = item
			found = true
			break
		}
	}
	if !found {
		return errors.New("站点不存在")
	}
	backup, err := sites.BackupCertificate(root, site.ID)
	if err != nil {
		return err
	}
	restore := func() {
		_ = sites.RestoreCertificate(root, site.ID, backup)
	}
	if err = sites.RemoveCertificate(root, site.ID); err != nil {
		restore()
		return err
	}
	if !site.HTTPS {
		return nil
	}
	names := []string{site.Domain}
	if site.SecondDomain != "" {
		names = append(names, site.SecondDomain)
	}
	if err = sites.EnsureCertificate(root, site.ID, names...); err != nil {
		restore()
		return err
	}
	if err = sites.TrustCA(root); err != nil {
		restore()
		return err
	}
	if err = a.changeSites(old, updatedSites(old, site), instances); err != nil {
		restore()
		return err
	}
	return nil
}

func updatedSites(old []model.Site, site model.Site) []model.Site {
	updated := make([]model.Site, 0, len(old))
	for _, item := range old {
		if item.ID == site.ID {
			updated = append(updated, site)
		} else {
			updated = append(updated, item)
		}
	}
	return updated
}

func (a *App) ensureNginxRunning(ctx context.Context) error {
	a.mu.RLock()
	instances := append([]model.Instance{}, a.data.Instances...)
	a.mu.RUnlock()
	var idle *model.Instance
	for i := range instances {
		if instances[i].Kind != "nginx" {
			continue
		}
		if a.manager.Inspect(instances[i]).PID != 0 {
			return nil
		}
		if idle == nil {
			copy := instances[i]
			idle = &copy
		}
	}
	if idle == nil {
		return errors.New("请先安装 Nginx 并创建实例")
	}
	if err := a.start(ctx, idle.ID); err != nil {
		return fmt.Errorf("启动 Nginx 失败：%w", err)
	}
	return nil
}

func (a *App) reloadRunningNginx(ctx context.Context) error {
	a.mu.RLock()
	instances := append([]model.Instance{}, a.data.Instances...)
	a.mu.RUnlock()
	found := false
	for _, v := range instances {
		if v.Kind != "nginx" || a.manager.Inspect(v).PID == 0 {
			continue
		}
		found = true
		pkg, err := a.packageByID(v.PackageID)
		if err != nil {
			return err
		}
		if err = a.manager.ValidateConfig(ctx, pkg, v); err != nil {
			return fmt.Errorf("站点配置未通过 Nginx 校验：%w", err)
		}
		if err = a.manager.Reload(ctx, pkg, v); err != nil {
			return fmt.Errorf("加载站点配置失败：%w", err)
		}
	}
	if !found {
		return errors.New("Nginx 未运行")
	}
	return nil
}
