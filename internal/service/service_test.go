package service

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// recordedCall captures one invocation of the fake Runner so tests can
// assert which binary + args Manager shelled out to.
type recordedCall struct {
	Name string
	Args []string
}

type fakeRunner struct {
	calls []recordedCall
	// next controls the response to the next call in order. Each entry is
	// {output, error}. A shorter list than calls is a test bug.
	next []fakeResponse
}

type fakeResponse struct {
	out []byte
	err error
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, recordedCall{Name: name, Args: append([]string(nil), args...)})
	if len(f.calls) > len(f.next) {
		return nil, errors.New("fakeRunner: no response configured for this call")
	}
	r := f.next[len(f.calls)-1]
	return r.out, r.err
}

func newFake(responses ...fakeResponse) *fakeRunner {
	return &fakeRunner{next: responses}
}

// exitErr produces something Manager.Status will classify as "non-zero
// exit" rather than a generic error. We have to start a real process
// that exits nonzero; /usr/bin/false (or `sh -c "exit 1"`) does the job.
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

func newManager(r *fakeRunner) *Manager {
	return &Manager{
		InitScript: "/etc/init.d/xray",
		XrayBin:    "/usr/bin/xray",
		ConfigPath: "/etc/xray/config.json",
		Run:        r.run,
	}
}

func TestValidate_Accepts(t *testing.T) {
	r := newFake(fakeResponse{out: []byte("Configuration OK.\n")})
	m := newManager(r)
	if err := m.Validate(context.Background(), ""); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(r.calls) != 1 || r.calls[0].Name != "/usr/bin/xray" {
		t.Fatalf("unexpected call: %+v", r.calls)
	}
	wantArgs := []string{"-test", "-config", "/etc/xray/config.json"}
	if !equalStrings(r.calls[0].Args, wantArgs) {
		t.Errorf("args: got %v, want %v", r.calls[0].Args, wantArgs)
	}
}

func TestValidate_Rejects(t *testing.T) {
	r := newFake(fakeResponse{
		out: []byte("Failed to parse: invalid character\nmore detail"),
		err: exitErr(t),
	})
	m := newManager(r)
	err := m.Validate(context.Background(), "/tmp/other.json")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "Failed to parse") {
		t.Errorf("error should surface xray output, got %q", err)
	}
	// Custom path should override ConfigPath in the args.
	if !strings.Contains(strings.Join(r.calls[0].Args, " "), "/tmp/other.json") {
		t.Errorf("custom path not passed, args=%v", r.calls[0].Args)
	}
}

func TestRestart_BlockedByValidation(t *testing.T) {
	// First call (xray -test) fails; init.d must NOT be called.
	r := newFake(fakeResponse{out: []byte("invalid config"), err: exitErr(t)})
	m := newManager(r)
	err := m.Restart(context.Background())
	if err == nil {
		t.Fatalf("expected restart to be blocked")
	}
	if !strings.Contains(err.Error(), "pre-restart validation failed") {
		t.Errorf("error should mention validation, got %q", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected exactly one call (xray -test), got %d: %+v", len(r.calls), r.calls)
	}
	if r.calls[0].Name != "/usr/bin/xray" {
		t.Errorf("first call should be xray -test, was %+v", r.calls[0])
	}
}

func TestRestart_HappyPath(t *testing.T) {
	r := newFake(
		fakeResponse{out: []byte("Configuration OK.")},
		fakeResponse{out: []byte("")},
	)
	m := newManager(r)
	if err := m.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(r.calls))
	}
	if r.calls[1].Name != "/etc/init.d/xray" || r.calls[1].Args[0] != "restart" {
		t.Errorf("second call should be init.d restart, got %+v", r.calls[1])
	}
}

func TestStartStop(t *testing.T) {
	r := newFake(fakeResponse{}, fakeResponse{})
	m := newManager(r)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if r.calls[0].Args[0] != "start" || r.calls[1].Args[0] != "stop" {
		t.Errorf("unexpected actions: %+v", r.calls)
	}
}

func TestStatus_Running(t *testing.T) {
	r := newFake(fakeResponse{out: []byte("running")})
	m := newManager(r)
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
	r := newFake(fakeResponse{out: []byte("inactive"), err: exitErr(t)})
	m := newManager(r)
	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Running {
		t.Errorf("expected not running")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
