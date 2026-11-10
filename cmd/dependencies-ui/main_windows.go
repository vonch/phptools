//go:build windows

// Isolated UI acceptance; owns its executable, profile, WebView data and runtime.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"phptools/internal/acceptance"
)

func main() {
	module := flag.String("playwright", "", "absolute path to installed Playwright package")
	node := flag.String("node", "node", "Node.js executable")
	suite := flag.String("suite", "dependencies", "UI suite: dependencies or sites")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, *node, *module, *suite); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, node, module, suite string) (runErr error) {
	if !filepath.IsAbs(module) {
		return errors.New("-playwright must be an absolute module directory")
	}
	if suite != "dependencies" && suite != "sites" {
		return errors.New("unknown UI suite")
	}
	root, err := acceptance.NewRoot("", suite+"-ui")
	if err != nil {
		return err
	}
	defer func() {
		err := root.Cleanup()
		// WebView2 can release its profile locks shortly after the manager exits.
		// Cleanup rechecks the owned directory identity and live processes on every attempt.
		for tries := 0; err != nil && tries < 30; tries++ {
			time.Sleep(250 * time.Millisecond)
			err = root.Cleanup()
		}
		if err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("cleanup %s: %w", root.Path, err))
		} else {
			fmt.Println("CLEANED", root.Path)
		}
	}()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	listener.Close()
	exe := filepath.Join(root.Path, "PHPTools-DependenciesQA.exe")
	build := exec.CommandContext(ctx, "go", "build", "-o", exe, "-ldflags", "-H windowsgui -X main.uiDebugPort="+port+" -X main.instanceID=local.phptools.dependenciesqa."+port, ".")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err = build.Run(); err != nil {
		return err
	}
	profile := filepath.Join(root.Path, "中文 profile")
	if suite == "sites" {
		if err := prepareSiteFixtures(profile, root.Path); err != nil {
			return err
		}
	} else if err := prepareHealthFixtures(root.Path); err != nil {
		return err
	}
	app := exec.Command(exe, "--config-dir", profile)
	app.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err = app.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- app.Wait() }()
	defer func() {
		select {
		case err := <-done:
			runErr = errors.Join(runErr, err)
		case <-time.After(8 * time.Second):
			// The script attempts Quit in finally. This isolated profile creates no
			// services; only the exact child we started may be killed on failure.
			runErr = errors.Join(runErr, errors.New("QA app did not quit normally"), app.Process.Kill())
			<-done
		}
	}()
	check := exec.CommandContext(ctx, node, "scripts/"+suite+"-ui.mjs", port, root.Path, module)
	check.Stdout, check.Stderr = os.Stdout, os.Stderr
	if err = check.Run(); err != nil {
		return err
	}
	return nil
}
