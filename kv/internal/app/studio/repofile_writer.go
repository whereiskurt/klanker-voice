package studio

// Repo-file WRITE side for the read-only scanner in repofile_adapter.go
// (16-RESEARCH.md Pattern 3 / Open Question 2). Every writer here LOCATES
// its target with the exact same traversal repofile_adapter.go's reader
// uses (scanYAMLLines/findTopLevelKey/parseYAMLKeywords/parseTOMLScalarLine)
// so read and write can never drift, then splices ONLY the located range
// against the RAW file bytes — never the comment-stripped yamlLine text —
// so every other line (comments, blank lines, block scalars, ordering)
// survives byte-for-byte. This file intentionally does not add a YAML/TOML
// parser module dependency, matching repofile_adapter.go's documented
// policy.
//
// Threat T-16-05 (DoS): WriteTelephonyGate validates gateMode via
// GateModeAllowed (validate.go, Plan 01) BEFORE the file is ever opened for
// write — validate-then-write, never write-then-validate — because a bad
// gate_mode value would make the voice pipeline's config loader refuse to
// boot at its next config read (16-RESEARCH.md Pitfall 2).
//
// Threat T-16-07 (Info Disclosure): this file writes ONLY gate_mode /
// require_gate / topic-map keyword terms — it makes no AWS Systems Manager
// Parameter Store call of any kind, and writes no secret VALUE. See
// 16-02-PLAN.md's verification step for the exact no-secrets-touched grep
// gate this file must pass.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// KeywordOp selects whether WriteTopicMapKeyword adds/replaces or removes a
// topic-map.yaml keyword entry. There are exactly two operations — "add"
// covers both "insert a brand-new term" and "replace an existing term's
// weight" (the term line is located by exact text match; if found, only its
// weight is rewritten in place, never duplicated).
type KeywordOp int

const (
	// KeywordAdd inserts a new `- term:` entry, or — if term already exists
	// under the topic — replaces its weight line in place.
	KeywordAdd KeywordOp = iota
	// KeywordRemove deletes an existing `- term:` entry (and its weight
	// continuation line, if any).
	KeywordRemove
)

// WriteTopicMapKeyword adds, replaces the weight of, or removes a
// `- term: "<term>"` entry (with an optional `weight: <n>` continuation
// line) under topicID's `keywords:` list in topic-map.yaml. It preserves
// every other line — other topics, comments, hook: >- block scalars,
// ordering — byte-for-byte, by splicing only the located raw-line range.
//
// op == KeywordAdd: if term already exists under topicID, its weight line
// is rewritten in place (weight <= 0 removes the weight line entirely);
// otherwise a new item is appended at the end of the existing keyword list.
// op == KeywordRemove: the matching term's line(s) are deleted; an error is
// returned (and nothing is written) if the term isn't found.
func (r RepoFiles) WriteTopicMapKeyword(topicID, term string, weight int, op KeywordOp) error {
	if err := validateCodeCharset(topicID); err != nil {
		return fmt.Errorf("invalid topic id: %w", err)
	}
	if err := validateCodeCharset(term); err != nil {
		return fmt.Errorf("invalid term: %w", err)
	}
	if strings.ContainsRune(term, '"') {
		return fmt.Errorf("invalid term %q: must not contain a double-quote character", term)
	}

	path := filepath.Join(r.Root, topicMapPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		return &RepoFileError{Path: path, Err: err}
	}
	rawLines := strings.Split(string(raw), "\n")

	lines, rawIdx := scanRawYAMLIndexed(rawLines)
	start, end, found := locateTopicListRange(lines, topicID, "keywords")
	if !found {
		return fmt.Errorf("topic %q not found (or has no keywords list) in %s", topicID, path)
	}

	const indent6 = "      "
	const indent8 = "        "

	var newRawLines []string
	switch op {
	case KeywordAdd:
		if itemStart, itemEnd, exists := locateKeywordItem(lines, start, end, term); exists {
			// Replace the existing item's weight in place; the term line
			// itself is preserved verbatim (including its inline comment).
			rawItemStart, rawItemEnd := yamlRangeToRaw(rawIdx, itemStart, itemEnd)
			termLineRaw := rawIdx[itemStart]
			replacement := []string{rawLines[termLineRaw]}
			if weight > 0 {
				replacement = append(replacement, fmt.Sprintf("%sweight: %d", indent8, weight))
			}
			newRawLines = spliceLines(rawLines, rawItemStart, rawItemEnd, replacement)
		} else {
			insert := []string{fmt.Sprintf("%s- term: %q", indent6, term)}
			if weight > 0 {
				insert = append(insert, fmt.Sprintf("%sweight: %d", indent8, weight))
			}
			insertAt := listInsertionPoint(len(rawLines), rawIdx, start, end)
			newRawLines = spliceLines(rawLines, insertAt, insertAt, insert)
		}
	case KeywordRemove:
		itemStart, itemEnd, exists := locateKeywordItem(lines, start, end, term)
		if !exists {
			return fmt.Errorf("term %q not found under topic %q in %s", term, topicID, path)
		}
		rawItemStart, rawItemEnd := yamlRangeToRaw(rawIdx, itemStart, itemEnd)
		newRawLines = spliceLines(rawLines, rawItemStart, rawItemEnd, nil)
	default:
		return fmt.Errorf("unknown KeywordOp %d", op)
	}

	if err := os.WriteFile(path, []byte(strings.Join(newRawLines, "\n")), 0o644); err != nil {
		return &RepoFileError{Path: path, Err: err}
	}
	return nil
}

