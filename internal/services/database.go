package services

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"phptools/internal/model"
)

var databaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
var databaseUserPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,31}$`)
var databaseHostPattern = regexp.MustCompile(`^[A-Za-z0-9_.:%-]{1,255}$`)
var databasePackagePattern = regexp.MustCompile(`^mysql-(?:5\.7|8\.0|8\.4)\.\d+-x64$`)

var databasePrivilegeOrder = []string{
	"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "INDEX", "ALTER",
	"REFERENCES", "CREATE TEMPORARY TABLES", "LOCK TABLES", "EXECUTE",
	"CREATE VIEW", "SHOW VIEW", "CREATE ROUTINE", "ALTER ROUTINE", "EVENT", "TRIGGER",
}

var databasePrivileges = func() map[string]bool {
	values := make(map[string]bool, len(databasePrivilegeOrder))
	for _, value := range databasePrivilegeOrder {
		values[value] = true
	}
	return values
}()

func systemDatabase(name string) bool {
	switch strings.ToLower(name) {
	case "information_schema", "mysql", "performance_schema", "sys":
		return true
	}
	return false
}

func validateDatabaseName(name string) error {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 64 ||
		name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.HasSuffix(name, " ") || strings.ContainsFunc(name, unicode.IsControl) {
		return errors.New("数据库名须为 1 到 64 个有效 UTF-8 字符，不能包含路径分隔符或控制字符，不能以空格结尾，也不能为 . 或 ..")
	}
	if systemDatabase(name) {
		return errors.New("系统数据库受保护，不能执行此操作")
	}
	return nil
}

func validateNewDatabaseName(name string) error {
	if !databaseNamePattern.MatchString(name) {
		return errors.New("新建数据库名只能包含字母、数字和下划线，长度为 1 到 64 个字符")
	}
	return validateDatabaseName(name)
}

func databaseIdentifier(name string) string { return "`" + strings.ReplaceAll(name, "`", "``") + "`" }

func protectedDatabaseAccount(user string) bool {
	value := strings.ToLower(user)
	return value == "" || value == "root" || strings.HasPrefix(value, "mysql.")
}

func validateDatabaseAccount(user, host string, modifying bool) error {
	if !modifying {
		// Existing accounts may contain Unicode, quotes or netmask hosts. These
		// are safe to read as SQL string literals after sqlPassword escaping.
		if !utf8.ValidString(user) || !utf8.ValidString(host) || utf8.RuneCountInString(user) > 32 || utf8.RuneCountInString(host) > 255 ||
			strings.ContainsFunc(user, unicode.IsControl) || strings.ContainsFunc(host, unicode.IsControl) {
			return errors.New("账号或 Host 不是有效的 MySQL 账号名称")
		}
		return nil
	}
	if modifying && protectedDatabaseAccount(user) {
		return errors.New("root、MySQL 系统账号和匿名账号受保护，不能执行此操作")
	}
	if user != "" && !databaseUserPattern.MatchString(user) {
		return errors.New("账号名须以字母、数字或下划线开头，只能包含字母、数字、下划线、点或短横线，最多 32 个字符")
	}
	if !databaseHostPattern.MatchString(host) {
		return errors.New("账号 Host 只能包含字母、数字、点、下划线、冒号、百分号或短横线，最多 255 个字符")
	}
	return nil
}

func validateDatabasePassword(password string) error {
	if len(password) < 8 || len(password) > 1024 || !utf8.ValidString(password) || strings.ContainsAny(password, "\x00\r\n") {
		return errors.New("账号密码长度须为 8 到 1024 字节，且不能包含换行或空字节")
	}
	return nil
}

func databaseAccountSQL(user, host string) string { return sqlPassword(user) + "@" + sqlPassword(host) }

