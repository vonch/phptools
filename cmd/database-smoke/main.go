//go:build windows

// Command database-smoke verifies database management against an isolated MySQL.
// It only reuses PHPTools-owned official packages and never adopts existing data.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zalando/go-keyring"
	"phptools/internal/acceptance"
	"phptools/internal/desktop"
	"phptools/internal/model"
	"phptools/internal/packages"
	"phptools/internal/services"
	"phptools/internal/store"
)

type report struct {
	Root      string   `json:"root"`
	Version   string   `json:"version"`
	Started   string   `json:"started"`
	Completed string   `json:"completed"`
	Checks    []string `json:"checks"`
	Error     string   `json:"error,omitempty"`
}

func main() {
	root := flag.String("root", "", "new runtime directory; defaults to a system temporary directory and is removed after the run")
	pkg := flag.String("mysql-package", "", "required PHPTools-owned installed package directory containing .phptools-package.json")
	version := flag.String("version", "", "required exact MySQL version, e.g. 5.7.44")
	reportPath := flag.String("report", filepath.Join("build", "qa", "database-acceptance.json"), "JSON report output path outside the temporary runtime")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, *root, *pkg, *version, *reportPath); err != nil {
		fmt.Fprintln(os.Stderr, "DATABASE ACCEPTANCE FAILED:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, root, source, version, reportPath string) (runErr error) {
	if (root != "" && !filepath.IsAbs(root)) || !filepath.IsAbs(source) || version == "" {
		return errors.New("-mysql-package and -version are required; supplied runtime and package paths must be absolute")
	}
	source = filepath.Clean(source)
	p, err := readOwnedPackage(source, version)
	if err != nil {
		return err
	}
	reportPath, err = filepath.Abs(reportPath)
	if err != nil {
		return err
	}
	if rel, relErr := filepath.Rel(source, reportPath); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return errors.New("acceptance report must be outside the read-only source package")
	}
	testRoot, err := acceptance.NewRoot(root, "database")
	if err != nil {
		return err
	}
	root = testRoot.Path
	if rel, relErr := filepath.Rel(root, reportPath); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return errors.Join(errors.New("acceptance report must be outside the temporary runtime"), testRoot.Cleanup())
	}
	r := report{Root: root, Version: version, Started: model.Now(), Checks: []string{}}
	defer func() {
		runErr = errors.Join(runErr, testRoot.Cleanup())
		r.Completed = model.Now()
		if runErr != nil {
			r.Error = runErr.Error()
		}
		data, err := json.MarshalIndent(r, "", "  ")
		if err == nil {
			err = os.MkdirAll(filepath.Dir(reportPath), 0700)
		}
		if err == nil {
			err = os.WriteFile(reportPath, data, 0600)
		}
		runErr = errors.Join(runErr, err)
	}()
	for _, pair := range [][2]string{{source, root}, {root, source}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return errors.New("acceptance root and source package must not overlap")
		}
	}
	db, err := store.Open(root)
	if err != nil {
		return err
	}
	root = db.Root
	defer func() {
		if db != nil {
			runErr = errors.Join(runErr, db.Close())
		}
	}()
	record := func(name string) {
		r.Checks = append(r.Checks, name)
		fmt.Println("PASS", name)
	}
	p, err = copyOwnedPackage(ctx, root, source, p)
	if err == nil {
		p, err = packages.Install(ctx, root, p, nil)
	}
	if err == nil {
		err = db.Put("package", p.ID, p)
	}
	closeErr := db.Close()
	db = nil
	if err = errors.Join(err, closeErr); err != nil {
		return err
	}
	record("official managed package recovered and registered without download")
	toolLaunches := &toolLaunchRecorder{}
	a, err := desktop.New(filepath.Join(root, "acceptance-profile"), desktop.Hooks{
		OpenDatabaseClient: toolLaunches.openClient,
		OpenURL:            toolLaunches.openURL,
	})
	if err != nil {
		return err
	}
	var credentialID string
	defer func() {
		quitErr := a.Quit()
		runErr = errors.Join(runErr, quitErr)
		if quitErr == nil && credentialID != "" {
			digest := sha256.Sum256([]byte(strings.ToLower(root)))
			service := "PHPTools/" + hex.EncodeToString(digest[:16])
			if err := keyring.Delete(service, credentialID); err != nil && !errors.Is(err, keyring.ErrNotFound) {
				runErr = errors.Join(runErr, fmt.Errorf("remove temporary MySQL credential: %w", err))
			}
		}
	}()
	if err = a.SetRoot(root); err != nil {
		return err
	}
	password := "DbSmoke!" + rand.Text()
	port, err := freePort()
	if err != nil {
		return err
	}
	i, err := a.CreateInstance(model.InstanceInput{Name: "Database acceptance " + version, PackageID: p.ID, Port: port, Password: password})
	credentialID = i.ID
	if err != nil {
		return err
	}
	id, err := a.StartInstance(i.ID)
	if err != nil {
		return err
	}
	if _, err = completedTask(ctx, a, id, "completed"); err != nil {
		return err
	}
	state, err := a.GetState()
	if err != nil {
		return err
	}
	for _, instance := range state.Instances {
		if instance.ID == i.ID {
			i = instance
		}
	}
	record("desktop creates and starts isolated MySQL instance")
	if err = exercise(ctx, a, i, password, record); err != nil {
		return err
	}
	return exerciseTools(a, i, toolLaunches, record)
}

