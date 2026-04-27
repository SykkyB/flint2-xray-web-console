// Package runner abstracts shelling out to external commands so that
// every package that touches the OS (service, xray) is unit-testable:
// tests inject a Fake that replays canned outputs; production gets the
// real os/exec implementation.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Runner runs an external command and returns its stdout, stderr, and
// any error. Implementations MUST honor ctx cancellation.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// CombinedFunc adapts a 2-tuple closure (combined stdout+stderr, error)
// into a Runner. Tests that don't care about the stdout/stderr split
// can use it to keep their fixtures terse:
//
//	Run: runner.CombinedFunc(func(ctx context.Context, name string, args ...string) ([]byte, error) { ... })
//
// The closure's bytes are returned as stdout; stderr is always empty.
type CombinedFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

func (f CombinedFunc) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	out, err := f(ctx, name, args...)
	return out, nil, err
}

// Exec is the production Runner backed by os/exec.
type Exec struct{}

func (Exec) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	return so.Bytes(), se.Bytes(), err
}

// ExitErr is what a non-zero exit looks like to callers that care to
// distinguish "ran but failed" from "could not even start".
type ExitErr struct {
	Cmd      string
	ExitCode int
	Stderr   string
}

func (e *ExitErr) Error() string {
	return fmt.Sprintf("%s: exit %d: %s", e.Cmd, e.ExitCode, e.Stderr)
}

// AsExitErr unwraps any os/exec error into a typed ExitErr if possible.
// Returns (*ExitErr, true) on success.
func AsExitErr(err error) (*ExitErr, bool) {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &ExitErr{
			ExitCode: ee.ExitCode(),
			Stderr:   string(ee.Stderr),
		}, true
	}
	return nil, false
}
