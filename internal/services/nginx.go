package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"phptools/internal/model"
)

type nginxDirective struct {
	args  []string
	depth int
	block bool
}

// scanNginx handles quoted arguments, escaped characters, comments, and
// directives on a single line; checking lines with a regexp would miss these.
func scanNginx(input string) ([]nginxDirective, error) {
	var directives []nginxDirective
	var args []string
	var word strings.Builder
	depth := 0
	quote := rune(0)
	escape := false
	comment := false
	started := false
	flush := func() {
		if started {
			args = append(args, word.String())
			word.Reset()
			started = false
		}
	}
	for _, r := range input {
		if comment {
			if r == '\n' {
				comment = false
			}
			continue
		}
		if escape {
			word.WriteRune(r)
			started = true
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			started = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			started = true
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			started = true
			continue
		}
		if r == '#' {
			flush()
			comment = true
			continue
		}
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		switch r {
		case ';':
			flush()
			if len(args) == 0 {
				return nil, errors.New("Nginx 配置包含空指令")
			}
			directives = append(directives, nginxDirective{args: args, depth: depth})
			args = nil
		case '{':
			flush()
			if len(args) == 0 {
				return nil, errors.New("Nginx 配置缺少块名称")
			}
			directives = append(directives, nginxDirective{args: args, depth: depth, block: true})
			args = nil
			depth++
		case '}':
			flush()
			if len(args) > 0 || depth == 0 {
				return nil, errors.New("Nginx 配置块或分号不完整")
			}
			depth--
		default:
			word.WriteRune(r)
			started = true
		}
	}
	flush()
	if quote != 0 || escape || depth != 0 || len(args) > 0 {
		return nil, errors.New("Nginx 配置包含未闭合的引号、块或指令")
	}
	return directives, nil
}

func (m *Manager) siteConfigGlob() string {
	return filepath.Join(m.root, "conf", "*.conf")
}

func (m *Manager) AdoptSiteConfigDirectory() error {
	previous := nginxQuote(filepath.Join(m.root, "sites", "*.conf"))
	current := nginxQuote(m.siteConfigGlob())
	entries, err := os.ReadDir(filepath.Join(m.root, "instances"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("无法读取 Nginx 实例目录")
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(m.root, "instances", entry.Name(), "conf", "nginx.conf")
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return errors.New("无法读取 Nginx 配置")
		}
		text := string(data)
		if !strings.Contains(text, previous) {
			continue
		}
		updated := strings.ReplaceAll(text, previous, current)
		tmp := path + ".tmp"
		if err = os.WriteFile(tmp, []byte(updated), 0600); err != nil {
			os.Remove(tmp)
			return errors.New("无法更新 Nginx 的站点配置目录")
		}
		if err = os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			return errors.New("无法更新 Nginx 的站点配置目录")
		}
	}
	return nil
}

func (m *Manager) validateNginxConfig(i model.Instance) error {
	return m.inspectNginxConfig(i, nil)
}

