// Package service wraps the xray procd init script and the `xray -test`
// config-validation subcommand. Nothing in this package mutates the xray
// config itself — it only starts/stops/restarts the service and asks
// xray whether a given config file would load.
//
// The exec-ing is routed through a pluggable runner.Runner so tests can
// exercise the package without actually invoking init.d.
package service

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"flint2-xray-web-console/internal/runner"
)

// Manager drives `/etc/init.d/xray` and `xray -test`. Paths come from
// the panel config and are not hardcoded.
type Manager struct {
	InitScript string // e.g. /etc/init.d/xray
	XrayBin    string // e.g. /usr/bin/xray
	ConfigPath string // e.g. /etc/xray/config.json

	// Timeout applies to each shell-out. Zero means no per-call timeout.
	Timeout time.Duration

	// Run is the command executor. Defaults to runner.Exec.
	Run runner.Runner
}

// State is a coarse view of the service: either it's running or it isn't,
// plus whatever the init script printed so the UI can show raw output.
type State struct {
	Running bool
	Raw     string
}

// Validate runs `xray -test -config <path>`. If path is empty, the
// Manager's ConfigPath is used. Returns nil if xray accepts the config;
// otherwise returns an error whose message is xray's stderr (trimmed).
//
// This is the guard we run before every restart: if validation fails we
// leave the currently-running xray alone.
func (m *Manager) Validate(ctx context.Context, path string) error {
	if path == "" {
		path = m.ConfigPath
	}
	ctx, cancel := m.withTimeout(ctx)
	defer cancel()
	stdout, stderr, err := m.runner().Run(ctx, m.XrayBin, "-test", "-config", path)
	if err != nil {
		return fmt.Errorf("xray -test rejected %s: %s", path, firstLine(combine(stdout, stderr)))
	}
	return nil
}

// Status shells out to `/etc/init.d/xray status`. Procd init scripts
// report "running" or "inactive" on stdout; we also treat a zero exit
// code as running for init scripts that skip the text.
func (m *Manager) Status(ctx context.Context) (State, error) {
	ctx, cancel := m.withTimeout(ctx)
	defer cancel()
	stdout, stderr, err := m.runner().Run(ctx, m.InitScript, "status")
	raw := strings.TrimSpace(string(combine(stdout, stderr)))
	if err != nil {
		// Non-zero exit typically means "not running"; surface raw so the
		// UI can show whatever procd said.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return State{Running: false, Raw: raw}, nil
		}
		return State{Raw: raw}, fmt.Errorf("status: %w", err)
	}
	running := strings.Contains(strings.ToLower(raw), "running") || raw == ""
	return State{Running: running, Raw: raw}, nil
}

// Start invokes `/etc/init.d/xray start`.
func (m *Manager) Start(ctx context.Context) error {
	return m.initAction(ctx, "start")
}

// Stop invokes `/etc/init.d/xray stop`.
func (m *Manager) Stop(ctx context.Context) error {
	return m.initAction(ctx, "stop")
}

// Restart validates ConfigPath first via `xray -test`, then invokes
// `/etc/init.d/xray restart`. If validation fails the running service
// is left untouched — the whole point is to never kill a working VPN
// with a broken config.
func (m *Manager) Restart(ctx context.Context) error {
	if err := m.Validate(ctx, ""); err != nil {
		return fmt.Errorf("pre-restart validation failed: %w", err)
	}
	return m.initAction(ctx, "restart")
}

// RestartWithoutValidation skips the `xray -test` guard. Reserved for
// cases where the caller has already validated (or knows the config
// wasn't touched); the normal path is Restart.
func (m *Manager) RestartWithoutValidation(ctx context.Context) error {
	return m.initAction(ctx, "restart")
}

func (m *Manager) initAction(ctx context.Context, action string) error {
	ctx, cancel := m.withTimeout(ctx)
	defer cancel()
	stdout, stderr, err := m.runner().Run(ctx, m.InitScript, action)
	if err != nil {
		return fmt.Errorf("%s: %s: %w", action, firstLine(combine(stdout, stderr)), err)
	}
	return nil
}

func (m *Manager) runner() runner.Runner {
	if m.Run != nil {
		return m.Run
	}
	return runner.Exec{}
}

func (m *Manager) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if m.Timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, m.Timeout)
}

// combine returns stderr if non-empty, else stdout. Most external tools
// that we care about print diagnostics on stderr; falling back to stdout
// covers init scripts that mix the two.
func combine(stdout, stderr []byte) []byte {
	if len(stderr) > 0 {
		return stderr
	}
	return stdout
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if s == "" {
		return "(no output)"
	}
	return s
}
