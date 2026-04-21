package xray

import (
	"fmt"

	"github.com/google/uuid"
)

// PrimaryInbound returns the VLESS inbound the panel edits. The current
// design assumes exactly one VLESS+Reality inbound (matching the stock
// Flint 2 config); if that assumption ever breaks we'll want an explicit
// selector instead of this helper.
func (f *File) PrimaryInbound() (*Inbound, error) {
	for idx := range f.Inbounds {
		in := &f.Inbounds[idx]
		if in.Protocol == "vless" {
			return in, nil
		}
	}
	return nil, fmt.Errorf("no VLESS inbound found in config")
}

// AddClient appends a new client with a fresh UUID and the given name/flow
// to the primary inbound. Returns the created Client so the caller can
// render the vless:// link right away. The config file is not written here;
// the caller owns persistence.
func (f *File) AddClient(name, flow string) (*Client, error) {
	in, err := f.PrimaryInbound()
	if err != nil {
		return nil, err
	}
	if in.Settings == nil {
		in.Settings = &Settings{}
	}
	for _, c := range in.Settings.Clients {
		if c.Email != "" && c.Email == name {
			return nil, fmt.Errorf("client with name %q already exists", name)
		}
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("generate uuid: %w", err)
	}
	c := Client{
		ID:    id.String(),
		Flow:  flow,
		Email: name,
	}
	in.Settings.Clients = append(in.Settings.Clients, c)
	return &in.Settings.Clients[len(in.Settings.Clients)-1], nil
}

// FindClient returns a pointer to the client with the given UUID in the
// primary inbound, or nil if not found.
func (f *File) FindClient(id string) *Client {
	in, err := f.PrimaryInbound()
	if err != nil {
		return nil
	}
	if in.Settings == nil {
		return nil
	}
	for idx := range in.Settings.Clients {
		if in.Settings.Clients[idx].ID == id {
			return &in.Settings.Clients[idx]
		}
	}
	return nil
}

// RemoveClient drops the client with the given UUID from the primary
// inbound. Returns the removed client (for possible stashing in the
// disabled store), or an error if it wasn't present.
func (f *File) RemoveClient(id string) (*Client, error) {
	in, err := f.PrimaryInbound()
	if err != nil {
		return nil, err
	}
	if in.Settings == nil {
		return nil, fmt.Errorf("client %s not found", id)
	}
	for idx, c := range in.Settings.Clients {
		if c.ID == id {
			removed := c
			in.Settings.Clients = append(in.Settings.Clients[:idx], in.Settings.Clients[idx+1:]...)
			return &removed, nil
		}
	}
	return nil, fmt.Errorf("client %s not found", id)
}