func readOwnedPackage(source, version string) (model.Package, error) {
	var p model.Package
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), source) {
		return p, errors.New("source package path must not contain symlinks or junctions")
	}
	data, err := os.ReadFile(filepath.Join(source, ".phptools-package.json"))
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(data, &p); err != nil {
		return p, err
	}
	if p.Kind != "mysql" || p.Version != version || p.ID != "mysql-"+version+"-x64" || !p.Verified || p.InstalledAt == "" {
		return p, errors.New("source must be a completed PHPTools MySQL package matching -version")
	}
	area := filepath.Dir(source)
	runtime := filepath.Dir(area)
	areaMarker, err := os.ReadFile(filepath.Join(area, ".phptools-packages"))
	if err != nil || string(areaMarker) != "PHPTools managed packages v1\n" {
		return p, errors.New("source is outside a PHPTools-owned package area")
	}
	runtimeMarker, err := os.ReadFile(filepath.Join(runtime, ".phptools-runtime"))
	if err != nil || string(runtimeMarker) != "PHPTools runtime v1\n" {
		return p, errors.New("source is outside a PHPTools-owned runtime")
	}
	rel, err := filepath.Rel(source, p.Path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return p, errors.New("source package path escapes its owned directory")
	}
	return p, nil
}

func copyOwnedPackage(ctx context.Context, root, source string, p model.Package) (model.Package, error) {
	area := filepath.Join(root, "packages")
	if err := os.WriteFile(filepath.Join(area, ".phptools-packages"), []byte("PHPTools managed packages v1\n"), 0600); err != nil {
		return p, err
	}
	target := filepath.Join(area, p.ID)
	if err := os.Mkdir(target, 0700); err != nil {
		return p, err
	}
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("package source contains a symlink or junction")
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		dest := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.Mkdir(dest, 0700)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		return errors.Join(copyErr, out.Close())
	})
	if err != nil {
		return p, err
	}
	rel, err := filepath.Rel(source, p.Path)
	if err != nil {
		return p, err
	}
	p.Path = filepath.Join(target, rel)
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return p, err
	}
	err = os.WriteFile(filepath.Join(target, ".phptools-package.json"), data, 0600)
	return p, err
}

