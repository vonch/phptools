package services

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"phptools/internal/model"
)

func TestDatabaseNamesAndAccountsRejectInvalidAndProtectedTargets(t *testing.T) {
	for _, name := range []string{"shop", "shop_2026", "123", strings.Repeat("a", 64), "中文", strings.Repeat("库", 64), "shop-test", "空 格", "a%b", "a`b", "a;DROP DATABASE x", "--help", "a.b", "hcgl.cc", "pzhscg.cc"} {
		if err := validateDatabaseName(name); err != nil {
			t.Fatalf("valid database %q: %v", name, err)
		}
	}
	for _, name := range []string{"", "mysql", "MYSQL", "sys", "information_schema", "performance_schema", ".", "..", "a\\b", "a/b", "../hcgl.cc", "a\x00b", "a\nb", "a\u007fb", "a ", string([]byte{0xff}), strings.Repeat("a", 65), strings.Repeat("库", 65)} {
		if err := validateDatabaseName(name); err == nil {
			t.Fatalf("accepted invalid/protected database %q", name)
		}
	}
	for _, name := range []string{"shop", "shop_2026", "123", strings.Repeat("a", 64)} {
		if err := validateNewDatabaseName(name); err != nil {
			t.Fatalf("valid new database %q: %v", name, err)
		}
	}
	for _, name := range []string{"中文", "shop-test", "a%b", "a`b", "a'b", "a;DROP DATABASE x", "空 格", "mysql", "a.b", "hcgl.cc", "pzhscg.cc"} {
		if err := validateNewDatabaseName(name); err == nil {
			t.Fatalf("creation should retain the narrow UI rule for %q", name)
		}
	}
	for _, user := range []string{"", "root", "ROOT", "mysql.session", "MYSQL.sys"} {
		if err := validateDatabaseAccount(user, "localhost", true); err == nil {
			t.Fatalf("accepted modifying protected user %q", user)
		}
		if err := validateDatabaseAccount(user, "localhost", false); err != nil {
			t.Fatalf("protected user's grants should be readable: %v", err)
		}
	}
	for _, pair := range [][2]string{{"app_user", "localhost"}, {"app-user", "127.0.0.1"}, {"app.user", "%"}, {"app", "::1"}, {"app", "192.168.%"}} {
		if err := validateDatabaseAccount(pair[0], pair[1], true); err != nil {
			t.Fatalf("valid account %v: %v", pair, err)
		}
	}
	for _, pair := range [][2]string{{"user' OR '1'='1", "localhost"}, {"user", "host'"}, {"user", ""}, {"user", "host\n"}, {strings.Repeat("a", 33), "%"}, {"user", strings.Repeat("a", 256)}} {
		if err := validateDatabaseAccount(pair[0], pair[1], true); err == nil {
			t.Fatalf("accepted unsafe account %v", pair)
		}
	}
}

func TestExistingDatabaseNamesRemainOneQuotedSQLIdentifier(t *testing.T) {
	name := "中文-空 格%_`; DROP DATABASE prod; --'"
	if err := validateDatabaseName(name); err != nil {
		t.Fatal(err)
	}
	if got := databaseIdentifier(name); got != "`中文-空 格%_``; DROP DATABASE prod; --'`" {
		t.Fatalf("identifier escaped quoting: %s", got)
	}
	input := model.DatabasePrivilegeInput{User: "app", Host: "localhost", Database: name, Privileges: []string{"SELECT"}}
	grant, err := databasePrivilegeSQL("GRANT", input, false)
	want := "GRANT SELECT ON `中文-空 格\\%\\_``; DROP DATABASE prod; --'`.* TO 'app'@'localhost';\n"
	if err != nil || grant != want {
		t.Fatalf("wildcard grant escaped quoted target: %q %v", grant, err)
	}
	revoke, err := databasePrivilegeSQL("REVOKE", input, true)
	want = "REVOKE SELECT ON `中文-空 格%_``; DROP DATABASE prod; --'`.* FROM 'app'@'localhost';\n"
	if err != nil || revoke != want {
		t.Fatalf("literal revoke escaped quoted target: %q %v", revoke, err)
	}
}

