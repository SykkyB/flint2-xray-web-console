package store

import (
	"path/filepath"
	"testing"

	"flint2-xray-web-console/internal/xray"
)

func TestList_MissingFile(t *testing.T) {
	d := New(filepath.Join(t.TempDir(), "nope.json"))
	got, err := d.List()
	if err != nil {
		t.Fatalf("List on missing file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list, got %d", len(got))
	}
}

func TestAdd_Remove_Roundtrip(t *testing.T) {
	d := New(filepath.Join(t.TempDir(), "disabled.json"))

	alice := xray.Client{ID: "11111111-1111-1111-1111-111111111111", Email: "alice", Flow: "xtls-rprx-vision"}
	bob := xray.Client{ID: "22222222-2222-2222-2222-222222222222", Email: "bob", Flow: "xtls-rprx-vision"}

	if err := d.Add(alice); err != nil {
		t.Fatalf("Add alice: %v", err)
	}
	if err := d.Add(bob); err != nil {
		t.Fatalf("Add bob: %v", err)
	}
	all, err := d.List()
	if err != nil || len(all) != 2 {
		t.Fatalf("List: got %d err=%v", len(all), err)
	}
	for _, c := range all {
		if c.DisabledAt.IsZero() {
			t.Errorf("DisabledAt not set for %s", c.Client.ID)
		}
	}

	if err := d.Add(alice); err == nil {
		t.Errorf("expected error on duplicate add")
	}

	got, err := d.FindByID(alice.ID)
	if err != nil || got == nil || got.Client.Email != "alice" {
		t.Errorf("FindByID alice: %+v err=%v", got, err)
	}
	if got, _ := d.FindByID("does-not-exist"); got != nil {
		t.Errorf("FindByID: expected nil")
	}

	removed, err := d.Remove(alice.ID)
	if err != nil || removed == nil || removed.ID != alice.ID {
		t.Fatalf("Remove alice: %+v err=%v", removed, err)
	}

	// Removing a non-existent client is a soft no-op, not an error.
	missing, err := d.Remove("does-not-exist")
	if err != nil || missing != nil {
		t.Errorf("Remove missing: got %+v err=%v", missing, err)
	}

	all, _ = d.List()
	if len(all) != 1 || all[0].Client.ID != bob.ID {
		t.Errorf("after remove: %+v", all)
	}
}

func TestWriteCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "disabled.json")
	d := New(path)
	if err := d.Add(xray.Client{ID: "x", Email: "x"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	all, _ := d.List()
	if len(all) != 1 {
		t.Errorf("round-trip failed")
	}
}
