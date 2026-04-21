package xray

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Runner is the command executor used for shelling out to the xray binary.
// Tests inject a fake; production code passes DefaultRunner.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// DefaultRunner shells out via os/exec.
func DefaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// KeyTool wraps `xray x25519`. Its zero value uses DefaultRunner and no
// per-call timeout.
type KeyTool struct {
	XrayBin string
	Run     Runner
	Timeout time.Duration
}

// Keypair is one X25519 private/public key pair, both base64-encoded.
type Keypair struct {
	Private string
	Public  string
}

// Generate runs `xray x25519` with no arguments and returns the generated
// keypair. Use GenerateAndSet from a higher layer to persist the result.
func (k *KeyTool) Generate(ctx context.Context) (Keypair, error) {
	out, err := k.exec(ctx)
	if err != nil {
		return Keypair{}, fmt.Errorf("xray x25519: %w: %s", err, strings.TrimSpace(string(out)))
	}
	priv, _ := extractKey(out, "Private key")
	pub, _ := extractKey(out, "Public key")
	if priv == "" || pub == "" {
		return Keypair{}, fmt.Errorf("xray x25519: could not parse output: %q", string(out))
	}
	return Keypair{Private: priv, Public: pub}, nil
}

// DerivePublic runs `xray x25519 -i <priv>` and returns the matching
// public key. It does not validate that priv is a legal X25519 scalar —
// xray itself will reject malformed input.
func (k *KeyTool) DerivePublic(ctx context.Context, priv string) (string, error) {
	if priv == "" {
		return "", fmt.Errorf("private key is empty")
	}
	out, err := k.exec(ctx, "-i", priv)
	if err != nil {
		return "", fmt.Errorf("xray x25519 -i: %w: %s", err, strings.TrimSpace(string(out)))
	}
	pub, _ := extractKey(out, "Public key")
	if pub == "" {
		return "", fmt.Errorf("xray x25519 -i: could not parse output: %q", string(out))
	}
	return pub, nil
}

func (k *KeyTool) exec(ctx context.Context, extra ...string) ([]byte, error) {
	if k.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, k.Timeout)
		defer cancel()
	}
	run := k.Run
	if run == nil {
		run = DefaultRunner
	}
	args := append([]string{"x25519"}, extra...)
	return run(ctx, k.XrayBin, args...)
}

// keyLineRe matches lines like "Public key: <base64>" produced by xray's
// x25519 subcommand. Labels vary between xray versions (sometimes with
// extra lines like "Password: …") so we match each label independently.
var keyLineRe = regexp.MustCompile(`(?mi)^\s*(Private key|Public key)\s*:\s*(\S+)\s*$`)

func extractKey(out []byte, label string) (string, bool) {
	want := strings.ToLower(label)
	for _, m := range keyLineRe.FindAllStringSubmatch(string(out), -1) {
		if strings.EqualFold(m[1], want) {
			return m[2], true
		}
	}
	return "", false
}