func TestExistingDottedDatabaseNamesRemainOneQuotedPrivilegeTarget(t *testing.T) {
	for _, name := range []string{"hcgl.cc", "pzhscg.cc"} {
		if got := databaseIdentifier(name); got != "`"+name+"`" {
			t.Fatalf("dotted database became a qualified identifier: %s", got)
		}
		input := model.DatabasePrivilegeInput{User: "app", Host: "localhost", Database: name, Privileges: []string{"SELECT"}}
		for _, partialRevokes := range []bool{false, true} {
			grant, err := databasePrivilegeSQL("GRANT", input, partialRevokes)
			want := "GRANT SELECT ON `" + name + "`.* TO 'app'@'localhost';\n"
			if err != nil || grant != want {
				t.Fatalf("dotted database grant changed target: %q %v", grant, err)
			}
			revoke, err := databasePrivilegeSQL("REVOKE", input, partialRevokes)
			want = "REVOKE SELECT ON `" + name + "`.* FROM 'app'@'localhost';\n"
			if err != nil || revoke != want {
				t.Fatalf("dotted database revoke changed target: %q %v", revoke, err)
			}
		}
	}
}

func TestExistingUnicodeAccountGrantsUseEscapedSQLLiterals(t *testing.T) {
	user := "业务'@'管理员"
	host := "127.0.0.0/255.255.0.0"
	if err := validateDatabaseAccount(user, host, false); err != nil {
		t.Fatalf("existing account grants should be readable: %v", err)
	}
	if got := databaseAccountSQL(user, host); got != "'业务''@''管理员'@'127.0.0.0/255.255.0.0'" {
		t.Fatalf("account escaped string literal: %s", got)
	}
	if err := validateDatabaseAccount(user, host, true); err == nil {
		t.Fatal("modification should retain strict validation")
	}
	for _, pair := range [][2]string{{"", ""}, {"中文", "localhost"}, {"user'; DROP USER root; --", "%"}} {
		if err := validateDatabaseAccount(pair[0], pair[1], false); err != nil {
			t.Fatalf("valid existing account is unreadable: %v", err)
		}
	}
	for _, pair := range [][2]string{{"user\x00", "localhost"}, {"user", "host\n"}, {strings.Repeat("用", 33), "localhost"}, {string([]byte{0xff}), "localhost"}} {
		if err := validateDatabaseAccount(pair[0], pair[1], false); err == nil {
			t.Fatalf("accepted malformed account %v", pair)
		}
	}
}

func TestDatabaseExportExplainsWindowsUnicodeClientLimitation(t *testing.T) {
	m := New(t.TempDir(), nil, func(string) (string, error) {
		t.Fatal("unsupported export read credentials")
		return "", nil
	})
	for _, name := range []string{"中文", "café", "test_数据库"} {
		err := m.ExportDatabase(context.Background(), model.Instance{}, name, filepath.Join(t.TempDir(), "export.sql"))
		if err == nil || !strings.Contains(err.Error(), "Windows MySQL") || !strings.Contains(err.Error(), "Unicode") {
			t.Fatalf("missing actionable Unicode export restriction for %q: %v", name, err)
		}
	}
}

