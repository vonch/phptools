package desktop

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
	"phptools/internal/model"
	"phptools/internal/services"
	"phptools/internal/store"
)

const managedDatabaseKind = "managed-database"

func databaseCredentialID(id string) string { return "database-" + id }

func (a *App) ListManagedDatabases(instanceID string) ([]model.ManagedDatabase, error) {
	if err := a.needRoot(); err != nil {
		return nil, err
	}
	if _, err := a.mysqlInstance(instanceID); err != nil {
		return nil, err
	}
	records, err := store.List[model.ManagedDatabase](a.db, managedDatabaseKind)
	if err != nil {
		return nil, err
	}
	result := make([]model.ManagedDatabase, 0, len(records))
	for _, record := range records {
		if record.InstanceID != instanceID {
			continue
		}
		record.Password = ""
		if password, err := keyring.Get(a.credentialService(), databaseCredentialID(record.ID)); err == nil {
			record.Password = password
		}
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}

func (a *App) CreateManagedDatabase(instanceID, name, password string) (model.ManagedDatabase, error) {
	if err := a.needRoot(); err != nil {
		return model.ManagedDatabase{}, err
	}
	if err := services.ValidateDatabaseLogin(name, password); err != nil {
		return model.ManagedDatabase{}, err
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if _, err := a.mysqlInstance(instanceID); err != nil {
		return model.ManagedDatabase{}, err
	}
	records, err := store.List[model.ManagedDatabase](a.db, managedDatabaseKind)
	if err != nil {
		return model.ManagedDatabase{}, err
	}
	for _, record := range records {
		if record.InstanceID == instanceID && strings.EqualFold(record.Name, name) {
			return model.ManagedDatabase{}, errors.New("PHPTools 已记录同名数据库")
		}
	}
	record := model.ManagedDatabase{
		ID: newID(), InstanceID: instanceID, Name: name, User: name, Host: "localhost",
		State: "pending", CreatedAt: model.Now(),
	}
	if err = keyring.Set(a.credentialService(), databaseCredentialID(record.ID), password); err != nil {
		return model.ManagedDatabase{}, errors.New("保存数据库密码失败")
	}
	if err = a.db.Put(managedDatabaseKind, record.ID, withoutPassword(record)); err != nil {
		_ = keyring.Delete(a.credentialService(), databaseCredentialID(record.ID))
		return model.ManagedDatabase{}, err
	}
	record.Password = password
	return a.syncSavedDatabase(record), nil
}

func (a *App) SyncManagedDatabase(id string) (model.ManagedDatabase, error) {
	if err := a.needRoot(); err != nil {
		return model.ManagedDatabase{}, err
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	record, err := a.managedDatabase(id)
	if err != nil {
		return model.ManagedDatabase{}, err
	}
	password, err := keyring.Get(a.credentialService(), databaseCredentialID(record.ID))
	if err != nil {
		return model.ManagedDatabase{}, errors.New("找不到这条数据库保存在 PHPTools 中的密码")
	}
	record.Password = password
	return a.syncSavedDatabase(record), nil
}

func (a *App) DeleteManagedDatabase(id string) error {
	if err := a.needRoot(); err != nil {
		return err
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	record, err := a.managedDatabase(id)
	if err != nil {
		return err
	}
	if record.State == "synced" {
		instance, err := a.mysqlInstance(record.InstanceID)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err = a.manager.DropDatabase(ctx, instance, record.Name); err != nil {
			return err
		}
		if err = a.manager.DropDatabaseAccount(ctx, instance, record.User, record.Host); err != nil {
			return errors.New("本地数据库已删除，但同名账号删除失败：" + err.Error())
		}
	}
	if err = a.db.Delete(managedDatabaseKind, record.ID); err != nil {
		return err
	}
	_ = keyring.Delete(a.credentialService(), databaseCredentialID(record.ID))
	return nil
}

func (a *App) syncSavedDatabase(record model.ManagedDatabase) model.ManagedDatabase {
	instance, err := a.mysqlInstance(record.InstanceID)
	if err != nil {
		record.State = "error"
		record.Error = err.Error()
		_ = a.db.Put(managedDatabaseKind, record.ID, withoutPassword(record))
		return record
	}
	if a.manager.Inspect(instance).State != "running" {
		record.State = "pending"
		record.Error = "已保存在 PHPTools。MySQL 尚未运行，启动后点同步写入本地数据库"
		_ = a.db.Put(managedDatabaseKind, record.ID, withoutPassword(record))
		return record
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = a.manager.CreateDatabase(ctx, instance, record.Name, record.Password); err != nil {
		record.State = "error"
		record.Error = err.Error()
	} else {
		record.State = "synced"
		record.Error = ""
	}
	_ = a.db.Put(managedDatabaseKind, record.ID, withoutPassword(record))
	return record
}

func (a *App) mysqlInstance(id string) (model.Instance, error) {
	instance, err := a.instanceByID(id)
	if err != nil {
		return instance, err
	}
	if instance.Kind != "mysql" {
		return instance, errors.New("请选择 MySQL 实例")
	}
	return instance, nil
}

func (a *App) managedDatabase(id string) (model.ManagedDatabase, error) {
	records, err := store.List[model.ManagedDatabase](a.db, managedDatabaseKind)
	if err != nil {
		return model.ManagedDatabase{}, err
	}
	for _, record := range records {
		if record.ID == id {
			return record, nil
		}
	}
	return model.ManagedDatabase{}, errors.New("PHPTools 中没有这条数据库记录")
}

func withoutPassword(record model.ManagedDatabase) model.ManagedDatabase {
	record.Password = ""
	return record
}
