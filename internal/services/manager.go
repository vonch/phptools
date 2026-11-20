// Package services owns only processes and files created beneath the PHPTools root.
package services

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"phptools/internal/model"
)

type Manager struct {
	root      string
	save      func(model.Instance) error
	getSecret func(string) (string, error)
	mu        sync.Mutex
}

type processRecord struct {
	PID            int    `json:"pid"`
	Created        uint64 `json:"created"`
	Executable     string `json:"executable"`
	Config         string `json:"config"`
	ConfigArgument string `json:"configArgument"`
	Bootstrap      bool   `json:"bootstrap,omitempty"`
}

func New(root string, save func(model.Instance) error, getSecret func(string) (string, error)) *Manager {
	abs, _ := filepath.Abs(root)
	return &Manager{root: abs, save: save, getSecret: getSecret}
}

func (m *Manager) instanceDir(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, `/\\:.`) {
		return "", errors.New("无效实例编号")
	}
	return filepath.Join(m.root, "instances", id), nil
}

func (m *Manager) validateInstance(i model.Instance) error {
	dir, err := m.instanceDir(i.ID)
	if err != nil {
		return err
	}
	if !within(dir, i.ConfigPath) || !within(dir, i.DataDir) || !within(dir, i.LogPath) {
		return errors.New("实例路径不属于 PHPTools，已拒绝操作")
	}
	if i.Port < 1 || i.Port > 65535 {
		return errors.New("端口必须在 1 到 65535 之间")
	}
	return nil
}

func within(root, target string) bool {
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	t, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(r, t)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (m *Manager) Create(ctx context.Context, pkg model.Package, input model.InstanceInput) (model.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return model.Instance{}, err
	}
	if input.Port < 1 || input.Port > 65535 {
		return model.Instance{}, errors.New("端口必须在 1 到 65535 之间")
	}
	if strings.TrimSpace(input.Name) == "" {
		return model.Instance{}, errors.New("请填写实例名称")
	}
	if pkg.ID != input.PackageID {
		return model.Instance{}, errors.New("安装包与实例不匹配")
	}
	if err := ValidatePHPPath(pkg, m.root); err != nil {
		return model.Instance{}, err
	}
	if !within(m.root, pkg.Path) {
		return model.Instance{}, errors.New("组件目录必须位于 PHPTools 环境目录内")
	}
	exe, err := findExecutable(pkg)
	if err != nil {
		return model.Instance{}, err
	}
	i := model.Instance{ID: rand.Text(), Name: strings.TrimSpace(input.Name), Kind: pkg.Kind, PackageID: pkg.ID, Port: input.Port, State: "stopped", CreatedAt: model.Now(), Executable: exe}
	dir, _ := m.instanceDir(i.ID)
	i.DataDir = filepath.Join(dir, "data")
	i.LogPath = filepath.Join(dir, "logs", "error.log")
	configNames := map[string]string{"php": "php.ini", "mysql": "my.ini", "nginx": "nginx.conf", "redis": "redis.conf"}
	name, ok := configNames[pkg.Kind]
	if !ok {
		return model.Instance{}, errors.New("不支持的组件类型")
	}
	i.ConfigPath = filepath.Join(dir, "conf", name)
	for _, path := range []string{filepath.Dir(i.ConfigPath), i.DataDir, filepath.Dir(i.LogPath), filepath.Join(m.root, "sites"), filepath.Join(m.root, "conf")} {
		if err = os.MkdirAll(path, 0700); err != nil {
			return model.Instance{}, err
		}
	}
	if err = m.writeInitialConfig(pkg, i); err != nil {
		return model.Instance{}, err
	}
	return i, nil
}