func exercise(ctx context.Context, a *desktop.App, i model.Instance, password string, record func(string)) error {
	const sourceDB, restoredDB, partialDB = "smoke_source", "smoke_restored", "smoke_partial"
	for _, name := range []string{sourceDB, restoredDB, partialDB, "smokeXsource"} {
		if err := a.CreateDatabase(i.ID, name, "Smoke-db-pass-1"); err != nil {
			return err
		}
	}
	list, err := a.ListDatabases(i.ID)
	if err != nil {
		return err
	}
	if info, ok := findDatabase(list, sourceDB); !ok || info.Tables != 0 || info.System || info.Charset != "utf8mb4" {
		return fmt.Errorf("unexpected newly created database: %+v", info)
	}
	record("desktop creates and lists utf8mb4 database with zero tables")
	for _, name := range []string{"mysql", "sys", "performance_schema", "information_schema"} {
		if a.DropDatabase(i.ID, name) == nil {
			return fmt.Errorf("system database %s was not protected", name)
		}
	}
	if a.DropDatabaseAccount(i.ID, "root", "localhost") == nil || a.ChangeDatabaseAccountPassword(i.ID, model.DatabaseAccountInput{User: "root", Host: "localhost", Password: "UnexpectedChange123!"}) == nil {
		return errors.New("root account was not protected")
	}
	record("desktop protects all system databases and root account")
	backups := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(i.DataDir))), "backups")
	fixture := filepath.Join(backups, "fixture.sql")
	if err = os.WriteFile(fixture, []byte(fixtureSQL), 0600); err != nil {
		return err
	}
	taskID, err := a.ImportDatabase(i.ID, sourceDB, fixture)
	if err != nil {
		return err
	}
	if _, err = completedTask(ctx, a, taskID, "completed"); err != nil {
		return err
	}
	if err = queryEquals(ctx, i, password, "USE `"+sourceDB+"`; SELECT COUNT(*) FROM items; SELECT COUNT(*) FROM audit; SELECT note_count();\n", "3\n1\n3"); err != nil {
		return err
	}
	list, err = a.ListDatabases(i.ID)
	if err != nil {
		return err
	}
	if info, ok := findDatabase(list, sourceDB); !ok || info.Tables != 2 {
		return fmt.Errorf("table count after import is %d, expected 2", info.Tables)
	}
	record("desktop import completes DELIMITER procedure/function/trigger and reports two tables")
	dump := filepath.Join(backups, "roundtrip.sql")
	taskID, err = a.ExportDatabase(i.ID, sourceDB, dump)
	if err != nil {
		return err
	}
	if _, err = completedTask(ctx, a, taskID, "completed"); err != nil {
		return err
	}
	if stat, err := os.Stat(dump); err != nil || stat.Size() == 0 {
		return errors.New("completed export has no SQL content")
	}
	original, err := os.ReadFile(dump)
	if err != nil {
		return err
	}
	taskID, err = a.ExportDatabase(i.ID, sourceDB, dump)
	if err != nil {
		return err
	}
	if _, err = completedTask(ctx, a, taskID, "failed"); err != nil {
		return err
	}
	unchanged, err := os.ReadFile(dump)
	if err != nil || string(original) != string(unchanged) {
		return errors.New("refused duplicate export modified the existing destination")
	}
	record("desktop refuses existing export destination and preserves its contents")
	taskID, err = a.ImportDatabase(i.ID, restoredDB, dump)
	if err != nil {
		return err
	}
	if _, err = completedTask(ctx, a, taskID, "completed"); err != nil {
		return err
	}
	if err = queryEquals(ctx, i, password, "USE `"+restoredDB+"`; CALL add_item('restored'); SELECT COUNT(*) FROM items; SELECT COUNT(*) FROM audit; SELECT note_count();\n", "4\n2\n4"); err != nil {
		return err
	}
	if err = queryEquals(ctx, i, password, "SELECT COUNT(*) FROM information_schema.EVENTS WHERE EVENT_SCHEMA='"+restoredDB+"' AND EVENT_NAME='smoke_event'; SELECT COUNT(*) FROM `"+restoredDB+"`.items WHERE note='中文与分号; text';\n", "1\n1"); err != nil {
		return err
	}
	record("desktop export and restore preserve Unicode rows, procedure, function, trigger and event")
	if err = exerciseAccount(ctx, a, i, password, sourceDB, record); err != nil {
		return err
	}
	if err = exerciseExistingNames(ctx, a, i, password, sourceDB, backups, record); err != nil {
		return err
	}
	if strings.HasPrefix(i.PackageID, "mysql-8.4.") {
		if err = exercisePartialRevokes(ctx, a, i, password, sourceDB); err != nil {
			return err
		}
		record("MySQL 8.4 partial_revokes mode grants and revokes exact underscore database without sibling access")
	}
	badSQL := filepath.Join(backups, "partial.sql")
	if err = os.WriteFile(badSQL, []byte("CREATE TABLE partial_rows (id INT PRIMARY KEY);\nINSERT INTO partial_rows VALUES (1);\nTHIS IS INVALID SQL;\n"), 0600); err != nil {
		return err
	}
	taskID, err = a.ImportDatabase(i.ID, partialDB, badSQL)
	if err != nil {
		return err
	}
	if _, err = completedTask(ctx, a, taskID, "failed"); err != nil {
		return err
	}
	if err = queryEquals(ctx, i, password, "SELECT COUNT(*) FROM `"+partialDB+"`.partial_rows;\n", "1"); err != nil {
		return err
	}
	record("desktop failed SQL import is marked failed and previously committed rows remain")
	if err = exerciseCancelledExport(ctx, a, i, password, sourceDB, backups); err != nil {
		return err
	}
	record("desktop cancelled export removes temporary output and leaves no destination file")
	manager := services.New(filepath.Dir(backups), nil, func(id string) (string, error) {
		if id != i.ID {
			return "", errors.New("unexpected instance credential request")
		}
		return password, nil
	})
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	preCancelled := filepath.Join(backups, "precancelled.sql")
	if manager.ExportDatabase(cancelled, i, sourceDB, preCancelled) == nil {
		return errors.New("service accepted already-cancelled export")
	}
	if _, err = os.Stat(preCancelled); !errors.Is(err, os.ErrNotExist) {
		return errors.New("already-cancelled export created a destination")
	}
	record("service rejects pre-cancelled export without publishing output")
	for _, name := range []string{sourceDB, restoredDB, partialDB, "smokeXsource"} {
		if err = a.DropDatabase(i.ID, name); err != nil {
			return err
		}
	}
	list, err = a.ListDatabases(i.ID)
	if err != nil {
		return err
	}
	if _, ok := findDatabase(list, sourceDB); ok {
		return errors.New("dropped database is still listed")
	}
	record("desktop drops test databases and list reflects removal")
	return nil
}