// WriteTelephonyGate rewrites the gate_mode and/or require_gate scalar
// line(s) inside telephony.toml's [telephony] block, preserving every other
// line (comments, key ordering, other sections) byte-for-byte.
//
// gateMode == "" leaves the existing gate_mode value untouched — this is
// the "no secret required" UI path, which only flips require_gate to false;
// it NEVER writes a gate_mode off-value (there isn't one — see types.go's
// SecretSpec.Mode doc comment and 16-RESEARCH.md Pitfall 2). A non-empty
// gateMode is validated via GateModeAllowed (validate.go) BEFORE the file
// is opened for write — validate-then-write, never write-then-validate
// (T-16-05): a bad mode must never reach telephony.toml, where the voice
// pipeline's config loader would refuse to boot on its next read.
//
// If gate_mode / require_gate keys are absent from the [telephony] block,
// they are inserted at the end of the block (never at file top).
func (r RepoFiles) WriteTelephonyGate(gateMode string, requireGate bool) error {
	if gateMode != "" && !GateModeAllowed(gateMode) {
		return fmt.Errorf("invalid gate_mode %q: must be one of dtmf, passphrase, either", gateMode)
	}

	path := filepath.Join(r.Root, telephonyConfigPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		return &RepoFileError{Path: path, Err: err}
	}
	rawLines := strings.Split(string(raw), "\n")

	blockStart, blockEnd, found := locateTelephonyBlock(rawLines)
	if !found {
		return &RepoFileError{Path: path, Err: fmt.Errorf("[telephony] block not found")}
	}

	gateModeLine, requireGateLine := -1, -1
	for i := blockStart; i < blockEnd; i++ {
		key, _, ok := parseTOMLScalarLine(strings.TrimSpace(rawLines[i]))
		if !ok {
			continue
		}
		switch key {
		case "gate_mode":
			gateModeLine = i
		case "require_gate":
			requireGateLine = i
		}
	}

	if gateMode != "" {
		if gateModeLine >= 0 {
			rawLines[gateModeLine] = rewriteTOMLQuotedValue(rawLines[gateModeLine], gateMode)
		} else {
			rawLines = spliceLines(rawLines, blockEnd, blockEnd, []string{fmt.Sprintf("gate_mode = %q", gateMode)})
			blockEnd++
		}
	}

	requireGateStr := "false"
	if requireGate {
		requireGateStr = "true"
	}
	if requireGateLine >= 0 {
		rawLines[requireGateLine] = rewriteTOMLBareValue(rawLines[requireGateLine], requireGateStr)
	} else {
		rawLines = spliceLines(rawLines, blockEnd, blockEnd, []string{fmt.Sprintf("require_gate = %s", requireGateStr)})
	}

	if err := os.WriteFile(path, []byte(strings.Join(rawLines, "\n")), 0o644); err != nil {
		return &RepoFileError{Path: path, Err: err}
	}
	return nil
}

