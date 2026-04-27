package http

import (
	"context"
	"fmt"
	nethttp "net/http"
	"sync"
	"time"

	"flint2-xray-web-console/internal/xray"
)

// stateCacheTTL bounds how long a /api/state response may be reused
// across concurrent pollers. The probe cost is dominated by reading
// /etc/xray/config.json, deriving the Reality public key (cached in
// s.pubKey but not always populated), reading the disabled store, and
// shelling out to /etc/init.d/xray status (procd). With multiple
// browser tabs polling we want at most ~1 actual probe set every 3s.
const stateCacheTTL = 3 * time.Second

// stateCache + stateRefreshSlot form a tiny single-flight cache around
// handleState. Multiple concurrent /api/state requests:
//   - return the cached payload if it's still fresh
//   - otherwise, exactly ONE goroutine refreshes; the rest wait on the
//     refresh's done channel and then return the same fresh payload.
//
// This collapses the per-request file reads + procd shell-out into one
// per ~3s no matter how many browser tabs are polling.
type stateCacheEntry struct {
	payload stateResponse
	at      time.Time
}

type stateRefreshSlot struct {
	done    chan struct{}
	payload stateResponse
}

type stateCache struct {
	mu      sync.Mutex
	entry   *stateCacheEntry
	pending *stateRefreshSlot
}

// invalidateState drops the cached /api/state payload so the next call
// will refresh from disk. Called from mutateConfig after the xray
// service is restarted, so post-mutation pollers see the new state
// immediately instead of waiting up to stateCacheTTL.
func (s *Server) invalidateState() {
	if s.stateCache == nil {
		return
	}
	s.stateCache.mu.Lock()
	s.stateCache.entry = nil
	s.stateCache.mu.Unlock()
}

func (s *Server) handleState(w nethttp.ResponseWriter, r *nethttp.Request) {
	c := s.stateCache
	if c == nil {
		// Defensive: a Server constructed without going through Handler()
		// has no cache, so collect inline rather than crashing.
		writeJSON(w, nethttp.StatusOK, s.collectState(r.Context()))
		return
	}

	c.mu.Lock()
	if c.entry != nil && time.Since(c.entry.at) < stateCacheTTL {
		payload := c.entry.payload
		c.mu.Unlock()
		writeJSON(w, nethttp.StatusOK, payload)
		return
	}
	if c.pending != nil {
		// Someone is already refreshing — wait for it.
		slot := c.pending
		c.mu.Unlock()
		<-slot.done
		writeJSON(w, nethttp.StatusOK, slot.payload)
		return
	}
	// We're the refresher.
	slot := &stateRefreshSlot{done: make(chan struct{})}
	c.pending = slot
	c.mu.Unlock()

	// Cap the whole probe set with a shared deadline. Detach from the
	// request context so a client closing its connection mid-refresh
	// doesn't poison the result that other waiters are also expecting.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	payload := s.collectState(ctx)

	c.mu.Lock()
	c.entry = &stateCacheEntry{payload: payload, at: time.Now()}
	c.pending = nil
	slot.payload = payload
	c.mu.Unlock()
	close(slot.done)

	writeJSON(w, nethttp.StatusOK, payload)
}

// collectState gathers a fresh stateResponse from disk and the service
// manager. Pure read-side; safe to call concurrently if the caller
// arranges its own single-flight (handleState does).
func (s *Server) collectState(ctx context.Context) stateResponse {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	f, err := xray.Read(s.ConfPath)
	if err != nil {
		return stateResponse{
			ServerAddress: s.Cfg.ServerAddress,
			Warnings:      []string{fmt.Sprintf("read xray config: %v", err)},
		}
	}
	in, err := f.PrimaryInbound()
	if err != nil {
		return stateResponse{
			ServerAddress: s.Cfg.ServerAddress,
			Warnings:      []string{err.Error()},
		}
	}

	resp := stateResponse{
		ServerAddress: s.Cfg.ServerAddress,
		Server: serverBlock{
			Listen: in.Listen,
			Port:   parsePort(in.Port),
			Flow:   primaryFlow(in),
		},
		StatsAPIEnabled:       f.API != nil && f.Stats != nil,
		OnlineTrackingEnabled: policyHasOnlineFlag(f.Policy),
	}

	if in.StreamSettings != nil && in.StreamSettings.RealitySettings != nil {
		rs := in.StreamSettings.RealitySettings
		rb := realityBlock{
			Dest:        rs.Dest,
			ServerNames: rs.ServerNames,
			ShortIDs:    rs.ShortIDs,
			Fingerprint: rs.Fingerprint,
			HasPrivate:  rs.PrivateKey != "",
		}
		if rs.PrivateKey != "" {
			pub, err := s.publicKey(ctx, rs.PrivateKey)
			if err != nil {
				resp.Warnings = append(resp.Warnings, fmt.Sprintf("derive public key: %v", err))
			} else {
				rb.PublicKey = pub
			}
		}
		resp.Server.Reality = rb
	}

	if in.Settings != nil {
		for _, c := range in.Settings.Clients {
			resp.Clients = append(resp.Clients, clientBlock{
				ID:   c.ID,
				Name: c.Email,
				Flow: c.Flow,
			})
		}
	}

	if s.Disabled != nil {
		disabled, err := s.Disabled.List()
		if err != nil {
			resp.Warnings = append(resp.Warnings, fmt.Sprintf("disabled store: %v", err))
		} else {
			for _, d := range disabled {
				resp.Disabled = append(resp.Disabled, disabledClientBlock{
					ID:         d.Client.ID,
					Name:       d.Client.Email,
					Flow:       d.Client.Flow,
					DisabledAt: d.DisabledAt,
				})
			}
		}
	}

	st, err := s.Service.Status(ctx)
	if err != nil {
		resp.Warnings = append(resp.Warnings, fmt.Sprintf("service status: %v", err))
	} else {
		resp.Service = st
	}

	return resp
}
