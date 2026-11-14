package main

import (
	"context"
	"fmt"

	"github.com/arldka/wxexport/internal/discover"
	"github.com/arldka/wxexport/internal/keys"
	"github.com/arldka/wxexport/internal/snapshot"
	"github.com/arldka/wxexport/internal/wcdb"
)

// openStore resolves keys, takes a consistent read-only snapshot of the core databases
// (contact, session, message shards), decrypts them, and opens a wcdb.Store over the plaintext
// copies. The caller must call the returned cleanup func (which removes the plaintext work dir)
// when done, typically via defer.
func openStore(ctx context.Context, e *env) (*wcdb.Store, func(), error) {
	c, inv, err := loadKeys(e)
	if err != nil {
		return nil, nil, err
	}
	core := discover.CoreDBs(inv)
	ks, _, err := keys.Resolve(c, core)
	if err != nil {
		return nil, nil, err
	}

	wd, err := snapshot.NewWorkDir()
	if err != nil {
		return nil, nil, err
	}
	cleanupWD := func() { wd.Cleanup() }

	snap, err := snapshot.Take(ctx, wd, core, ks, snapshot.Options{})
	if err != nil {
		cleanupWD()
		return nil, nil, fmt.Errorf("snapshot: %w", err)
	}

	store, err := wcdb.Open(e.account.Wxid, snap.Files)
	if err != nil {
		cleanupWD()
		return nil, nil, err
	}
	cleanup := func() {
		store.Close()
		cleanupWD()
	}
	return store, cleanup, nil
}
