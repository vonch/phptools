package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"phptools/internal/model"
)

// Database operations share the lifecycle lock with instance start/stop and
// configuration changes. Nothing can stop or remove an instance mid-operation.
func withDatabase[T any](a *App, instanceID string, work func(context.Context, model.Instance) (T, error)) (T, error) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	var zero T
	instance, err := a.databaseInstance(instanceID)
	if err != nil {
		return zero, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return work(ctx, instance)
}

func (a *App) databaseInstance(instanceID string) (model.Instance, error) {
	if err := a.needRoot(); err != nil {
		return model.Instance{}, err
	}
	instance, err := a.instanceByID(instanceID)
	if err != nil {
		return instance, err
	}
	if instance.Kind != "mysql" {
		return instance, errors.New("请选择 MySQL 实例")
	}
	return instance, nil
}

func (a *App) databaseOperation(instanceID string, work func(context.Context, model.Instance) error) error {
	_, err := withDatabase(a, instanceID, func(ctx context.Context, instance model.Instance) (struct{}, error) {
		return struct{}{}, work(ctx, instance)
	})
	return err
}

func (a *App) ListDatabases(instanceID string) ([]model.DatabaseInfo, error) {
	return withDatabase(a, instanceID, func(ctx context.Context, instance model.Instance) ([]model.DatabaseInfo, error) {
		return a.manager.ListDatabases(ctx, instance)
	})
}

func (a *App) CreateDatabase(instanceID, name, password string) error {
	return a.databaseOperation(instanceID, func(ctx context.Context, instance model.Instance) error {
		return a.manager.CreateDatabase(ctx, instance, name, password)
	})
}

func (a *App) DropDatabase(instanceID, name string) error {
	return a.databaseOperation(instanceID, func(ctx context.Context, instance model.Instance) error {
		return a.manager.DropDatabase(ctx, instance, name)
	})
}

func (a *App) ListDatabaseAccounts(instanceID string) ([]model.DatabaseAccount, error) {
	return withDatabase(a, instanceID, func(ctx context.Context, instance model.Instance) ([]model.DatabaseAccount, error) {
		return a.manager.ListDatabaseAccounts(ctx, instance)
	})
}

func (a *App) CreateDatabaseAccount(instanceID string, input model.DatabaseAccountInput) error {
	return a.databaseOperation(instanceID, func(ctx context.Context, instance model.Instance) error {
		return a.manager.CreateDatabaseAccount(ctx, instance, input)
	})
}

func (a *App) ChangeDatabaseAccountPassword(instanceID string, input model.DatabaseAccountInput) error {
	return a.databaseOperation(instanceID, func(ctx context.Context, instance model.Instance) error {
		return a.manager.ChangeDatabaseAccountPassword(ctx, instance, input)
	})
}

func (a *App) DropDatabaseAccount(instanceID, user, host string) error {
	return a.databaseOperation(instanceID, func(ctx context.Context, instance model.Instance) error {
		return a.manager.DropDatabaseAccount(ctx, instance, user, host)
	})
}

func (a *App) DatabaseAccountGrants(instanceID, user, host string) ([]string, error) {
	return withDatabase(a, instanceID, func(ctx context.Context, instance model.Instance) ([]string, error) {
		return a.manager.DatabaseAccountGrants(ctx, instance, user, host)
	})
}

func (a *App) GrantDatabasePrivileges(instanceID string, input model.DatabasePrivilegeInput) error {
	return a.databaseOperation(instanceID, func(ctx context.Context, instance model.Instance) error {
		return a.manager.GrantDatabasePrivileges(ctx, instance, input)
	})
}

func (a *App) RevokeDatabasePrivileges(instanceID string, input model.DatabasePrivilegeInput) error {
	return a.databaseOperation(instanceID, func(ctx context.Context, instance model.Instance) error {
		return a.manager.RevokeDatabasePrivileges(ctx, instance, input)
	})
}

func validateSQLPath(path string) error {
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Ext(path), ".sql") {
		return errors.New("请选择绝对路径的 .sql 文件")
	}
	return nil
}

func (a *App) SelectSQLImport() (string, error) {
	if a.hooks.PickSQLImport == nil {
		return "", errors.New("SQL 文件选择器尚未初始化")
	}
	path, err := withoutDialogCancel(a.hooks.PickSQLImport())
	if err != nil || path == "" {
		return path, err
	}
	if err = validateSQLPath(path); err != nil {
		return "", err
	}
	return path, nil
}

func (a *App) SelectSQLExport(database string) (string, error) {
	if a.hooks.PickSQLExport == nil {
		return "", errors.New("SQL 保存文件对话框尚未初始化")
	}
	// Only a filename is supplied to the dialog; even names from an existing
	// server cannot inject a directory into the suggested export location.
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, database)
	if name == "" {
		name = "database"
	}
	path, err := withoutDialogCancel(a.hooks.PickSQLExport(name + "-" + time.Now().Format("20060102-150405") + ".sql"))
	if err != nil || path == "" {
		return path, err
	}
	if filepath.Ext(path) == "" {
		path += ".sql"
	}
	if err = validateSQLPath(path); err != nil {
		return "", err
	}
	if _, err = os.Lstat(path); err == nil {
		return "", errors.New("目标文件已存在，请选择新的文件名")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return path, nil
}

func (a *App) databaseTask(action, instanceID, database, path string) (string, error) {
	if _, err := a.databaseInstance(instanceID); err != nil {
		return "", err
	}
	if err := validateSQLPath(path); err != nil {
		return "", err
	}
	return a.task(action, instanceID, func(ctx context.Context, taskID string) error {
		a.opMu.Lock()
		defer a.opMu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		instance, err := a.databaseInstance(instanceID)
		if err != nil {
			return err
		}
		switch action {
		case "database-import":
			a.progress(taskID, 0, 0, fmt.Sprintf("正在导入数据库 %s", database))
			return a.manager.ImportDatabase(ctx, instance, database, path, func(done, total int64) {
				a.progress(taskID, done, total, fmt.Sprintf("正在导入数据库 %s；已发送字节不代表已提交", database))
			})
		case "database-export":
			a.progress(taskID, 0, 0, fmt.Sprintf("正在导出数据库 %s 到 %s", database, path))
			return a.manager.ExportDatabase(ctx, instance, database, path)
		default:
			return errors.New("未知数据库任务")
		}
	})
}

func (a *App) ImportDatabase(instanceID, database, path string) (string, error) {
	return a.databaseTask("database-import", instanceID, database, path)
}

func (a *App) ExportDatabase(instanceID, database, path string) (string, error) {
	return a.databaseTask("database-export", instanceID, database, path)
}
