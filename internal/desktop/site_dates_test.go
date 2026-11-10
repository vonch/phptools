package desktop

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"phptools/internal/model"
	"phptools/internal/store"
)

func TestSiteAdditionDateIsAssignedOnceAndCannotBeOverwritten(t *testing.T) {
	before := time.Now().UTC().Truncate(time.Second)
	created, err := siteForSave(model.Site{
		Name: "new", CreatedAt: "forged", UpdatedAt: "forged", LastActiveAt: "forged",
	}, nil)
	if err != nil || created.ID == "" || created.LastActiveAt != "" {
		t.Fatalf("creation failed: %+v, %v", created, err)
	}
	stamp, err := time.Parse(time.RFC3339, created.CreatedAt)
	if err != nil || stamp.Before(before) || stamp.After(time.Now()) {
		t.Fatalf("invalid addition timestamp: %q", created.CreatedAt)
	}
	if created.UpdatedAt != created.CreatedAt {
		t.Fatal("new site must record the same creation and modification time")
	}
	a := testApp(t)
	if err := a.db.Put("site", created.ID, created); err != nil {
		t.Fatal(err)
	}
	saved, err := store.List[model.Site](a.db, "site")
	if err != nil {
		t.Fatal(err)
	}
	edited := created
	edited.Name, edited.CreatedAt, edited.LastActiveAt = "renamed", "forged", "forged"
	edited, err = siteForSave(edited, saved)
	if err != nil || edited.CreatedAt != created.CreatedAt || edited.Name != "renamed" || edited.LastActiveAt != "" {
		t.Fatalf("editing changed server-owned timestamps: %+v, %v", edited, err)
	}
	legacy, err := siteForSave(model.Site{ID: "legacy", CreatedAt: "forged"}, []model.Site{{ID: "legacy"}})
	if err != nil || legacy.CreatedAt != "" {
		t.Fatal("legacy site received an invented addition date")
	}
	if _, err := time.Parse(time.RFC3339Nano, legacy.UpdatedAt); err != nil {
		t.Fatal("editing a legacy site must record its modification time")
	}
	if _, err = siteForSave(model.Site{ID: "deleted"}, saved); err == nil {
		t.Fatal("unknown site was accepted")
	}
}

func TestSiteModificationPersistsAndFailedSaveKeepsOldDates(t *testing.T) {
	a := testApp(t)
	original := model.Site{
		ID: "edited-site", Name: "Original", Domain: "edited-site.test", Port: 8081,
		Root: t.TempDir(), CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-02T00:00:00Z",
	}
	input := original
	input.Name, input.UpdatedAt = "Edited", "2099-01-01T00:00:00Z"
	edited, err := siteForSave(input, []model.Site{original})
	if err != nil {
		t.Fatal(err)
	}
	if stamp, err := time.Parse(time.RFC3339Nano, edited.UpdatedAt); err != nil ||
		stamp.After(time.Now()) || edited.UpdatedAt == original.UpdatedAt || edited.CreatedAt != original.CreatedAt {
		t.Fatalf("edit timestamp is not server assigned: %+v", edited)
	}
	if err = a.db.Put("site", edited.ID, edited); err != nil {
		t.Fatal(err)
	}
	a.db.Close()
	a.db = nil
	reopened, err := New(a.configDir, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	if len(reopened.data.Sites) != 1 || reopened.data.Sites[0] != edited {
		t.Fatal("restart lost modification timestamp")
	}
	// No Nginx instance exists, so the save must fail before touching hosts.
	failed := edited
	failed.Name = "Must not persist"
	if _, err = reopened.SaveSite(failed); err == nil {
		t.Fatal("save without Nginx unexpectedly succeeded")
	}
	stored, err := store.List[model.Site](reopened.db, "site")
	if err != nil || len(stored) != 1 || stored[0] != edited || reopened.data.Sites[0] != edited {
		t.Fatal("failed save changed the stored site or its timestamps")
	}
}

func TestSiteActivityEnrichesSnapshotWithoutChangingStoredSite(t *testing.T) {
	a := testApp(t)
	original := model.Site{ID: "activity", CreatedAt: "2026-10-01T00:00:00Z"}
	a.data.Sites = []model.Site{original}
	if err := a.db.Put("site", original.ID, original); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(a.data.Settings.Root, "logs", original.ID+"-access.log")
	if err := os.WriteFile(log, []byte("127.0.0.1 - - [20/Oct/2026:17:00:00 +0800] \"GET / HTTP/1.1\" 200 0 \"-\" \"test\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := a.GetState()
	if err != nil || len(state.Sites) != 1 || state.Sites[0].LastActiveAt != "2026-10-20T09:00:00Z" {
		t.Fatalf("snapshot activity missing: %+v, %v", state.Sites, err)
	}
	stored, err := store.List[model.Site](a.db, "site")
	if err != nil || len(stored) != 1 || stored[0] != original || a.data.Sites[0] != original {
		t.Fatal("snapshot read changed stored site data")
	}
}

func TestDateColumnPreferencesPersist(t *testing.T) {
	a, err := New(t.TempDir(), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"createdAt", "lastActiveAt"} {
		preferences := defaultSiteView()
		preferences.SortBy, preferences.SortDirection = key, "desc"
		preferences.HiddenColumns = []string{"name", "root", "phpInstanceId", "enabled", "createdAt", "lastActiveAt"}
		if err := a.SaveSiteView(preferences); err != nil {
			t.Fatal(err)
		}
		loaded, err := New(a.configDir, Hooks{})
		if err != nil || loaded.data.SiteView.SortBy != key || len(loaded.data.SiteView.HiddenColumns) != 6 {
			t.Fatalf("date preferences did not survive restart: %v", err)
		}
	}
}