// allowedManifestSourceKinds is the exact source `kind` enum manifest.yaml's
// own header comment documents (lines 17-19): docs | code | transcript |
// diagram. Checked BEFORE the file is ever opened for write
// (validate-then-write, T-17-08) — a bad kind must never reach
// manifest.yaml, since refresh_knowledge.py's read_manifest() has no
// tolerance for an unrecognized kind.
var allowedManifestSourceKinds = map[string]bool{
	"docs":       true,
	"code":       true,
	"transcript": true,
	"diagram":    true,
}

// ManifestSourceKindAllowed reports whether kind is one of manifest.yaml's
// four valid source kinds.
func ManifestSourceKindAllowed(kind string) bool {
	return allowedManifestSourceKinds[kind]
}

// validateManifestSourcePath rejects a blank path or one containing a
// control character (mirrors studio_files.go's validateGreetingText). An
// absolute path is deliberately NOT rejected — a sibling local checkout
// (e.g. /Users/khundeck/working/klankrmkr/docs) is an existing, legitimate
// manifest.yaml convention (17-RESEARCH.md Security Domain); path shape is
// not the safety boundary here, the public:true gate is.
func validateManifestSourcePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path must not be blank")
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return fmt.Errorf("path %q contains a control character", path)
		}
	}
	return nil
}

// WriteManifestSource appends a `- path: <path>` source item (with a
// `kind:` field and a hardcoded `public: true`) to topicID's `sources:` list
// in manifest.yaml (KNOW-02), preserving every other line — other topics,
// comments, note: >- block scalars, ordering — byte-for-byte, by splicing
// only the located raw-line range (WriteTopicMapKeyword's exact discipline,
// reused via locateTopicListRange).
//
// public: true is hardcoded here, never a caller parameter — the signature
// deliberately has no `public` argument, so a source can never be added as
// non-public from this console (17-RESEARCH.md locked decision: a source
// missing public:true is silently excluded by refresh_knowledge.py's D-02
// gate anyway, so writing a false one would be a confusing no-op). kind is
// validated against allowedManifestSourceKinds and path is validated via
// validateManifestSourcePath BEFORE the file is ever opened
// (validate-then-write, T-17-08). Returns an error (writing nothing) if
// topicID is not present in manifest.yaml — KNOW-02 is "add a source to an
// existing pack," not "create a new pack."
func (r RepoFiles) WriteManifestSource(topicID, path, kind string) error {
	if !ManifestSourceKindAllowed(kind) {
		return fmt.Errorf("invalid kind %q: must be one of docs, code, transcript, diagram", kind)
	}
	if err := validateManifestSourcePath(path); err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	fullPath := filepath.Join(r.Root, manifestPath)
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		return &RepoFileError{Path: fullPath, Err: err}
	}
	rawLines := strings.Split(string(raw), "\n")

	lines, rawIdx := scanRawYAMLIndexed(rawLines)
	start, end, found := locateTopicListRange(lines, topicID, "sources")
	if !found {
		return fmt.Errorf("topic %q not found (or has no sources list) in %s", topicID, fullPath)
	}

	const indent6 = "      "
	const indent8 = "        "
	insert := []string{
		fmt.Sprintf("%s- path: %s", indent6, path),
		fmt.Sprintf("%skind: %s", indent8, kind),
		fmt.Sprintf("%spublic: true", indent8),
	}
	insertAt := listInsertionPoint(len(rawLines), rawIdx, start, end)
	newRawLines := spliceLines(rawLines, insertAt, insertAt, insert)

	if err := os.WriteFile(fullPath, []byte(strings.Join(newRawLines, "\n")), 0o644); err != nil {
		return &RepoFileError{Path: fullPath, Err: err}
	}
	return nil
}