func exerciseAccount(ctx context.Context, a *desktop.App, i model.Instance, adminPassword, database string, record func(string)) error {
	input := model.DatabaseAccountInput{User: "smoke_user", Host: "127.0.0.1", Password: "Account!" + rand.Text() + "'\\\""}
	if _, err := mysqlQuery(ctx, i, "root", adminPassword, "CREATE TABLE smokeXsource.items (id INT); INSERT INTO smokeXsource.items VALUES (1);\n"); err != nil {
		return err
	}
	if err := a.CreateDatabaseAccount(i.ID, input); err != nil {
		return err
	}
	accounts, err := a.ListDatabaseAccounts(i.ID)
	if err != nil {
		return err
	}
	found := false
	for _, account := range accounts {
		if account.User == input.User && account.Host == input.Host && !account.Protected {
			found = true
		}
	}
	if !found {
		return errors.New("created account is absent from desktop account list")
	}
	privilege := model.DatabasePrivilegeInput{User: input.User, Host: input.Host, Database: database, Privileges: []string{"SELECT", "INSERT"}}
	if err = a.GrantDatabasePrivileges(i.ID, privilege); err != nil {
		return err
	}
	grants, err := a.DatabaseAccountGrants(i.ID, input.User, input.Host)
	if err != nil {
		return err
	}
	if !strings.Contains(strings.Join(grants, "\n"), "SELECT") {
		return errors.New("desktop does not show granted SELECT privilege")
	}
	if _, err = mysqlQuery(ctx, i, input.User, input.Password, "SELECT COUNT(*) FROM `"+database+"`.items;\n"); err != nil {
		return err
	}
	if _, err = mysqlQuery(ctx, i, input.User, input.Password, "SELECT COUNT(*) FROM `smokeXsource`.items;\n"); err == nil {
		return errors.New("underscore in database grant matched a different database")
	}
	if _, err = mysqlQuery(ctx, i, input.User, input.Password, "INSERT INTO `"+database+"`.items(note) VALUES ('granted insert');\n"); err != nil {
		return err
	}
	privilege.Privileges = []string{"INSERT"}
	if err = a.RevokeDatabasePrivileges(i.ID, privilege); err != nil {
		return err
	}
	if _, err = mysqlQuery(ctx, i, input.User, input.Password, "INSERT INTO `"+database+"`.items(note) VALUES ('must fail');\n"); err == nil {
		return errors.New("account retained revoked INSERT privilege")
	}
	oldPassword := input.Password
	input.Password = "Changed!" + rand.Text() + "'\\"
	if err = a.ChangeDatabaseAccountPassword(i.ID, input); err != nil {
		return err
	}
	if _, err = mysqlQuery(ctx, i, input.User, oldPassword, "SELECT 1;\n"); err == nil {
		return errors.New("old account password still authenticates")
	}
	if _, err = mysqlQuery(ctx, i, input.User, input.Password, "SELECT COUNT(*) FROM `"+database+"`.items;\n"); err != nil {
		return err
	}
	if err = a.DropDatabaseAccount(i.ID, input.User, input.Host); err != nil {
		return err
	}
	if _, err = mysqlQuery(ctx, i, input.User, input.Password, "SELECT 1;\n"); err == nil {
		return errors.New("dropped account still authenticates")
	}
	accounts, err = a.ListDatabaseAccounts(i.ID)
	if err != nil {
		return err
	}
	for _, account := range accounts {
		if account.User == input.User && account.Host == input.Host {
			return errors.New("dropped account is still listed")
		}
	}
	record("desktop account create/list/grant/show/revoke/password change/drop match real authentication")
	return nil
}

