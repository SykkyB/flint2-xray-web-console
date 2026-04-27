package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"sync"
	"sync/atomic"
	"testing"

	"flint2-xray-web-console/internal/runner"
	"flint2-xray-web-console/internal/service"
	"flint2-xray-web-console/internal/xray"
)

// TestState_CacheHit verifies that a second /api/state request within
// stateCacheTTL reuses the cached payload — the inner status shell-out
// must not run twice in close succession.
func TestState_CacheHit(t *testing.T) {
	srv, _ := newTestServer(t)
	var statusCalls int32
	srv.Service = &service.Manager{
		InitScript: srv.Cfg.XrayInit,
		XrayBin:    srv.Cfg.XrayBin,
		ConfigPath: srv.ConfPath,
		Run: runner.CombinedFunc(func(ctx context.Context, name string, args ...string) ([]byte, error) {
			atomic.AddInt32(&statusCalls, 1)
			return []byte("running"), nil
		}),
	}

	h := srv.Handler()
	for i := 0; i < 4; i++ {
		w := doAuthedRequest(h, "GET", "/api/state")
		if w.Code != nethttp.StatusOK {
			t.Fatalf("iter %d: code=%d", i, w.Code)
		}
	}
	if got := atomic.LoadInt32(&statusCalls); got != 1 {
		t.Errorf("service.Status shell-out ran %d times across 4 cached requests, want 1", got)
	}
}

// TestState_SingleFlight runs many concurrent /api/state requests and
// verifies they collapse into ONE refresh.
func TestState_SingleFlight(t *testing.T) {
	srv, _ := newTestServer(t)

	// Block the first refresh on a channel so the rest pile up as
	// waiters; otherwise the first might complete before the others
	// even start.
	release := make(chan struct{})
	var statusCalls int32
	srv.Service = &service.Manager{
		InitScript: srv.Cfg.XrayInit,
		XrayBin:    srv.Cfg.XrayBin,
		ConfigPath: srv.ConfPath,
		Run: runner.CombinedFunc(func(ctx context.Context, name string, args ...string) ([]byte, error) {
			atomic.AddInt32(&statusCalls, 1)
			<-release
			return []byte("running"), nil
		}),
	}

	h := srv.Handler()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := doAuthedRequest(h, "GET", "/api/state")
			if w.Code != nethttp.StatusOK {
				t.Errorf("code=%d", w.Code)
			}
		}()
	}
	// Give the goroutines time to all enter handleState. The first one
	// will be holding `pending`; the rest will be parked on slot.done.
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&statusCalls); got != 1 {
		t.Errorf("8 concurrent /api/state collapsed to %d refreshes, want 1", got)
	}
}

// TestState_InvalidateOnMutate ensures that after a mutation (which
// calls invalidateState) the next /api/state actually re-reads disk.
func TestState_InvalidateOnMutate(t *testing.T) {
	srv, _ := newTestServer(t)

	var keyCalls int32
	srv.Keys = &xray.KeyTool{
		XrayBin: srv.Cfg.XrayBin,
		Run: runner.CombinedFunc(func(ctx context.Context, name string, args ...string) ([]byte, error) {
			atomic.AddInt32(&keyCalls, 1)
			return []byte("Public key: DERIVED_PUBKEY\n"), nil
		}),
	}

	h := srv.Handler()

	// First call: warms both pubKey cache and state cache.
	w := doAuthedRequest(h, "GET", "/api/state")
	if w.Code != nethttp.StatusOK {
		t.Fatalf("warm: code=%d", w.Code)
	}
	var first stateResponse
	_ = json.Unmarshal(w.Body.Bytes(), &first)
	if len(first.Clients) != 2 {
		t.Fatalf("warm: clients=%d", len(first.Clients))
	}

	// Invalidate (simulates what mutateConfig does post-restart) and
	// also drop the pubkey cache so we can observe a re-derive on the
	// next state read.
	srv.invalidateState()
	srv.InvalidatePublicKey()

	w = doAuthedRequest(h, "GET", "/api/state")
	if w.Code != nethttp.StatusOK {
		t.Fatalf("after invalidate: code=%d", w.Code)
	}
	if got := atomic.LoadInt32(&keyCalls); got != 2 {
		t.Errorf("key derivation ran %d times across two state reads with invalidation, want 2", got)
	}
}