func databasePrivilegeSQL(action string, input model.DatabasePrivilegeInput, partialRevokes bool) (string, error) {
	if err := validateDatabaseName(input.Database); err != nil {
		return "", err
	}
	if err := validateDatabaseAccount(input.User, input.Host, true); err != nil {
		return "", err
	}
	if action != "GRANT" && action != "REVOKE" {
		return "", errors.New("无效权限操作")
	}
	if len(input.Privileges) == 0 || len(input.Privileges) > len(databasePrivileges) {
		return "", errors.New("请至少选择一项数据库权限")
	}
	privileges := []string{}
	seen := map[string]bool{}
	for _, value := range input.Privileges {
		value = strings.ToUpper(strings.TrimSpace(value))
		if !databasePrivileges[value] {
			return "", errors.New("包含不支持的数据库权限")
		}
		if !seen[value] {
			privileges = append(privileges, value)
			seen[value] = true
		}
	}
	// Without partial_revokes MySQL interprets _ and % in database-level grants
	// as wildcards even inside backticks. With it enabled, names are literal.
	database := input.Database
	if !partialRevokes {
		database = strings.NewReplacer("_", `\_`, "%", `\%`).Replace(database)
	}
	target := databaseIdentifier(database) + ".*"
	connector := " TO "
	if action == "REVOKE" {
		connector = " FROM "
	}
	return action + " " + strings.Join(privileges, ", ") + " ON " + target + connector + databaseAccountSQL(input.User, input.Host) + ";\n", nil
}

// databaseInstance is called while m.mu is held, serializing database clients
// with service start/stop and verifying the exact managed server process.
func (m *Manager) databaseInstance(i model.Instance) (model.Instance, error) {
	if i.Kind != "mysql" {
		return i, errors.New("数据库操作只适用于 MySQL 实例")
	}
	if err := m.validateInstance(i); err != nil {
		return i, err
	}
	if !within(filepath.Join(m.root, "packages"), i.Executable) || !strings.EqualFold(filepath.Base(i.Executable), "mysqld.exe") {
		return i, errors.New("MySQL 程序不属于当前环境，已拒绝操作")
	}
	if _, err := databaseDumpOptions(i.PackageID); err != nil {
		return i, err
	}
	i = m.inspect(i)
	if i.State != "running" || i.PID == 0 {
		return i, errors.New("MySQL 未运行或无法确认进程归属，请先检查实例状态")
	}
	dir, _ := m.instanceDir(i.ID)
	raw, err := os.ReadFile(filepath.Join(dir, "process.json"))
	var record processRecord
	if err != nil || json.Unmarshal(raw, &record) != nil || record.Bootstrap {
		return i, errors.New("MySQL 尚未完成初始化或缺少可信进程记录")
	}
	if !within(filepath.Join(m.root, "packages"), i.Executable) || !strings.EqualFold(filepath.Base(i.Executable), "mysqld.exe") {
		return i, errors.New("MySQL 程序不属于当前环境，已拒绝操作")
	}
	return i, nil
}

type databaseOutput struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *databaseOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if remaining < len(p) {
		b.overflow = true
		if remaining > 0 {
			_, _ = b.Buffer.Write(p[:remaining])
		}
	} else {
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func databaseCommandError(ctx context.Context, stderr string, passwords []string, sensitive bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sensitive {
		return errors.New("MySQL 账号操作失败，请检查账号是否存在、密码策略及实例日志")
	}
	for _, password := range passwords {
		if password != "" {
			stderr = strings.ReplaceAll(stderr, password, "[redacted]")
			stderr = strings.ReplaceAll(stderr, strings.Trim(sqlPassword(password), "'"), "[redacted]")
		}
	}
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return errors.New("MySQL 客户端执行失败，请检查实例连接和日志")
	}
	return fmt.Errorf("MySQL 客户端执行失败：%s", stderr)
}

