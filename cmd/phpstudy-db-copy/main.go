//go:build windows

// Command phpstudy-db-copy copies business databases into a separate owned instance.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/zalando/go-keyring"
	"phptools/internal/desktop"
	"phptools/internal/model"
	"phptools/internal/services"
	"phptools/internal/store"
)

const destination = `E:\PHPTools`
const sourceConfig = `D:\phpstudy_pro\WWW\fstnewweb\application\database.php`
const sourceClient = `D:\phpstudy_pro\Extensions\MySQL5.7.26\bin\mysql.exe`

type migrationState struct {
	Phase                       string         `json:"phase"`
	Root                        string         `json:"root"`
	Instance                    model.Instance `json:"instance"`
	Package                     model.Package  `json:"package"`
	BackupDir                   string         `json:"backupDir"`
	MetadataBackup              string         `json:"metadataBackup"`
	PreparedAt                  string         `json:"preparedAt"`
	CompletedAt                 string         `json:"completedAt,omitempty"`
	SQLBackup                   string         `json:"sqlBackup,omitempty"`
	Error                       string         `json:"error,omitempty"`
	SourcePID                   int            `json:"sourcePID,omitempty"`
	SourceCreated               string         `json:"sourceCreated,omitempty"`
	SourceShutdownAt            string         `json:"sourceShutdownAt,omitempty"`
	ColdBackup                  string         `json:"coldBackup,omitempty"`
	SourceOriginalReadOnly      string         `json:"sourceOriginalReadOnly,omitempty"`
	SourceOriginalSuperReadOnly string         `json:"sourceOriginalSuperReadOnly,omitempty"`
	SourceWriteProtected        bool           `json:"sourceWriteProtected"`
}

