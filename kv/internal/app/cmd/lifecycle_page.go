// Package cmd -- the maintenance-page swap behind kv hibernate / kv wake
// (2026-09-10 spec §7).
//
// IMPORTANT: unlike the site.hcl flags, this is NOT protected by the
// "config is the guard" property. The SPA is published to S3 by
// .github/workflows/build-voice.yml, not by terraform, so a build landing
// during hibernation would sync the real index.html straight back over the
// maintenance page with no failure anywhere. build-voice.yml carries a
// `hibernated` check for exactly that reason -- if that check is ever
// removed, this swap silently stops holding.
//
// The real SPA is shadowed, never destroyed: index.html is copied to
// index.spa.html first, and restored from there at wake.
package cmd

import (
	"context"
	"fmt"
	"io"
)

// Object keys and headers for the swap. IndexCacheControl mirrors what
// build-voice.yml uploads index.html with -- the shell is not
// content-hashed, so it must stay uncacheable or a stale shadowed SPA keeps
// being served from the edge.
const (
	IndexKey          = "index.html"
	SPABackupKey      = "index.spa.html"
	IndexCacheControl = "no-cache, no-store, must-revalidate"
	IndexContentType  = "text/html"
)

// PageStoreAPI is the narrow S3 seam the page swap runs through, so the
// orchestration is testable without touching the cloud -- matching how
// every other lifecycle seam in this package is shaped.
type PageStoreAPI interface {
	CopyObject(ctx context.Context, bucket, srcKey, dstKey string) error
	PutObject(ctx context.Context, bucket, key string, body []byte, cacheControl, contentType string) error
	DeleteObject(ctx context.Context, bucket, key string) error
	HeadObject(ctx context.Context, bucket, key string) (exists bool, err error)
}

// SwapToMaintenance saves the live SPA shell aside (unless a save is
// already there from an interrupted run) and puts the maintenance page in
// its place.
func SwapToMaintenance(ctx context.Context, api PageStoreAPI, bucket string, page []byte, w io.Writer) error {
	backupExists, err := api.HeadObject(ctx, bucket, SPABackupKey)
	if err != nil {
		return fmt.Errorf("head %s: %w", SPABackupKey, err)
	}

	if backupExists {
		// An earlier run already saved the real SPA. index.html now holds
		// the maintenance page, so copying it over the backup would destroy
		// the only remaining copy of the shell.
		fmt.Fprintf(w, "  %s already saved from an earlier run -- not re-copying\n", SPABackupKey)
	} else {
		if err := api.CopyObject(ctx, bucket, IndexKey, SPABackupKey); err != nil {
			return fmt.Errorf("save %s to %s: %w", IndexKey, SPABackupKey, err)
		}
		fmt.Fprintf(w, "  saved %s -> %s\n", IndexKey, SPABackupKey)
	}

	if err := api.PutObject(ctx, bucket, IndexKey, page, IndexCacheControl, IndexContentType); err != nil {
		return fmt.Errorf("put maintenance page at %s: %w", IndexKey, err)
	}
	fmt.Fprintf(w, "  put maintenance page at %s\n", IndexKey)
	return nil
}

// RestoreSPA puts the saved shell back and clears the shadow copy. With no
// saved copy present it is a reported no-op -- never a blanking write.
func RestoreSPA(ctx context.Context, api PageStoreAPI, bucket string, w io.Writer) error {
	backupExists, err := api.HeadObject(ctx, bucket, SPABackupKey)
	if err != nil {
		return fmt.Errorf("head %s: %w", SPABackupKey, err)
	}
	if !backupExists {
		fmt.Fprintf(w, "  no %s present -- leaving %s untouched\n", SPABackupKey, IndexKey)
		return nil
	}

	if err := api.CopyObject(ctx, bucket, SPABackupKey, IndexKey); err != nil {
		return fmt.Errorf("restore %s from %s: %w", IndexKey, SPABackupKey, err)
	}
	if err := api.DeleteObject(ctx, bucket, SPABackupKey); err != nil {
		return fmt.Errorf("delete %s: %w", SPABackupKey, err)
	}
	fmt.Fprintf(w, "  restored %s from %s\n", IndexKey, SPABackupKey)
	return nil
}
