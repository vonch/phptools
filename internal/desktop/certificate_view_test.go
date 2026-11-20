package desktop

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"phptools/internal/model"
)

func TestCertificateViewPersistsWithoutEnvironment(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "中文 preferences")
	notified := 0
	a, err := New(dir, Hooks{Notify: func() { notified++ }})
	if err != nil {
		t.Fatal(err)
	}
	want := model.CertificateViewPreferences{
		HiddenColumns: []string{"kind", "actions"},
		SortBy:        "notAfter", SortDirection: "desc",
	}
	input := model.CertificateViewPreferences{
		HiddenColumns: []string{"kind", "actions", "kind"},
		SortBy:        "notAfter", SortDirection: "desc",
	}
	if err = a.SaveCertificateView(input); err != nil {
		t.Fatal(err)
	}
	if notified != 1 || a.db != nil || a.manager != nil || !reflect.DeepEqual(a.data.CertificateView, want) {
		t.Fatal("certificate preferences must normalize and notify without initializing services")
	}
	input.HiddenColumns[0] = "siteName"
	if !reflect.DeepEqual(a.data.CertificateView, want) {
		t.Fatal("saved preferences alias caller slices")
	}
	b, err := New(dir, Hooks{})
	if err != nil || !reflect.DeepEqual(b.data.CertificateView, want) {
		t.Fatalf("restart lost certificate preferences: %+v, %v", b, err)
	}
	if err = b.SaveCertificateView(defaultCertificateView()); err != nil {
		t.Fatal(err)
	}
	c, err := New(dir, Hooks{})
	if err != nil || !reflect.DeepEqual(c.data.CertificateView, defaultCertificateView()) {
		t.Fatalf("restored defaults did not persist: %v", err)
	}
}

func TestCertificateViewRejectsInvalidPreferences(t *testing.T) {
	changes := map[string]func(*model.CertificateViewPreferences){
		"unknown column": func(v *model.CertificateViewPreferences) { v.HiddenColumns = []string{"unknown"} },
		"all hidden": func(v *model.CertificateViewPreferences) {
			v.HiddenColumns = []string{"siteName", "domains", "kind", "notAfter", "actions"}
		},
		"unknown sort":      func(v *model.CertificateViewPreferences) { v.SortBy = "unknown" },
		"empty sort":        func(v *model.CertificateViewPreferences) { v.SortBy = "" },
		"unknown direction": func(v *model.CertificateViewPreferences) { v.SortDirection = "descending" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			a, err := New(t.TempDir(), Hooks{})
			if err != nil {
				t.Fatal(err)
			}
			preferences := defaultCertificateView()
			change(&preferences)
			if err = a.SaveCertificateView(preferences); err == nil {
				t.Fatal("invalid preferences were saved")
			}
			if !reflect.DeepEqual(a.data.CertificateView, defaultCertificateView()) {
				t.Fatal("rejected preferences changed the saved view")
			}
		})
	}
}

func TestCertificateViewInvalidFileUsesDefaultsWithDiagnostic(t *testing.T) {
	for _, content := range []string{"broken JSON", `{"hiddenColumns":["unknown"]}`, `{"sortBy":"invalid"}`} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "certificate-view.json"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		a, err := New(dir, Hooks{})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a.data.CertificateView, defaultCertificateView()) || len(a.data.Diagnostics) != 1 {
			t.Fatal("invalid file must use defaults and explain the problem")
		}
	}
}
