package services

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"phptools/internal/model"
)

func findExecutable(pkg model.Package) (string, error) {
	filenames := map[string]string{"php": "php-cgi.exe", "mysql": "mysqld.exe", "nginx": "nginx.exe", "redis": "redis-server.exe"}
	name, ok := filenames[pkg.Kind]
	if !ok {
		return "", errors.New("不支持的组件类型")
	}
	if pkg.Path == "" {
		return "", errors.New("组件尚未安装")
	}
	var candidates []string
	err := filepath.WalkDir(pkg.Path, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("组件目录不允许符号链接：%s", path)
		}
		if !entry.IsDir() && strings.EqualFold(entry.Name(), name) {
			candidates = append(candidates, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("组件必须包含唯一的 %s，实际找到 %d 个", name, len(candidates))
	}
	return filepath.Abs(candidates[0])
}

func nginxQuote(path string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(filepath.ToSlash(path), `\`, `/`), `"`, `\"`) + `"`
}
func iniQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `/`), `"`, `\"`) + `"`
}
func redisQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func cygwinPath(path string) string {
	path = strings.ReplaceAll(path, `\`, `/`)
	if len(path) >= 3 && path[1] == ':' && path[2] == '/' {
		return "/cygdrive/" + strings.ToLower(path[:1]) + path[2:]
	}
	return path
}

func (m *Manager) writeInitialConfig(pkg model.Package, i model.Instance) error {
	dir, _ := m.instanceDir(i.ID)
	bin := filepath.Dir(i.Executable)
	var contents string
	switch i.Kind {
	case "php":
		base, err := os.ReadFile(filepath.Join(bin, "php.ini-development"))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err = os.MkdirAll(filepath.Join(i.DataDir, "sessions"), 0700); err != nil {
			return err
		}
		contents = enablePHPExtensions(string(base), filepath.Join(bin, "ext")) + "\n; PHPTools instance overrides\n" +
			"extension_dir=" + iniQuote(filepath.Join(bin, "ext")) + "\nerror_log=" + iniQuote(i.LogPath) + "\nlog_errors=On\ndisplay_errors=Off\ncgi.fix_pathinfo=1\n" +
			"session.save_path=" + iniQuote(filepath.Join(i.DataDir, "sessions")) + "\ndate.timezone=Asia/Shanghai\n"
	case "nginx":
		if err := os.MkdirAll(filepath.Join(dir, "temp"), 0700); err != nil {
			return err
		}
		params, err := os.ReadFile(filepath.Join(bin, "conf", "fastcgi_params"))
		if err != nil {
			return fmt.Errorf("Nginx 发行包缺少 fastcgi_params：%w", err)
		}
		if err = os.WriteFile(filepath.Join(filepath.Dir(i.ConfigPath), "fastcgi_params"), params, 0600); err != nil {
			return err
		}
		contents = "worker_processes 1;\nerror_log " + nginxQuote(i.LogPath) + " notice;\npid " + nginxQuote(filepath.Join(dir, "nginx.pid")) + ";\n" +
			"events { worker_connections 1024; }\nhttp {\n  include " + nginxQuote(filepath.Join(bin, "conf", "mime.types")) + ";\n  default_type application/octet-stream;\n" +
			"  access_log " + nginxQuote(filepath.Join(filepath.Dir(i.LogPath), "access.log")) + ";\n  sendfile on;\n  keepalive_timeout 65;\n" +
			fmt.Sprintf("  server {\n    listen 127.0.0.1:%d default_server;\n    server_name localhost;\n    location = /__phptools { return 200 'PHPTools is running'; add_header Content-Type text/plain; }\n    location / { return 404; }\n  }\n", i.Port) +
			"  include " + nginxQuote(m.siteConfigGlob()) + ";\n}\n"
	case "mysql":
		if !(strings.HasPrefix(pkg.Version, "5.7.") || strings.HasPrefix(pkg.Version, "8.0.") || strings.HasPrefix(pkg.Version, "8.4.")) {
			return errors.New("仅支持 MySQL 5.7、8.0 和 8.4")
		}
		// These options and --initialize-insecure are supported by all three branches.
		// Authentication defaults remain native to each version; no removed 8.4 flags are injected.
		contents = "[mysqld]\nbasedir=" + iniQuote(filepath.Dir(bin)) + "\ndatadir=" + iniQuote(i.DataDir) + "\n" +
			fmt.Sprintf("port=%d\nbind-address=127.0.0.1\n", i.Port) + "pid-file=" + iniQuote(filepath.Join(dir, "mysql.pid")) + "\nlog-error=" + iniQuote(i.LogPath) + "\n" +
			"character-set-server=utf8mb4\ncollation-server=utf8mb4_unicode_ci\nskip-name-resolve=1\n"
		// X Plugin is built in and enabled by default in MySQL 8. Disable its
		// separate listener so independent instances never contend for port 33060.
		if strings.HasPrefix(pkg.Version, "8.") {
			contents += "mysqlx=0\n"
		}
	case "redis":
		contents = fmt.Sprintf("bind 127.0.0.1\nport %d\nprotected-mode yes\ndaemonize no\n", i.Port) +
			"dir " + redisQuote(cygwinPath(i.DataDir)) + "\nlogfile " + redisQuote(cygwinPath(i.LogPath)) + "\npidfile " + redisQuote(cygwinPath(filepath.Join(dir, "redis.pid"))) + "\n" +
			"dbfilename dump.rdb\nappendonly yes\nappendfilename appendonly.aof\nsave 900 1\nsave 300 10\nsave 60 10000\n"
	default:
		return errors.New("不支持的组件类型")
	}
	return os.WriteFile(i.ConfigPath, []byte(contents), 0600)
}

func enablePHPExtensions(ini, extensionDir string) string {
	enabled := map[string]bool{}
	for _, line := range strings.Split(ini, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "extension") {
			continue
		}
		value = strings.TrimSpace(strings.SplitN(value, ";", 2)[0])
		value = strings.Trim(value, `"'`)
		name := strings.TrimPrefix(strings.TrimSuffix(strings.ToLower(filepath.Base(value)), ".dll"), "php_")
		enabled[name] = true
	}
	var additions strings.Builder
	for _, name := range []string{"curl", "fileinfo", "mbstring", "mysqli", "openssl", "pdo_mysql", "zip"} {
		if enabled[name] {
			continue
		}
		filename := "php_" + name + ".dll"
		info, err := os.Stat(filepath.Join(extensionDir, filename))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		fmt.Fprintf(&additions, "extension=%s\n", filename)
	}
	if additions.Len() == 0 {
		return ini
	}
	return ini + "\n; PHPTools available development extensions\n" + additions.String()
}

func nginxArgs(i model.Instance, extra ...string) []string {
	dir := filepath.Dir(filepath.Dir(i.ConfigPath))
	return append([]string{"-p", filepath.ToSlash(dir) + "/", "-c", filepath.ToSlash(i.ConfigPath)}, extra...)
}
func startArgs(i model.Instance) []string {
	switch i.Kind {
	case "php":
		return []string{"-b", "127.0.0.1:" + strconv.Itoa(i.Port), "-c", i.ConfigPath}
	case "nginx":
		return nginxArgs(i)
	case "mysql":
		return mysqlServerArgs(i, "--console")
	case "redis":
		return []string{cygwinPath(i.ConfigPath)}
	}
	return nil
}
func configArgument(i model.Instance) string {
	if i.Kind == "redis" {
		return cygwinPath(i.ConfigPath)
	}
	return i.ConfigPath
}

func mysqlServerArgs(i model.Instance, extra ...string) []string {
	dir := filepath.Dir(filepath.Dir(i.ConfigPath))
	args := []string{"--defaults-file=" + i.ConfigPath, "--basedir=" + filepath.Dir(filepath.Dir(i.Executable)), "--datadir=" + i.DataDir, "--port=" + strconv.Itoa(i.Port), "--bind-address=127.0.0.1", "--pid-file=" + filepath.Join(dir, "mysql.pid"), "--log-error=" + i.LogPath}
	if strings.HasPrefix(i.PackageID, "mysql-8.") {
		args = append(args, "--no-monitor", "--mysqlx=0")
	}
	return append(args, extra...)
}

func verifyNginxPID(i model.Instance) error {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(i.ConfigPath)), "nginx.pid"))
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid != i.PID {
		return errors.New("Nginx PID 文件与受管进程不一致，已拒绝发送信号")
	}
	return nil
}

