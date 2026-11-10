package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phptools/internal/model"
)

func TestManualHealthCheckOffersTargetedRepairs(t *testing.T) {
	a := testApp(t)
	_, i := desktopHealthFixture(t, a, "nginx")
	dir := filepath.Dir(filepath.Dir(i.ConfigPath))
	logs, temp := filepath.Dir(i.LogPath), filepath.Join(dir, "temp")
	for _, path := range []string{logs, temp} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.CheckEnvironment(); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	for _, code := range []string{"logs", "temp"} {
		item := desktopHealthItem(t, a.data.HealthReport.Items, i.ID+":"+code)
		if !item.Repairable || item.Status != "warning" || item.RepairID != item.ID {
			t.Fatalf("manual check did not offer repair: %+v", item)
		}
	}
	if _, err := os.Stat(logs); !os.IsNotExist(err) {
		t.Fatalf("manual check recreated logs: %v", err)
	}
	taskID, err := a.RepairHealthItem(i.ID + ":logs")
	if err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	if task := a.data.Tasks[0]; task.ID != taskID || task.State != "completed" || task.Action != "environment-repair" {
		t.Fatalf("repair task did not complete: %+v", task)
	}
	if info, err := os.Stat(logs); err != nil || !info.IsDir() {
		t.Fatalf("selected directory not repaired: %v", err)
	}
	if _, err := os.Stat(temp); !os.IsNotExist(err) {
		t.Fatalf("repair also changed unrelated directory: %v", err)
	}
	if item := desktopHealthItem(t, a.data.HealthReport.Items, i.ID+":logs"); item.Status != "repaired" || item.Repairable {
		t.Fatalf("report did not reflect successful repair: %+v", item)
	}
	if item := desktopHealthItem(t, a.data.HealthReport.Items, i.ID+":temp"); item.Status != "warning" || !item.Repairable {
		t.Fatalf("report lost remaining issue: %+v", item)
	}
	if !a.data.HealthReport.Full {
		t.Fatal("repair discarded the full report's check coverage")
	}
	if _, err := a.RepairHealthItem(i.ID + ":logs"); err == nil {
		t.Fatal("repair accepted a resolved issue")
	}
}

func TestHealthRepairRejectsUnknownUnsupportedAndStaleReportItems(t *testing.T) {
	a := testApp(t)
	_, i := desktopHealthFixture(t, a, "php")
	for _, id := range []string{"", "unknown:logs", i.ID + ":config", "site:unknown:config"} {
		if _, err := a.RepairHealthItem(id); err == nil {
			t.Fatalf("repair accepted unsupported or unknown item %q", id)
		}
	}
	logs := filepath.Dir(i.LogPath)
	if err := os.Remove(logs); err != nil {
		t.Fatal(err)
	}
	if err := a.inspectEnvironment(context.Background(), "manual", false, ""); err != nil {
		t.Fatal(err)
	}
	a.opMu.Lock()
	taskID, err := a.RepairHealthItem(i.ID + ":logs")
	if err != nil {
		a.opMu.Unlock()
		t.Fatal(err)
	}
	a.mu.Lock()
	a.data.HealthReport = &model.HealthReport{Items: []model.HealthItem{}}
	a.mu.Unlock()
	a.opMu.Unlock()
	a.wg.Wait()
	if task := a.data.Tasks[0]; task.ID != taskID || task.State != "failed" || !strings.Contains(task.Error, "检测结果已变化") {
		t.Fatalf("queued repair used stale eligibility: %+v", task)
	}
	if _, err := os.Stat(logs); !os.IsNotExist(err) {
		t.Fatalf("stale report repaired directory: %v", err)
	}
}

func TestHealthRepairRechecksFilesystemAndPublishesFailure(t *testing.T) {
	a := testApp(t)
	_, i := desktopHealthFixture(t, a, "php")
	logs := filepath.Dir(i.LogPath)
	if err := os.Remove(logs); err != nil {
		t.Fatal(err)
	}
	if err := a.inspectEnvironment(context.Background(), "manual", false, ""); err != nil {
		t.Fatal(err)
	}
	// The previously missing directory has become a file since inspection.
	if err := os.WriteFile(logs, []byte("user content"), 0600); err != nil {
		t.Fatal(err)
	}
	taskID, err := a.RepairHealthItem(i.ID + ":logs")
	if err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	if task := a.data.Tasks[0]; task.ID != taskID || task.State != "failed" || task.Error == "" {
		t.Fatalf("failed repair presented as success: %+v", task)
	}
	if content, err := os.ReadFile(logs); err != nil || string(content) != "user content" {
		t.Fatalf("unsafe repair changed user's file: %v", err)
	}
	for _, item := range a.data.HealthReport.Items {
		if item.TargetID == i.ID && (item.Repairable || item.Status == "repaired") {
			t.Fatalf("failure report still offers unsafe repair: %+v", item)
		}
	}
}