func (m *Manager) Start(ctx context.Context, pkg model.Package, i model.Instance) (model.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateInstance(i); err != nil {
		return i, err
	}
	if i.Kind != pkg.Kind || i.PackageID != pkg.ID {
		return i, errors.New("安装包与实例不匹配")
	}
	i = m.inspect(i)
	if i.PID != 0 {
		if i.State == "running" {
			if i.Kind == "mysql" {
				dir, _ := m.instanceDir(i.ID)
				b, _ := os.ReadFile(filepath.Join(dir, "process.json"))
				var p processRecord
				_ = json.Unmarshal(b, &p)
				if p.Bootstrap {
					return i, errors.New("MySQL 首次初始化进程仍在运行，请先停止此实例后再启动")
				}
			}
			return i, nil
		}
		return i, errors.New(i.Error)
	}
	if i.State == "error" {
		return i, errors.New(i.Error)
	}
	if !within(m.root, pkg.Path) {
		return markStartFailure(i, errors.New("组件目录必须位于 PHPTools 环境目录内"))
	}
	if err := availablePort(i.Port); err != nil {
		return markStartFailure(i, err)
	}
	exe, err := findExecutable(pkg)
	if err != nil {
		return i, err
	}
	i.Executable = exe
	if i.Kind == "nginx" {
		dir, _ := m.instanceDir(i.ID)
		if err := os.MkdirAll(filepath.Join(dir, "temp"), 0700); err != nil {
			return i, err
		}
		entries, _ := os.ReadDir(filepath.Join(m.root, "instances"))
		for _, entry := range entries {
			if entry.Name() == i.ID || !entry.IsDir() {
				continue
			}
			b, err := os.ReadFile(filepath.Join(m.root, "instances", entry.Name(), "process.json"))
			if err != nil {
				continue
			}
			var p processRecord
			if json.Unmarshal(b, &p) == nil && strings.EqualFold(filepath.Base(p.Executable), "nginx.exe") && verifyProcess(p) == nil {
				return markStartFailure(i, errors.New("已有 Nginx 实例运行，请先停止它"))
			}
		}
	}
	if err = m.validateConfig(ctx, pkg, i); err != nil {
		return markStartFailure(i, err)
	}
	if i.Kind == "mysql" {
		i, err = m.prepareMySQL(ctx, pkg, i)
		if err != nil {
			return markStartFailure(i, err)
		}
	}
	args := startArgs(i)
	i, err = m.launch(i, args, nil)
	if err != nil {
		return markStartFailure(i, err)
	}
	if err = m.waitReady(ctx, i, 30*time.Second); err != nil {
		current := m.inspect(i)
		if current.PID == 0 {
			return markStartFailure(current, fmt.Errorf("启动失败，请查看 %s：%w", i.LogPath, err))
		}
		current.State = "error"
		current.Error = "启动就绪检查失败；进程已保留，可安全重试停止：" + err.Error()
		_ = m.persist(current)
		return current, errors.New(current.Error)
	}
	i.State = "running"
	i.Error = ""
	err = m.persist(i)
	return i, err
}

func markStartFailure(i model.Instance, err error) (model.Instance, error) {
	if i.PID == 0 {
		i.State = "error"
		i.Error = err.Error()
	}
	return i, err
}

func (m *Manager) launch(i model.Instance, args, extraEnv []string) (model.Instance, error) {
	log, err := os.OpenFile(i.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return i, err
	}
	cmd := exec.Command(i.Executable, args...)
	cmd.Dir = filepath.Dir(i.Executable)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Env = append(os.Environ(), extraEnv...)
	if i.Kind == "php" {
		cmd.Env = append(cmd.Env, "PHP_FCGI_MAX_REQUESTS=0")
	}
	hideConsole(cmd)
	if err = cmd.Start(); err != nil {
		_ = log.Close()
		return i, fmt.Errorf("无法启动 %s：%w", i.Kind, err)
	}
	i.PID = cmd.Process.Pid
	created, _, err := processIdentity(i.PID)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = log.Close()
		return i, err
	}
	i.ProcessCreated = created
	i.State = "starting"
	i.Error = ""
	p := processRecord{PID: i.PID, Created: created, Executable: i.Executable, Config: i.ConfigPath, ConfigArgument: configArgument(i)}
	for _, arg := range args {
		if arg == "--skip-networking" {
			p.Bootstrap = true
		}
	}
	if err = m.writeRecord(i, p); err != nil {
		// Retain the identity in memory and report it; never hide a live database process.
		i.State = "error"
		i.Error = "进程已启动，但无法保存进程归属：" + err.Error()
		_ = m.persist(i)
		go func() { _ = cmd.Wait(); _ = log.Close() }()
		return i, errors.New(i.Error)
	}
	go func() { _ = cmd.Wait(); _ = log.Close() }()
	if err = m.persist(i); err != nil {
		i.State = "error"
		i.Error = "进程已启动，但无法保存实例状态：" + err.Error()
		return i, errors.New(i.Error)
	}
	return i, nil
}

func (m *Manager) persist(i model.Instance) error {
	if m.save != nil {
		return m.save(i)
	}
	return nil
}

func (m *Manager) writeRecord(i model.Instance, p processRecord) error {
	dir, _ := m.instanceDir(i.ID)
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "process.json"), b, 0600)
}

func (m *Manager) Inspect(i model.Instance) model.Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inspect(i)
}

