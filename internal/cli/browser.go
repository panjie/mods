package cli

import (
	"context"
	"os/exec"
	"runtime"
	"time"
)

// Pass the complete authorization URL as one argument, never through a shell:
// OAuth query parameters include ampersands and other shell metacharacters.
func browserCommand(goos, target string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{target}
	case "windows":
		return "rundll32.exe", []string{"url.dll,FileProtocolHandler", target}
	default:
		return "xdg-open", []string{target}
	}
}

var openAuthBrowser = func(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, args := browserCommand(runtime.GOOS, target)
	// Do not attach terminal streams: opener output must not corrupt the TUI.
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	// Some launchers remain alive for the browser's lifetime. Reap them in the
	// background instead of blocking authorization or killing the browser.
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