func exercisePartialRevokes(ctx context.Context, a *desktop.App, i model.Instance, adminPassword, database string) (runErr error) {
	if _, err := mysqlQuery(ctx, i, "root", adminPassword, "SET GLOBAL partial_revokes=ON;\n"); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := mysqlQuery(cleanupCtx, i, "root", adminPassword, "SET GLOBAL partial_revokes=OFF;\n")
		runErr = errors.Join(runErr, err)
	}()
	input := model.DatabaseAccountInput{User: "smoke_partial_user", Host: "127.0.0.1", Password: "Partial!" + rand.Text()}
	if err := a.CreateDatabaseAccount(i.ID, input); err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, a.DropDatabaseAccount(i.ID, input.User, input.Host)) }()
	privilege := model.DatabasePrivilegeInput{User: input.User, Host: input.Host, Database: database, Privileges: []string{"SELECT"}}
	if err := a.GrantDatabasePrivileges(i.ID, privilege); err != nil {
		return err
	}
	if _, err := mysqlQuery(ctx, i, input.User, input.Password, "SELECT COUNT(*) FROM `"+database+"`.items;\n"); err != nil {
		return err
	}
	if _, err := mysqlQuery(ctx, i, input.User, input.Password, "SELECT COUNT(*) FROM smokeXsource.items;\n"); err == nil {
		return errors.New("partial_revokes mode granted access to sibling database")
	}
	if err := a.RevokeDatabasePrivileges(i.ID, privilege); err != nil {
		return err
	}
	if _, err := mysqlQuery(ctx, i, input.User, input.Password, "SELECT COUNT(*) FROM `"+database+"`.items;\n"); err == nil {
		return errors.New("partial_revokes mode retained revoked SELECT access")
	}
	return nil
}

