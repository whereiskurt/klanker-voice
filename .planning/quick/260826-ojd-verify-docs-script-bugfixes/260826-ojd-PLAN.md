---
phase: 260826-ojd-verify-docs-script-bugfixes
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - scripts/verify-operator-docs.sh
  - apps/auth/webapp/src/app/(authlogin)/login/page.tsx
autonomous: true
requirements: [QUICK-260826-ojd]

must_haves:
  truths:
    - "A broken relative link in any checked doc makes scripts/verify-operator-docs.sh exit non-zero (not just print a failure line)."
    - "A soft-expired access code (expiresAt present and in the past) is no longer treated as a live credential by the leak check."
    - "An access code with no expiresAt field is still checked — never-expiring codes must not be silently dropped."
    - "A bare occurrence of a live access code in a checked doc still produces a failure line and a non-zero exit."
    - "The live code `demo` no longer matches the tier name `demo-tier` inside docs/assets/terminal/code-tier-list.session."
    - "On the clean tree the full run exits 0 with no failure lines and no skip lines."
    - "The auth login access-code input placeholder reads demodemo2026."
  artifacts:
    - scripts/verify-operator-docs.sh
    - apps/auth/webapp/src/app/(authlogin)/login/page.tsx
  key_links:
    - "The link-check while-loop must run in the CURRENT shell (process substitution), not at the end of a pipeline — otherwise bad() increments a subshell-local counter and the exit code stays 0."
    - "`kv code list --json` emits a single pretty-printed JSON ARRAY; expiresAt is `*int64` with omitempty (epoch MILLISECONDS, absent when the code never expires) — verified in kv/internal/app/cmd/code.go:30 and :523-528."
    - "The node parse must distinguish 'could not read the listing' (keep the existing skip branch) from 'listing read fine, zero active codes' (an ok, not a skip)."
---

<objective>
Repair three defects in `scripts/verify-operator-docs.sh` that leave it red on clean docs while structurally unable to fail on a broken link, and refresh one stale access-code placeholder in the auth login page.

Purpose: this script is the only mechanical guard that a credential or an unredacted caller number has not leaked into pages that sync to a public wiki. Right now its loudest check (links) cannot influence the exit code at all, and its credential check reports a false positive on a correctly-redacted capture — which trains the operator to ignore the output.

Output: a corrected verify script that passes clean AND has been watched failing on a deliberately-broken input, plus the placeholder edit, committed atomically and pushed to `main`.
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@./.claude/CLAUDE.md

@scripts/verify-operator-docs.sh
@apps/auth/webapp/src/app/(authlogin)/login/page.tsx
@docs/assets/terminal/code-tier-list.session
</context>

<pre_verified_facts>
Do NOT re-derive these. They were confirmed against the live repo during planning:

- Line 49 is the ONLY bare failure printf in the script. The only other two direct printfs of a status marker are the two skip branches at lines 69 and 187, which are intentional and must stay skips. Expected audit answer: **one** offending failure path. The executor must still run the audit and report the number it found.
- The link-check loop at lines 45-50 is the tail of a pipeline (`grep | sed | sort -u | while read`), so its body already runs in a subshell. Swapping the printf for `bad()` alone does NOT fix the exit code — the increment would be lost. The same script already uses the correct pattern at lines 175-178 (`while read ...; done < <( ... )`); mirror it.
- `kv code list --json` returns one pretty-printed JSON array (`json.NewEncoder` + `SetIndent("", "  ")`, kv/internal/app/cmd/code.go:523-528). Field `ExpiresAt *int64` with `json:"expiresAt,omitempty"` (code.go:30) — epoch milliseconds, key absent entirely when the code never expires.
- The existing line-oriented `sed` extraction has a second latent defect: against a compact single-line array the greedy `.*` would capture only the LAST code. Parsing the JSON removes this too.
- `node` is v22.1.0 and on PATH. `bash` on PATH is GNU bash 5.3 (`mapfile` and process substitution both available).
- `kv/bin/kv` already exists and is executable. If it ever goes missing: `go build -C kv -o bin/kv ./cmd/kv`.
- Zero relative doc links are currently broken (full link scan run during planning, clean). The verify gate is achievable without touching any doc.
- The false positive is `demo` matching `demo-tier` at docs/assets/terminal/code-tier-list.session lines 8 and 18. `demo` does NOT match `kphdemo-tier` even today (grep `-w` already rejects the alnum-preceded case) — the fix only needs to additionally reject hyphen adjacency.
- Current branch is `main`.
</pre_verified_facts>

