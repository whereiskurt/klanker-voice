package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "site-hcl", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// --------------------------------------------------------------------------
// Test 1: reading the unpaused fixture returns false; reading the paused
// fixture returns true.

func TestPausedFlag_ReadUnpaused(t *testing.T) {
	got, err := ReadPausedFlag(mustReadFixture(t, "unpaused.hcl"))
	if err != nil {
		t.Fatalf("ReadPausedFlag(unpaused.hcl) error: %v", err)
	}
	if got != false {
		t.Errorf("ReadPausedFlag(unpaused.hcl) = %v, want false", got)
	}
}

func TestPausedFlag_ReadPaused(t *testing.T) {
	got, err := ReadPausedFlag(mustReadFixture(t, "paused.hcl"))
	if err != nil {
		t.Fatalf("ReadPausedFlag(paused.hcl) error: %v", err)
	}
	if got != true {
		t.Errorf("ReadPausedFlag(paused.hcl) = %v, want true", got)
	}
}

// --------------------------------------------------------------------------
// Test 2: flipping the unpaused fixture to true produces bytes
// byte-identical to the paused fixture, and vice versa -- proving no
// reformat, no re-indent, no comment loss.

func TestPausedFlag_SetProducesByteIdenticalFixture(t *testing.T) {
	unpaused := mustReadFixture(t, "unpaused.hcl")
	paused := mustReadFixture(t, "paused.hcl")

	out, changed, err := SetPausedFlag(unpaused, true)
	if err != nil {
		t.Fatalf("SetPausedFlag(unpaused, true) error: %v", err)
	}
	if !changed {
		t.Error("SetPausedFlag(unpaused, true) changed = false, want true")
	}
	if !bytes.Equal(out, paused) {
		t.Errorf("SetPausedFlag(unpaused, true) not byte-identical to paused.hcl\ngot:\n%s\nwant:\n%s", out, paused)
	}

	out2, changed2, err := SetPausedFlag(paused, false)
	if err != nil {
		t.Fatalf("SetPausedFlag(paused, false) error: %v", err)
	}
	if !changed2 {
		t.Error("SetPausedFlag(paused, false) changed = false, want true")
	}
	if !bytes.Equal(out2, unpaused) {
		t.Errorf("SetPausedFlag(paused, false) not byte-identical to unpaused.hcl\ngot:\n%s\nwant:\n%s", out2, unpaused)
	}
}

// --------------------------------------------------------------------------
// Test 3: flipping a fixture to the value it already holds returns
// changed=false and returns the input bytes unmodified.

func TestPausedFlag_SetAlreadyInStateIsNoOp(t *testing.T) {
	unpaused := mustReadFixture(t, "unpaused.hcl")

	out, changed, err := SetPausedFlag(unpaused, false)
	if err != nil {
		t.Fatalf("SetPausedFlag(unpaused, false) error: %v", err)
	}
	if changed {
		t.Error("SetPausedFlag(unpaused, false) changed = true, want false (already unpaused)")
	}
	if !bytes.Equal(out, unpaused) {
		t.Error("SetPausedFlag(unpaused, false) bytes were modified, want unmodified input")
	}

	paused := mustReadFixture(t, "paused.hcl")
	out2, changed2, err := SetPausedFlag(paused, true)
	if err != nil {
		t.Fatalf("SetPausedFlag(paused, true) error: %v", err)
	}
	if changed2 {
		t.Error("SetPausedFlag(paused, true) changed = true, want false (already paused)")
	}
	if !bytes.Equal(out2, paused) {
		t.Error("SetPausedFlag(paused, true) bytes were modified, want unmodified input")
	}
}

// --------------------------------------------------------------------------
// Test 4: flipping twice -- false to true to false -- returns bytes
// byte-identical to the original input (idempotence under round trip).

func TestPausedFlag_RoundTripIsByteIdentical(t *testing.T) {
	original := mustReadFixture(t, "unpaused.hcl")

	toTrue, changed, err := SetPausedFlag(original, true)
	if err != nil {
		t.Fatalf("SetPausedFlag(original, true) error: %v", err)
	}
	if !changed {
		t.Error("SetPausedFlag(original, true) changed = false, want true")
	}

	backToFalse, changed2, err := SetPausedFlag(toTrue, false)
	if err != nil {
		t.Fatalf("SetPausedFlag(toTrue, false) error: %v", err)
	}
	if !changed2 {
		t.Error("SetPausedFlag(toTrue, false) changed = false, want true")
	}

	if !bytes.Equal(backToFalse, original) {
		t.Errorf("round-trip false->true->false not byte-identical to original\ngot:\n%s\nwant:\n%s", backToFalse, original)
	}
}

// --------------------------------------------------------------------------
// Test 5: a fixture with no `paused` assignment returns a named not-found
// error and nil bytes.