func (m *Manager) inspectNginxConfig(i model.Instance, visit func(nginxDirective) error) error {
	dir := filepath.Dir(filepath.Dir(i.ConfigPath))
	pid := filepath.Join(dir, "nginx.pid")
	mime := filepath.Join(filepath.Dir(i.Executable), "conf", "mime.types")
	params := filepath.Join(filepath.Dir(i.ConfigPath), "fastcgi_params")
	siteGlob := m.siteConfigGlob()
	data, err := os.ReadFile(i.ConfigPath)
	if err != nil {
		return err
	}
	main, err := scanNginx(string(data))
	if err != nil {
		return err
	}
	pidCount := 0
	for _, d := range main {
		if d.args[0] == "pid" {
			pidCount++
			if d.depth != 0 || len(d.args) != 2 || !samePath(filepath.FromSlash(d.args[1]), pid) {
				return errors.New("Nginx pid 必须指向当前实例 nginx.pid")
			}
		}
	}
	if pidCount != 1 {
		return errors.New("Nginx 主配置必须且只能包含一个当前实例的 pid 指令")
	}
	active := map[string]bool{}
	var inspect func([]nginxDirective, bool, int) error
	inspect = func(ds []nginxDirective, isMain bool, baseDepth int) error {
		for _, d := range ds {
			if len(d.args) == 0 {
				continue
			}
			if visit != nil {
				effective := d
				effective.depth += baseDepth
				if err := visit(effective); err != nil {
					return err
				}
			}
			key := d.args[0]
			if key == "pid" && !isMain {
				return errors.New("Nginx 包含文件不得设置 pid")
			}
			if key == "error_log" || key == "access_log" || strings.HasSuffix(key, "_temp_path") {
				if len(d.args) < 2 {
					return errors.New("Nginx 日志或临时目录指令缺少路径")
				}
				path := d.args[1]
				if key == "access_log" && path == "off" {
					continue
				}
				if strings.ContainsAny(path, "$\x00\r\n") || !filepath.IsAbs(filepath.FromSlash(path)) {
					return fmt.Errorf("Nginx %s 必须使用受管目录内的绝对路径", key)
				}
				if !within(dir, filepath.FromSlash(path)) && !(key == "error_log" || key == "access_log") {
					return errors.New("Nginx 临时路径必须位于本实例目录")
				}
				if (key == "error_log" || key == "access_log") && !within(dir, filepath.FromSlash(path)) && !within(filepath.Join(m.root, "logs"), filepath.FromSlash(path)) {
					return errors.New("Nginx 日志必须位于 PHPTools 实例或日志目录")
				}
			}
			if key != "include" {
				continue
			}
			if len(d.args) != 2 {
				return errors.New("Nginx include 参数无效")
			}
			path := filepath.FromSlash(d.args[1])
			if path == "fastcgi_params" {
				path = params
			}
			if !samePath(path, mime) && !samePath(path, params) && !samePath(path, siteGlob) {
				return errors.New("受管 Nginx 只允许包含发行包 mime.types、本实例 fastcgi_params 和 PHPTools 站点配置")
			}
			if d.depth == 0 && isMain {
				return errors.New("Nginx 主上下文不得使用 include")
			}
			paths := []string{path}
			if samePath(path, siteGlob) {
				paths, err = filepath.Glob(path)
				if err != nil {
					return err
				}
			}
			for _, include := range paths {
				if active[include] {
					return errors.New("Nginx include 存在循环引用")
				}
				active[include] = true
				info, err := os.Lstat(include)
				if err != nil {
					return err
				}
				if info.Mode()&os.ModeSymlink != 0 {
					return errors.New("Nginx include 不得使用链接")
				}
				b, err := os.ReadFile(include)
				if err != nil {
					return err
				}
				included, err := scanNginx(string(b))
				if err != nil {
					return err
				}
				if err = inspect(included, false, baseDepth+d.depth); err != nil {
					return err
				}
				delete(active, include)
			}
		}
		return nil
	}
	return inspect(main, true, 0)
}

type nginxListen struct {
	address netip.Addr
	port    uint16
}

type nginxListener struct {
	nginxListen
	pid int
}

func parseNginxListen(value string) (nginxListen, error) {
	listen := nginxListen{address: netip.IPv4Unspecified(), port: 80}
	port := value
	if strings.Contains(value, ":") || strings.HasPrefix(value, "[") {
		if address, err := netip.ParseAddr(strings.Trim(value, "[]")); err == nil {
			listen.address = address.Unmap()
			return listen, nil
		}
		host, number, err := net.SplitHostPort(value)
		if err != nil {
			return nginxListen{}, fmt.Errorf("无法检查 Nginx listen %q：请使用数字 IP 和端口", value)
		}
		port = number
		if host != "*" && host != "" {
			address, err := netip.ParseAddr(host)
			if err != nil {
				return nginxListen{}, fmt.Errorf("无法检查 Nginx listen %q：请使用数字 IP 和端口", value)
			}
			listen.address = address.Unmap()
		}
	} else if address, err := netip.ParseAddr(value); err == nil {
		listen.address = address.Unmap()
		return listen, nil
	}
	if port == "" || strings.IndexFunc(port, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return nginxListen{}, fmt.Errorf("无法检查 Nginx listen %q：端口必须是数字", value)
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return nginxListen{}, fmt.Errorf("Nginx listen %q 的端口必须在 1 到 65535 之间", value)
	}
	listen.port = uint16(number)
	return listen, nil
}

