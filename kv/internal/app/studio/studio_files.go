package studio

// Studio-owned repo files that have no existing home elsewhere in the
// codebase (16-RESEARCH.md Architectural Responsibility Map):
//
//   - apps/voice/configs/studio/dids.yaml — per-DID default rule + opening
//     greeting (DID-02). NOT a VoIP.ms provisioning list: DID inventory
//     truth is VoIP.ms getDIDsInfo (cmd/voipms.go's ListInboundDIDs); this
//     file only carries metadata for a DID that already exists there. It is
//     merged with the live VoIP.ms list at read time in Plan 04.
//   - apps/voice/configs/studio/rule-order.yaml — operator-facing
//     authoring/presentation order for the rules table (RULE-03). This file
//     is PRESENTATION ONLY and is read by no runtime resolver: inbound
//     caller-id resolution is a deterministic exact-match GSI lookup, not a
//     first-match-wins scan (16-RESEARCH.md Anti-Patterns) — reordering this
//     file's contents never changes which rule a call actually gets.
//
// Both readers/writers reuse repofile_adapter.go's line-scanner helpers
// (scanYAMLLines/findTopLevelKey/yamlKeyVal/yamlScalar/skipYAMLBlock) for
// reads and repofile_writer.go's raw-line-splice helpers
// (scanRawYAMLIndexed/spliceLines/yamlRangeToRaw/listInsertionPoint) for
// writes, per the codebase's documented "no new YAML parser dependency"
// policy (repofile_adapter.go's package doc comment). No new module
// dependency is added by this file.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// studioDIDsPath / ruleOrderPath are the two studio-owned config surfaces
// this file reads/writes, relative to RepoFiles.Root.
const (
	studioDIDsPath = "apps/voice/configs/studio/dids.yaml"
	ruleOrderPath  = "apps/voice/configs/studio/rule-order.yaml"
)

// DIDMeta is one dids.yaml row: the per-DID default routing rule (an
// AccessCode id) and opening greeting, keyed by Did (E.164). Label/Region
// are operator-facing display hints; all four non-Did fields are optional
// (a DID may exist in the registry with only a Did set).
type DIDMeta struct {
	Did         string `json:"did"`
	Label       string `json:"label"`
	Region      string `json:"region"`
	DefaultRule string `json:"defaultRule"`
	Greeting    string `json:"greeting"`
}

// --------------------------------------------------------------------------
// DID metadata (dids.yaml)

// ReadDIDMeta parses dids.yaml's top-level `dids:` list into []DIDMeta. A
// missing file, an empty file, or a `dids: []` (no entries yet — the seeded
// state) all degrade gracefully to an empty slice with no error, mirroring
// the VoIP.ms-degradation philosophy elsewhere in this package
// (16-RESEARCH.md "must-have: reading a DID with no entry never errors").
func (r RepoFiles) ReadDIDMeta() ([]DIDMeta, error) {
	path := filepath.Join(r.Root, studioDIDsPath)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []DIDMeta{}, nil
		}
		return nil, &RepoFileError{Path: path, Err: err}
	}
	defer f.Close()

	lines, err := scanYAMLLines(f)
	if err != nil {
		return nil, &RepoFileError{Path: path, Err: err}
	}
	metas := []DIDMeta{}

	idx, isFlowEmpty, found := findTopLevelListKey(lines, "dids")
	if !found || isFlowEmpty {
		return metas, nil
	}
	i := idx + 1
	for i < len(lines) && lines[i].indent > 0 {
		l := lines[i]
		if l.indent == 2 && strings.HasPrefix(l.text, "- did:") {
			meta := DIDMeta{Did: yamlScalar(strings.TrimPrefix(l.text, "- did:"))}
			i++
			for i < len(lines) && lines[i].indent >= 4 {
				fl := lines[i]
				if fl.indent != 4 {
					i++
					continue
				}
				key, val, _ := yamlKeyVal(fl.text)
				switch key {
				case "label":
					meta.Label = yamlScalar(val)
					i++
				case "region":
					meta.Region = yamlScalar(val)
					i++
				case "default_rule":
					meta.DefaultRule = yamlScalar(val)
					i++
				case "greeting":
					meta.Greeting = yamlScalar(val)
					i++
				default:
					i++
					i = skipYAMLBlock(lines, i, fl.indent)
				}
			}
			metas = append(metas, meta)
		} else {
			i++
		}
	}
	return metas, nil
}