<!-- planner-discipline-allow: FAIL: -->
<!-- planner-discipline-allow: SKIP: -->

<tasks>

<task type="auto">
  <name>Task 1: Make the link checker able to fail, and audit every other failure path</name>
  <files>scripts/verify-operator-docs.sh</files>
  <action>
Fix the link check at lines 42-51 so a broken relative target actually counts.

Two changes, both required — the first alone is insufficient:

1. Replace the bare failure printf at line 49 with a call to the existing `bad()` helper (line 36), preserving the current `<file> -> <target>` detail in the message it passes. Do not change the message shape beyond routing it through `bad()`.

2. Restructure the inner loop so it does NOT run in a subshell. Today the loop is the last stage of a `grep | sed | sort -u | while read` pipeline, so any increment of `fails` inside it is discarded when the subshell exits — the exit code would still be 0 even after change 1. Move the `grep`/`sed`/`sort -u` chain into a process substitution feeding the loop's stdin (`while read -r t; do ... done < <( ... )`), which is the pattern this same script already uses for the phone-number loop at lines 175-178. Keep the outer `for f in "${DOCS[@]}"` loop and the `d=$(dirname "$f")` binding exactly as they are.

Then AUDIT the whole script for any other place that reports a failure by printing directly instead of calling `bad()`, and fix each the same way. Scan every branch, not just the ones near the link check. Deliberate exclusions: the two skip branches (kv-not-found, and the access-code listing read failure) are informational, not failures — leave them printing directly.

REPORT in the SUMMARY the exact number of direct-print failure paths the audit found and fixed. State the number explicitly; do not just say "audited".

Change nothing else in this task. Do not touch the access-code block (Task 2 owns it), do not relax any assertion, and do not edit any file under docs/.
  </action>
  <verify>
    <automated>printf '\n[deliberate temporary break](./__no_such_file__.md)\n' >> docs/ops/pause-resume.md; AWS_PROFILE=klanker-application AWS_REGION=us-east-1 KV=kv/bin/kv bash scripts/verify-operator-docs.sh; echo "BROKEN_LINK_EXIT=$?"; git checkout -- docs/ops/pause-resume.md; git status --porcelain docs/</automated>
  </verify>
  <done>
- `BROKEN_LINK_EXIT` is >= 1 (report the observed number). Before this fix the same experiment exits 0 — if you observe 0, the subshell restructure (change 2) did not land.
- The run's output names the broken target `__no_such_file__.md` on a failure line.
- `git status --porcelain docs/` prints nothing after the revert — the temporary break is NOT staged, NOT committed, and NOT left on disk.
- SUMMARY states the audit count of direct-print failure paths found.
  </done>
</task>

<task type="auto">
  <name>Task 2: Filter expired codes out of the leak check and stop matching inside hyphenated identifiers</name>
  <files>scripts/verify-operator-docs.sh</files>
  <action>
Rework the live access-code leak check (currently lines 181-199). Two independent defects, one block.

DEFECT A — expired codes are still treated as credentials. `kv code expire` sets `expiresAt` rather than deleting the row, so retired codes linger in the listing forever and keep failing a check whose own comment says it asserts no *current* code appears. The existing line-oriented `sed` cannot correlate a code with its own `expiresAt`, so replace the extraction with a real JSON parse.

Use `node -e` reading the `kv code list --json` output on stdin. Do NOT add a `jq` dependency. The parser must:
  - `JSON.parse` the whole payload (a single pretty-printed array) and iterate its entries;
  - emit one code per line for every entry whose `expiresAt` is ABSENT (never-expiring codes MUST still be checked) or whose `expiresAt` is greater than `Date.now()` (epoch milliseconds — the units the field is already in);
  - drop entries whose `expiresAt` is less than or equal to now;
  - exit non-zero if stdin is empty or not parseable as an array, so the caller can tell a read failure apart from a legitimately-empty result.

Preserve the two outcomes distinctly in the shell:
  - parse/read failed → keep the existing skip line, unchanged wording, exactly as today;
  - parse succeeded but the active set is empty → print an `ok:` line saying there are no active codes to check. It must NOT print a skip, because the verify gate below asserts zero skip lines and "all codes retired" is a passing state, not an unknown one.
