package desktop

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
	"phptools/internal/model"
	"phptools/internal/packages"
	"phptools/internal/services"
	"phptools/internal/sites"
	"phptools/internal/store"
)

type Hooks struct {
	Notify             func()
	StateRead          func()
	PickDirectory      func(title, initialDirectory string) (string, error)
	OpenDirectory      func(string) error
	PickSQLImport      func() (string, error)
	PickSQLExport      func(string) (string, error)
	PickDatabaseClient func() (string, error)
	OpenDatabaseClient func(string) error
	OpenURL            func(string) error
	Exit               func()
}

type App struct {
	mu           sync.RWMutex
	opMu         sync.Mutex
	db           *store.Store
	manager      *services.Manager
	data         model.Snapshot
	jobs         map[string]context.CancelFunc
	wg           sync.WaitGroup
	closing      bool
	updating     bool
	updateMu     sync.Mutex
	updateClient *http.Client
	updateDone   chan struct{}
	configDir    string
	hooks        Hooks
	activity     sites.ActivityReader
}

const appVersion = "0.1.70"

func New(configDir string, hooks Hooks) (*App, error) {
	if configDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		configDir = filepath.Join(base, "PHPTools")
	}
	configDir, err := filepath.Abs(configDir)
	if err != nil {
		return nil, err
	}
	a := &App{configDir: configDir, hooks: hooks, jobs: map[string]context.CancelFunc{}}
	a.data = model.Snapshot{Settings: model.Settings{SuggestedRoot: `D:\PHPTools`, Version: appVersion, UpdateManifestURL: defaultUpdateManifestURL}, Packages: packages.SeedCatalog(), Instances: []model.Instance{}, Sites: []model.Site{}, Tasks: []model.Task{}, Diagnostics: []model.Diagnostic{}}
	a.loadSidebar()
	a.loadSiteView()
	a.loadCertificateView()
	a.loadUpdateResult()
	var config struct {
		Root string `json:"root"`
	}
	if raw, err := os.ReadFile(filepath.Join(configDir, "settings.json")); err == nil {
		if err = json.Unmarshal(raw, &config); err != nil {
			return nil, err
		}
		if config.Root != "" {
			if err = a.open(config.Root); err != nil {
				a.data.Diagnostics = append(a.data.Diagnostics, model.Diagnostic{Level: "error", Message: "上次环境目录无法打开：" + err.Error()})
			}
		}
		if bytes.Contains(raw, []byte(`"updateManifestUrl"`)) {
			// Keep the saved directory when it cannot be opened. The address is
			// removed either way, and the in-memory root stays unchanged.
			openedRoot := a.data.Settings.Root
			if openedRoot == "" {
				a.data.Settings.Root = config.Root
			}
			if err = a.writeSettingsLocked(); err != nil {
				a.data.Diagnostics = append(a.data.Diagnostics, model.Diagnostic{Level: "warning", Message: "无法清理旧的更新设置：" + err.Error()})
			}
			a.data.Settings.Root = openedRoot
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if a.manager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = a.checkEnvironment(ctx, "startup", false, "")
		cancel()
	}
	return a, nil
}

func (a *App) open(root string) error {
	db, err := store.Open(root)
	if err != nil {
		return err
	}
	pkgs, err := store.List[model.Package](db, "package")
	if err != nil {
		db.Close()
		return err
	}
	instances, err := store.List[model.Instance](db, "instance")
	if err != nil {
		db.Close()
		return err
	}
	siteList, err := store.List[model.Site](db, "site")
	if err != nil {
		db.Close()
		return err
	}
	byID := map[string]model.Package{}
	for _, v := range packages.SeedCatalog() {
		byID[v.ID] = v
	}
	for _, v := range pkgs {
		byID[v.ID] = v
	}
	a.data.Packages = []model.Package{}
	for _, v := range byID {
		a.data.Packages = append(a.data.Packages, v)
	}
	a.db = db
	a.data.Settings.Root = db.Root
	a.data.Instances = instances
	for i := range siteList {
		siteList[i].Rewrite = sites.UpgradeRewrite(siteList[i].Rewrite)
	}
	a.data.Sites = siteList
	if err = sites.MigrateSiteConfigs(db.Root); err != nil {
		db.Close()
		a.db = nil
		return err
	}
	a.manager = services.New(db.Root, a.saveInstance, a.getSecret)
	if err = a.manager.AdoptSiteConfigDirectory(); err != nil {
		return err
	}
	return nil
}

