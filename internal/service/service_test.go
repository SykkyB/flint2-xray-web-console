package service

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"flint2-xray-web-console/internal/runner"
)

// exitErr produces something Manager.Status will classify as "non-zero
// exit" rather than a generic error. We have to start a real process
// that exits nonzero; `sh -c "exit 1"` does the job.
func exitErr(t *testing.T) error {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 1")
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected nonzero exit to produce an error")
	}
	var e *exec.ExitError
	if !errors.As(err, &e) {
		t.Fatalf("expected *exec.ExitError, got %T", err)
	}
	return err
}

func newManager(r runner.Runner) *Manager {
	return &Manager{
		InitScript: "/etc/init.d/xray",
		XrayBin:    "/usr/bin/xray",
		ConfigPath: "/etc/xray/config.json",
		Run:        r,
	}
}

func TestValidate_Accepts(t *testing.T) {
	f := &runner.Fake{}
	f.On(runner.FakeCall{Match: "/usr/bin/xray -test", Stdout: "Configuration OK.\n"})
	m := newManager(f)
	if err := m.Validate(context.Background(), ""); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	got := f.Executed()
	if len(got) != 1 || got[0] != "/usr/bin/xray -test -config /etc/xray/config.json" {
		t.Errorf("unexpected calls: %v", got)
	}
}

func TestValidate_Rejects(t *testing.T) {
	f := &runner.Fake{}
	f.On(runner.FakeCall{
		Match:  "/usr/bin/xray -test",
		Stderr: "Failed to parse: invalid character\nmore detail",
		Err:    exitErr(t),
	})
	m := newManager(f)
	err := m.Validate(context.Background(), "/tmp/other.json")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "Failed to parse") {
		t.Errorf("error should surface xray output, got %q", err)
	}
	got := f.Executed()
	if len(got) != 1 || !strings.Contains(got[0], "/tmp/other.json") {
		t.Errorf("custom path not passed: %v", got)
	}
}

func TestRestart_BlockedByValidation(t *testing.T) {
	f := &runner.Fake{}
	f.On(runner.FakeCall{Match: "/usr/bin/xray -test", Stdout: "invalid config", Err: exitErr(t)})
	// init.d intentionally NOT registered: a fall-through call would fail
	// the "no match" assertion in runner.Fake, which is what we want.
	m := newManager(f)
	err := m.Restart(context.Background())
	if err == nil {
		t.Fatalf("expected restart to be blocked")
	}
	if !strings.Contains(err.Error(), "pre-restart validation failed") {
		t.Errorf("error should mention validation, got %q", err)
	}
	got := f.Executed()
	if len(got) != 1 {
		t.Fatalf("expected exactly one call (xray -test), got %d: %v", len(got), got)
	}
	if !strings.HasPrefix(got[0], "/usr/bin/xray -test") {
		t.Errorf("first call should be xray -test, was %q", got[0])
	}
}

func TestRestart_HappyPath(t *testing.T) {
	f := &runner.Fake{}
	f.On(runner.FakeCall{Match: "/usr/bin/xray -test", Stdout: "Configuration OK."})
	f.On(runner.FakeCall{Match: "/etc/init.d/xray restart", Stdout: ""})
	m := newManager(f)
	if err := m.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	got := f.Executed()
	if len(got) != 2 {
		t.Fatalf("expected 2 calls, got %d: %v", len(got), got)
	}
	if !strings.HasPrefix(got[1], "/etc/init.d/xray restart") {
		t.Errorf("second call should be init.d restart, got %q", got[1])
	}
}

func TestStartStop(t *testing.T) {
	f := &runner.Fake{}
	f.On(runner.FakeCall{Match: "/etc/init.d/xray start"})
	f.On(runner.FakeCall{Match: "/etc/init.d/xray stop"})
	m := newManager(f)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	got := f.Executed()
	if len(got) != 2 || !strings.HasSuffix(got[0], "start") || !strings.HasSuffix(got[1], "stop") {
		t.Errorf("unexpected actions: %v", got)
	}
}

func TestStatus_Running(t *testing.T) {
	f := &runner.Fake{}
	f.On(runner.FakeCall{Match: "/etc/init.d/xray status", Stdout: "running"})
	m := newManager(f)
	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Running {
		t.Errorf("expected running")
	}
	if st.Raw != "running" {
		t.Errorf("raw: got %q", st.Raw)
	}
}

func TestStatus_NotRunning(t *testing.T) {
	f := &runner.Fake{}
	f.On(runner.FakeCall{Match: "/etc/init.d/xray status", Stdout: "inactive", Err: exitErr(t)})
	m := newManager(f)
	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Running {
		t.Errorf("expected not running")
	}
}
