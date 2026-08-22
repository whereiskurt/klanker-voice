package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// SiteHCLRelPath is the repo-relative path to the terragrunt site config
// that owns the `paused` operator switch (D-15). It is the sole input to
// ReadPausedFlagFile / SetPausedFlagFile, resolved beneath a repo root
// (see repoRoot() in knowledge.go).
const SiteHCLRelPath = "infra/terraform/live/site/site.hcl"

// ErrPausedFlagNotFound is returned when no top-level `paused = true` or
// `paused = false` assignment is found in the scanned source.
var ErrPausedFlagNotFound = errors.New("paused flag not found")

// ErrPausedFlagAmbiguous is returned when more than one top-level `paused`
// assignment is found. ReadPausedFlag/SetPausedFlag never guess which one
// is authoritative -- D-31 requires malformed input to be a named error,
// never a silent guess.
var ErrPausedFlagAmbiguous = errors.New("paused flag ambiguous: multiple assignments found")

// pausedAssignmentRe matches a line's code portion (i.e. with any trailing
// comment already stripped by codePortion) that is, but for surrounding
// whitespace, exactly a `paused = true` or `paused = false` assignment.
// Capture group 1 is the boolean literal.
var pausedAssignmentRe = regexp.MustCompile(`^\s*paused\s*=\s*(true|false)\s*$`)

// codePortion returns the byte offset within line where a trailing `#` or
// `//` comment begins (or len(line) if the line carries no comment). It
// tracks double-quoted string literals with backslash-escaping so a `#` or
// `/` pair inside a string literal is never mistaken for a comment marker
// -- this is what keeps a decoy like `release_notes = "... paused ..."`
// from being misread.
func codePortion(line []byte) int {
	inString := false
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
		case c == '#':
			return i
		case c == '/' && i+1 < len(line) && line[i+1] == '/':
			return i
		}
	}
	return len(line)
}

// locatePausedFlag scans lines (each a line of the source, split on "\n")
// for exactly one top-level `paused = true|false` assignment and returns
// its index into lines. It returns ErrPausedFlagNotFound if none match, or
// ErrPausedFlagAmbiguous if more than one does -- never picking one.
func locatePausedFlag(lines [][]byte) (int, error) {
	found := -1
	for i, line := range lines {
		code := line[:codePortion(line)]
		if !pausedAssignmentRe.Match(code) {
			continue
		}
		if found != -1 {
			return -1, fmt.Errorf("%w: lines %d and %d", ErrPausedFlagAmbiguous, found+1, i+1)
		}
		found = i
	}
	if found == -1 {
		return -1, ErrPausedFlagNotFound
	}
	return found, nil
}

// parsePausedValue extracts the boolean literal from a line already known
// (via locatePausedFlag) to hold a top-level `paused` assignment.
func parsePausedValue(line []byte) bool {
	code := line[:codePortion(line)]
	m := pausedAssignmentRe.FindSubmatchIndex(code)
	return string(code[m[2]:m[3]]) == "true"
}

// ReadPausedFlag reports the current value of the single top-level `paused`
// assignment in src.
func ReadPausedFlag(src []byte) (bool, error) {
	lines := bytes.Split(src, []byte("\n"))
	idx, err := locatePausedFlag(lines)
	if err != nil {
		return false, err
	}
	return parsePausedValue(lines[idx]), nil
}

// SetPausedFlag flips the single top-level `paused` assignment in src to
// want, returning the rewritten bytes. It is implemented as a line-oriented
// scan over the raw bytes, not an HCL parse-and-render round trip: a
// whole-file HCL formatter/writer would normalize formatting across the
// whole file, which would turn the intended one-line diff into a
// whole-file diff and defeat `kv pause`'s show-the-diff-and-confirm step
// (spec §5.3 step 3). Only the boolean literal's bytes are replaced; every
// other byte -- leading whitespace, `=` spacing, trailing comments, every
// other line -- is copied through unchanged. When src already holds want,
// the input slice is returned unmodified with changed=false (D-18: an
// already-in-state flip is a reported no-op, never a spurious diff).
func SetPausedFlag(src []byte, want bool) (out []byte, changed bool, err error) {
	lines := bytes.Split(src, []byte("\n"))
	idx, err := locatePausedFlag(lines)
	if err != nil {
		return nil, false, err
	}
	line := lines[idx]
	if parsePausedValue(line) == want {
		return src, false, nil
	}

	code := line[:codePortion(line)]
	m := pausedAssignmentRe.FindSubmatchIndex(code)
	newLiteral := "false"
	if want {
		newLiteral = "true"
	}
	newLine := make([]byte, 0, len(line)+1)
	newLine = append(newLine, line[:m[2]]...)
	newLine = append(newLine, newLiteral...)
	newLine = append(newLine, line[m[3]:]...)
	lines[idx] = newLine

	return bytes.Join(lines, []byte("\n")), true, nil
}

// ReadPausedFlagFile joins repoRoot with SiteHCLRelPath, reads it, and
// delegates to ReadPausedFlag, wrapping any error with the relative path so
// the operator sees which file failed.
func ReadPausedFlagFile(repoRoot string) (bool, error) {
	path := filepath.Join(repoRoot, SiteHCLRelPath)
	src, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", SiteHCLRelPath, err)
	}
	value, err := ReadPausedFlag(src)
	if err != nil {
		return false, fmt.Errorf("%s: %w", SiteHCLRelPath, err)
	}
	return value, nil
}

// SetPausedFlagFile joins repoRoot with SiteHCLRelPath, reads it, delegates
// to SetPausedFlag, and (only if changed) writes the result back preserving
// the file's existing mode. Any error is wrapped with the relative path so
// the operator sees which file failed.
func SetPausedFlagFile(repoRoot string, want bool) (changed bool, err error) {
	path := filepath.Join(repoRoot, SiteHCLRelPath)
	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", SiteHCLRelPath, err)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", SiteHCLRelPath, err)
	}
	out, changed, err := SetPausedFlag(src, want)
	if err != nil {
		return false, fmt.Errorf("%s: %w", SiteHCLRelPath, err)
	}
	if !changed {
		return false, nil
	}
	if err := os.WriteFile(path, out, info.Mode()); err != nil {
		return false, fmt.Errorf("write %s: %w", SiteHCLRelPath, err)
	}
	return true, nil
}