func TestDatabasePrivilegesQuoteExactDatabaseAndRevokeOnlySelection(t *testing.T) {
	input := model.DatabasePrivilegeInput{User: "app", Host: "127.0.0.1", Database: "shop_test", Privileges: []string{"select", "UPDATE", "select"}}
	grant, err := databasePrivilegeSQL("GRANT", input, false)
	if err != nil {
		t.Fatal(err)
	}
	if grant != "GRANT SELECT, UPDATE ON `shop\\_test`.* TO 'app'@'127.0.0.1';\n" {
		t.Fatalf("grant escaped into a wildcard: %s", grant)
	}
	literal, err := databasePrivilegeSQL("GRANT", input, true)
	if err != nil || literal != "GRANT SELECT, UPDATE ON `shop_test`.* TO 'app'@'127.0.0.1';\n" {
		t.Fatalf("partial_revokes should use literal database name: %q %v", literal, err)
	}
	input.Privileges = []string{"INSERT"}
	revoke, err := databasePrivilegeSQL("REVOKE", input, false)
	if err != nil || revoke != "REVOKE INSERT ON `shop\\_test`.* FROM 'app'@'127.0.0.1';\n" {
		t.Fatalf("revoke affects unselected privileges: %q %v", revoke, err)
	}
	for _, permissions := range [][]string{nil, {"ALL PRIVILEGES"}, {"SUPER"}, {"SELECT; DROP USER root"}, {"GRANT OPTION"}} {
		input.Privileges = permissions
		if _, err := databasePrivilegeSQL("GRANT", input, false); err == nil {
			t.Fatalf("accepted unsafe privileges %v", permissions)
		}
	}
	input.Privileges = []string{"SELECT"}
	input.User = "root"
	if _, err := databasePrivilegeSQL("REVOKE", input, false); err == nil {
		t.Fatal("accepted removing protected root privileges")
	}
}

func TestDatabaseListPreservesEncodedNamesAndRejectsMalformedResults(t *testing.T) {
	h := func(value string) string { return hex.EncodeToString([]byte(value)) }
	line := h("shop\tname\n") + "\t" + h("utf8mb4") + "\t" + h("utf8mb4_unicode_ci") + "\t2\t4096\r\n"
	line += h("mysql") + "\t" + h("utf8mb4") + "\t" + h("utf8mb4_general_ci") + "\t20\t8192\n"
	rows, err := parseDatabaseList(line)
	if err != nil || len(rows) != 2 {
		t.Fatalf("parse: %+v %v", rows, err)
	}
	if rows[0].Name != "shop\tname\n" || rows[0].Tables != 2 || rows[0].SizeBytes != 4096 || rows[0].System || !rows[1].System {
		t.Fatalf("unexpected parsed information: %+v", rows)
	}
	for _, output := range []string{"a\tb", "GG\t61\t61\t0\t0", "61\t61\t61\t-1\t0", "61\t61\t61\t1\tx", "FF\t61\t61\t0\t0"} {
		if _, err := parseDatabaseList(output); err == nil {
			t.Fatalf("accepted malformed output %q", output)
		}
	}
}

func TestDatabaseClientOutputBoundedAndSecretsRedacted(t *testing.T) {
	output := &databaseOutput{limit: 8}
	if n, err := output.Write([]byte("abcdef")); n != 6 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := output.Write([]byte("ghijkl")); n != 6 || err != nil || output.String() != "abcdefgh" || !output.overflow {
		t.Fatalf("unbounded/incomplete output: %+v %d %v", output, n, err)
	}
	password := `root'\secret`
	message := "bad " + password + " and " + strings.Trim(sqlPassword(password), "'")
	err := databaseCommandError(context.Background(), message, []string{password}, false)
	if strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("password leaked: %s", err)
	}
	err = databaseCommandError(context.Background(), "server echoed brand-new-password", nil, true)
	if strings.Contains(err.Error(), "brand-new-password") {
		t.Fatalf("new account password leaked: %s", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = databaseCommandError(ctx, "anything", nil, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestDatabaseDumpOptionsMatchSupportedServerVersion(t *testing.T) {
	for _, packageID := range []string{"mysql-5.7.44-x64", "mysql-8.0.44-x64", "mysql-8.4.9-x64"} {
		args, err := databaseDumpOptions(packageID)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(args, " ")
		for _, flag := range []string{"--single-transaction", "--routines", "--events", "--triggers", "--hex-blob", "--no-tablespaces", "--set-gtid-purged=OFF"} {
			if !strings.Contains(joined, flag) {
				t.Fatalf("%s missing %s", packageID, flag)
			}
		}
		if strings.Contains(joined, "--column-statistics=0") != strings.HasPrefix(packageID, "mysql-8.") {
			t.Fatalf("wrong version-specific flags: %s %v", packageID, args)
		}
	}
	for _, packageID := range []string{"mysql-fixture", "mysql-5.6.50-x64", "mysql-8.1.0-x64", "mysql-8.4.9-x64 --bad", "php-8.0.44-x64"} {
		if _, err := databaseDumpOptions(packageID); err == nil {
			t.Fatalf("accepted unsupported server %q", packageID)
		}
	}
}

func TestSQLImportEncodingCheckedBeforeExecution(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		offset  int64
		valid   bool
	}{
		{"plain", "CREATE TABLE t(id INT);\n", 0, true},
		{"BOM", "\ufeffDELIMITER $$\nSELECT '中文';$$\n", 3, true},
		{"boundary", strings.Repeat("a", (64<<10)-1) + "中文", 0, true},
		{"replacement rune", "SELECT '�';", 0, true},
		{"invalid UTF8", "SELECT '" + string([]byte{0xff}) + "';", 0, false},
		{"UTF16", string([]byte{0xff, 0xfe, 'S', 0}), 0, false},
		{"truncated UTF8", "SELECT '" + string([]byte{0xe4, 0xb8}), 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "*.sql")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err = file.WriteString(test.content); err != nil {
				t.Fatal(err)
			}
			if _, err = file.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			offset, err := validateSQLFile(context.Background(), file)
			if (err == nil) != test.valid || (test.valid && offset != test.offset) {
				t.Fatalf("offset=%d error=%v", offset, err)
			}
		})
	}
}