func (m *Manager) inspect(i model.Instance) model.Instance {
	if err := m.validateInstance(i); err != nil {
		i.State = "error"
		i.Error = err.Error()
		return i
	}
	dir, _ := m.instanceDir(i.ID)
	b, err := os.ReadFile(filepath.Join(dir, "process.json"))
	var p processRecord
	if err != nil || json.Unmarshal(b, &p) != nil {
		if i.PID != 0 {
			// A missing record cannot authorize control of a live process. It also
			// must not keep an exited process (or a reused PID) stuck forever.
			created, executable, identityErr := processIdentity(i.PID)
			gone := errors.Is(identityErr, errProcessGone)
			if identityErr == nil && i.ProcessCreated != 0 && i.Executable != "" {
				gone = created != i.ProcessCreated || !samePath(executable, i.Executable)
			}
			if !gone {
				i.State = "error"
				i.Error = "缺少可信进程记录，拒绝操作该进程"
				return i
			}
		}
		i.PID = 0
		i.ProcessCreated = 0
		i.State = "stopped"
		i.Error = ""
		return i
	}
	if !samePath(p.Config, i.ConfigPath) || !within(m.root, p.Executable) || identityDisagrees(i, p) {
		// The instance row and process.json are both claims about the same process.
		// A disagreement never authorizes adopting either PID: the record may have
		// been replaced, or the row may be stale. Once both claimed processes have
		// exited, keeping the disagreement would trap Stop and Start forever.
		if samePath(p.Config, i.ConfigPath) && within(m.root, p.Executable) && identityDisagrees(i, p) && claimExited(i.PID, i.ProcessCreated, i.Executable) && claimExited(p.PID, p.Created, p.Executable) {
			i.PID = 0
			i.ProcessCreated = 0
			i.State = "stopped"
			i.Error = ""
			return i
		}
		i.State = "error"
		i.Error = "进程归属记录与实例不一致，拒绝操作"
		return i
	}
	if err = verifyProcess(p); err != nil {
		if errors.Is(err, errProcessGone) {
			i.PID = 0
			i.ProcessCreated = 0
			i.State = "stopped"
			i.Error = ""
			return i
		}
		i.State = "error"
		i.Error = "无法确认进程归属：" + err.Error()
		i.PID = p.PID
		return i
	}
	i.PID = p.PID
	i.ProcessCreated = p.Created
	i.Executable = p.Executable
	i.State = "running"
	i.Error = ""
	return i
}

func identityDisagrees(i model.Instance, p processRecord) bool {
	return i.PID != 0 && (i.PID != p.PID || i.ProcessCreated != p.Created || !samePath(i.Executable, p.Executable))
}

// claimExited reports whether this exact process is no longer running.
// An access error or an incomplete identity is not proof that it exited.
func claimExited(pid int, created uint64, executable string) bool {
	if pid <= 0 {
		return true
	}
	gotCreated, gotExe, err := processIdentity(pid)
	if errors.Is(err, errProcessGone) {
		return true
	}
	if err != nil || created == 0 || executable == "" {
		return false
	}
	return gotCreated != created || !samePath(gotExe, executable)
}

func (m *Manager) Stop(ctx context.Context, i model.Instance) (model.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stop(ctx, i)
}

func (m *Manager) stop(ctx context.Context, i model.Instance) (model.Instance, error) {
	i = m.inspect(i)
	if i.State == "stopped" {
		return i, m.persist(i)
	}
	if i.State != "running" {
		return i, errors.New(i.Error)
	}
	var err error
	switch i.Kind {
	case "php":
		err = terminateOwned(i)
	case "nginx":
		if err = verifyNginxPID(i); err == nil {
			err = m.signalNginx(ctx, i, "quit")
		}
	case "mysql":
		dir, _ := m.instanceDir(i.ID)
		b, readErr := os.ReadFile(filepath.Join(dir, "process.json"))
		var record processRecord
		if readErr != nil || json.Unmarshal(b, &record) != nil {
			err = errors.New("无法读取 MySQL 进程归属记录")
		} else {
			err = m.mysqlShutdown(ctx, i, record.Bootstrap)
		}
	case "redis":
		cli := filepath.Join(filepath.Dir(i.Executable), "redis-cli.exe")
		err = runCommand(ctx, cli, []string{"-h", "127.0.0.1", "-p", strconv.Itoa(i.Port), "shutdown", "save"}, nil)
	default:
		err = errors.New("不支持的组件类型")
	}
	if err == nil {
		err = waitStopped(ctx, i, 30*time.Second)
	}
	if err != nil {
		i.State = "error"
		i.Error = "停止失败，进程已保留供重试：" + err.Error()
		_ = m.persist(i)
		return i, errors.New(i.Error)
	}
	i.PID = 0
	i.ProcessCreated = 0
	i.State = "stopped"
	i.Error = ""
	// Keep the record until the cleared identity is saved. A failed save or
	// interrupted shutdown must not leave metadata pointing at a deleted record.
	if err := m.persist(i); err != nil {
		return i, err
	}
	dir, _ := m.instanceDir(i.ID)
	_ = os.Remove(filepath.Join(dir, "process.json"))
	return i, nil
}

