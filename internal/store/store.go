package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	Root string
}

// Open only adopts empty directories or directories previously owned by PHPTools.
func Open(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil || !filepath.IsAbs(root) {
		return nil, errors.New("请选择绝对路径作为环境目录")
	}
	if filepath.Dir(abs) == abs {
		return nil, errors.New("不能将磁盘根目录作为环境目录")
	}
	for p := abs; ; p = filepath.Dir(p) {
		if _, err := os.Stat(filepath.Join(p, "COM", "phpstudy_pro.exe")); err == nil {
			return nil, errors.New("请另外选择一个新目录，不要使用其他软件的安装目录")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(abs)) {
		return nil, errors.New("环境目录不能包含符号链接或目录联接")
	}
	marker := filepath.Join(abs, ".phptools-runtime")
	if data, err := os.ReadFile(marker); err == nil {
		if string(data) != "PHPTools runtime v1\n" {
			return nil, errors.New("环境目录所有权标记无效")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		entries, err := os.ReadDir(abs)
		if err != nil {
			return nil, err
		}
		if len(entries) != 0 {
			return nil, errors.New("请选择空目录；此目录尚未由 PHPTools 管理")
		}
		if err := os.WriteFile(marker, []byte("PHPTools runtime v1\n"), 0600); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	for _, name := range []string{"packages", "instances", "sites", "logs", "cache", "backups"} {
		if err := os.MkdirAll(filepath.Join(abs, name), 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(abs, "metadata.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS records (kind TEXT NOT NULL,id TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(kind,id)); PRAGMA user_version=1;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, Root: abs}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Put(kind, id string, v any) error {
	if id == "" {
		return errors.New("记录 ID 不能为空")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO records(kind,id,payload) VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload`, kind, id, string(data))
	return err
}

func (s *Store) Delete(kind, id string) error {
	_, err := s.db.Exec(`DELETE FROM records WHERE kind=? AND id=?`, kind, id)
	return err
}

func (s *Store) Replace(kind string, values map[string]any) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM records WHERE kind=?`, kind); err != nil {
		return err
	}
	for id, v := range values {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO records(kind,id,payload) VALUES(?,?,?)`, kind, id, string(raw)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func List[T any](s *Store, kind string) ([]T, error) {
	result := []T{}
	rows, err := s.db.Query(`SELECT payload FROM records WHERE kind=? ORDER BY id`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v T
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("读取 %s 记录: %w", kind, err)
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
