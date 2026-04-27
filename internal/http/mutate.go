package http

import (
	"context"
	"fmt"

	"flint2-xray-web-console/internal/xray"
)

// mutateConfig is the one spot that rewrites /etc/xray/config.json. It
// serialises all writes behind s.writeMu, so two concurrent requests
// can't produce interleaved config states, and it calls Restart (which
// runs `xray -test` first) so the on-disk config and the running xray
// stay in sync.
//
// The flow:
//  1. Read the current config.
//  2. Call fn to mutate it in place. fn returns an error to abort.
//  3. Write the new config atomically (tmp + rename, with .bak of the
//     previous contents).
//  4. Restart xray. If restart fails the new config is already on disk;
//     the error bubbles up so the caller can surface it, but the
//     previous running instance is untouched because xray -test would
//     have failed the pre-restart check in that case. If xray -test
//     passed but the init script still failed, we leave the new config
//     in place — the next restart will pick it up.
func (s *Server) mutateConfig(ctx context.Context, fn func(*xray.File) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	f, err := xray.Read(s.ConfPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	if err := fn(f); err != nil {
		return err
	}
	if err := xray.Write(s.ConfPath, f); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := s.Service.Restart(ctx); err != nil {
		return fmt.Errorf("restart xray: %w", err)
	}
	// Drop the cached /api/state so the next poll reflects the mutation
	// instead of waiting up to stateCacheTTL.
	s.invalidateState()
	return nil
}
