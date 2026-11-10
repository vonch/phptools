package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"phptools/internal/model"
)

func (a *App) updateResumePath() string {
	return filepath.Join(a.configDir, "update-resume.json")
}

func (a *App) runningInstanceIDs() []string {
	if a.manager == nil {
		return nil
	}
	var ids []string
	for _, instance := range a.orderedInstances(false) {
		if a.manager.Inspect(instance).State == "running" {
			ids = append(ids, instance.ID)
		}
	}
	return ids
}

func (a *App) writeUpdateResume(ids []string) error {
	path := a.updateResumePath()
	if len(ids) == 0 {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	raw, err := json.Marshal(struct {
		IDs []string `json:"ids"`
	}{IDs: ids})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ResumeUpdatedInstances starts the instances that were running when an update
// stopped them. The record is removed before starting so a later launch does
// not repeat the same recovery.
func (a *App) ResumeUpdatedInstances() (string, error) {
	if err := a.needRoot(); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(a.updateResumePath())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var saved struct {
		IDs []string `json:"ids"`
	}
	if err = json.Unmarshal(raw, &saved); err != nil {
		return "", err
	}
	if err = os.Remove(a.updateResumePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if len(saved.IDs) == 0 {
		return "", nil
	}
	return a.task("resume-instances", "update", func(ctx context.Context, id string) error {
		a.opMu.Lock()
		defer a.opMu.Unlock()
		wanted := map[string]struct{}{}
		for _, instanceID := range saved.IDs {
			wanted[instanceID] = struct{}{}
		}
		seen := map[string]model.Instance{}
		var failures []error
		for _, instance := range a.orderedInstances(false) {
			if _, ok := wanted[instance.ID]; !ok {
				continue
			}
			seen[instance.ID] = instance
			if err := a.start(ctx, instance.ID); err != nil {
				failures = append(failures, fmt.Errorf("%s：%w", instance.Name, err))
			}
		}
		for _, instanceID := range saved.IDs {
			if _, ok := seen[instanceID]; !ok {
				failures = append(failures, fmt.Errorf("实例 %s 已不存在", instanceID))
			}
		}
		if joined := errors.Join(failures...); joined != nil && !errors.Is(joined, context.Canceled) {
			a.checkAfterServiceFailure()
		}
		return errors.Join(failures...)
	})
}
