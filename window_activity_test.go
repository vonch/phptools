package main

import (
	"errors"
	"reflect"
	"testing"
)

type activityProbe struct {
	controller *windowActivityController
	queue      []func()
	calls      []string
	completion func(bool, error)
	suspendErr error
	resumeErr  error
	errors     []error
}

func newActivityProbe() *activityProbe {
	p := &activityProbe{}
	p.controller = newWindowActivityController(windowActivityActions{
		dispatch: func(fn func()) { p.queue = append(p.queue, fn) },
		hide:     func() { p.calls = append(p.calls, "hide") },
		show:     func() { p.calls = append(p.calls, "show") },
		suspend: func(callback func(bool, error)) error {
			p.calls = append(p.calls, "suspend")
			p.completion = callback
			return p.suspendErr
		},
		resume: func() error { p.calls = append(p.calls, "resume"); return p.resumeErr },
		emit: func(active bool) {
			if active {
				p.calls = append(p.calls, "active")
			} else {
				p.calls = append(p.calls, "inactive")
			}
		},
		report: func(err error) { p.errors = append(p.errors, err) },
	})
	return p
}

func (p *activityProbe) flush() {
	for len(p.queue) > 0 {
		fn := p.queue[0]
		p.queue = p.queue[1:]
		fn()
	}
}

func TestTrayHidesBeforeSuspendingAndResumesBeforeShowing(t *testing.T) {
	p := newActivityProbe()
	p.controller.Hide()
	p.flush()
	if p.controller.IsActive() || !reflect.DeepEqual(p.calls, []string{"inactive", "hide", "suspend"}) {
		t.Fatalf("hide order/state: %v, active=%t", p.calls, p.controller.IsActive())
	}
	p.controller.Show()
	p.flush()
	if !p.controller.IsActive() || !reflect.DeepEqual(p.calls, []string{"inactive", "hide", "suspend", "resume", "show", "active"}) {
		t.Fatalf("restore order/state: %v, active=%t", p.calls, p.controller.IsActive())
	}
}

func TestNewerWindowRequestSupersedesQueuedWork(t *testing.T) {
	for _, visible := range []bool{false, true} {
		p := newActivityProbe()
		if visible {
			p.controller.Hide()
			p.controller.Show()
		} else {
			p.controller.Show()
			p.controller.Hide()
		}
		p.flush()
		want := []string{"inactive", "hide", "suspend"}
		if visible {
			want = []string{"resume", "show", "active"}
		}
		if p.controller.IsActive() != visible || !reflect.DeepEqual(p.calls, want) {
			t.Fatalf("newer request did not win: calls=%v, active=%t", p.calls, p.controller.IsActive())
		}
	}
}

func TestLateSuspendErrorCannotChangeRestoredWindow(t *testing.T) {
	p := newActivityProbe()
	p.controller.Hide()
	p.flush()
	complete := p.completion
	p.controller.Show()
	p.flush()
	complete(false, errors.New("stale request"))
	if !p.controller.IsActive() || len(p.errors) != 0 {
		t.Fatal("stale suspension result affected the restored window")
	}
}

func TestSuspendFailureKeepsUIInactiveUntilRestore(t *testing.T) {
	p := newActivityProbe()
	p.suspendErr = errors.New("runtime failure")
	p.controller.Hide()
	p.flush()
	if p.controller.IsActive() || len(p.errors) != 1 {
		t.Fatal("suspension failure left UI polling active or went unreported")
	}
	p.resumeErr = errors.New("resume failure")
	p.controller.Show()
	p.flush()
	if !p.controller.IsActive() || len(p.errors) != 2 {
		t.Fatal("restore failure was not reported or prevented opening the window")
	}
}