func TestDatabaseExportPublishesCompleteFileWithoutReplacingExisting(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("database export publication requires Windows")
	}
	dir := t.TempDir()
	temporary := filepath.Join(dir, ".partial.sql")
	destination := filepath.Join(dir, "backup.sql")
	if err := os.WriteFile(temporary, []byte("complete dump"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishDatabaseExport(temporary, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("published temporary file was not moved")
	}
	content, err := os.ReadFile(destination)
	if err != nil || string(content) != "complete dump" {
		t.Fatalf("published incomplete output %q: %v", content, err)
	}
	if err = os.WriteFile(temporary, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = publishDatabaseExport(temporary, destination); err == nil {
		t.Fatal("existing destination replaced")
	}
	content, _ = os.ReadFile(destination)
	if string(content) != "complete dump" {
		t.Fatal("existing backup modified")
	}
	missing := filepath.Join(dir, "failed.sql")
	if err = publishDatabaseExport(filepath.Join(dir, "absent.sql"), missing); err == nil {
		t.Fatal("published nonexistent export")
	}
	if _, err = os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed export left a destination file")
	}
}

func TestDatabaseImportProgressIsThrottled(t *testing.T) {
	calls := 0
	var reported int64
	reader := &databaseImportReader{reader: strings.NewReader(strings.Repeat("a", 100)), total: 100, lastProgress: time.Now(), progress: func(done, total int64) {
		calls++
		reported = done
		if total != 100 {
			t.Fatalf("incorrect total %d", total)
		}
	}}
	buffer := make([]byte, 10)
	for range 5 {
		if _, err := reader.Read(buffer); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 0 {
		t.Fatal("progress emitted repeatedly within 200 ms")
	}
	reader.lastProgress = time.Now().Add(-time.Second)
	if _, err := reader.Read(buffer); err != nil || calls != 1 || reported != 60 {
		t.Fatalf("progress failed to update after interval: %d %d %v", calls, reported, err)
	}
}

func TestDatabaseOperationsRejectUnownedInstanceBeforeReadingCredentials(t *testing.T) {
	root := t.TempDir()
	m := New(root, nil, func(string) (string, error) {
		t.Fatal("operation read a password before verifying the server")
		return "", nil
	})
	dir := filepath.Join(root, "instances", "unowned")
	i := model.Instance{ID: "unowned", Kind: "mysql", PackageID: "mysql-5.7.44-x64", Port: 3307,
		Executable: filepath.Join(root, "packages", "mysql-5.7.44-x64", "bin", "mysqld.exe"),
		ConfigPath: filepath.Join(dir, "conf", "my.ini"), DataDir: filepath.Join(dir, "data"), LogPath: filepath.Join(dir, "logs", "error.log"),
		PID: os.Getpid(), State: "running"}
	ctx := context.Background()
	checks := []struct {
		name string
		call func() error
	}{
		{"list databases", func() error { _, err := m.ListDatabases(ctx, i); return err }},
		{"create database", func() error { return m.CreateDatabase(ctx, i, "shop", "password123") }},
		{"drop database", func() error { return m.DropDatabase(ctx, i, "shop") }},
		{"list accounts", func() error { _, err := m.ListDatabaseAccounts(ctx, i); return err }},
		{"create account", func() error {
			return m.CreateDatabaseAccount(ctx, i, model.DatabaseAccountInput{User: "app", Host: "localhost", Password: "password123"})
		}},
		{"change account", func() error {
			return m.ChangeDatabaseAccountPassword(ctx, i, model.DatabaseAccountInput{User: "app", Host: "localhost", Password: "password123"})
		}},
		{"drop account", func() error { return m.DropDatabaseAccount(ctx, i, "app", "localhost") }},
		{"read grants", func() error { _, err := m.DatabaseAccountGrants(ctx, i, "root", "localhost"); return err }},
		{"grant", func() error {
			return m.GrantDatabasePrivileges(ctx, i, model.DatabasePrivilegeInput{User: "app", Host: "localhost", Database: "shop", Privileges: []string{"SELECT"}})
		}},
		{"revoke", func() error {
			return m.RevokeDatabasePrivileges(ctx, i, model.DatabasePrivilegeInput{User: "app", Host: "localhost", Database: "shop", Privileges: []string{"SELECT"}})
		}},
		{"export", func() error { return m.ExportDatabase(ctx, i, "shop", filepath.Join(root, "backup.sql")) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); err == nil {
				t.Fatal("accepted unowned server")
			}
		})
	}
	source := filepath.Join(root, "source.sql")
	if err := os.WriteFile(source, []byte("SELECT 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.ImportDatabase(ctx, i, "shop", source, nil); err == nil {
		t.Fatal("import accepted unowned server")
	}
	i.Executable = filepath.Join(t.TempDir(), "mysqld.exe")
	if _, err := m.databaseInstance(i); err == nil {
		t.Fatal("accepted executable outside managed packages")
	}
}

func TestSchemaDirectoryListsLocalMetadataWithoutServer(t *testing.T) {
	root := t.TempDir()
	shop := filepath.Join(root, "shop_db")
	if err := os.Mkdir(shop, 0700); err != nil {
		t.Fatal(err)
	}
	opt := "default-character-set=utf8mb4\ndefault-collation=utf8mb4_unicode_ci\n"
	if err := os.WriteFile(filepath.Join(shop, "db.opt"), []byte(opt), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"items.frm", "items.ibd", "audit.frm"} {
		if err := os.WriteFile(filepath.Join(shop, name), []byte("table"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mysqlDir := filepath.Join(root, "mysql")
	if err := os.Mkdir(mysqlDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mysqlDir, "db.opt"), []byte("default-character-set=latin1\ndefault-collation=latin1_swedish_ci\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "#innodb_temp"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "auto.cnf"), []byte("server"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := readSchemaDirectory(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Name != "mysql" || !rows[0].System || rows[1].Name != "shop_db" || rows[1].System {
		t.Fatalf("unexpected schemas: %+v", rows)
	}
	if rows[1].Charset != "utf8mb4" || rows[1].Collation != "utf8mb4_unicode_ci" || rows[1].Tables != 2 || rows[1].SizeBytes != int64(len(opt)+5+5+5) {
		t.Fatalf("shop metadata: %+v", rows[1])
	}
	mysql8, err := readSchemaDirectory(root, true)
	if err != nil || mysql8[1].Tables != 1 {
		t.Fatalf("mysql 8 table count: %+v %v", mysql8, err)
	}
	if err = os.Symlink(shop, filepath.Join(root, "linked_db")); err == nil {
		rows, err = readSchemaDirectory(root, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Name == "linked_db" {
				t.Fatal("directory link was listed as a database")
			}
		}
	}
}