Capture the node exit status explicitly rather than inferring it from empty output.

DEFECT B — the word-boundary match fires inside hyphenated tokens. `grep -rqiw` treats `-` as a word boundary, so the live code `demo` matches the TIER name `demo-tier` in docs/assets/terminal/code-tier-list.session, where the code column is correctly redacted to `<code-03>`. That is a false positive on properly-redacted content.

Replace `-w` with an extended-regex match that additionally rejects hyphen and underscore adjacency: require the character before the code to be start-of-line or outside `[[:alnum:]_-]`, and the character after it to be end-of-line or outside `[[:alnum:]_-]`. Keep `-r`, `-q` and `-i`; pass the pattern via `-e` so a leading parenthesis is never read as an option. Required behavior: `demo` must NOT match `demo-tier` or `kphdemo-tier`, but MUST still match a bare `demo` and normal punctuation boundaries such as `demo.`, `"demo"`, `demo,` and `demo` at end of line.

Because the code value is interpolated into a regex, escape extended-regex metacharacters in the code before building the pattern (a sed character-class escape over the usual ERE metacharacter set) so a code containing punctuation cannot alter the pattern's meaning.

Leave the failure message itself as-is — it deliberately does not echo the leaked value, since this script is public. Do not widen or narrow which files are scanned. Do not weaken the check to make it green.
  </action>
  <verify>
    <automated>CODE=$(AWS_PROFILE=klanker-application AWS_REGION=us-east-1 kv/bin/kv code list --json 2>/dev/null | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{const a=JSON.parse(s);const n=Date.now();const c=a.find(r=>(r.expiresAt===undefined||r.expiresAt>n)&&/[a-zA-Z]/.test(r.code)&&r.code.length!==10);if(!c)process.exit(1);console.log(c.code)})'); echo "PICKED_LEN=${#CODE}"; printf '\n%s\n' "$CODE" >> docs/ops/pause-resume.md; AWS_PROFILE=klanker-application AWS_REGION=us-east-1 KV=kv/bin/kv bash scripts/verify-operator-docs.sh; echo "LEAKED_CODE_EXIT=$?"; git checkout -- docs/ops/pause-resume.md; git status --porcelain docs/</automated>
  </verify>
  <done>
- `LEAKED_CODE_EXIT` is >= 1 and the run's output contains the access-code leak failure line — a bare live code planted in a checked doc is STILL caught after the boundary tightening. Report the observed exit code.
- `git status --porcelain docs/` prints nothing after the revert — the planted code is NOT staged, NOT committed, and NOT left on disk.
- A separate clean run (no planted code) reports the access-code check as ok: the `demo` / `demo-tier` false positive in docs/assets/terminal/code-tier-list.session is gone, and no skip line is emitted for the access-code section.
- SUMMARY records how many codes the live listing returned in total versus how many survived the active filter.
  </done>
</task>

<task type="auto">
  <name>Task 3: Refresh the login placeholder, prove the full gate green, commit and push</name>
  <files>apps/auth/webapp/src/app/(authlogin)/login/page.tsx</files>
  <action>
Change the access-code input placeholder at apps/auth/webapp/src/app/(authlogin)/login/page.tsx:224 from `demo` to `demodemo2026`. The `demo` code was retired today (soft-expired 2026-08-26T18:10:40Z) and replaced by `demodemo2026` on the same tier (`demo-tier`, group `conference`). Change only the placeholder string — nothing else in that file, and no other source file.

Then run the full verify gate on a clean tree and confirm it is green with a completely quiet output (details in `<verify>` / `<done>`).

Commit each task's work atomically (this task's commit covers the placeholder), then push all of this task-set's commits to `main`. No PR, no branch — the current branch is already `main`. Before pushing, confirm `git status --porcelain` shows no leftover doc edits from either negative test.

SUMMARY must note, explicitly: apps/auth/webapp is a DEPLOYED Next.js app, so this placeholder will NOT change on auth.klankermaker.ai until the auth service is redeployed. This task does NOT deploy and must not attempt to.

