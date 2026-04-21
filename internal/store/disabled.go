// Package store persists xray clients that the panel has disabled. A
// disabled client is held here instead of in the live xray config, so
// xray doesn't see the UUID and the link can't authenticate; re-enabling
// restores the exact same Client verbatim (same UUID, same flow).
//
// The file format is a small JSON document kept under panel.yaml's
// disabled_store path. Writes are atomic (tmp + rename) and keep a .bak
// of the previous contents, same strategy as internal/xray.Write.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"flint2-xray-web-console/internal/xray"
)

// DisabledClient is an xray.Client plus the moment it was taken out of
// the live config. The timestamp is informational — the panel UI shows
// it, but nothing depends on it.
type DisabledClient struct {
	Client     xray.Client `json:"client"`
	DisabledAt time.Time   `json:"disabledAt"`
}

// disabledFile is the on-disk shape.
type disabledFile struct {
	Version int              `json:"version"`
	Clients []DisabledClient `json:"clients"`
}

// Disabled is the disabled-clients store. Zero value is a usable
// empty-backed store; use New for a real file.
type Disabled struct {
	Path string
}

// New returns a store backed by the given path. If the file does not
// exist it is created on the first write; Read from a missing file
// returns an empty list without error.
func New(path string) *Disabled {
	return &Disabled{Path: path}
}

// List returns all currently-disabled clients. A missing file is not
// an error and yields an empty slice.
func (d *Disabled) List() ([]DisabledClient, error) {
	raw, err := os.ReadFile(d.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read disabled store: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var f disabledFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse disabled store: %w", err)
	}
	return f.Clients, nil
}

// FindByID returns the disabled client matching id, or (nil, nil) if
// there is no such client.
func (d *Disabled) FindByID(id string) (*DisabledClient, error) {
	all, err := d.List()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Client.ID == id {
			return &all[i], nil
		}
	}
	return nil, nil
}

// Add appends c to the store. Returns an error if the same UUID is
// already present — disabling an already-disabled client is a bug.
func (d *Disabled) Add(c xray.Client) error {
	all, err := d.List()
	if err != nil {
		return err
	}
	for _, existing := range all {
		if existing.Client.ID == c.ID {
			return fmt.Errorf("client %s is already disabled", c.ID)
		}
	}
	all = append(all, DisabledClient{Client: c, DisabledAt: time.Now().UTC()})
	return d.write(all)
}

// Remove deletes the client with the given id and returns it. If no
// such client is present, returns (nil, nil) — the caller decides
// whether that's an error in context.
func (d *Disabled) Remove(id string) (*xray.Client, error) {
	all, err := d.List()
	if err != nil {
		return nil, err
	}
	for i, existing := range all {
		if existing.Client.ID == id {
			removed := existing.Client
			all = append(all[:i], all[i+1:]...)
			if err := d.write(all); err != nil {
				return nil, err
			}
			return &removed, nil
		}
	}
	return nil, nil
}

func (d *Disabled) write(clients []DisabledClient) error {
	if err := os.MkdirAll(filepath.Dir(d.Path), 0o700); err != nil {
		return fmt.Errorf("mkdir for disabled store: %w", err)
	}
	if clients == nil {
		clients = []DisabledClient{}
	}
	out, err := json.MarshalIndent(disabledFile{Version: 1, Clients: clients}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal disabled store: %w", err)
	}
	if err := backupIfExists(d.Path); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(d.Path), ".disabled-*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := os.Rename(tmpName, d.Path); err != nil {
		return fmt.Errorf("rename tmp: %w", err)
	}
	return nil
}

func backupIfExists(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read existing for backup: %w", err)
	}
	return os.WriteFile(path+".bak", src, 0o600)
}
