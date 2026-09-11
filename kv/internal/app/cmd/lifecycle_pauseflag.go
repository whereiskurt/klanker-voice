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

// PausedFlagName and HibernatedFlagName are the two top-level booleans in
// site.hcl that the lifecycle commands own. `paused` scales services to
// zero (2026-08-12 spec §5); `hibernated` additionally empties the service
// list and drops the NAT Gateway and ALB (2026-09-10 spec §3).
const (
	PausedFlagName     = "paused"
	HibernatedFlagName = "hibernated"
)

// assignmentRe builds the matcher for one named top-level boolean
// assignment. It is compiled per call rather than kept in a package-level
// var because the flag name is now a parameter; these files are small and
// the commands run once per invocation, so the cost is irrelevant next to
// the clarity of not caching a regexp keyed by a string.
// Capture group 1 is the boolean literal.
func assignmentRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`^\s*` + regexp.QuoteMeta(name) + `\s*=\s*(true|false)\s*$`)
}

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

// locateLifecycleFlag scans lines for exactly one top-level `name = true|false`
// assignment and returns its index. It returns ErrPausedFlagNotFound if none
// match, or ErrPausedFlagAmbiguous if more than one does -- never picking one.
func locateLifecycleFlag(lines [][]byte, name string) (int, error) {
	re := assignmentRe(name)
	found := -1
	for i, line := range lines {
		code := line[:codePortion(line)]
		if !re.Match(code) {
			continue
		}
		if found != -1 {
			return -1, fmt.Errorf("%w: %s on lines %d and %d", ErrPausedFlagAmbiguous, name, found+1, i+1)
		}
		found = i
	}
	if found == -1 {
		return -1, fmt.Errorf("%w: %s", ErrPausedFlagNotFound, name)
	}
	return found, nil
}

// parseLifecycleValue extracts the boolean literal from a line already known
// (via locateLifecycleFlag) to hold a top-level `name` assignment.
func parseLifecycleValue(line []byte, name string) bool {
	code := line[:codePortion(line)]
	m := assignmentRe(name).FindSubmatchIndex(code)
	return string(code[m[2]:m[3]]) == "true"
}

// ReadLifecycleFlag reports the current value of the single top-level `name`
// assignment in src.
func ReadLifecycleFlag(src []byte, name string) (bool, error) {
	lines := bytes.Split(src, []byte("\n"))
	idx, err := locateLifecycleFlag(lines, name)
	if err != nil {
		return false, err
	}
	return parseLifecycleValue(lines[idx], name), nil
}

// SetLifecycleFlag flips the single top-level `name` assignment in src to
// want, returning the rewritten bytes. It is a line-oriented scan over raw
// bytes, not an HCL parse-and-render round trip: a whole-file formatter
// would normalize the whole file and turn the intended one-line diff into a
// whole-file diff, defeating the show-the-diff-and-confirm step. Only the
// boolean literal's bytes are replaced.
func SetLifecycleFlag(src []byte, name string, want bool) (out []byte, changed bool, err error) {
	lines := bytes.Split(src, []byte("\n"))
	idx, err := locateLifecycleFlag(lines, name)
	if err != nil {
		return nil, false, err
	}
	line := lines[idx]
	if parseLifecycleValue(line, name) == want {
		return src, false, nil
	}

	code := line[:codePortion(line)]
	m := assignmentRe(name).FindSubmatchIndex(code)
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

// ReadPausedFlag reports the current value of the top-level `paused`
// assignment in src. Retained as the name every shipped call site uses.
func ReadPausedFlag(src []byte) (bool, error) {
	return ReadLifecycleFlag(src, PausedFlagName)
}

// SetPausedFlag flips the top-level `paused` assignment in src to want.
// Retained as the name every shipped call site uses.
func SetPausedFlag(src []byte, want bool) (out []byte, changed bool, err error) {
	return SetLifecycleFlag(src, PausedFlagName, want)
}

// ReadLifecycleFlagFile joins repoRoot with SiteHCLRelPath, reads it, and
// delegates to ReadLifecycleFlag, wrapping any error with the relative path
// so the operator sees which file failed.
func ReadLifecycleFlagFile(repoRoot, name string) (bool, error) {
	path := filepath.Join(repoRoot, SiteHCLRelPath)
	src, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", SiteHCLRelPath, err)
	}
	value, err := ReadLifecycleFlag(src, name)
	if err != nil {
		return false, fmt.Errorf("%s: %w", SiteHCLRelPath, err)
	}
	return value, nil
}

// SetLifecycleFlagFile joins repoRoot with SiteHCLRelPath, reads it,
// delegates to SetLifecycleFlag, and (only if changed) writes the result
// back preserving the file's existing mode.
func SetLifecycleFlagFile(repoRoot, name string, want bool) (changed bool, err error) {
	path := filepath.Join(repoRoot, SiteHCLRelPath)
	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", SiteHCLRelPath, err)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", SiteHCLRelPath, err)
	}
	out, changed, err := SetLifecycleFlag(src, name, want)
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

// ReadPausedFlagFile reports the `paused` flag's value in site.hcl.
func ReadPausedFlagFile(repoRoot string) (bool, error) {
	return ReadLifecycleFlagFile(repoRoot, PausedFlagName)
}

// SetPausedFlagFile flips the `paused` flag in site.hcl to want.
func SetPausedFlagFile(repoRoot string, want bool) (changed bool, err error) {
	return SetLifecycleFlagFile(repoRoot, PausedFlagName, want)
}
