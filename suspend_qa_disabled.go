//go:build !suspendqa

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"phptools/internal/desktop"
)

func suspendAcceptanceStateRead() {}

func startSuspendAcceptance(*application.App, *application.WebviewWindow, *desktop.App, *windowActivityController, func()) {
}