func (m *Manager) nginxListenEndpoints(i model.Instance) ([]nginxListen, error) {
	set := map[nginxListen]bool{}
	type serverBlock struct {
		depth     int
		hasListen bool
	}
	var servers []serverBlock
	finishServer := func() {
		last := servers[len(servers)-1]
		if !last.hasListen {
			set[nginxListen{address: netip.IPv4Unspecified(), port: 80}] = true
		}
		servers = servers[:len(servers)-1]
	}
	err := m.inspectNginxConfig(i, func(d nginxDirective) error {
		for len(servers) > 0 && d.depth <= servers[len(servers)-1].depth {
			finishServer()
		}
		if d.block && d.args[0] == "server" {
			servers = append(servers, serverBlock{depth: d.depth})
		}
		if d.args[0] != "listen" {
			return nil
		}
		if len(d.args) < 2 {
			return errors.New("Nginx listen 缺少地址或端口")
		}
		for _, option := range d.args[2:] {
			if option == "udp" {
				return errors.New("受管 Nginx 当前只支持检查 TCP listen")
			}
		}
		listen, err := parseNginxListen(d.args[1])
		if err != nil {
			return err
		}
		set[listen] = true
		if len(servers) > 0 {
			servers[len(servers)-1].hasListen = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for len(servers) > 0 {
		finishServer()
	}
	listens := make([]nginxListen, 0, len(set))
	for listen := range set {
		listens = append(listens, listen)
	}
	sort.Slice(listens, func(a, b int) bool {
		if listens[a].port != listens[b].port {
			return listens[a].port < listens[b].port
		}
		return listens[a].address.Compare(listens[b].address) < 0
	})
	return listens, nil
}

func nginxListenOverlap(a, b nginxListen) bool {
	if a.port != b.port {
		return false
	}
	if a.address == b.address {
		return true
	}
	// The TCP owner table does not expose IPV6_V6ONLY. Treat :: as potentially
	// dual-stack so a wildcard listener cannot silently share IPv4 traffic.
	if a.address.IsUnspecified() {
		return a.address.Is6() || b.address.Is4()
	}
	if b.address.IsUnspecified() {
		return b.address.Is6() || a.address.Is4()
	}
	return false
}

func checkNginxListeners(listens []nginxListen, listeners []nginxListener, owned func(int) bool) error {
	for _, listen := range listens {
		for _, listener := range listeners {
			if nginxListenOverlap(listen, listener.nginxListen) && !owned(listener.pid) {
				return fmt.Errorf("Nginx 端口 %d 已被其他进程占用（%s，PID %d）；请更换实例或站点端口", listen.port, net.JoinHostPort(listener.address.String(), strconv.Itoa(int(listener.port))), listener.pid)
			}
		}
	}
	return nil
}

func nginxOwnedProcessIDs(i model.Instance, parents map[int]int, identity func(int) (uint64, string, error), verify func(processRecord) error) (map[int]bool, error) {
	owned := map[int]bool{}
	if i.State != "running" || i.PID <= 0 {
		return owned, nil
	}
	master := processRecord{PID: i.PID, Created: i.ProcessCreated, Executable: i.Executable, Config: i.ConfigPath, ConfigArgument: configArgument(i)}
	if err := verify(master); err != nil {
		return nil, err
	}
	owned[i.PID] = true
	for pid, parent := range parents {
		if parent != i.PID || pid == i.PID {
			continue
		}
		created, executable, err := identity(pid)
		if err != nil || created < i.ProcessCreated || !samePath(executable, i.Executable) {
			continue
		}
		worker := processRecord{PID: pid, Created: created, Executable: executable, Config: i.ConfigPath, ConfigArgument: configArgument(i)}
		if verify(worker) == nil {
			owned[pid] = true
		}
	}
	return owned, nil
}

func (m *Manager) validateNginxPorts(i model.Instance) error {
	listens, err := m.nginxListenEndpoints(i)
	if err != nil {
		return err
	}
	listeners, err := nginxTCPListeners()
	if err != nil {
		return fmt.Errorf("无法检查 Nginx 监听端口，已取消操作：%w", err)
	}
	current := m.inspect(i)
	owned, err := nginxOwnedListenerProcesses(current)
	if err != nil {
		return fmt.Errorf("无法核实 Nginx 监听进程归属，已取消操作：%w", err)
	}
	return checkNginxListeners(listens, listeners, func(pid int) bool { return owned[pid] })
}

func (m *Manager) signalNginx(ctx context.Context, i model.Instance, signal string) error {
	if signal != "quit" && signal != "reload" {
		return errors.New("无效 Nginx 信号")
	}
	if err := verifyNginxPID(i); err != nil {
		return err
	}
	dir := filepath.Dir(filepath.Dir(i.ConfigPath))
	private := filepath.Join(dir, "private")
	if err := os.MkdirAll(private, 0700); err != nil {
		return err
	}
	if err := protectPath(private, true); err != nil {
		return err
	}
	f, err := os.CreateTemp(private, "nginx-signal-*.conf")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	if err = protectPath(path, false); err != nil {
		_ = f.Close()
		return err
	}
	_, err = f.WriteString("pid " + nginxQuote(filepath.Join(dir, "nginx.pid")) + ";\nerror_log " + nginxQuote(i.LogPath) + " notice;\nevents {}\n")
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return runCommand(ctx, i.Executable, []string{"-p", filepath.ToSlash(dir) + "/", "-c", filepath.ToSlash(path), "-s", signal}, nil)
}
