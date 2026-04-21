package xray

import (
	"context"
	"strings"
	"testing"
)

func fakeRunner(out string, err error) Runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(out), err
	}
}

func TestGenerate(t *testing.T) {
	out := "Private key: cDABCDEFGH\nPublic key: EGabcdEFGH\n"
	k := &KeyTool{XrayBin: "/usr/bin/xray", Run: fakeRunner(out, nil)}
	kp, err := k.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if kp.Private != "cDABCDEFGH" || kp.Public != "EGabcdEFGH" {
		t.Errorf("parse: got %+v", kp)
	}
}

func TestGenerate_ExtraLines(t *testing.T) {
	// Some xray versions emit additional labels between private/public.
	out := "Private key: priv-xyz\nPassword: pw\nHash32: h\nPublic key: pub-xyz\n"
	k := &KeyTool{XrayBin: "/usr/bin/xray", Run: fakeRunner(out, nil)}
	kp, err := k.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if kp.Private != "priv-xyz" || kp.Public != "pub-xyz" {
		t.Errorf("parse: got %+v", kp)
	}
}

func TestGenerate_Xray26Format(t *testing.T) {
	// xray v26 dropped the space in "PrivateKey:" and renamed the public
	// key line to "Password (PublicKey):" alongside a new Hash32 line.
	out := "PrivateKey: priv-xyz\nPassword (PublicKey): pub-xyz\nHash32: deadbeef\n"
	k := &KeyTool{XrayBin: "/usr/bin/xray", Run: fakeRunner(out, nil)}
	kp, err := k.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if kp.Private != "priv-xyz" || kp.Public != "pub-xyz" {
		t.Errorf("parse: got %+v", kp)
	}
}

func TestGenerate_Unparseable(t *testing.T) {
	k := &KeyTool{XrayBin: "/usr/bin/xray", Run: fakeRunner("something totally different", nil)}
	if _, err := k.Generate(context.Background()); err == nil {
		t.Errorf("expected parse error")
	}
}

func TestDerivePublic_CallArgs(t *testing.T) {
	var gotArgs []string
	k := &KeyTool{
		XrayBin: "/usr/bin/xray",
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			gotArgs = args
			return []byte("Public key: DERIVED"), nil
		},
	}
	pub, err := k.DerivePublic(context.Background(), "SOME_PRIV")
	if err != nil {
		t.Fatalf("DerivePublic: %v", err)
	}
	if pub != "DERIVED" {
		t.Errorf("pub: got %q", pub)
	}
	want := []string{"x25519", "-i", "SOME_PRIV"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Errorf("args: got %v, want %v", gotArgs, want)
	}
}

func TestDerivePublic_EmptyPriv(t *testing.T) {
	k := &KeyTool{XrayBin: "/usr/bin/xray"}
	if _, err := k.DerivePublic(context.Background(), ""); err == nil {
		t.Errorf("expected error for empty priv")
	}
}