func (m *Manager) ValidateConfig(ctx context.Context, pkg model.Package, i model.Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.validateConfig(ctx, pkg, i)
}

func (m *Manager) validateConfig(ctx context.Context, pkg model.Package, i model.Instance) error {
	if err := m.validateInstance(i); err != nil {
		return err
	}
	if pkg.ID != i.PackageID || pkg.Kind != i.Kind {
		return errors.New("安装包与实例不匹配")
	}
	exe, err := findExecutable(pkg)
	if err != nil {
		return err
	}
	switch i.Kind {
	case "nginx":
		if err := m.validateNginxConfig(i); err != nil {
			return err
		}
		if err := m.validateNginxPorts(i); err != nil {
			return err
		}
		return runCommand(ctx, exe, nginxArgs(i, "-t"), nil)
	case "php":
		return runCommand(ctx, exe, []string{"-c", i.ConfigPath, "-i"}, nil)
	case "mysql":
		if err := validateMySQLConfig(i); err != nil {
			return err
		}
		return runCommand(ctx, exe, []string{"--defaults-file=" + i.ConfigPath, "--verbose", "--help"}, nil)
	case "redis":
		return validateRedisConfig(i)
	}
	return errors.New("不支持的组件类型")
}

func (m *Manager) Reload(ctx context.Context, pkg model.Package, i model.Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i.Kind != "nginx" {
		return errors.New("此组件需要停止后重新启动以加载配置")
	}
	i = m.inspect(i)
	if i.State != "running" {
		return errors.New("Nginx 尚未运行，或无法核实进程归属")
	}
	if err := m.validateConfig(ctx, pkg, i); err != nil {
		return err
	}
	if err := verifyNginxPID(i); err != nil {
		return err
	}
	var size int64
	if stat, err := os.Stat(i.LogPath); err == nil {
		size = stat.Size()
	}
	if err := m.signalNginx(ctx, i, "reload"); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(700 * time.Millisecond):
	}
	if f, err := os.Open(i.LogPath); err == nil {
		defer f.Close()
		_, _ = f.Seek(size, 0)
		buf := make([]byte, 16*1024)
		n, _ := f.Read(buf)
		if strings.Contains(string(buf[:n]), "[emerg]") {
			return fmt.Errorf("Nginx 拒绝加载配置，请查看 %s", i.LogPath)
		}
	}
	if m.inspect(i).State != "running" {
		return errors.New("Nginx 重载后未正常运行")
	}
	return nil
}

func availablePort(port int) error {
	l, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("端口 %d 已被占用：%w", port, err)
	}
	return l.Close()
}

func (m *Manager) waitReady(ctx context.Context, i model.Instance, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastError error
	for time.Now().Before(deadline) {
		if err := verifyProcess(processRecord{PID: i.PID, Created: i.ProcessCreated, Executable: i.Executable, Config: i.ConfigPath, ConfigArgument: configArgument(i)}); err != nil {
			return err
		}
		conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(i.Port)), 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			if err = m.probeReady(ctx, i); err == nil {
				return nil
			}
			lastError = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	if lastError != nil {
		return fmt.Errorf("服务就绪检查超时：%w", lastError)
	}
	return errors.New("等待本机端口就绪超时")
}

func waitStopped(ctx context.Context, i model.Instance, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		created, exe, err := processIdentity(i.PID)
		if errors.Is(err, errProcessGone) {
			return nil
		}
		if err != nil {
			return err
		}
		if created != i.ProcessCreated || !samePath(exe, i.Executable) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("等待进程正常退出超时")
}

func runCommand(ctx context.Context, exe string, args []string, stdin *strings.Reader) error {
	commandCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, exe, args...)
	cmd.Dir = filepath.Dir(exe)
	hideConsole(cmd)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if missingRuntimeLibrary(err) {
			return fmt.Errorf("%s 无法启动：系统缺少 Visual C++ 运行库。安装文件已保留。请到设置中的运行依赖，安装 Visual C++ v14 x64 后重新启动该实例", filepath.Base(exe))
		}
		output := strings.TrimSpace(stderr.String())
		if len(output) > 2000 {
			output = output[:2000]
		}
		return fmt.Errorf("%s 执行失败：%w %s", filepath.Base(exe), err, output)
	}
	return nil
}

func missingRuntimeLibrary(err error) bool {
	var exit *exec.ExitError
	if errors.As(err, &exit) && uint32(exit.ExitCode()) == 0xC0000135 {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "0xc0000135")
}

func samePath(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }
