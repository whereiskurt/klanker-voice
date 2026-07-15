package studio

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// RepoFiles reads the three repo config surfaces a Rule assembles from,
// rooted at Root (the klanker-voice repo root — callers resolve this, e.g.
// via `git rev-parse --show-toplevel` as cmd/knowledge.go's repoRoot()
// does). Tests point Root at a temp fixture directory.
type RepoFiles struct {
	Root string
}

// manifestPath / topicMapPath / telephonyConfigPath are the three repo files
// RepoFiles reads, relative to Root.
const (
	manifestPath        = "apps/voice/knowledge/manifest.yaml"
	topicMapPath        = "apps/voice/knowledge/router/topic-map.yaml"
	telephonyConfigPath = "apps/voice/configs/telephony.toml"
)

// RepoFileError is a typed error for a repo-file read failure (including
// "not found") — callers turn it into a per-section note in the assembled
// view rather than panicking or surfacing a raw *os.PathError.
type RepoFileError struct {
	Path string
	Err  error
}

func (e *RepoFileError) Error() string {
	return fmt.Sprintf("read %s: %v", e.Path, e.Err)
}

func (e *RepoFileError) Unwrap() error { return e.Err }

// ReadManifest parses manifest.yaml into []KnowledgePack (id, spoken_name,
// pack, sources[].{path,kind,public}). A pack is Talkable when every one of
// its sources carries public:true (manifest.yaml's D-02 convention: sources
// missing public:true are excluded from talkable material); a pack with no
// sources defaults Talkable=true (nothing to hide).
func (r RepoFiles) ReadManifest() ([]KnowledgePack, error) {
	path := filepath.Join(r.Root, manifestPath)
	f, err := os.Open(path)
	if err != nil {
		return nil, &RepoFileError{Path: path, Err: err}
	}
	defer f.Close()

	lines, err := scanYAMLLines(f)
	if err != nil {
		return nil, &RepoFileError{Path: path, Err: err}
	}
	packs := []KnowledgePack{}

	i := findTopLevelKey(lines, "topics")
	if i < 0 {
		return packs, nil
	}
	i++
	for i < len(lines) && lines[i].indent > 0 {
		l := lines[i]
		if l.indent == 2 && strings.HasPrefix(l.text, "- id:") {
			pack := KnowledgePack{ID: yamlScalar(strings.TrimPrefix(l.text, "- id:"))}
			i++
			for i < len(lines) && lines[i].indent >= 4 {
				fl := lines[i]
				if fl.indent != 4 {
					i++
					continue
				}
				key, val, _ := yamlKeyVal(fl.text)
				switch key {
				case "spoken_name":
					pack.SpokenName = yamlScalar(val)
					i++
				case "pack":
					pack.Pack = yamlScalar(val)
					i++
				case "sources":
					i++
					srcs, ni := parseYAMLSources(lines, i)
					pack.Sources = srcs
					i = ni
				default:
					// scalar, block scalar, or nested structure we don't
					// need — skip past any deeper-indented continuation.
					i++
					i = skipYAMLBlock(lines, i, fl.indent)
				}
			}
			pack.Talkable = true
			for _, s := range pack.Sources {
				if !s.Public {
					pack.Talkable = false
					break
				}
			}
			packs = append(packs, pack)
		} else {
			i++
		}
	}
	return packs, nil
}

// parseYAMLSources parses a manifest.yaml pack's `sources:` list (items at
// indent 6, fields at indent 8), returning the parsed sources and the index
// just past the list.
func parseYAMLSources(lines []yamlLine, i int) ([]KnowledgeSource, int) {
	out := []KnowledgeSource{}
	for i < len(lines) && lines[i].indent >= 6 {
		l := lines[i]
		if l.indent == 6 && strings.HasPrefix(l.text, "- path:") {
			src := KnowledgeSource{Path: yamlScalar(strings.TrimPrefix(l.text, "- path:"))}
			i++
			for i < len(lines) && lines[i].indent == 8 {
				key, val, _ := yamlKeyVal(lines[i].text)
				switch key {
				case "kind":
					src.Kind = yamlScalar(val)
					i++
				case "public":
					src.Public = yamlScalar(val) == "true"
					i++
				default:
					i++
					i = skipYAMLBlock(lines, i, 8)
				}
			}
			out = append(out, src)
		} else {
			i++
		}
	}
	return out, i
}