// WriteDIDMeta upserts m into dids.yaml, keyed by its (normalized) Did: an
// existing row's fields are replaced in place; a new Did is appended at the
// end of the list. Every other row and the file's header comment survive
// byte-for-byte (repofile_writer.go's raw-line-splice discipline — only the
// located range is touched).
//
// m.Did is validated/normalized via normalizeE164 (validate.go); m.Label,
// m.Region, m.DefaultRule are validated via validateCodeCharset when
// non-empty (control-char + length rejection); m.Greeting is validated for
// control characters only (free-text opening-greeting content is not bound
// by validateCodeCharset's length limit, which is sized for id-shaped
// strings). Validation runs BEFORE any file is opened for write.
func (r RepoFiles) WriteDIDMeta(m DIDMeta) error {
	// normalizeE164 strips any character that isn't a digit or '+' before
	// validating shape, so a control character in the raw input would
	// otherwise be silently dropped rather than rejected — check the
	// charset on the RAW value first (mirrors validateCodeCharset's
	// control-char rejection), then normalize.
	if err := validateCodeCharset(m.Did); err != nil {
		return fmt.Errorf("invalid did: %w", err)
	}
	normalizedDID, err := normalizeE164(m.Did)
	if err != nil {
		return fmt.Errorf("invalid did: %w", err)
	}
	m.Did = normalizedDID

	if err := validateOptionalCodeField("label", m.Label); err != nil {
		return err
	}
	if err := validateOptionalCodeField("region", m.Region); err != nil {
		return err
	}
	if err := validateOptionalCodeField("default_rule", m.DefaultRule); err != nil {
		return err
	}
	if m.Greeting != "" {
		if err := validateGreetingText(m.Greeting); err != nil {
			return fmt.Errorf("invalid greeting: %w", err)
		}
	}

	path := filepath.Join(r.Root, studioDIDsPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		return &RepoFileError{Path: path, Err: err}
	}
	rawLines := strings.Split(string(raw), "\n")

	lines, rawIdx := scanRawYAMLIndexed(rawLines)
	keyLineIdx, itemsStart, itemsEnd, isFlowEmpty, found := locateTopLevelListRange(lines, "dids")
	if !found {
		return &RepoFileError{Path: path, Err: fmt.Errorf("top-level %q key not found", "dids:")}
	}

	block := formatDIDMetaBlock(m)

	var newRawLines []string
	switch {
	case isFlowEmpty:
		keyRaw := rawIdx[keyLineIdx]
		replacement := append([]string{"dids:"}, block...)
		newRawLines = spliceLines(rawLines, keyRaw, keyRaw+1, replacement)
	default:
		if itemStart, itemEnd, exists := locateDIDItem(lines, itemsStart, itemsEnd, m.Did); exists {
			rawItemStart, rawItemEnd := yamlRangeToRaw(rawIdx, itemStart, itemEnd)
			newRawLines = spliceLines(rawLines, rawItemStart, rawItemEnd, block)
		} else {
			insertAt := listInsertionPoint(len(rawLines), rawIdx, itemsStart, itemsEnd)
			newRawLines = spliceLines(rawLines, insertAt, insertAt, block)
		}
	}

	if err := os.WriteFile(path, []byte(strings.Join(newRawLines, "\n")), 0o644); err != nil {
		return &RepoFileError{Path: path, Err: err}
	}
	return nil
}

// formatDIDMetaBlock renders m as the raw lines of one `- did:` block (item
// at indent 2, fields at indent 4). Empty optional fields are omitted
// entirely rather than written as empty strings, so a partially-populated
// DID row stays minimal.
func formatDIDMetaBlock(m DIDMeta) []string {
	const indent2 = "  "
	const indent4 = "    "
	lines := []string{fmt.Sprintf("%s- did: %q", indent2, m.Did)}
	if m.Label != "" {
		lines = append(lines, fmt.Sprintf("%slabel: %q", indent4, m.Label))
	}
	if m.Region != "" {
		lines = append(lines, fmt.Sprintf("%sregion: %q", indent4, m.Region))
	}
	if m.DefaultRule != "" {
		lines = append(lines, fmt.Sprintf("%sdefault_rule: %q", indent4, m.DefaultRule))
	}
	if m.Greeting != "" {
		lines = append(lines, fmt.Sprintf("%sgreeting: %q", indent4, m.Greeting))
	}
	return lines
}