// --------------------------------------------------------------------------
// Raw-line-aware helpers. These operate on the SAME yamlLine-index space
// repofile_adapter.go's traversal helpers (findTopLevelKey/
// parseYAMLKeywords/yamlKeyVal/yamlScalar/skipYAMLBlock) use, plus a
// parallel rawIdx slice mapping each yamlLine back to its raw (unfiltered)
// line number, so a located range can be translated into a raw-line splice
// that never touches a blank line, comment, or block-scalar body the
// yamlLine scan dropped.

// scanRawYAMLIndexed is scanYAMLLines' algorithm applied to an already-split
// raw line slice, additionally recording each yamlLine's raw line index.
func scanRawYAMLIndexed(rawLines []string) (lines []yamlLine, rawIdx []int) {
	for i, raw := range rawLines {
		trimmed := strings.TrimLeft(raw, " ")
		indent := len(raw) - len(trimmed)
		content := strings.TrimRight(trimmed, " \t\r")
		if content == "" || strings.HasPrefix(content, "#") {
			continue
		}
		lines = append(lines, yamlLine{indent: indent, text: content})
		rawIdx = append(rawIdx, i)
	}
	return lines, rawIdx
}

// locateTopicListRange walks the exact `topics:` / `- id:` / `<listKey>:`
// traversal ReadTopicMap/ReadManifest use to find topicID's <listKey>
// sub-list range in yamlLine-index space: [start, end), where start is the
// index of the first list item (or, for an empty list, the index of the
// first line after `<listKey>:`) and end is one past the last item. found is
// false if the topic or its <listKey> key doesn't exist. Generalized from a
// keywords-only "locateTopicKeywordsRange" (topic-map.yaml's
// `keywords:`/`- term:` and manifest.yaml's `sources:`/`- path:` lists share
// the identical indent-4-key/indent-6-item/indent-8-field shape) so
// WriteTopicMapKeyword and WriteManifestSource can never drift from one
// traversal.
func locateTopicListRange(lines []yamlLine, topicID, listKey string) (start, end int, found bool) {
	i := findTopLevelKey(lines, "topics")
	if i < 0 {
		return 0, 0, false
	}
	i++
	for i < len(lines) && lines[i].indent > 0 {
		l := lines[i]
		if l.indent == 2 && strings.HasPrefix(l.text, "- id:") {
			id := yamlScalar(strings.TrimPrefix(l.text, "- id:"))
			i++
			for i < len(lines) && lines[i].indent >= 4 {
				fl := lines[i]
				if fl.indent != 4 {
					i++
					continue
				}
				key, _, _ := yamlKeyVal(fl.text)
				if key == listKey {
					i++
					listStart := i
					ni := skipTopicListItems(lines, i)
					if id == topicID {
						return listStart, ni, true
					}
					i = ni
				} else {
					i++
					i = skipYAMLBlock(lines, i, fl.indent)
				}
			}
		} else {
			i++
		}
	}
	return 0, 0, false
}

// skipTopicListItems advances past a topics[].<listKey> list's items,
// generically — any indent-6 `- ...` item followed by indent-8+ field/
// block-scalar continuation lines (works for both topic-map.yaml's
// `- term:` keywords and manifest.yaml's `- path:` sources, since both
// share the identical indent-6-item/indent-8+-continuation shape) —
// returning the yamlLine index just past the list. Only the range boundary
// is needed here (never per-item text), unlike parseYAMLKeywords/
// parseYAMLSources.
func skipTopicListItems(lines []yamlLine, i int) int {
	for i < len(lines) && lines[i].indent >= 6 {
		i++
	}
	return i
}