// ReadTopicMap parses topic-map.yaml into []Unlock — one Unlock per keyword
// term, Add = [the topic's id] (spec §5 "topic-map phrases -> unlocks").
func (r RepoFiles) ReadTopicMap() ([]Unlock, error) {
	path := filepath.Join(r.Root, topicMapPath)
	f, err := os.Open(path)
	if err != nil {
		return nil, &RepoFileError{Path: path, Err: err}
	}
	defer f.Close()

	lines, err := scanYAMLLines(f)
	if err != nil {
		return nil, &RepoFileError{Path: path, Err: err}
	}
	unlocks := []Unlock{}

	i := findTopLevelKey(lines, "topics")
	if i < 0 {
		return unlocks, nil
	}
	i++
	for i < len(lines) && lines[i].indent > 0 {
		l := lines[i]
		if l.indent == 2 && strings.HasPrefix(l.text, "- id:") {
			topicID := yamlScalar(strings.TrimPrefix(l.text, "- id:"))
			i++
			for i < len(lines) && lines[i].indent >= 4 {
				fl := lines[i]
				if fl.indent != 4 {
					i++
					continue
				}
				key, _, _ := yamlKeyVal(fl.text)
				if key == "keywords" {
					i++
					terms, ni := parseYAMLKeywords(lines, i)
					for _, term := range terms {
						unlocks = append(unlocks, Unlock{Phrase: term, Add: []string{topicID}})
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
	return unlocks, nil
}

// parseYAMLKeywords parses a topic-map.yaml topic's `keywords:` list (items
// at indent 6 `- term: "..."`, a sibling `weight:` field at indent 8 that is
// not needed for Unlock and is skipped), returning the term strings and the
// index just past the list.
func parseYAMLKeywords(lines []yamlLine, i int) ([]string, int) {
	terms := []string{}
	for i < len(lines) && lines[i].indent >= 6 {
		l := lines[i]
		if l.indent == 6 && strings.HasPrefix(l.text, "- term:") {
			terms = append(terms, yamlScalar(strings.TrimPrefix(l.text, "- term:")))
			i++
			for i < len(lines) && lines[i].indent == 8 {
				i++
			}
		} else {
			i++
		}
	}
	return terms, i
}

// ReadTelephonyGate returns gate_mode from the [telephony] block of
// telephony.toml — a minimal single-section line scan (no TOML dependency),
// mirroring cmd/telephony.go's scanTelephonyBlock/parseTOMLScalarLine.
func (r RepoFiles) ReadTelephonyGate() (string, error) {
	path := filepath.Join(r.Root, telephonyConfigPath)
	f, err := os.Open(path)
	if err != nil {
		return "", &RepoFileError{Path: path, Err: err}
	}
	defer f.Close()

	inBlock := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if line == "[telephony]" || strings.HasPrefix(line, "[telephony]") {
				inBlock = true
				continue
			}
			if inBlock {
				break
			}
			continue
		}
		if !inBlock {
			continue
		}
		key, value, ok := parseTOMLScalarLine(line)
		if !ok {
			continue
		}
		if key == "gate_mode" {
			return value, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", &RepoFileError{Path: path, Err: err}
	}
	return "", nil
}

// parseTOMLScalarLine splits a "key = value  # comment" line, stripping
// surrounding quotes from value and any trailing " #"-prefixed inline
// comment. Mirrors cmd/telephony.go's parseTOMLScalarLine exactly (kept as
// a local copy — studio must not import cmd, which will depend on studio in
// Phase 15-02).
func parseTOMLScalarLine(line string) (key, value string, ok bool) {
	if strings.HasPrefix(line, "#") {
		return "", "", false
	}
	rawKey, rawValue, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(rawKey)
	value = strings.TrimSpace(rawValue)
	if before, _, found := strings.Cut(value, " #"); found {
		value = strings.TrimSpace(before)
	}
	value = strings.Trim(value, `"`)
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

// --------------------------------------------------------------------------
// Minimal indentation-based YAML subset scanner.
//
// klanker-voice's manifest.yaml and topic-map.yaml both have the same shape:
// a top-level `topics:` key holding a list of maps, each with scalar fields,
// one nested list-of-maps field (sources / keywords), and occasional
// multi-line block scalars (note/hook, `>-`) that must be skipped rather
// than mis-parsed as sibling keys. This is a deliberately narrow scanner for
// exactly that shape — not a general YAML parser — per the plan's
// prohibition on adding a new module dependency.

// yamlLine is one non-blank, non-full-line-comment line with its leading
// whitespace measured off as indent.
type yamlLine struct {
	indent int
	text   string
}

// scanYAMLLines reads r into yamlLines, dropping blank lines and full-line
// `#` comments. It does not strip inline comments — block-scalar body lines
// (skipped wholesale by skipYAMLBlock) may legitimately contain a `#`.
func scanYAMLLines(r io.Reader) ([]yamlLine, error) {
	var out []yamlLine
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		raw := scanner.Text()
		trimmed := strings.TrimLeft(raw, " ")
		indent := len(raw) - len(trimmed)
		content := strings.TrimRight(trimmed, " \t\r")
		if content == "" || strings.HasPrefix(content, "#") {
			continue
		}
		out = append(out, yamlLine{indent: indent, text: content})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// findTopLevelKey returns the index of the line "<key>:" at indent 0, or -1
// if not found.
func findTopLevelKey(lines []yamlLine, key string) int {
	want := key + ":"
	for i, l := range lines {
		if l.indent == 0 && l.text == want {
			return i
		}
	}
	return -1
}

// yamlKeyVal splits a "key: value" (or "key:" with no value) line on the
// first colon.
func yamlKeyVal(text string) (key, val string, hasVal bool) {
	before, after, found := strings.Cut(text, ":")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(before)
	val = strings.TrimSpace(after)
	return key, val, val != ""
}

// yamlScalar strips a "key: " prefix remainder down to its bare value,
// trimming surrounding quotes.
//
// For a quoted scalar (leading `"` or `'`), the value is everything between
// the opening quote and its matching closing quote — anything after that
// closing quote, including a trailing inline `# comment`, is discarded, and
// a `#` genuinely inside the quotes is preserved untouched (topic-map.yaml
// has real entries like `- term: "clanker maker"  # common ASR mis-hearing
// of "klanker"` where the comment must be stripped but an in-quote `#` must
// not be).
//
// For an unquoted scalar, a trailing ` #...` inline comment is stripped
// first (mirroring parseTOMLScalarLine), then any stray surrounding quotes
// are trimmed as a best-effort fallback.
func yamlScalar(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, ":")
	s = strings.TrimSpace(s)
	if len(s) > 0 && (s[0] == '"' || s[0] == '\'') {
		quote := s[0]
		if end := strings.IndexByte(s[1:], quote); end >= 0 {
			return s[1 : 1+end]
		}
		// Unterminated quote (malformed input) — fall through to the
		// best-effort unquoted handling below.
	}
	if before, _, found := strings.Cut(s, " #"); found {
		s = strings.TrimSpace(before)
	}
	s = strings.Trim(s, `"`)
	s = strings.Trim(s, `'`)
	return s
}

// skipYAMLBlock advances past any lines more indented than keyIndent — used
// both for multi-line block scalars (note/hook `>-`) and for nested
// structures under a key we don't otherwise care about.
func skipYAMLBlock(lines []yamlLine, i, keyIndent int) int {
	for i < len(lines) && lines[i].indent > keyIndent {
		i++
	}
	return i
}