// locateDIDItem scans the [start, end) yamlLine range (dids.yaml's item
// list, as returned by locateTopLevelListRange) for a `- did:` item whose
// normalized Did equals did, returning the item's yamlLine-index boundaries.
// Mirrors repofile_writer.go's locateKeywordItem, adapted to dids.yaml's
// indent 2 item / indent 4 field shape.
func locateDIDItem(lines []yamlLine, start, end int, did string) (itemStart, itemEnd int, found bool) {
	i := start
	for i < end {
		l := lines[i]
		if l.indent == 2 && strings.HasPrefix(l.text, "- did:") {
			d := yamlScalar(strings.TrimPrefix(l.text, "- did:"))
			begin := i
			i++
			for i < end && lines[i].indent >= 4 {
				i++
			}
			if d == did {
				return begin, i, true
			}
			continue
		}
		i++
	}
	return 0, 0, false
}

// --------------------------------------------------------------------------
// Rule order (rule-order.yaml) — PRESENTATION/AUTHORING ORDER ONLY.

// ReadRuleOrder parses rule-order.yaml's top-level `order:` list into an
// ordered slice of AccessCode ids. A missing file, an empty file, or an
// `order: []` (the seeded state) all degrade gracefully to an empty slice
// with no error — callers fall back to the existing DynamoDB read order.
//
// NOTE: this order is never consulted by any runtime resolver. See this
// file's package doc comment and WriteRuleOrder's doc comment.
func (r RepoFiles) ReadRuleOrder() ([]string, error) {
	path := filepath.Join(r.Root, ruleOrderPath)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, &RepoFileError{Path: path, Err: err}
	}
	defer f.Close()

	lines, err := scanYAMLLines(f)
	if err != nil {
		return nil, &RepoFileError{Path: path, Err: err}
	}
	order := []string{}

	idx, isFlowEmpty, found := findTopLevelListKey(lines, "order")
	if !found || isFlowEmpty {
		return order, nil
	}
	i := idx + 1
	for i < len(lines) && lines[i].indent > 0 {
		l := lines[i]
		if l.indent == 2 && strings.HasPrefix(l.text, "-") {
			order = append(order, yamlScalar(strings.TrimPrefix(l.text, "-")))
		}
		i++
	}
	return order, nil
}

// WriteRuleOrder rewrites rule-order.yaml's `order:` list to exactly
// codeIDs, in the given sequence — a full replace, not an upsert (there is
// only ever one ordering, not per-id rows to preserve). The file's header
// comment (which explains the presentation-only limitation to any operator
// reading the raw file) survives byte-for-byte; only the `order:` list body
// is touched.
//
// IMPORTANT: this function persists an operator-facing DISPLAY order only.
// No runtime resolver reads rule-order.yaml — inbound caller-id resolution
// (resolvePhoneToCode) is a deterministic exact-match GSI lookup, not a
// first-match-wins scan across overlapping rules, so there is nothing for a
// "first match" order to change at call time (16-RESEARCH.md Anti-Patterns).
// A true runtime first-match-wins rule engine, if ever built, is v2 —
// WriteRuleOrder's contract must not be silently repurposed as that engine's
// persistence layer without a corresponding runtime-resolver change and a
// re-audit of this doc comment.
//
// Each code id is validated via validateCodeCharset (control-char + length
// rejection) BEFORE the file is opened for write.
func (r RepoFiles) WriteRuleOrder(codeIDs []string) error {
	for _, id := range codeIDs {
		if err := validateCodeCharset(id); err != nil {
			return fmt.Errorf("invalid code id %q: %w", id, err)
		}
	}

	path := filepath.Join(r.Root, ruleOrderPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		return &RepoFileError{Path: path, Err: err}
	}
	rawLines := strings.Split(string(raw), "\n")

	lines, rawIdx := scanRawYAMLIndexed(rawLines)
	keyLineIdx, itemsStart, itemsEnd, isFlowEmpty, found := locateTopLevelListRange(lines, "order")
	if !found {
		return &RepoFileError{Path: path, Err: fmt.Errorf("top-level %q key not found", "order:")}
	}

	const indent2 = "  "
	newItems := make([]string, 0, len(codeIDs))
	for _, id := range codeIDs {
		newItems = append(newItems, fmt.Sprintf("%s- %s", indent2, id))
	}

	var newRawLines []string
	switch {
	case isFlowEmpty:
		keyRaw := rawIdx[keyLineIdx]
		replacement := append([]string{"order:"}, newItems...)
		newRawLines = spliceLines(rawLines, keyRaw, keyRaw+1, replacement)
	case itemsEnd > itemsStart:
		rawStart, rawEnd := yamlRangeToRaw(rawIdx, itemsStart, itemsEnd)
		newRawLines = spliceLines(rawLines, rawStart, rawEnd, newItems)
	default:
		insertAt := listInsertionPoint(len(rawLines), rawIdx, itemsStart, itemsEnd)
		newRawLines = spliceLines(rawLines, insertAt, insertAt, newItems)
	}

	if err := os.WriteFile(path, []byte(strings.Join(newRawLines, "\n")), 0o644); err != nil {
		return &RepoFileError{Path: path, Err: err}
	}
	return nil
}