func (m *Manager) databaseCommand(ctx context.Context, i model.Instance, executable string, args []string, stdin io.Reader, stdout io.Writer, sensitive bool) error {
	password, err := m.secret(i.ID)
	if err != nil {
		return errors.New("无法读取此 MySQL 实例的管理凭据")
	}
	conf, cleanup, err := m.clientFile(i, false, password)
	if err != nil {
		return errors.New("无法创建受保护的 MySQL 客户端凭据文件")
	}
	defer cleanup()
	cli := filepath.Join(filepath.Dir(i.Executable), executable)
	if !within(filepath.Join(m.root, "packages"), cli) {
		return errors.New("MySQL 客户端路径不属于当前环境")
	}
	cmd := exec.CommandContext(ctx, cli, append([]string{"--defaults-file=" + conf}, args...)...)
	cmd.Dir = filepath.Dir(cli)
	cmd.Env = mysqlClientEnv(i)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	stderr := &databaseOutput{limit: 8192}
	cmd.Stderr = stderr
	hideConsole(cmd)
	if err = cmd.Run(); err != nil {
		return databaseCommandError(ctx, stderr.String(), []string{password}, sensitive)
	}
	return nil
}

func (m *Manager) databaseQuery(ctx context.Context, i model.Instance, sql string, sensitive bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out := &databaseOutput{limit: 8 << 20}
	// Reset the session's quoting convention rather than depending on an
	// instance's configurable NO_BACKSLASH_ESCAPES/ANSI_QUOTES modes.
	mode := ""
	if strings.HasPrefix(i.PackageID, "mysql-5.7.") {
		// 5.7 can otherwise implicitly create a passwordless user via GRANT.
		mode = "NO_AUTO_CREATE_USER"
	}
	input := strings.NewReader("SET SESSION sql_mode='" + mode + "';\n" + sql)
	err := m.databaseCommand(ctx, i, "mysql.exe", []string{"--batch", "--raw", "--skip-column-names", "--default-character-set=utf8mb4", "--connect-timeout=10"}, input, out, sensitive)
	if err != nil {
		return "", err
	}
	if out.overflow {
		return "", errors.New("MySQL 查询结果超过 8MB，请使用外部数据库客户端")
	}
	return out.String(), nil
}

func databaseHex(value string) (string, error) {
	b, err := hex.DecodeString(value)
	if err != nil || !utf8.Valid(b) {
		return "", errors.New("MySQL 返回了无法识别的文本结果")
	}
	return string(b), nil
}

func databaseRows(output string, columns int) ([][]string, error) {
	rows := [][]string{}
	for _, row := range strings.Split(strings.TrimRight(output, "\r\n"), "\n") {
		if row == "" {
			continue
		}
		fields := strings.Split(strings.TrimSuffix(row, "\r"), "\t")
		if len(fields) != columns {
			return nil, errors.New("MySQL 返回了无法识别的查询结果")
		}
		rows = append(rows, fields)
	}
	return rows, nil
}

func (m *Manager) ListDatabases(ctx context.Context, i model.Instance) ([]model.DatabaseInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Schema folders and db.opt are local metadata. Joining information_schema.TABLES
	// opens every table and makes the database page wait several seconds.
	dataDir, mysql8, err := m.schemaDirectory(i)
	if err != nil {
		return nil, err
	}
	return readSchemaDirectory(dataDir, mysql8)
}

func (m *Manager) schemaDirectory(i model.Instance) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.databaseInstance(i)
	if err != nil {
		return "", false, err
	}
	return i.DataDir, strings.HasPrefix(i.PackageID, "mysql-8."), nil
}

func readSchemaDirectory(dataDir string, mysql8 bool) ([]model.DatabaseInfo, error) {
	info, err := os.Lstat(dataDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, errors.New("MySQL 数据目录不可用")
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, err
	}
	result := []model.DatabaseInfo{}
	for _, entry := range entries {
		name := entry.Name()
		if !schemaDirectoryName(name) || entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 || !entry.IsDir() {
			continue
		}
		dir := filepath.Join(dataDir, name)
		charset, collation, err := readDatabaseOptions(filepath.Join(dir, "db.opt"))
		if err != nil {
			return nil, err
		}
		tables, size, err := schemaUsage(dir, mysql8)
		if err != nil {
			return nil, err
		}
		result = append(result, model.DatabaseInfo{
			Name: name, Charset: charset, Collation: collation,
			Tables: tables, SizeBytes: size, System: systemDatabase(name),
		})
	}
	sort.Slice(result, func(a, b int) bool {
		return strings.ToLower(result[a].Name) < strings.ToLower(result[b].Name)
	})
	return result, nil
}