func main() {
	mode := flag.String("mode", "prepare", "prepare or copy")
	statePath := flag.String("state", `build\migration\db-state.json`, "non-sensitive state file")
	accounts := flag.String("accounts", "", "protected source account export for copy")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	var err error
	switch *mode {
	case "prepare":
		err = prepare(ctx, *statePath)
	case "copy":
		err = copyDatabases(ctx, *statePath, *accounts)
	case "preflight":
		err = sourcePreflight(ctx, *statePath, *accounts)
	case "shutdown-source":
		err = shutdownSource(ctx, *statePath)
	case "sync-defaults":
		err = syncSourceDefaults(ctx, *statePath)
	case "diagnose":
		err = diagnoseSource(ctx)
	case "cold-backup":
		err = coldBackup(ctx, *statePath)
	case "protect-source":
		err = protectSource(ctx, *statePath)
	case "verify-resume":
		err = verifyResume(ctx, *statePath, *accounts)
	default:
		err = errors.New("unknown migration mode")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func saveJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func sourcePassword() (string, error) {
	raw, err := os.ReadFile(sourceConfig)
	if err != nil {
		return "", err
	}
	get := func(key string) string {
		return phpValue(raw, key)
	}
	if get("username") != "root" || get("hostname") != "127.0.0.1" {
		return "", errors.New("source account changed; inspect its configuration")
	}
	p := get("password")
	if p == "" || strings.ContainsAny(p, "\x00\r\n") {
		return "", errors.New("source root credential missing")
	}
	return p, nil
}

func phpValue(raw []byte, key string) string {
	m := regexp.MustCompile(`['"]` + key + `['"]\s*=>\s*['"]([^'"\r\n]*)['"]`).FindSubmatch(raw)
	if len(m) == 2 {
		return string(m[1])
	}
	return ""
}

func ensureManagerClosed(ctx context.Context) error {
	c := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "if (Get-Process -Name PHPTools -ErrorAction SilentlyContinue) { exit 10 }; exit 0")
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if c.Run() != nil {
		return errors.New("PHPTools must be closed before metadata migration")
	}
	return nil
}

func prepare(ctx context.Context, statePath string) (runErr error) {
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("state file already exists; inspect it before preparing again")
	}
	if err := ensureManagerClosed(ctx); err != nil {
		return err
	}
	db, err := store.Open(destination)
	if err != nil {
		return err
	}
	defer db.Close()
	pkgs, err := store.List[model.Package](db, "package")
	if err != nil {
		return err
	}
	var pkg model.Package
	for _, p := range pkgs {
		if p.Kind == "mysql" && p.Version == "5.7.44" && p.Path != "" && p.Verified {
			pkg = p
		}
	}
	if pkg.ID == "" {
		return errors.New("verified MySQL 5.7.44 package missing")
	}
	instances, err := store.List[model.Instance](db, "instance")
	if err != nil {
		return err
	}
	for _, i := range instances {
		if i.Port == 13306 {
			return errors.New("port 13306 already allocated")
		}
	}
	l, err := net.Listen("tcp4", "127.0.0.1:13306")
	if err != nil {
		return err
	}
	l.Close()
	backupDir, err := os.MkdirTemp(filepath.Join(destination, "backups"), "phpstudy-migration-20261020-db-")
	if err != nil {
		return err
	}
	if err = services.ProtectPrivatePath(backupDir, true); err != nil {
		return err
	}
	backup := filepath.Join(backupDir, "metadata-before-db.db")
	conn, err := sql.Open("sqlite", filepath.Join(destination, "metadata.db"))
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(filepath.ToSlash(backup), "'", "''")+"'")
	err = errors.Join(err, conn.Close())
	if err != nil {
		return err
	}
	password := "Migration!" + rand.Text()
	service := desktop.CredentialService(destination)
	manager := services.New(destination, func(i model.Instance) error { return db.Put("instance", i.ID, i) }, func(id string) (string, error) { return keyring.Get(service, id) })
	i, err := manager.Create(ctx, pkg, model.InstanceInput{Name: "phpStudy MySQL 5.7（迁移）", PackageID: pkg.ID, Port: 13306})
	if err != nil {
		return err
	}
	state := migrationState{Phase: "preparing", Root: destination, Instance: i, Package: pkg, BackupDir: backupDir, MetadataBackup: backup, PreparedAt: model.Now()}
	defer func() {
		if runErr != nil {
			state.Error = runErr.Error()
			state.Phase = "prepare-failed"
			current := manager.Inspect(state.Instance)
			if current.PID != 0 {
				stopped, stopErr := manager.Stop(context.Background(), current)
				state.Instance = stopped
				runErr = errors.Join(runErr, stopErr)
			}
			_ = saveJSON(statePath, state)
		}
	}()
	if err = keyring.Set(service, i.ID, password); err != nil {
		return err
	}
	if err = db.Put("instance", i.ID, i); err != nil {
		_ = keyring.Delete(service, i.ID)
		return err
	}
	if err = saveJSON(statePath, state); err != nil {
		return err
	}
	raw, err := os.ReadFile(i.ConfigPath)
	if err != nil {
		return err
	}
	settings := "\n; Preserve source phpStudy SQL defaults during migration\ncharacter-set-server=utf8\ncollation-server=utf8_unicode_ci\ndefault-storage-engine=MyISAM\nlower_case_table_names=1\nmax_allowed_packet=1G\ninnodb_buffer_pool_size=64M\nevent_scheduler=OFF\nskip-name-resolve=0\n"
	if err = os.WriteFile(i.ConfigPath, append(raw, []byte(settings)...), 0600); err != nil {
		return err
	}
	i, err = manager.Start(ctx, pkg, i)
	state.Instance = i
	if err != nil {
		return err
	}
	session, err := newClient(destination, filepath.Join(filepath.Dir(i.Executable), "mysql.exe"), i.Port, password)
	if err != nil {
		return err
	}
	defer session.close()
	rows, err := session.query(ctx, "SELECT @@version,@@port,@@datadir;\n")
	if err != nil {
		return err
	}
	if len(rows) != 1 || len(rows[0]) != 3 || rows[0][0] != "5.7.44" || rows[0][1] != "13306" {
		return errors.New("prepared server identity mismatch")
	}
	state.Phase = "prepared"
	if err = saveJSON(statePath, state); err != nil {
		return err
	}
	fmt.Printf("PREPARED instance=%s port=13306 metadata=%s state=%s\n", i.ID, backup, statePath)
	return nil
}
