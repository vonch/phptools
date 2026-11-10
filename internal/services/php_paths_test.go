package services

import (
	"phptools/internal/model"
	"testing"
)

func TestLegacyPHPPathBoundary(t *testing.T) {
	for _, tt := range []struct {
		version, path string
		allowed       bool
	}{
		{"5.6.40", `D:\projects\space project`, true},
		{"5.6.40", `D:\项目\project`, false},
		{"7.0.33", `D:\项目\project`, false},
		{"7.1.33", `D:\项目\project`, true},
		{"8.0.30", `D:\项目\space project`, true},
	} {
		t.Run(tt.version+tt.path, func(t *testing.T) {
			err := ValidatePHPPath(model.Package{Kind: "php", Version: tt.version}, tt.path)
			if (err == nil) != tt.allowed {
				t.Fatalf("allowed=%v error=%v", tt.allowed, err)
			}
		})
	}
}