// --------------------------------------------------------------------------
// Shared top-level-list-key location helpers, used by both dids.yaml's
// `dids:` reader/writer (above) and rule-order.yaml's `order:`
// reader/writer (above). Both files are seeded as a flow-form empty list
// ("dids: []" / "order: []") and grow into the block form ("<key>:"
// followed by indented `- ...` items) on first write, so every
// reader/writer above must tolerate both shapes.

// findTopLevelListKey locates a top-level "<key>:" line in yamlLine-index
// space, tolerating both the block form ("key:" — items, if any, follow at
// deeper indent) and the seeded flow-empty form ("key: []"). isFlowEmpty is
// true only for the latter; found is false if key doesn't appear at
// indent 0 at all.
func findTopLevelListKey(lines []yamlLine, key string) (idx int, isFlowEmpty, found bool) {
	blockForm := key + ":"
	flowForm := key + ": []"
	for i, l := range lines {
		if l.indent != 0 {
			continue
		}
		if l.text == flowForm {
			return i, true, true
		}
		if l.text == blockForm {
			return i, false, true
		}
	}
	return 0, false, false
}

// locateTopLevelListRange is findTopLevelListKey's raw-line-splice-ready
// counterpart: it additionally computes the yamlLine-index range of the
// key's existing items ([itemsStart, itemsEnd), equal if there are none),
// for callers that need to locate/replace/append within that range (via
// yamlRangeToRaw/listInsertionPoint/spliceLines, exactly as
// repofile_writer.go's topic-keywords helpers do).
func locateTopLevelListRange(lines []yamlLine, key string) (keyLineIdx, itemsStart, itemsEnd int, isFlowEmpty, found bool) {
	idx, flowEmpty, ok := findTopLevelListKey(lines, key)
	if !ok {
		return 0, 0, 0, false, false
	}
	if flowEmpty {
		return idx, idx, idx, true, true
	}
	j := idx + 1
	for j < len(lines) && lines[j].indent > 0 {
		j++
	}
	return idx, idx + 1, j, false, true
}

// --------------------------------------------------------------------------
// Field validation helpers specific to this file's free-text fields.

// validateOptionalCodeField validates val via validateCodeCharset only when
// non-empty (dids.yaml's label/region/default_rule fields are all
// optional), wrapping any error with the field name for a legible message.
func validateOptionalCodeField(name, val string) error {
	if val == "" {
		return nil
	}
	if err := validateCodeCharset(val); err != nil {
		return fmt.Errorf("invalid %s: %w", name, err)
	}
	return nil
}

// validateGreetingText rejects a greeting containing a control character.
// Unlike validateCodeCharset, it has no length bound — greeting is
// free-form opening-greeting text/reference, not an id-shaped string.
func validateGreetingText(s string) error {
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("greeting contains a control character")
		}
	}
	return nil
}