// Redis has no config-only validation mode. Check the critical ownership and loopback
// directives here; its actual parser is exercised by startup and the readiness check.
func validateRedisConfig(i model.Instance) error {
	f, err := os.Open(i.ConfigPath)
	if err != nil {
		return err
	}
	defer f.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields, err := redisFields(line)
		if err != nil {
			return err
		}
		if len(fields) > 1 {
			if strings.EqualFold(fields[0], "include") {
				return errors.New("受管 Redis 配置不支持 include；请直接编辑当前实例配置")
			}
			values[strings.ToLower(fields[0])] = strings.Join(fields[1:], " ")
		}
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	if values["port"] != strconv.Itoa(i.Port) || values["bind"] != "127.0.0.1" || values["daemonize"] != "no" || values["dir"] != cygwinPath(i.DataDir) {
		return errors.New("Redis 配置必须保留实例端口、127.0.0.1 绑定、daemonize no 和独立数据目录")
	}
	if values["logfile"] != cygwinPath(i.LogPath) || values["pidfile"] != cygwinPath(filepath.Join(filepath.Dir(filepath.Dir(i.ConfigPath)), "redis.pid")) {
		return errors.New("Redis 日志和 PID 路径必须保留在本实例目录内")
	}
	return nil
}

func validateMySQLConfig(i model.Instance) error {
	b, err := os.ReadFile(i.ConfigPath)
	if err != nil {
		return err
	}
	values := map[string]string{}
	section := ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "!") {
			return errors.New("受管 MySQL 配置不支持 include；请直接编辑当前实例配置")
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		if section != "mysqld" && section != "server" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimPrefix(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "_", "-"), "loose-")
		if !ok {
			value = ""
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		values[key] = value
	}
	if values["port"] != strconv.Itoa(i.Port) || values["bind-address"] != "127.0.0.1" || !samePath(filepath.FromSlash(values["datadir"]), i.DataDir) {
		return errors.New("MySQL 配置必须保留实例端口、127.0.0.1 绑定和独立数据目录")
	}
	dir := filepath.Dir(filepath.Dir(i.ConfigPath))
	if !samePath(filepath.FromSlash(values["pid-file"]), filepath.Join(dir, "mysql.pid")) || !samePath(filepath.FromSlash(values["log-error"]), i.LogPath) {
		return errors.New("MySQL 日志和 PID 路径必须保留在本实例目录内")
	}
	return nil
}