SUMMARY must also note that scripts/verify-operator-docs.sh is not wired into any CI workflow, so nothing currently gates on its exit code — which is precisely why the exit-code defect survived.
  </action>
  <verify>
    <automated>git status --porcelain; AWS_PROFILE=klanker-application AWS_REGION=us-east-1 KV=kv/bin/kv bash scripts/verify-operator-docs.sh 2>&1 | tee /tmp/kv-verify-out.txt; echo "CLEAN_EXIT=${PIPESTATUS[0]}"; grep -c 'FAIL:' /tmp/kv-verify-out.txt; grep -c 'SKIP:' /tmp/kv-verify-out.txt; grep -n 'placeholder=' apps/auth/webapp/src/app/'(authlogin)'/login/page.tsx</automated>
  </verify>
  <done>
- `CLEAN_EXIT` is 0 on the clean tree.
- Both marker counts over the captured run output are 0 — the run reports no failed checks and no skipped checks. (These greps target the captured stdout at /tmp/kv-verify-out.txt, not any repo file.)
- The placeholder grep shows `demodemo2026` on the access-code input; the email input's own placeholder is untouched.
- `git status --porcelain` is clean apart from the intended tracked-file changes; no docs/ entry appears.
- Commits are pushed to `main`; `git log origin/main -1` shows the final commit.
- Pinning `KV=kv/bin/kv` was used for every run — the `kv` on PATH is stale and produces 13 false failures. If any run was done without the pin, redo it.
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| repo docs → public wiki | docs/operators/ and docs/ops/ sync outward via scripts/sync-wiki.py; anything committed there is published |
| live DynamoDB access-code table → this script's stdout | real credential values transit the check; the script itself is public and must never carry them |

## STRIDE Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation Plan |
|-----------|----------|-----------|----------|-------------|-----------------|
| T-ojd-01 | Information disclosure | access-code leak check, verify-operator-docs.sh:181-199 | high | mitigate | Narrowing the match to reject hyphen/underscore adjacency must not narrow it past a bare occurrence; Task 2's negative test plants a real live code and requires a non-zero exit before the fix is accepted. |
| T-ojd-02 | Tampering | link check, verify-operator-docs.sh:42-51 | medium | mitigate | A check that cannot influence the exit code silently authorises broken/redirected doc references; Task 1 requires an observed non-zero exit on a deliberately-broken link, not merely a printed line. |
| T-ojd-03 | Information disclosure | the fix itself | high | mitigate | The failure message stays value-free (it names the condition, never the code) because this script is published; no deny-list of literal secrets is introduced. |
| T-ojd-04 | Information disclosure | negative-test artifacts | high | mitigate | Both negative tests plant real content (a live code) in a doc that syncs public; each task's `<done>` requires `git status --porcelain docs/` to be empty after revert, before any commit. |
| T-ojd-SC | Tampering | dependency surface | low | accept | No package-manager install occurs: node and bash are already present, and a `jq` dependency is explicitly prohibited. |
</threat_model>

<verification>
1. Clean-tree run with `AWS_PROFILE=klanker-application AWS_REGION=us-east-1 KV=kv/bin/kv` exits 0 with zero failure lines and zero skip lines.
2. Negative test 1 (broken relative link) observed exiting non-zero, then reverted; observed exit code reported.
3. Negative test 2 (bare live access code planted in a checked doc) observed producing the leak failure and a non-zero exit, then reverted; observed exit code reported.
4. `docs/` is untouched in the final commit set — both negative-test edits reverted before committing.
5. No assertion was weakened, no check removed, no scanned-file list shortened. The docs are clean; the script was what was wrong.
6. Scope held to exactly two files: scripts/verify-operator-docs.sh and apps/auth/webapp/src/app/(authlogin)/login/page.tsx.
</verification>

<success_criteria>
- The link check counts its failures and drives the exit code (proven by a watched failure, not by inspection).
- The access-code leak check ignores soft-expired codes, still checks never-expiring ones, and no longer trips on hyphenated tier names — while still catching a bare live code (proven by a watched failure).
- The audit count of direct-print failure paths is reported in the SUMMARY.
- The login placeholder reads demodemo2026, with the not-live-until-redeploy caveat recorded.
- Work is committed atomically and pushed to `main`, no PR.
</success_criteria>

<output>
Create `.planning/quick/260826-ojd-verify-docs-script-bugfixes/260826-ojd-SUMMARY.md` when done.
</output>
