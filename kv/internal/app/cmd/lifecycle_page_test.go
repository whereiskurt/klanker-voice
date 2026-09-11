package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

type fakePageStore struct {
	objects map[string]string // key -> marker for what is stored
	calls   []string
	headErr error
}

func newFakePageStore() *fakePageStore {
	return &fakePageStore{objects: map[string]string{}}
}

func (f *fakePageStore) CopyObject(ctx context.Context, bucket, srcKey, dstKey string) error {
	if _, ok := f.objects[srcKey]; !ok {
		return fmt.Errorf("no such key: %s", srcKey)
	}
	f.calls = append(f.calls, "copy:"+srcKey+"->"+dstKey)
	f.objects[dstKey] = f.objects[srcKey]
	return nil
}

func (f *fakePageStore) PutObject(ctx context.Context, bucket, key string, body []byte, cacheControl, contentType string) error {
	f.calls = append(f.calls, "put:"+key+":"+cacheControl)
	f.objects[key] = string(body)
	return nil
}

func (f *fakePageStore) DeleteObject(ctx context.Context, bucket, key string) error {
	f.calls = append(f.calls, "delete:"+key)
	delete(f.objects, key)
	return nil
}

func (f *fakePageStore) HeadObject(ctx context.Context, bucket, key string) (bool, error) {
	if f.headErr != nil {
		return false, f.headErr
	}
	_, ok := f.objects[key]
	return ok, nil
}

// The real SPA is saved before it is shadowed -- never destroyed.
func TestSwapToMaintenance_SavesSPAThenOverwritesIndex(t *testing.T) {
	store := newFakePageStore()
	store.objects[IndexKey] = "REAL-SPA"

	if err := SwapToMaintenance(context.Background(), store, "bucket", []byte("MAINT"), io.Discard); err != nil {
		t.Fatalf("SwapToMaintenance error: %v", err)
	}

	if store.objects[SPABackupKey] != "REAL-SPA" {
		t.Errorf("%s = %q, want the saved real SPA", SPABackupKey, store.objects[SPABackupKey])
	}
	if store.objects[IndexKey] != "MAINT" {
		t.Errorf("%s = %q, want the maintenance page", IndexKey, store.objects[IndexKey])
	}
	// The copy must happen before the overwrite, or the SPA is lost.
	if len(store.calls) < 2 || !strings.HasPrefix(store.calls[0], "copy:") {
		t.Errorf("calls = %v, want the copy first", store.calls)
	}
}

// index.html is served no-cache; the maintenance page must inherit that or
// CloudFront/browsers keep serving the shadowed SPA.
func TestSwapToMaintenance_UsesNoCacheForIndex(t *testing.T) {
	store := newFakePageStore()
	store.objects[IndexKey] = "REAL-SPA"

	if err := SwapToMaintenance(context.Background(), store, "bucket", []byte("MAINT"), io.Discard); err != nil {
		t.Fatalf("SwapToMaintenance error: %v", err)
	}

	found := false
	for _, c := range store.calls {
		if c == "put:"+IndexKey+":"+IndexCacheControl {
			found = true
		}
	}
	if !found {
		t.Errorf("calls = %v, want a put of %s with %q", store.calls, IndexKey, IndexCacheControl)
	}
}

// An interrupted prior run leaves index.spa.html already present. Re-running
// must NOT overwrite it with the maintenance page now sitting at index.html.
func TestSwapToMaintenance_DoesNotClobberAnExistingBackup(t *testing.T) {
	store := newFakePageStore()
	store.objects[SPABackupKey] = "REAL-SPA"
	store.objects[IndexKey] = "MAINT"

	if err := SwapToMaintenance(context.Background(), store, "bucket", []byte("MAINT"), io.Discard); err != nil {
		t.Fatalf("SwapToMaintenance error: %v", err)
	}

	if store.objects[SPABackupKey] != "REAL-SPA" {
		t.Fatalf("%s = %q -- an existing backup was clobbered, losing the real SPA",
			SPABackupKey, store.objects[SPABackupKey])
	}
	for _, c := range store.calls {
		if strings.HasPrefix(c, "copy:") {
			t.Errorf("calls = %v, want no copy when a backup already exists", store.calls)
		}
	}
}

// Restore puts the SPA back and clears the shadow copy.
func TestRestoreSPA_RestoresAndDeletesBackup(t *testing.T) {
	store := newFakePageStore()
	store.objects[SPABackupKey] = "REAL-SPA"
	store.objects[IndexKey] = "MAINT"

	if err := RestoreSPA(context.Background(), store, "bucket", io.Discard); err != nil {
		t.Fatalf("RestoreSPA error: %v", err)
	}

	if store.objects[IndexKey] != "REAL-SPA" {
		t.Errorf("%s = %q, want the real SPA restored", IndexKey, store.objects[IndexKey])
	}
	if _, ok := store.objects[SPABackupKey]; ok {
		t.Errorf("%s still present, want it deleted after a restore", SPABackupKey)
	}
}

// With no backup present there is nothing to restore -- and the live
// index.html must be left strictly alone rather than blanked.
func TestRestoreSPA_NoBackupIsANoOp(t *testing.T) {
	store := newFakePageStore()
	store.objects[IndexKey] = "REAL-SPA"

	if err := RestoreSPA(context.Background(), store, "bucket", io.Discard); err != nil {
		t.Fatalf("RestoreSPA error: %v", err)
	}

	if store.objects[IndexKey] != "REAL-SPA" {
		t.Errorf("%s = %q, want it untouched", IndexKey, store.objects[IndexKey])
	}
	for _, c := range store.calls {
		if strings.HasPrefix(c, "put:") || strings.HasPrefix(c, "delete:") {
			t.Errorf("calls = %v, want no mutation when there is no backup", store.calls)
		}
	}
}