func exerciseExistingNames(ctx context.Context, a *desktop.App, i model.Instance, adminPassword, importDatabase, backups string, record func(string)) error {
	cases := []struct {
		name, sibling string
		exportable    bool
	}{
		{"ascii-db space%`_", "ascii-db spaceX`Y", true},
		{"--help", "--helpSibling", true},
		{"中文-库 空格%`_", "中文-库 空格X`Y", false},
	}
	const restored = "smoke_existing_restored"
	identifier := func(value string) string { return "`" + strings.ReplaceAll(value, "`", "``") + "`" }
	var sql strings.Builder
	for _, item := range cases {
		for _, database := range []string{item.name, item.sibling} {
			quoted := identifier(database)
			fmt.Fprintf(&sql, "CREATE DATABASE %s CHARACTER SET utf8mb4; CREATE TABLE %s.items(id INT); INSERT INTO %s.items VALUES (1);\n", quoted, quoted, quoted)
		}
	}
	path := filepath.Join(backups, "existing-databases.sql")
	if err := os.WriteFile(path, []byte(sql.String()), 0600); err != nil {
		return err
	}
	id, err := a.ImportDatabase(i.ID, importDatabase, path)
	if err != nil {
		return err
	}
	if _, err = completedTask(ctx, a, id, "completed"); err != nil {
		return err
	}
	list, err := a.ListDatabases(i.ID)
	if err != nil {
		return err
	}
	path = filepath.Join(backups, "existing-more.sql")
	if err = os.WriteFile(path, []byte("INSERT INTO items VALUES (2);\n"), 0600); err != nil {
		return err
	}
	account := model.DatabaseAccountInput{User: "smoke_named_user", Host: "127.0.0.1", Password: "Named!" + rand.Text()}
	if err = a.CreateDatabaseAccount(i.ID, account); err != nil {
		return err
	}
	for index, item := range cases {
		if info, ok := findDatabase(list, item.name); !ok || info.Tables != 1 {
			return fmt.Errorf("imported database %q was not listed accurately", item.name)
		}
		id, err = a.ImportDatabase(i.ID, item.name, path)
		if err != nil {
			return err
		}
		if _, err = completedTask(ctx, a, id, "completed"); err != nil {
			return err
		}
		if err = queryEquals(ctx, i, adminPassword, "SELECT COUNT(*) FROM "+identifier(item.name)+".items;\n", "2"); err != nil {
			return err
		}
		privilege := model.DatabasePrivilegeInput{User: account.User, Host: account.Host, Database: item.name, Privileges: []string{"SELECT"}}
		if err = a.GrantDatabasePrivileges(i.ID, privilege); err != nil {
			return err
		}
		if _, err = mysqlQuery(ctx, i, account.User, account.Password, "SELECT COUNT(*) FROM "+identifier(item.name)+".items;\n"); err != nil {
			return err
		}
		if _, err = mysqlQuery(ctx, i, account.User, account.Password, "SELECT COUNT(*) FROM "+identifier(item.sibling)+".items;\n"); err == nil {
			return errors.New("percent/underscore grant incorrectly matched sibling database")
		}
		if err = a.RevokeDatabasePrivileges(i.ID, privilege); err != nil {
			return err
		}
		if _, err = mysqlQuery(ctx, i, account.User, account.Password, "SELECT COUNT(*) FROM "+identifier(item.name)+".items;\n"); err == nil {
			return errors.New("complex database SELECT remained after revoke")
		}
		dump := filepath.Join(backups, "existing-export-"+strconv.Itoa(index)+".sql")
		id, err = a.ExportDatabase(i.ID, item.name, dump)
		if err != nil {
			return err
		}
		if item.exportable {
			if _, err = completedTask(ctx, a, id, "completed"); err != nil {
				return err
			}
			if err = a.CreateDatabase(i.ID, restored, "Smoke-db-pass-1"); err != nil {
				return err
			}
			id, err = a.ImportDatabase(i.ID, restored, dump)
			if err != nil {
				return err
			}
			if _, err = completedTask(ctx, a, id, "completed"); err != nil {
				return err
			}
			if err = queryEquals(ctx, i, adminPassword, "SELECT COUNT(*) FROM "+identifier(restored)+".items;\n", "2"); err != nil {
				return err
			}
			if err = a.DropDatabase(i.ID, restored); err != nil {
				return err
			}
		} else {
			task, err := completedTask(ctx, a, id, "failed")
			if err != nil {
				return err
			}
			if !strings.Contains(task.Error, "Windows") || !strings.Contains(task.Error, "ASCII") {
				return errors.New("Unicode export failure did not explain the Windows client limitation")
			}
			if _, err = os.Stat(dump); !errors.Is(err, os.ErrNotExist) {
				return errors.New("refused Unicode export left a destination file")
			}
		}
		for _, database := range []string{item.name, item.sibling} {
			if err = a.DropDatabase(i.ID, database); err != nil {
				return err
			}
		}
	}
	if err = a.DropDatabaseAccount(i.ID, account.User, account.Host); err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(backups, ".phptools-export-*.sql"))
	if err != nil || len(files) != 0 {
		return errors.New("complex database export left temporary SQL files")
	}
	record("desktop imports, lists, grants, revokes, exports, restores and deletes ASCII special names including --help")
	record("Unicode target import/list/grant/revoke/drop work; export explains Windows limitation and leaves no output")
	return nil
}