func schemaDirectoryName(name string) bool {
	return utf8.ValidString(name) && name != "." && name != ".." && !strings.HasPrefix(name, "#") &&
		!strings.ContainsAny(name, `/\`) && !strings.ContainsFunc(name, unicode.IsControl)
}

func readDatabaseOptions(path string) (string, string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", "", nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4096))
	if err != nil {
		return "", "", err
	}
	charset, collation := "", ""
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(strings.TrimSuffix(line, "\r")), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "default-character-set":
			charset = strings.TrimSpace(value)
		case "default-collation":
			collation = strings.TrimSpace(value)
		}
	}
	return charset, collation, nil
}

func schemaUsage(dir string, mysql8 bool) (int64, int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}
	marker := ".frm"
	if mysql8 {
		marker = ".ibd"
	}
	var tables, size int64
	for _, entry := range entries {
		if entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 || entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, 0, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		size += info.Size()
		if strings.HasSuffix(strings.ToLower(info.Name()), marker) {
			tables++
		}
	}
	return tables, size, nil
}

func parseDatabaseList(output string) ([]model.DatabaseInfo, error) {
	rows, err := databaseRows(output, 5)
	if err != nil {
		return nil, err
	}
	result := []model.DatabaseInfo{}
	for _, row := range rows {
		name, nameErr := databaseHex(row[0])
		charset, charsetErr := databaseHex(row[1])
		collation, collationErr := databaseHex(row[2])
		tables, tablesErr := strconv.ParseInt(row[3], 10, 64)
		size, sizeErr := strconv.ParseInt(row[4], 10, 64)
		if nameErr != nil || charsetErr != nil || collationErr != nil || tablesErr != nil || sizeErr != nil || tables < 0 || size < 0 {
			return nil, errors.New("MySQL 返回了无法识别的数据库信息")
		}
		result = append(result, model.DatabaseInfo{Name: name, Charset: charset, Collation: collation, Tables: tables, SizeBytes: size, System: systemDatabase(name)})
	}
	return result, nil
}

func ValidateDatabaseLogin(name, password string) error {
	if err := validateNewDatabaseName(name); err != nil {
		return err
	}
	if err := validateDatabaseAccount(name, "localhost", true); err != nil {
		return errors.New("数据库名会同时作为登录账号，最多 32 个字符，只能包含字母、数字和下划线，且不能使用 root")
	}
	return validateDatabasePassword(password)
}

func (m *Manager) CreateDatabase(ctx context.Context, i model.Instance, name, password string) error {
	if err := ValidateDatabaseLogin(name, password); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.databaseInstance(i)
	if err != nil {
		return err
	}
	count, err := m.queryCount(ctx, i, "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME="+sqlPassword(name)+";\n")
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.New("数据库已存在")
	}
	count, err = m.queryCount(ctx, i, "SELECT COUNT(*) FROM mysql.user WHERE User="+sqlPassword(name)+" AND Host='localhost';\n")
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.New("已存在同名账号 " + name + "@localhost，请换一个数据库名")
	}
	if _, err = m.databaseQuery(ctx, i, "CREATE DATABASE "+databaseIdentifier(name)+" CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;\n", false); err != nil {
		return err
	}
	if _, err = m.databaseQuery(ctx, i, "CREATE USER "+databaseAccountSQL(name, "localhost")+" IDENTIFIED BY "+sqlPassword(password)+";\n", true); err != nil {
		if _, dropErr := m.databaseQuery(ctx, i, "DROP DATABASE "+databaseIdentifier(name)+";\n", false); dropErr != nil {
			return errors.New("创建账号失败，且新建的空数据库未能删除，请检查后重试")
		}
		return err
	}
	grant, err := m.grantSQL(ctx, i, model.DatabasePrivilegeInput{User: name, Host: "localhost", Database: name, Privileges: append([]string(nil), databasePrivilegeOrder...)}, "GRANT")
	if err != nil {
		m.removeCreatedDatabase(ctx, i, name)
		return err
	}
	if _, err = m.databaseQuery(ctx, i, grant, false); err != nil {
		m.removeCreatedDatabase(ctx, i, name)
		return err
	}
	return nil
}

func (m *Manager) removeCreatedDatabase(ctx context.Context, i model.Instance, name string) {
	_, _ = m.databaseQuery(ctx, i, "DROP USER "+databaseAccountSQL(name, "localhost")+";\n", false)
	_, _ = m.databaseQuery(ctx, i, "DROP DATABASE "+databaseIdentifier(name)+";\n", false)
}

func (m *Manager) queryCount(ctx context.Context, i model.Instance, sql string) (int64, error) {
	out, err := m.databaseQuery(ctx, i, sql, false)
	if err != nil {
		return 0, err
	}
	out = strings.TrimSpace(out)
	if strings.ContainsAny(out, "\r\n") {
		return 0, errors.New("MySQL 返回了无法识别的查询结果")
	}
	count, err := strconv.ParseInt(out, 10, 64)
	if err != nil || count < 0 {
		return 0, errors.New("MySQL 返回了无法识别的查询结果")
	}
	return count, nil
}

func (m *Manager) DropDatabase(ctx context.Context, i model.Instance, name string) error {
	if err := validateDatabaseName(name); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.databaseInstance(i)
	if err != nil {
		return err
	}
	_, err = m.databaseQuery(ctx, i, "DROP DATABASE "+databaseIdentifier(name)+";\n", false)
	return err
}

func (m *Manager) ListDatabaseAccounts(ctx context.Context, i model.Instance) ([]model.DatabaseAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.databaseInstance(i)
	if err != nil {
		return nil, err
	}
	out, err := m.databaseQuery(ctx, i, "SELECT HEX(User), HEX(Host) FROM mysql.user ORDER BY User,Host;\n", false)
	if err != nil {
		return nil, err
	}
	rows, err := databaseRows(out, 2)
	if err != nil {
		return nil, err
	}
	result := []model.DatabaseAccount{}
	for _, row := range rows {
		user, userErr := databaseHex(row[0])
		host, hostErr := databaseHex(row[1])
		if userErr != nil || hostErr != nil {
			return nil, errors.New("MySQL 返回了无法识别的账号信息")
		}
		result = append(result, model.DatabaseAccount{User: user, Host: host, Protected: protectedDatabaseAccount(user)})
	}
	return result, nil
}

func (m *Manager) CreateDatabaseAccount(ctx context.Context, i model.Instance, input model.DatabaseAccountInput) error {
	return m.databaseAccountPassword(ctx, i, input, true)
}

func (m *Manager) ChangeDatabaseAccountPassword(ctx context.Context, i model.Instance, input model.DatabaseAccountInput) error {
	return m.databaseAccountPassword(ctx, i, input, false)
}

func (m *Manager) databaseAccountPassword(ctx context.Context, i model.Instance, input model.DatabaseAccountInput, create bool) error {
	if err := validateDatabaseAccount(input.User, input.Host, true); err != nil {
		return err
	}
	if err := validateDatabasePassword(input.Password); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.databaseInstance(i)
	if err != nil {
		return err
	}
	verb := "ALTER USER "
	if create {
		verb = "CREATE USER "
	}
	_, err = m.databaseQuery(ctx, i, verb+databaseAccountSQL(input.User, input.Host)+" IDENTIFIED BY "+sqlPassword(input.Password)+";\n", true)
	return err
}

func (m *Manager) DropDatabaseAccount(ctx context.Context, i model.Instance, user, host string) error {
	if err := validateDatabaseAccount(user, host, true); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.databaseInstance(i)
	if err != nil {
		return err
	}
	_, err = m.databaseQuery(ctx, i, "DROP USER "+databaseAccountSQL(user, host)+";\n", false)
	return err
}

func (m *Manager) DatabaseAccountGrants(ctx context.Context, i model.Instance, user, host string) ([]string, error) {
	if err := validateDatabaseAccount(user, host, false); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.databaseInstance(i)
	if err != nil {
		return nil, err
	}
	out, err := m.databaseQuery(ctx, i, "SHOW GRANTS FOR "+databaseAccountSQL(user, host)+";\n", false)
	if err != nil {
		return nil, err
	}
	result := []string{}
	for _, line := range strings.Split(strings.TrimRight(out, "\r\n"), "\n") {
		if line != "" {
			result = append(result, strings.TrimSuffix(line, "\r"))
		}
	}
	return result, nil
}

func (m *Manager) GrantDatabasePrivileges(ctx context.Context, i model.Instance, input model.DatabasePrivilegeInput) error {
	return m.changeDatabasePrivileges(ctx, i, input, "GRANT")
}

func (m *Manager) RevokeDatabasePrivileges(ctx context.Context, i model.Instance, input model.DatabasePrivilegeInput) error {
	return m.changeDatabasePrivileges(ctx, i, input, "REVOKE")
}

func (m *Manager) changeDatabasePrivileges(ctx context.Context, i model.Instance, input model.DatabasePrivilegeInput, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.databaseInstance(i)
	if err != nil {
		return err
	}
	sql, err := m.grantSQL(ctx, i, input, action)
	if err != nil {
		return err
	}
	_, err = m.databaseQuery(ctx, i, sql, false)
	return err
}

func (m *Manager) grantSQL(ctx context.Context, i model.Instance, input model.DatabasePrivilegeInput, action string) (string, error) {
	partial := false
	if strings.HasPrefix(i.PackageID, "mysql-8.") {
		mode, err := m.databaseQuery(ctx, i, "SELECT @@GLOBAL.partial_revokes;\n", false)
		if err != nil {
			return "", err
		}
		switch strings.TrimSpace(mode) {
		case "0":
		case "1":
			partial = true
		default:
			return "", errors.New("无法确认 MySQL partial_revokes 配置，已拒绝权限修改")
		}
	}
	return databasePrivilegeSQL(action, input, partial)
}

func validateSQLFile(ctx context.Context, file *os.File) (int64, error) {
	reader := bufio.NewReaderSize(file, 64<<10)
	var offset int64
	var checked int64
	for {
		if checked >= 64<<10 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			checked = 0
		}
		value, size, err := reader.ReadRune()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, errors.New("无法读取 SQL 文件")
		}
		if value == utf8.RuneError && size == 1 {
			return 0, errors.New("SQL 文件不是有效 UTF-8，请先转换编码后再导入")
		}
		if offset == 0 && value == '\ufeff' {
			offset = int64(size)
		} else if offset == 0 {
			offset = -1
		}
		checked += int64(size)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if offset < 0 {
		offset = 0
	}
	return offset, nil
}

type databaseImportReader struct {
	reader       io.Reader
	done         int64
	total        int64
	progress     func(int64, int64)
	lastProgress time.Time
}

func (r *databaseImportReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.done += int64(n)
	if n != 0 && r.progress != nil && time.Since(r.lastProgress) >= 200*time.Millisecond {
		r.progress(r.done, r.total)
		r.lastProgress = time.Now()
	}
	return n, err
}

func (m *Manager) ImportDatabase(ctx context.Context, i model.Instance, database, path string, progress func(int64, int64)) error {
	if err := validateDatabaseName(database); err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		return errors.New("SQL 文件必须使用绝对路径")
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("无法打开所选 SQL 文件")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("请选择非空的普通 SQL 文件")
	}
	offset, err := validateSQLFile(ctx, file)
	if err != nil {
		return err
	}
	if offset >= info.Size() {
		return errors.New("SQL 文件没有可导入的内容")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err = m.databaseInstance(i)
	if err != nil {
		return err
	}
	if progress != nil {
		progress(0, info.Size()-offset)
	}
	reader := &databaseImportReader{reader: io.NewSectionReader(file, offset, info.Size()-offset), total: info.Size() - offset, progress: progress, lastProgress: time.Now()}
	args := []string{"--batch", "--binary-mode", "--default-character-set=utf8mb4", "--connect-timeout=10"}
	// MySQL's Windows clients do not reliably preserve Unicode in argv. Send
	// the initial database as a quoted UTF-8 SQL identifier through stdin.
	input := io.MultiReader(strings.NewReader("USE "+databaseIdentifier(database)+";\n"), reader)
	if err = m.databaseCommand(ctx, i, "mysql.exe", args, input, io.Discard, false); err != nil {
		return fmt.Errorf("导入失败，部分 SQL 可能已经执行，请检查数据库后再决定是否重试：%w", err)
	}
	if progress != nil {
		progress(reader.total, reader.total)
	}
	return nil
}

func databaseDumpOptions(packageID string) ([]string, error) {
	if !databasePackagePattern.MatchString(packageID) {
		return nil, errors.New("数据库管理仅支持已安装的 MySQL 5.7、8.0 或 8.4 实例")
	}
	args := []string{"--single-transaction", "--routines", "--events", "--triggers", "--hex-blob", "--no-tablespaces", "--set-gtid-purged=OFF", "--default-character-set=utf8mb4"}
	if strings.HasPrefix(packageID, "mysql-8.") {
		args = append(args, "--column-statistics=0")
	}
	return args, nil
}

func publishDatabaseExport(temporary, destination string) error {
	if err := moveDatabaseExport(temporary, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("导出目标文件已存在，请选择新的文件名")
		}
		return errors.New("无法发布导出文件，请检查目标目录权限")
	}
	return nil
}

func (m *Manager) ExportDatabase(ctx context.Context, i model.Instance, database, path string) error {
	if err := validateDatabaseName(database); err != nil {
		return err
	}
	if strings.ContainsFunc(database, func(r rune) bool { return r > 127 }) {
		return errors.New("Windows MySQL 命令行客户端无法正确接收非 ASCII 数据库名，请使用支持 Unicode 的外部客户端导出此数据库")
	}
	if !filepath.IsAbs(path) {
		return errors.New("导出文件必须使用绝对路径")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("导出目标文件已存在，请选择新的文件名")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("无法检查导出目标文件")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.databaseInstance(i)
	if err != nil {
		return err
	}
	args, err := databaseDumpOptions(i.PackageID)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".phptools-export-*.sql")
	if err != nil {
		return errors.New("无法在目标目录创建临时导出文件")
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	defer file.Close()
	if err = protectPath(temporary, false); err != nil {
		return errors.New("无法保护临时数据库导出文件")
	}
	// A valid existing database name can start with '-'. End option parsing
	// explicitly so it cannot be interpreted as a mysqldump flag.
	args = append(args, "--", database)
	if err = m.databaseCommand(ctx, i, "mysqldump.exe", args, nil, file, false); err != nil {
		return fmt.Errorf("数据库导出失败，未发布目标文件：%w", err)
	}
	if err = file.Sync(); err != nil {
		return errors.New("无法完整写入数据库导出文件，未发布目标文件")
	}
	if err = file.Close(); err != nil {
		return errors.New("无法关闭数据库导出文件，未发布目标文件")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return publishDatabaseExport(temporary, path)
}