func TestPausedFlag_MissingFlagIsNotFoundError(t *testing.T) {
	missing := mustReadFixture(t, "missing-flag.hcl")

	if _, err := ReadPausedFlag(missing); !errors.Is(err, ErrPausedFlagNotFound) {
		t.Errorf("ReadPausedFlag(missing-flag.hcl) error = %v, want errors.Is ErrPausedFlagNotFound", err)
	}

	out, changed, err := SetPausedFlag(missing, true)
	if !errors.Is(err, ErrPausedFlagNotFound) {
		t.Errorf("SetPausedFlag(missing-flag.hcl) error = %v, want errors.Is ErrPausedFlagNotFound", err)
	}
	if out != nil {
		t.Error("SetPausedFlag(missing-flag.hcl) out != nil, want nil on error")
	}
	if changed {
		t.Error("SetPausedFlag(missing-flag.hcl) changed = true, want false on error")
	}
}

// --------------------------------------------------------------------------
// Test 6: a fixture with two top-level `paused` assignments returns a
// named ambiguous error and nil bytes, rather than picking one.

func TestPausedFlag_DuplicateFlagIsAmbiguousError(t *testing.T) {
	dup := mustReadFixture(t, "duplicate-flag.hcl")

	if _, err := ReadPausedFlag(dup); !errors.Is(err, ErrPausedFlagAmbiguous) {
		t.Errorf("ReadPausedFlag(duplicate-flag.hcl) error = %v, want errors.Is ErrPausedFlagAmbiguous", err)
	}

	out, changed, err := SetPausedFlag(dup, true)
	if !errors.Is(err, ErrPausedFlagAmbiguous) {
		t.Errorf("SetPausedFlag(duplicate-flag.hcl) error = %v, want errors.Is ErrPausedFlagAmbiguous", err)
	}
	if out != nil {
		t.Error("SetPausedFlag(duplicate-flag.hcl) out != nil, want nil on error")
	}
	if changed {
		t.Error("SetPausedFlag(duplicate-flag.hcl) changed = true, want false on error")
	}
}

// --------------------------------------------------------------------------
// Test 7: an occurrence of the word `paused` inside a comment or inside a
// string does not satisfy the match -- covered by missing-flag.hcl, which
// carries both decoys and must still return not-found (not a false match).

func TestPausedFlag_CommentAndStringDecoysAreNotMatched(t *testing.T) {
	missing := mustReadFixture(t, "missing-flag.hcl")
	if !bytes.Contains(missing, []byte("paused")) {
		t.Fatal("fixture sanity check failed: missing-flag.hcl must still mention the word 'paused' in a comment/string decoy")
	}
	if _, err := ReadPausedFlag(missing); !errors.Is(err, ErrPausedFlagNotFound) {
		t.Errorf("ReadPausedFlag(missing-flag.hcl) with decoys error = %v, want errors.Is ErrPausedFlagNotFound (decoys must not match)", err)
	}
}

// --------------------------------------------------------------------------
// File-level wrappers against the real repo.

func TestPausedFlag_ReadPausedFlagFile_RealRepo(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot() error: %v", err)
	}
	got, err := ReadPausedFlagFile(root)
	if err != nil {
		t.Fatalf("ReadPausedFlagFile(realRepo) error: %v", err)
	}
	if got != false {
		t.Errorf("ReadPausedFlagFile(realRepo) = %v, want false (site.hcl must ship unpaused)", got)
	}
}

func TestPausedFlag_SetPausedFlagFile_RoundTripsOnTempCopy(t *testing.T) {
	tmp := t.TempDir()
	siteDir := filepath.Join(tmp, filepath.Dir(SiteHCLRelPath))
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatalf("mkdir temp site dir: %v", err)
	}
	unpaused := mustReadFixture(t, "unpaused.hcl")
	dest := filepath.Join(tmp, SiteHCLRelPath)
	if err := os.WriteFile(dest, unpaused, 0o644); err != nil {
		t.Fatalf("write temp site.hcl: %v", err)
	}

	changed, err := SetPausedFlagFile(tmp, true)
	if err != nil {
		t.Fatalf("SetPausedFlagFile(tmp, true) error: %v", err)
	}
	if !changed {
		t.Error("SetPausedFlagFile(tmp, true) changed = false, want true")
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read back temp site.hcl: %v", err)
	}
	paused := mustReadFixture(t, "paused.hcl")
	if !bytes.Equal(got, paused) {
		t.Errorf("SetPausedFlagFile wrote bytes not matching paused.hcl fixture\ngot:\n%s\nwant:\n%s", got, paused)
	}

	// Idempotent no-op on the second call.
	changed2, err := SetPausedFlagFile(tmp, true)
	if err != nil {
		t.Fatalf("SetPausedFlagFile(tmp, true) second call error: %v", err)
	}
	if changed2 {
		t.Error("SetPausedFlagFile(tmp, true) second call changed = true, want false (already paused)")
	}
}