const fixtureSQL = `CREATE TABLE items (id INT PRIMARY KEY AUTO_INCREMENT, note VARCHAR(255)) ENGINE=InnoDB;
CREATE TABLE audit (note VARCHAR(255)) ENGINE=InnoDB;
INSERT INTO items(note) VALUES ('first'),('中文与分号; text');
DELIMITER $$
CREATE PROCEDURE add_item(IN p_note VARCHAR(255))
BEGIN
  INSERT INTO items(note) VALUES (p_note);
END$$
CREATE FUNCTION note_count() RETURNS INT DETERMINISTIC READS SQL DATA
BEGIN
  RETURN (SELECT COUNT(*) FROM items);
END$$
CREATE TRIGGER items_after_insert AFTER INSERT ON items FOR EACH ROW
BEGIN
  INSERT INTO audit(note) VALUES (NEW.note);
END$$
DELIMITER ;
CALL add_item('procedure');
CREATE EVENT smoke_event ON SCHEDULE AT CURRENT_TIMESTAMP + INTERVAL 1 DAY DISABLE DO INSERT INTO audit(note) VALUES ('event');
`

func exerciseCancelledExport(ctx context.Context, a *desktop.App, i model.Instance, password, database, backups string) error {
	var sql strings.Builder
	fmt.Fprintf(&sql, "USE `%s`; CREATE TABLE bulk_data (payload LONGBLOB) ENGINE=InnoDB; INSERT INTO bulk_data VALUES (REPEAT('x',1048576));\n", database)
	for range 6 {
		sql.WriteString("INSERT INTO bulk_data SELECT payload FROM bulk_data;\n")
	}
	if _, err := mysqlQuery(ctx, i, "root", password, sql.String()); err != nil {
		return err
	}
	destination := filepath.Join(backups, "cancelled.sql")
	id, err := a.ExportDatabase(i.ID, database, destination)
	if err != nil {
		return err
	}
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		files, err := filepath.Glob(filepath.Join(backups, ".phptools-export-*.sql"))
		if err != nil {
			return err
		}
		started := false
		for _, file := range files {
			if stat, err := os.Stat(file); err == nil && stat.Size() > 4096 {
				started = true
			}
		}
		if started {
			if err = a.CancelTask(id); err != nil {
				return err
			}
			break
		}
		state, err := a.GetState()
		if err != nil {
			return err
		}
		for _, task := range state.Tasks {
			if task.ID == id && task.State != "running" {
				return fmt.Errorf("export finished before cancellation could be verified: %s %s", task.State, task.Error)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			_ = a.CancelTask(id)
			return errors.New("export did not begin writing within two minutes")
		case <-poll.C:
		}
	}
	if _, err = completedTask(ctx, a, id, "cancelled"); err != nil {
		return err
	}
	if _, err = os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		return errors.New("cancelled export left its destination file")
	}
	files, err := filepath.Glob(filepath.Join(backups, ".phptools-export-*.sql"))
	if err != nil || len(files) != 0 {
		return errors.New("cancelled export left a temporary SQL file")
	}
	return nil
}

