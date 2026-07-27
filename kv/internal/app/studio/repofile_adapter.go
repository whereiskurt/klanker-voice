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

// announcementWordsUnsetSentinel mirrors klanker_voice.telephony.controller.
// ANNOUNCEMENT_WORDS_UNSET_SENTINEL (D-03a) — both kv operator surfaces
// (kv telephony list, kv studio) must apply the IDENTICAL resolution rule
// the Python controller applies: once a words_env_var resolves to this
// literal value, the spoken trigger is DISABLED, exactly like an empty
// value. Without this, a shell or container carrying the sentinel would
// report a spoken trigger as live when it is actually inert — the console
// would be lying about the one thing this panel exists to show. Matched
// on the stripped, lowercased WHOLE value (see envTriggerStatus) — never a
// substring/token test.
const announcementWordsUnsetSentinel = "__unset__"

// ParseTelephonyGames parses every [[telephony.announcement]] block in the
// telephony TOML at path into a []GameEntry (quick task 260727-pdh), one
// per block, in file order. A minimal line scan (no TOML dependency),
// mirroring ReadTelephonyGate's scanning shape: a
// "[[telephony.announcement]]" header line opens a new entry; any
// subsequent line beginning with "[" closes it (re-opening another entry
// when it is itself another announcement header, or ending the scan's
// interest in the block otherwise). Lines inside a block are read via
// parseTOMLScalarLine (code_env_var/words_env_var) and parseTOMLArrayLine
// (dids/sms_reply_dids) — both already skip full-line comments, so a
// commented-out example line (the shipped telephony.toml's historical
// "# dids = [...]" precedent) is never mistaken for live config. A
// missing file returns a typed *RepoFileError; every caller degrades that
// to an empty games section rather than failing the whole view/report.
func ParseTelephonyGames(path string) ([]GameEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &RepoFileError{Path: path, Err: err}
	}
	defer f.Close()

	games := []GameEntry{}
	var current *GameEntry
	inBlock := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if current != nil {
				games = append(games, *current)
				current = nil
			}
			if strings.HasPrefix(line, "[[telephony.announcement]]") {
				current = &GameEntry{DIDs: []string{}, SmsReplyDIDs: []string{}}
				inBlock = true
			} else {
				inBlock = false
			}
			continue
		}
		if !inBlock || current == nil {
			continue
		}
		if key, values, ok := parseTOMLArrayLine(line); ok {
			switch key {
			case "dids":
				current.DIDs = values
			case "sms_reply_dids":
				current.SmsReplyDIDs = values
			}
			continue
		}
		key, value, ok := parseTOMLScalarLine(line)
		if !ok {
			continue
		}
		switch key {
		case "code_env_var":
			current.CodeEnvVar = value
		case "words_env_var":
			current.WordsEnvVar = value
		}
	}
	if current != nil {
		games = append(games, *current)
	}
	if err := scanner.Err(); err != nil {
		return nil, &RepoFileError{Path: path, Err: err}
	}
	return games, nil
}

// ReadTelephonyGames wraps ParseTelephonyGames, joining Root with the
// package-level telephonyConfigPath constant — the same convention
// ReadTelephonyGate already follows.
func (r RepoFiles) ReadTelephonyGames() ([]GameEntry, error) {
	path := filepath.Join(r.Root, telephonyConfigPath)
	return ParseTelephonyGames(path)
}

// AnnotateGameEnv fills CodeStatus/WordsStatus on every entry in games from
// the process environment (quick task 260727-pdh) — a PURE function that
// reads env var NAMES and returns STATUSES only; it never places a value
// on the returned struct (the same name-only posture SecretRef already
// documents). This reports the LOCAL shell's environment ONLY — the
// deployed values live in SSM and reach the container through
// telephony-edge's task definition; an operator reading "not set" locally
// is seeing their own shell, not prod.
func AnnotateGameEnv(games []GameEntry) []GameEntry {
	out := make([]GameEntry, len(games))
	for i, g := range games {
		g.CodeStatus = envTriggerStatus(g.CodeEnvVar)
		g.WordsStatus = envTriggerStatus(g.WordsEnvVar)
		out[i] = g
	}
	return out
}

// envTriggerStatus resolves one env var NAME to a "set" / "not set" / ""
// (no env var name configured) status. Applies the SAME D-03a sentinel
// rule the Python controller applies (announcementWordsUnsetSentinel,
// stripped + lowercased, exact whole-value match) so this console can
// never report an inert spoken trigger as live.
func envTriggerStatus(name string) string {
	if name == "" {
		return ""
	}
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "not set"
	}
	if strings.ToLower(value) == announcementWordsUnsetSentinel {
		return "not set"
	}
	return "set"
}

// parseTOMLArrayLine splits a "key = [\"a\", \"b\"]  # comment" line into
// its key and a slice of unquoted, trimmed array elements (quick task
// 260727-pdh) — parseTOMLScalarLine's quote-trimming only strips leading/
// trailing quote CHARACTERS from the whole value, so it never correctly
// splits a bracketed array literal into its individual elements. Skips
// full-line comments exactly as parseTOMLScalarLine does — load-bearing,
// not just hygiene: the shipped telephony.toml contains a commented-out
// example array line inside an announcement block, and a parser that
// mis-skipped the comment marker would surface phantom config to the
// operator. Returns ok=false for a scalar line, a comment line, or any
// line whose value is not bracketed. An empty array (`key = []`) returns
// ok=true with a non-nil, empty values slice.
func parseTOMLArrayLine(line string) (key string, values []string, ok bool) {
	if strings.HasPrefix(line, "#") {
		return "", nil, false
	}
	rawKey, rawValue, found := strings.Cut(line, "=")
	if !found {
		return "", nil, false
	}
	key = strings.TrimSpace(rawKey)
	value := strings.TrimSpace(rawValue)
	if idx := strings.Index(value, "]"); idx >= 0 {
		// Drop any trailing inline comment AFTER the closing bracket
		// (e.g. `dids = ["123"]  # a note`).
		value = value[:idx+1]
	}
	if key == "" || !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return "", nil, false
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
	if inner == "" {
		return key, []string{}, true
	}
	parts := strings.Split(inner, ",")
	values = make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(strings.Trim(strings.TrimSpace(p), `"`))
		if v != "" {
			values = append(values, v)
		}
	}
	return key, values, true
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