func redisFields(line string) ([]string, error) {
	var result []string
	var token strings.Builder
	quote := rune(0)
	escape := false
	started := false
	flush := func() {
		if started {
			result = append(result, token.String())
			token.Reset()
			started = false
		}
	}
	for _, r := range line {
		if escape {
			token.WriteRune(r)
			escape = false
			started = true
			continue
		}
		if r == '\\' && quote != 0 {
			escape = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				token.WriteRune(r)
			}
			started = true
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			started = true
			continue
		}
		if r == ' ' || r == '\t' {
			flush()
			continue
		}
		if r == '#' && !started {
			break
		}
		token.WriteRune(r)
		started = true
	}
	if quote != 0 || escape {
		return nil, errors.New("Redis 配置包含未闭合引号")
	}
	flush()
	return result, nil
}

func (m *Manager) secret(id string) (string, error) {
	if m.getSecret == nil {
		return "", errors.New("无法读取 Windows 凭据管理器中的 MySQL 密码")
	}
	password, err := m.getSecret(id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "secret not found in keyring") {
			return "", errors.New("找不到这个 MySQL 实例的管理密码。请删除该实例后重新打开软件，会自动重建并保存管理密码，然后再启动。")
		}
		if hasChinese(err.Error()) {
			return "", err
		}
		return "", errors.New("无法读取 Windows 凭据管理器中的 MySQL 密码")
	}
	if password == "" || strings.ContainsAny(password, "\x00\r\n") {
		return "", errors.New("MySQL 密码不能为空或包含换行、空字节")
	}
	return password, nil
}

func hasChinese(text string) bool {
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

func mysqlPassword(password string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(password) + `"`
}
func sqlPassword(password string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `''`).Replace(password) + "'"
}
func memoryName(i model.Instance) string { return "PHPTools_" + i.ID }

