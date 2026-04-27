package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFakeMatchesPrefix(t *testing.T) {
	f := &Fake{}
	f.On(FakeCall{Match: "/bin/foo --version", Stdout: "1.2.3"})
	f.On(FakeCall{Match: "/bin/foo run", Stderr: "boom", Err: errors.New("exit 1")})

	stdout, _, err := f.Run(context.Background(), "/bin/foo", "--version")
	if err != nil || string(stdout) != "1.2.3" {
		t.Fatalf("version branch: stdout=%q err=%v", stdout, err)
	}

	_, stderr, err := f.Run(context.Background(), "/bin/foo", "run", "--once")
	if err == nil || string(stderr) != "boom" {
		t.Fatalf("run branch: stderr=%q err=%v", stderr, err)
	}

	if got := f.Executed(); len(got) != 2 || !strings.HasPrefix(got[1], "/bin/foo run") {
		t.Errorf("executed: %#v", got)
	}
}

func TestFakeUnmatchedFails(t *testing.T) {
	f := &Fake{}
	f.On(FakeCall{Match: "/bin/foo", Stdout: "ok"})
	_, _, err := f.Run(context.Background(), "/bin/bar")
	if err == nil {
		t.Errorf("unmatched call should fail")
	}
}