func completedTask(ctx context.Context, a *desktop.App, id, wanted string) (model.Task, error) {
	deadline := time.NewTimer(7 * time.Minute)
	defer deadline.Stop()
	poll := time.NewTicker(25 * time.Millisecond)
	defer poll.Stop()
	for {
		state, err := a.GetState()
		if err != nil {
			return model.Task{}, err
		}
		for _, task := range state.Tasks {
			if task.ID == id && task.State != "running" {
				if task.State != wanted {
					return task, fmt.Errorf("task %s expected %s, got %s: %s", task.Action, wanted, task.State, task.Error)
				}
				return task, nil
			}
		}
		select {
		case <-ctx.Done():
			_ = a.CancelTask(id)
			return model.Task{}, ctx.Err()
		case <-deadline.C:
			_ = a.CancelTask(id)
			return model.Task{}, errors.New("desktop task did not finish within seven minutes")
		case <-poll.C:
		}
	}
}

func findDatabase(databases []model.DatabaseInfo, name string) (model.DatabaseInfo, bool) {
	for _, database := range databases {
		if database.Name == name {
			return database, true
		}
	}
	return model.DatabaseInfo{}, false
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	return port, listener.Close()
}

func queryEquals(ctx context.Context, i model.Instance, password, sql, want string) error {
	output, err := mysqlQuery(ctx, i, "root", password, sql)
	if err != nil {
		return err
	}
	if strings.TrimSpace(strings.ReplaceAll(output, "\r\n", "\n")) != want {
		return fmt.Errorf("SQL result %q does not match %q", strings.TrimSpace(output), want)
	}
	return nil
}

func mysqlQuery(ctx context.Context, i model.Instance, user, password, sql string) (string, error) {
	// The service startup already protects this instance's private directory.
	dir := filepath.Join(filepath.Dir(i.DataDir), "private")
	f, err := os.CreateTemp(dir, "database-acceptance-client-*.cnf")
	if err != nil {
		return "", err
	}
	name := f.Name()
	defer os.Remove(name)
	quoted := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(password) + `"`
	_, writeErr := fmt.Fprintf(f, "[client]\nuser=%s\npassword=%s\nprotocol=TCP\nhost=127.0.0.1\nport=%d\n", user, quoted, i.Port)
	if err = errors.Join(writeErr, f.Close()); err != nil {
		return "", err
	}
	commandCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, filepath.Join(filepath.Dir(i.Executable), "mysql.exe"), "--defaults-file="+name, "--batch", "--skip-column-names", "--default-character-set=utf8mb4", "--connect-timeout=10", "--max-allowed-packet="+strconv.Itoa(16<<20))
	cmd.Env = append(os.Environ(), "MYSQL_TEST_LOGIN_FILE="+filepath.Join(dir, "unused.mylogin.cnf"))
	cmd.Dir = filepath.Dir(i.Executable)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	cmd.Stdin = strings.NewReader(sql)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("acceptance SQL query failed: %w: %s", err, strings.ReplaceAll(strings.TrimSpace(stderr.String()), password, "[redacted]"))
	}
	return string(output), nil
}