func (m *Manager) clientFile(i model.Instance, memory bool, password string) (string, func(), error) {
	dir, _ := m.instanceDir(i.ID)
	dir = filepath.Join(dir, "private")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", nil, err
	}
	if err := protectPath(dir, true); err != nil {
		return "", nil, err
	}
	f, err := os.CreateTemp(dir, "mysql-client-*.cnf")
	if err != nil {
		return "", nil, err
	}
	name := f.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err = protectPath(name, false); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, err
	}
	contents := "[client]\nuser=root\npassword=" + mysqlPassword(password) + "\n"
	if memory {
		contents += "protocol=MEMORY\nshared-memory-base-name=" + memoryName(i) + "\n"
	} else {
		contents += fmt.Sprintf("protocol=TCP\nhost=127.0.0.1\nport=%d\n", i.Port)
	}
	_, err = f.WriteString(contents)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return name, cleanup, nil
}

func (m *Manager) mysqlShutdown(ctx context.Context, i model.Instance, memory bool) error {
	password, err := m.secret(i.ID)
	if err != nil {
		return mysqlShutdownEvent(i)
	}
	conf, cleanup, err := m.clientFile(i, memory, password)
	if err != nil {
		return mysqlShutdownEvent(i)
	}
	defer cleanup()
	cli := filepath.Join(filepath.Dir(i.Executable), "mysqladmin.exe")
	// Do not surface client output: a malformed configuration must never expose credentials.
	cmdctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdctx, cli, "--defaults-file="+conf, "shutdown")
	cmd.Dir = filepath.Dir(cli)
	cmd.Env = mysqlClientEnv(i)
	hideConsole(cmd)
	if err = cmd.Run(); err != nil {
		if memory { // A failed first initialization may still have its empty root password.
			emptyConf, emptyCleanup, emptyErr := m.clientFile(i, true, "")
			if emptyErr == nil {
				defer emptyCleanup()
				fallback := exec.CommandContext(cmdctx, cli, "--defaults-file="+emptyConf, "shutdown")
				fallback.Dir = filepath.Dir(cli)
				fallback.Env = mysqlClientEnv(i)
				hideConsole(fallback)
				if fallback.Run() == nil {
					return nil
				}
			}
		}
		if eventErr := mysqlShutdownEvent(i); eventErr != nil {
			return fmt.Errorf("MySQL 正常关闭失败（密码未写入命令行）：%w；原生关闭事件：%v", err, eventErr)
		}
		return nil
	}
	return nil
}

func (m *Manager) mysqlQuery(ctx context.Context, i model.Instance, memory bool, password, sql string) error {
	conf, cleanup, err := m.clientFile(i, memory, password)
	if err != nil {
		return err
	}
	defer cleanup()
	cli := filepath.Join(filepath.Dir(i.Executable), "mysql.exe")
	cmdctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdctx, cli, "--defaults-file="+conf, "--batch", "--skip-column-names")
	cmd.Dir = filepath.Dir(cli)
	cmd.Env = mysqlClientEnv(i)
	cmd.Stdin = strings.NewReader(sql)
	hideConsole(cmd)
	if err = cmd.Run(); err != nil {
		return errors.New("MySQL 本机认证或初始化语句执行失败")
	}
	return nil
}

func mysqlClientEnv(i model.Instance) []string {
	// mysql clients otherwise load the current user's .mylogin.cnf even with an
	// explicit defaults file. Use their documented test-login-file override.
	return append(os.Environ(), "MYSQL_TEST_LOGIN_FILE="+filepath.Join(filepath.Dir(filepath.Dir(i.ConfigPath)), "private", "unused.mylogin.cnf"))
}

func (m *Manager) probeReady(ctx context.Context, i model.Instance) error {
	switch i.Kind {
	case "mysql":
		password, err := m.secret(i.ID)
		if err != nil {
			return err
		}
		return m.mysqlQuery(ctx, i, false, password, "SELECT 1;\n")
	case "redis":
		conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(i.Port)), time.Second)
		if err != nil {
			return err
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		if _, err = io.WriteString(conn, "*1\r\n$4\r\nPING\r\n"); err != nil {
			return err
		}
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return err
		}
		if line != "+PONG\r\n" {
			return errors.New("Redis 未返回 PONG")
		}
	case "nginx":
		client := http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/__phptools", i.Port), nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 100))
		if resp.StatusCode != 200 || string(body) != "PHPTools is running" {
			return errors.New("Nginx 健康检查失败")
		}
	}
	return nil
}