func (a *App) GetState() (model.Snapshot, error) {
	if a.hooks.StateRead != nil {
		a.hooks.StateRead()
	}
	if a.opMu.TryLock() {
		a.mu.RLock()
		instances := append([]model.Instance{}, a.data.Instances...)
		manager := a.manager
		a.mu.RUnlock()
		if manager != nil {
			for _, v := range instances {
				next := manager.Inspect(v)
				if !reflect.DeepEqual(v, next) {
					_ = a.saveInstance(next)
				}
			}
		}
		a.opMu.Unlock()
	}
	a.mu.RLock()
	var result model.Snapshot
	raw, err := json.Marshal(a.data)
	a.mu.RUnlock()
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	if err == nil {
		a.activity.Apply(result.Settings.Root, result.Sites)
		memory := services.ApplicationMemory()
		result.MemoryUsage = &memory
	}
	return result, err
}

func (a *App) SelectDirectory(title string) (string, error) {
	if a.hooks.PickDirectory == nil {
		return "", errors.New("目录选择器尚未初始化")
	}
	return withoutDialogCancel(a.hooks.PickDirectory(title, ""))
}

func (a *App) SetRoot(root string) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.db != nil {
		return errors.New("当前环境已初始化；首版不支持移动环境目录")
	}
	if a.closing || a.updating {
		return errors.New("软件正在退出或升级，请稍后重试")
	}
	previous := a.data
	if err := a.open(root); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed && a.db != nil {
			a.db.Close()
			a.db = nil
			a.manager = nil
			a.data = previous
		}
	}()
	if err := os.MkdirAll(a.configDir, 0700); err != nil {
		return err
	}
	if err := a.writeSettingsLocked(); err != nil {
		return err
	}
	committed = true
	return nil
}

// writeSettingsLocked persists user-level settings. The caller must hold a.mu.
func (a *App) writeSettingsLocked() error {
	if err := os.MkdirAll(a.configDir, 0700); err != nil {
		return err
	}
	data := struct {
		Root string `json:"root"`
	}{Root: a.data.Settings.Root}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(a.configDir, "settings.json.tmp")
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(a.configDir, "settings.json")); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (a *App) needRoot() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.db == nil {
		return errors.New("请先选择独立环境目录")
	}
	if a.closing {
		return errors.New("软件正在停止服务")
	}
	if a.updating {
		return errors.New("应用正在升级，请等待完成或取消升级任务")
	}
	return nil
}
func (a *App) notify() {
	if a.hooks.Notify != nil {
		a.hooks.Notify()
	}
}

func (a *App) saveInstance(instance model.Instance) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.db == nil {
		return errors.New("环境未初始化")
	}
	if err := a.db.Put("instance", instance.ID, instance); err != nil {
		return err
	}
	for i, v := range a.data.Instances {
		if v.ID == instance.ID {
			a.data.Instances[i] = instance
			return nil
		}
	}
	a.data.Instances = append(a.data.Instances, instance)
	return nil
}

func (a *App) packageByID(id string) (model.Package, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, p := range a.data.Packages {
		if p.ID == id {
			return p, nil
		}
	}
	return model.Package{}, errors.New("组件版本不存在")
}
func (a *App) instanceByID(id string) (model.Instance, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, v := range a.data.Instances {
		if v.ID == id {
			return v, nil
		}
	}
	return model.Instance{}, errors.New("实例不存在")
}
func (a *App) credentialService() string {
	return CredentialService(a.data.Settings.Root)
}

// CredentialService identifies the Windows credential namespace of a runtime.
func CredentialService(root string) string {
	h := sha256.Sum256([]byte(strings.ToLower(root)))
	return "PHPTools/" + hex.EncodeToString(h[:16])
}
func (a *App) getSecret(id string) (string, error) { return keyring.Get(a.credentialService(), id) }
func newID() string                                { return rand.Text() }
