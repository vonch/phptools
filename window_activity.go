package main

import (
	"fmt"
	"sync/atomic"
)

type windowActivityActions struct {
	dispatch func(func())
	hide     func()
	show     func()
	suspend  func(func(bool, error)) error
	resume   func() error
	emit     func(bool)
	report   func(error)
}

// All native window operations run together on the UI thread. A newer request
// supersedes queued work, so a delayed close cannot hide a reopened window.
type windowActivityController struct {
	actions    windowActivityActions
	generation atomic.Uint64
	active     atomic.Bool
}

func newWindowActivityController(actions windowActivityActions) *windowActivityController {
	c := &windowActivityController{actions: actions}
	c.active.Store(true)
	return c
}

func (c *windowActivityController) IsActive() bool { return c.active.Load() }

func (c *windowActivityController) Hide() {
	generation := c.generation.Add(1)
	c.actions.dispatch(func() {
		if c.generation.Load() != generation {
			return
		}
		c.active.Store(false)
		c.actions.emit(false)
		c.actions.hide()
		err := c.actions.suspend(func(success bool, err error) {
			if c.generation.Load() == generation && !c.IsActive() && err != nil {
				c.actions.report(fmt.Errorf("WebView2 挂起失败：%w", err))
			}
		})
		if err != nil {
			c.actions.report(fmt.Errorf("WebView2 挂起请求失败：%w", err))
		}
	})
}

func (c *windowActivityController) Show() {
	generation := c.generation.Add(1)
	c.actions.dispatch(func() {
		if c.generation.Load() != generation {
			return
		}
		if err := c.actions.resume(); err != nil {
			c.actions.report(fmt.Errorf("WebView2 恢复失败：%w", err))
		}
		c.actions.show()
		c.active.Store(true)
		c.actions.emit(true)
	})
}
