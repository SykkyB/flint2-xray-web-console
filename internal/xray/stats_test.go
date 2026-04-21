package xray

import (
	"context"
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
