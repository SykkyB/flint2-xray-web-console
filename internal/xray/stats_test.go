package xray

import (
	"context"
	"strings"
	"testing"
)

func TestParseStats_StringValues(t *testing.T) {
	raw := []byte(`{
	  "stat": [
	    {"name": "user>>>alice>>>traffic>>>uplink", "value": "1024"},
	    {"name": "user>>>alice>>>traffic>>>downlink", "value": "4096"},
	    {"name": "user>>>bob>>>traffic>>>uplink", "value": "0"},
	    {"name": "something-else", "value": "999"}
	  ]
	}`)
	stats, err := parseStats(raw)
	if err != nil {
		t.Fatalf("parseStats: %v", err)
	}
	byEmail := map[string]UserStats{}
	for _, s := range stats {
		byEmail[s.Email] = s
	}
	if byEmail["alice"].Uplink != 1024 || byEmail["alice"].Downlink != 4096 {
		t.Errorf("alice: %+v", byEmail["alice"])
	}
	if byEmail["bob"].Uplink != 0 {
		t.Errorf("bob: %+v", byEmail["bob"])
	}
	if _, ok := byEmail["something-else"]; ok {
		t.Errorf("non-user entry should be skipped")
	}
}

func TestParseStats_NumericValues(t *testing.T) {
	raw := []byte(`{"stat":[{"name":"user>>>alice>>>traffic>>>uplink","value":1234}]}`)
	stats, _ := parseStats(raw)
	if len(stats) != 1 || stats[0].Uplink != 1234 {
		t.Errorf("numeric values: %+v", stats)
	}
}

func TestParseStats_Empty(t *testing.T) {
	stats, err := parseStats([]byte(""))
	if err != nil || len(stats) != 0 {
		t.Errorf("empty: stats=%+v err=%v", stats, err)
	}
}

func TestQueryUsers_Args(t *testing.T) {
	var gotArgs []string
	c := &StatsClient{
		XrayBin: "/usr/bin/xray",
		Server:  "127.0.0.1:10085",
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			gotArgs = args
			return []byte(`{"stat":[]}`), nil
		},
	}
	if _, err := c.QueryUsers(context.Background()); err != nil {
		t.Fatalf("QueryUsers: %v", err)
	}
	want := []string{"api", "statsquery", "-server", "127.0.0.1:10085", "-pattern", "user>>>"}
	if len(gotArgs) != len(want) {
		t.Fatalf("args len: got %d want %d (%v)", len(gotArgs), len(want), gotArgs)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("args[%d]: got %q want %q", i, gotArgs[i], want[i])
		}
	}
}

func TestQueryUsers_NoServer(t *testing.T) {
	c := &StatsClient{XrayBin: "/usr/bin/xray"}
	if _, err := c.QueryUsers(context.Background()); err == nil {
		t.Errorf("expected error when server is empty")
	}
}

func TestParseOnlineUsers_Empty(t *testing.T) {
	out, err := parseOnlineUsers([]byte("{}"))
	if err != nil || len(out) != 0 {
		t.Errorf("empty: got %+v err=%v", out, err)
	}
	out, err = parseOnlineUsers([]byte(""))
	if err != nil || len(out) != 0 {
		t.Errorf("blank: got %+v err=%v", out, err)
	}
}

func TestParseOnlineUsers_FlatMap(t *testing.T) {
	// Form we guess xray actually emits: flat {"email": count}.
	raw := []byte(`{"alice": 2, "bob": "1"}`)
	out, err := parseOnlineUsers(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := map[string]int{}
	for _, u := range out {
		got[u.Email] = u.Sessions
	}
	if got["alice"] != 2 || got["bob"] != 1 {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestParseOnlineUsers_WrapperObject(t *testing.T) {
	raw := []byte(`{"users": {"alice": 1}}`)
	out, err := parseOnlineUsers(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out) != 1 || out[0].Email != "alice" || out[0].Sessions != 1 {
		t.Errorf("unexpected: %+v", out)
	}
}

func TestParseOnlineUsers_WrapperArray(t *testing.T) {
	raw := []byte(`{"users": [{"email":"alice","count":3}]}`)
	out, err := parseOnlineUsers(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out) != 1 || out[0].Email != "alice" || out[0].Sessions != 3 {
		t.Errorf("unexpected: %+v", out)
	}
}

func TestParseOnlineUsers_WrapperStringArray(t *testing.T) {
	// Real xray v26 shape: array of `user>>>{email}>>>online` strings.
	// Duplicates count as multiple sessions; unrelated entries are skipped.
	raw := []byte(`{"users":["user>>>Poco F5 Pro>>>online","user>>>TestCli>>>online","user>>>Poco F5 Pro>>>online","garbage"]}`)
	out, err := parseOnlineUsers(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := map[string]int{}
	for _, u := range out {
		got[u.Email] = u.Sessions
	}
	if got["Poco F5 Pro"] != 2 || got["TestCli"] != 1 {
		t.Errorf("unexpected counts: %+v", got)
	}
	if len(got) != 2 {
		t.Errorf("unexpected rows: %+v", out)
	}
}

func TestResetUsers_Args(t *testing.T) {
	var gotArgs []string
	c := &StatsClient{
		XrayBin: "/usr/bin/xray",
		Server:  "127.0.0.1:10085",
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			gotArgs = args
			return []byte(``), nil
		},
	}
	if err := c.ResetUsers(context.Background()); err != nil {
		t.Fatalf("ResetUsers: %v", err)
	}
	want := []string{"api", "statsquery", "-server", "127.0.0.1:10085", "-pattern", "user>>>", "-reset"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Errorf("args: got %v want %v", gotArgs, want)
	}
}

func TestQueryOnline_Args(t *testing.T) {
	var gotArgs []string
	c := &StatsClient{
		XrayBin: "/usr/bin/xray",
		Server:  "127.0.0.1:10085",
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			gotArgs = args
			return []byte(`{}`), nil
		},
	}
	if _, err := c.QueryOnline(context.Background()); err != nil {
		t.Fatalf("QueryOnline: %v", err)
	}
	want := []string{"api", "statsgetallonlineusers", "--server=127.0.0.1:10085"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Errorf("args: got %v want %v", gotArgs, want)
	}
}