func (m *Manager) prepareMySQL(ctx context.Context, pkg model.Package, i model.Instance) (model.Instance, error) {
	password, err := m.secret(i.ID)
	if err != nil {
		return i, err
	}
	dir, _ := m.instanceDir(i.ID)
	marker := filepath.Join(dir, "mysql-initialized")
	if _, err = os.Stat(marker); err == nil {
		return i, nil
	} else if !os.IsNotExist(err) {
		return i, err
	}
	if _, err = os.Stat(filepath.Join(i.DataDir, "mysql")); os.IsNotExist(err) {
		entries, readErr := os.ReadDir(i.DataDir)
		if readErr != nil {
			return i, readErr
		}
		if len(entries) > 0 {
			return i, errors.New("MySQL 数据目录包含未完成的初始化文件，请检查日志并使用新的空数据目录重建实例")
		}
		initctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		cmd := exec.CommandContext(initctx, i.Executable, mysqlServerArgs(i, "--initialize-insecure", "--console")...)
		cmd.Dir = filepath.Dir(i.Executable)
		hideConsole(cmd)
		log, logErr := os.OpenFile(i.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if logErr != nil {
			cancel()
			return i, logErr
		}
		cmd.Stdout = log
		cmd.Stderr = log
		err = cmd.Run()
		_ = log.Close()
		cancel()
		if err != nil {
			return i, fmt.Errorf("MySQL 数据目录初始化失败，请查看 %s：%w", i.LogPath, err)
		}
	} else if err != nil {
		return i, err
	}
	// Bootstrap with networking disabled. SQL travels through stdin of a local
	// shared-memory client, never through a process argument or an SQL file.
	i, err = m.launch(i, mysqlServerArgs(i, "--skip-networking", "--shared-memory", "--shared-memory-base-name="+memoryName(i), "--console"), nil)
	if err != nil {
		return i, err
	}
	deadline := time.Now().Add(60 * time.Second)
	authenticated := false
	emptyPassword := false
	for time.Now().Before(deadline) {
		if _, _, err = processIdentity(i.PID); err != nil {
			return i, fmt.Errorf("MySQL 初始化进程退出，请查看 %s", i.LogPath)
		}
		if m.mysqlQuery(ctx, i, true, password, "SELECT 1;\n") == nil {
			authenticated = true
			break
		}
		if m.mysqlQuery(ctx, i, true, "", "SELECT 1;\n") == nil {
			emptyPassword = true
			break
		}
		select {
		case <-ctx.Done():
			i.State = "error"
			i.Error = "初始化被取消，未开放网络；进程保留供重试停止"
			_ = m.persist(i)
			return i, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if !authenticated && !emptyPassword {
		i.State = "error"
		i.Error = "MySQL 初始化认证超时，未开放网络；请检查日志和凭据"
		_ = m.persist(i)
		return i, errors.New(i.Error)
	}
	if emptyPassword {
		if err = m.mysqlQuery(ctx, i, true, "", "ALTER USER 'root'@'localhost' IDENTIFIED BY "+sqlPassword(password)+";\n"); err != nil {
			i.State = "error"
			i.Error = err.Error()
			_ = m.persist(i)
			return i, err
		}
	}
	// TCP loopback is a distinct account when skip-name-resolve is enabled.
	loopbackSQL := "CREATE USER IF NOT EXISTS 'root'@'127.0.0.1' IDENTIFIED BY " + sqlPassword(password) + ";\nALTER USER 'root'@'127.0.0.1' IDENTIFIED BY " + sqlPassword(password) + ";\nGRANT ALL PRIVILEGES ON *.* TO 'root'@'127.0.0.1' WITH GRANT OPTION;\n"
	if err = m.mysqlQuery(ctx, i, true, password, loopbackSQL); err != nil {
		return i, err
	}
	if err = m.mysqlQuery(ctx, i, true, password, "SELECT 1;\n"); err != nil {
		return i, err
	}
	// Record successful credential setup before shutting down so interrupted
	// bootstrap never silently replaces an existing password or data directory.
	if err = os.WriteFile(marker, []byte(model.Now()+"\n"), 0600); err != nil {
		return i, err
	}
	if err = m.mysqlShutdown(ctx, i, true); err != nil {
		i.State = "error"
		i.Error = err.Error()
		_ = m.persist(i)
		return i, err
	}
	if err = waitStopped(ctx, i, 30*time.Second); err != nil {
		return i, err
	}
	i.PID = 0
	i.ProcessCreated = 0
	i.State = "stopped"
	i.Error = ""
	_ = os.Remove(filepath.Join(dir, "process.json"))
	if err = m.persist(i); err != nil {
		return i, err
	}
	return i, nil
}
