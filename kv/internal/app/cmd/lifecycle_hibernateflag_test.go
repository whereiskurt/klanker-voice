package cmd

import (
	"bytes"
	"errors"
	"testing"
)

// The generalized engine must locate a named flag, not just `paused`.
func TestLifecycleFlag_ReadsHibernatedIndependentOfPaused(t *testing.T) {
	src := []byte("paused     = true\nhibernated = false\n")

	paused, err := ReadLifecycleFlag(src, PausedFlagName)
	if err != nil {
		t.Fatalf("ReadLifecycleFlag(paused) error: %v", err)
	}
	if paused != true {
		t.Errorf("paused = %v, want true", paused)
	}

	hib, err := ReadLifecycleFlag(src, HibernatedFlagName)
	if err != nil {
		t.Fatalf("ReadLifecycleFlag(hibernated) error: %v", err)
	}
	if hib != false {
		t.Errorf("hibernated = %v, want false", hib)
	}
}

// Flipping one flag must leave every other byte -- including the other
// flag's line, alignment and trailing comments -- untouched.
func TestLifecycleFlag_SetTouchesOnlyTheNamedFlag(t *testing.T) {
	src := []byte("paused     = true  # operator switch\nhibernated = false # deeper switch\n")
	want := []byte("paused     = true  # operator switch\nhibernated = true # deeper switch\n")

	out, changed, err := SetLifecycleFlag(src, HibernatedFlagName, true)
	if err != nil {
		t.Fatalf("SetLifecycleFlag error: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	if !bytes.Equal(out, want) {
		t.Errorf("SetLifecycleFlag produced:\n%q\nwant:\n%q", out, want)
	}
}

// A name that appears only inside a string literal is not an assignment.
func TestLifecycleFlag_IgnoresDecoyInStringLiteral(t *testing.T) {
	src := []byte("note = \"hibernated = true\"\nhibernated = false\n")

	got, err := ReadLifecycleFlag(src, HibernatedFlagName)
	if err != nil {
		t.Fatalf("ReadLifecycleFlag error: %v", err)
	}
	if got != false {
		t.Errorf("got %v, want false -- the decoy inside the string literal was matched", got)
	}
}

// Two assignments are never silently disambiguated.
func TestLifecycleFlag_AmbiguousIsAnError(t *testing.T) {
	src := []byte("hibernated = false\nhibernated = true\n")

	_, err := ReadLifecycleFlag(src, HibernatedFlagName)
	if !errors.Is(err, ErrPausedFlagAmbiguous) {
		t.Fatalf("error = %v, want ErrPausedFlagAmbiguous", err)
	}
}

// A missing flag is a named error, never a false default.
func TestLifecycleFlag_MissingIsAnError(t *testing.T) {
	src := []byte("paused = false\n")

	_, err := ReadLifecycleFlag(src, HibernatedFlagName)
	if !errors.Is(err, ErrPausedFlagNotFound) {
		t.Fatalf("error = %v, want ErrPausedFlagNotFound", err)
	}
}

// Setting a flag to the value it already holds is a reported no-op, and
// returns the input bytes unmodified.
func TestLifecycleFlag_AlreadyInStateIsNoOp(t *testing.T) {
	src := []byte("hibernated = true\n")

	out, changed, err := SetLifecycleFlag(src, HibernatedFlagName, true)
	if err != nil {
		t.Fatalf("SetLifecycleFlag error: %v", err)
	}
	if changed {
		t.Error("changed = true, want false for an already-in-state flip")
	}
	if !bytes.Equal(out, src) {
		t.Error("out != src for a no-op flip")
	}
}