// locateKeywordItem scans the [start, end) yamlLine range (a topic's
// keywords list, as returned by locateTopicKeywordsRange) for a `- term:`
// item whose value equals term, mirroring parseYAMLKeywords' own per-item
// walk (indent 6 `- term:` line, then any indent-8 continuation lines) but
// returning the item's yamlLine-index boundaries instead of just its text.
func locateKeywordItem(lines []yamlLine, start, end int, term string) (itemStart, itemEnd int, found bool) {
	i := start
	for i < end {
		l := lines[i]
		if l.indent == 6 && strings.HasPrefix(l.text, "- term:") {
			t := yamlScalar(strings.TrimPrefix(l.text, "- term:"))
			begin := i
			i++
			for i < end && lines[i].indent == 8 {
				i++
			}
			if t == term {
				return begin, i, true
			}
			continue
		}
		i++
	}
	return 0, 0, false
}

// yamlRangeToRaw converts a non-empty [start, end) yamlLine-index range
// (end > start) into the [rawStart, rawEnd) raw-line range spanning exactly
// its significant lines. rawEnd is one past the LAST significant line's raw
// index — NOT the raw index of the next significant yamlLine — so any blank
// lines or comments between this range and the next significant line are
// left outside the splice for the caller to preserve untouched.
func yamlRangeToRaw(rawIdx []int, start, end int) (rawStart, rawEnd int) {
	return rawIdx[start], rawIdx[end-1] + 1
}

// listInsertionPoint returns the raw line index at which a new list item
// (a topic-map.yaml keyword or a manifest.yaml source) should be appended
// for the (possibly empty) [start, end) yamlLine range: one past the last
// existing item's raw line for a non-empty list, or — for an empty list —
// immediately before the next significant line (or end-of-file).
func listInsertionPoint(rawLen int, rawIdx []int, start, end int) int {
	if end > start {
		return rawIdx[end-1] + 1
	}
	if start < len(rawIdx) {
		return rawIdx[start]
	}
	return rawLen
}

// spliceLines returns rawLines with [start, end) replaced by insert,
// leaving everything before start and at/after end untouched.
func spliceLines(rawLines []string, start, end int, insert []string) []string {
	out := make([]string, 0, len(rawLines)-(end-start)+len(insert))
	out = append(out, rawLines[:start]...)
	out = append(out, insert...)
	out = append(out, rawLines[end:]...)
	return out
}

// locateTelephonyBlock finds the raw-line range of the [telephony] section
// body — [start, end), start being the first line after the `[telephony]`
// header, end being the raw index of the next `[section]` header line (or
// len(rawLines) if [telephony] is the last section) — mirroring
// ReadTelephonyGate's own inBlock scan.
func locateTelephonyBlock(rawLines []string) (start, end int, found bool) {
	inBlock := false
	for i, raw := range rawLines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if inBlock {
				return start, i, true
			}
			if strings.HasPrefix(line, "[telephony]") {
				inBlock = true
				found = true
				start = i + 1
			}
			continue
		}
	}
	if inBlock {
		return start, len(rawLines), true
	}
	return 0, 0, false
}

// rewriteTOMLQuotedValue replaces the first "..."-quoted value on the
// right-hand side of line's first "=" with newValue, preserving everything
// else on the line (key, spacing, any trailing inline comment) verbatim.
func rewriteTOMLQuotedValue(line, newValue string) string {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return line
	}
	rest := line[eq+1:]
	q1 := strings.IndexByte(rest, '"')
	if q1 < 0 {
		return line
	}
	q2 := strings.IndexByte(rest[q1+1:], '"')
	if q2 < 0 {
		return line
	}
	q2 += q1 + 1
	return line[:eq+1] + rest[:q1+1] + newValue + rest[q2:]
}

// rewriteTOMLBareValue replaces the unquoted value token on the right-hand
// side of line's first "=" (e.g. a boolean like `true`/`false`) with
// newValue, preserving leading spacing and any trailing inline comment
// verbatim.
func rewriteTOMLBareValue(line, newValue string) string {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return line
	}
	rest := line[eq+1:]
	i := 0
	for i < len(rest) && rest[i] == ' ' {
		i++
	}
	j := i
	for j < len(rest) && rest[j] != ' ' && rest[j] != '\t' {
		j++
	}
	return line[:eq+1] + rest[:i] + newValue + rest[j:]
}
