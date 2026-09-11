# Hibernate / Wake Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `kv hibernate` / `kv wake` — a third lifecycle tier that destroys the ECS services, NAT Gateway and ALB while retaining the NAT EIP, DNS, certs and all durable data, taking the AWS bill from ~$60/mo paused to ~$14/mo with no data restore needed to return.

**Architecture:** A second git-tracked boolean (`hibernated`) beside the shipped `paused` flag in `site.hcl` drives three config sites (`ecs_services.services`, `network.hcl`'s NAT and ALB toggles, the CloudFront unit's ALB-origin switch). Because terragrunt applies dependencies first — backwards for removal — `kv hibernate` dispatches two **strictly sequential** CI applies: phase 1 (`ecs-service,cloudfront`) removes everything referencing the ALB, then phase 2 (`network`) deletes the ALB and NAT. Wake runs them in reverse, which is terragrunt's natural order. All orchestration reuses the shipped `kv pause` seams (`GitAPI`, `GHAPI`, `ECSAPI`, `TargetHealthAPI`) so nothing new constructs an AWS client directly.

**Tech Stack:** Go 1.26.x + cobra v1.10.2 (`kv` CLI), Terraform 1.14.3 + Terragrunt (0.99.1 local / **0.97.1 in CI** — note the skew), aws-sdk-go-v2, GitHub Actions (`terragrunt-apply.yml`, dispatched never modified).

**Spec:** `docs/superpowers/specs/2026-09-10-hibernate-wake-design.md`

## Global Constraints

Every task's requirements implicitly include this section. Values copied verbatim from the spec.

- **D-02 — hibernated implies paused.** `hibernated = true` must force services to zero regardless of `paused`. `kv hibernate` sets only `hibernated` and leaves `paused` untouched; `kv wake` clears only `hibernated`.
- **D-03 — never `ecs_services.enabled = false`.** `exclude { actions = ["all"] }` in `ecs-service/terragrunt.hcl:13-16` skips the unit *including its destroy*, orphaning running, billing services that also block the ALB delete. The unit stays `enabled = true`; the **`services` list goes empty**.
- **D-07 — phase 2 never dispatches until phase 1 is terminally successful.** `terragrunt-apply.yml` sets `cancel-in-progress: true` on a group keyed by workflow+ref; both phases share it, so an eager phase-2 dispatch cancels phase 1 mid-destroy. No flag, including `--yes`, may relax this.
- **D-08 — module lists are always explicit; empty is refused.** The workflow's `modules` input treats empty as *apply all*, which would apply the `email` unit and steal the account's single active SES receipt rule set (`docs/operators/ses-active-rule-set-fix.md`). The string `email` must appear in no dispatch of either command.
- **D-11 — the kill-switch is never read, written, or referenced.** Orthogonal mechanism (`killswitch.go`), same rule as `kv pause` (D-24 of the pause spec).
- **DID release is never automated.** No code path may call the VoIP.ms DID cancellation API; no flag may release a DID.
- **D-12 — `--dry-run` issues no mutating call:** no workflow dispatch, no S3 write, no invalidation, no `update-service`.
- **Naming:** "klanker-voice" everywhere; never "voiceai" (copyright).
- **Terraform variable defaults must preserve today's behavior.** `alb_origin_enabled` defaults `true`; `retain_eip` defaults `false`. Every existing call site keeps current behavior with no change.

---

## File Structure

**New Go files** (all in `kv/internal/app/cmd/`, matching the one-file-per-concern layout of the shipped `lifecycle_*.go` set):

| File | Responsibility |
|---|---|
| `lifecycle_hibernateflag.go` | Thin `hibernated`-specific wrappers over the generalized flag engine |
| `lifecycle_phases.go` | The two-phase sequential dispatch engine + empty-module refusal |
| `lifecycle_page.go` | The S3 maintenance-page swap seam and its two operations |
| `lifecycle_verify.go` | Post-apply assertions: ALB gone, NAT gone, EIP retained, no services left |
| `hibernate.go` | `kv hibernate` / `kv wake` cobra commands + the completion report |

**Modified Go files:**

| File | Change |
|---|---|
| `lifecycle_pauseflag.go` | Generalize the line scanner to take a flag name; keep `ReadPausedFlag`/`SetPausedFlag` as wrappers so every shipped call site and test is untouched |
| `pause.go` | `kv pause status` grows a `hibernated` row |
| `root.go` | Register `NewHibernateCmd` / `NewWakeCmd` |

**Modified Terraform / config:**

| File | Change |
|---|---|
| `infra/terraform/modules/cloudfront/v1.0.0/variables.tf` + `main.tf` | `alb_origin_enabled` variable; ALB origin and the `/api/*` + `/health` behaviors become `dynamic` |
| `infra/terraform/modules/network/v1.0.0/natgw.tf` + `variables.tf` + `outputs.tf` | EIP lifetime split from the gateway's via `retain_eip` |
| `infra/terraform/live/site/site.hcl` | `hibernated` flag; `ecs_services.services` goes empty under it |
| `infra/terraform/live/site/region/us-east-1/network/network.hcl` | NAT/ALB `enabled` read the flag; `retain_eip = true` |
| `infra/terraform/live/site/global/cloudfront/terragrunt.hcl` | Passes `alb_origin_enabled` |
| `.github/workflows/build-voice.yml` | Skips the `index.html` upload + invalidation while hibernated |

**New non-code:** `apps/voice/client/public/maintenance.html`, `docs/ops/hibernate-wake.md`.

**Build/test commands** (run from the repo root):
- Go: `cd kv && go test ./internal/app/cmd/ -run <TestName> -v`
- Terraform module validate: `cd infra/terraform/modules/cloudfront/v1.0.0 && terraform init -backend=false && terraform validate`
- Terragrunt plan (needs `set -a && . infra/.envrc && set +a` and valid AWS SSO first): `terragrunt run -- plan` from the unit directory.

---

## Task 1: Generalize the lifecycle flag engine

The shipped scanner in `lifecycle_pauseflag.go` hardcodes `paused` in a package-level regexp. Hibernate needs the identical byte-preserving, comment-safe, ambiguity-refusing behavior for a second flag. Generalize by flag name; keep the existing exported functions as wrappers so no shipped call site or test changes.

**Files:**
- Modify: `kv/internal/app/cmd/lifecycle_pauseflag.go`
- Create: `kv/internal/app/cmd/lifecycle_hibernateflag.go`
- Create: `kv/internal/app/cmd/testdata/site-hcl/hibernated.hcl`
- Create: `kv/internal/app/cmd/testdata/site-hcl/unhibernated.hcl`
- Test: `kv/internal/app/cmd/lifecycle_hibernateflag_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces:
  - `const PausedFlagName = "paused"`, `const HibernatedFlagName = "hibernated"`
  - `func ReadLifecycleFlag(src []byte, name string) (bool, error)`
  - `func SetLifecycleFlag(src []byte, name string, want bool) (out []byte, changed bool, err error)`
  - `func ReadLifecycleFlagFile(repoRoot, name string) (bool, error)`
  - `func SetLifecycleFlagFile(repoRoot, name string, want bool) (changed bool, err error)`
  - `func ReadHibernatedFlagFile(repoRoot string) (bool, error)`
  - `func SetHibernatedFlagFile(repoRoot string, want bool) (changed bool, err error)`
  - Unchanged, now wrappers: `ReadPausedFlag`, `SetPausedFlag`, `ReadPausedFlagFile`, `SetPausedFlagFile`
  - Errors keep their existing identities: `ErrPausedFlagNotFound`, `ErrPausedFlagAmbiguous` (reused for any flag name; message text carries the name)

- [ ] **Step 1: Write the failing test**

Create `kv/internal/app/cmd/lifecycle_hibernateflag_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd kv && go test ./internal/app/cmd/ -run 'TestLifecycleFlag' -v`
Expected: FAIL — `undefined: ReadLifecycleFlag`, `undefined: PausedFlagName`, `undefined: HibernatedFlagName`, `undefined: SetLifecycleFlag`.

- [ ] **Step 3: Generalize the engine in `lifecycle_pauseflag.go`**

Replace the package-level `pausedAssignmentRe` and the four `locate`/`parse`/`Read`/`Set` functions with name-parameterized versions. Leave `codePortion` exactly as it is — it is already flag-agnostic and its string-literal tracking is what defeats the decoy test.

Replace this block:

```go
var pausedAssignmentRe = regexp.MustCompile(`^\s*paused\s*=\s*(true|false)\s*$`)
```

with:

```go
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
```

Replace `locatePausedFlag`, `parsePausedValue`, `ReadPausedFlag` and `SetPausedFlag` with:

```go
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
```

Generalize the two file helpers the same way — replace `ReadPausedFlagFile` and `SetPausedFlagFile` with:

```go
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
```

- [ ] **Step 4: Add the hibernated wrappers**

Create `kv/internal/app/cmd/lifecycle_hibernateflag.go`:

```go
// Package cmd -- the `hibernated` flag accessors. The scanning, comment
// preservation, decoy rejection and ambiguity refusal all live in the
// generalized engine in lifecycle_pauseflag.go; this file exists only so
// the hibernate call sites read as clearly as the pause ones do.
//
// `hibernated` is the deeper of the two operator switches in site.hcl. It
// implies `paused` (2026-09-10 spec D-02): when it is true the service list
// goes empty, so the paused overrides become moot. kv hibernate sets only
// this flag and never touches `paused`, so an operator who hibernates from
// a paused stack and later wakes lands back in the paused state they
// started from.
package cmd

// ReadHibernatedFlagFile reports the `hibernated` flag's value in site.hcl.
func ReadHibernatedFlagFile(repoRoot string) (bool, error) {
	return ReadLifecycleFlagFile(repoRoot, HibernatedFlagName)
}

// SetHibernatedFlagFile flips the `hibernated` flag in site.hcl to want.
func SetHibernatedFlagFile(repoRoot string, want bool) (changed bool, err error) {
	return SetLifecycleFlagFile(repoRoot, HibernatedFlagName, want)
}
```

- [ ] **Step 5: Run the new tests**

Run: `cd kv && go test ./internal/app/cmd/ -run 'TestLifecycleFlag' -v`
Expected: PASS (6 tests).

- [ ] **Step 6: Run the shipped pause tests to prove nothing regressed**

Run: `cd kv && go test ./internal/app/cmd/ -run 'TestPausedFlag' -v`
Expected: PASS — every shipped test, unchanged, including the byte-identical fixture round trip.

- [ ] **Step 7: Add the site.hcl fixtures**

Copy the two shipped fixtures and add a `hibernated` line to each, so later tasks can assert flag independence against realistic input:

```bash
cd kv/internal/app/cmd/testdata/site-hcl
sed 's/^\(\s*\)paused = false$/\1paused = false\n\1hibernated = false/' unpaused.hcl > unhibernated.hcl
sed 's/^\(\s*\)paused = false$/\1paused = false\n\1hibernated = true/'  unpaused.hcl > hibernated.hcl
diff unhibernated.hcl hibernated.hcl
```

Expected: a single-line difference, `hibernated = false` vs `hibernated = true`. If `sed` matched nothing (the fixture's indentation differs), edit both files by hand instead — the requirement is only that the two differ in exactly that one line.

- [ ] **Step 8: Run the whole cmd package**

Run: `cd kv && go test ./internal/app/cmd/`
Expected: PASS, no regressions anywhere in the package.

- [ ] **Step 9: Commit**

```bash
git add kv/internal/app/cmd/lifecycle_pauseflag.go \
        kv/internal/app/cmd/lifecycle_hibernateflag.go \
        kv/internal/app/cmd/lifecycle_hibernateflag_test.go \
        kv/internal/app/cmd/testdata/site-hcl/hibernated.hcl \
        kv/internal/app/cmd/testdata/site-hcl/unhibernated.hcl
git commit -m "feat(kv): generalize the lifecycle flag engine by flag name

ReadPausedFlag/SetPausedFlag become thin wrappers over a name-parameterized
engine, so \`hibernated\` gets the same byte-preserving, comment-safe,
decoy-rejecting, ambiguity-refusing treatment \`paused\` already has. Every
shipped call site and test is unchanged.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 2: CloudFront module — conditional ALB origin

`global/cloudfront/terragrunt.hcl:131` reads `try(dependency.use1_network.outputs.alb_dns_name, "")`, but `try` intercepts errors, not nulls — and `network/v1.0.0/outputs.tf:121-124` returns `null` when the ALB is disabled. That null reaches `domain_name` and fails the apply. Config alone cannot fix this; the module must be able to omit the ALB origin entirely.

**Files:**
- Modify: `infra/terraform/modules/cloudfront/v1.0.0/variables.tf`
- Modify: `infra/terraform/modules/cloudfront/v1.0.0/main.tf:111-112` (ALB origin), `:141` (`/api/*` behavior), `:154` (`/health` behavior)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: input `alb_origin_enabled` (bool, default `true`) on the cloudfront module. Task 4 passes it from the unit.

- [ ] **Step 1: Read the three blocks you are about to change**

Run: `sed -n '100,165p' infra/terraform/modules/cloudfront/v1.0.0/main.tf`
Confirm you can see: the S3 `origin` block, the ALB `origin` block whose `domain_name` reads `...alb_dns_name`, the default cache behavior targeting `s3-${local.primary}`, and two `ordered_cache_behavior` blocks targeting `alb-${local.primary}` for `/api/*` and `/health`. Note the exact attribute lines — you will reproduce them inside `dynamic` blocks and must not drop any.

- [ ] **Step 2: Add the variable**

Append to `infra/terraform/modules/cloudfront/v1.0.0/variables.tf`:

```hcl
# When false, the distribution is built with NO ALB origin and none of the
# ALB-targeted cache behaviors -- only the S3 origin and its default
# behavior survive. This is what lets the ALB be destroyed underneath a
# retained distribution during hibernation (2026-09-10 spec §5.1).
#
# It cannot be inferred from alb_dns_name being empty: the network module's
# output is null (not "") when the ALB is disabled, and the consuming unit's
# try() does not intercept null -- a null would reach domain_name and fail
# the apply before any conditional could run.
#
# Defaults true so every existing call site is unaffected.
variable "alb_origin_enabled" {
  description = "Include the ALB origin and its /api/*,/health cache behaviors"
  type        = bool
  default     = true
}
```

- [ ] **Step 3: Make the ALB origin conditional**

In `main.tf`, wrap the ALB `origin` block (the one whose `origin_id` is `"alb-${local.primary}"`) in a `dynamic`. Preserve every attribute the original block had — copy them across verbatim, changing only `var.` / `local.` references that must become `origin.value` lookups if you introduce any. The minimal shape, keeping the original body intact:

```hcl
  # ALB origin - the dynamic app surface (/api/offer SDP signaling, /health).
  # Omitted entirely while hibernated: the ALB is destroyed, its dns_name
  # output is null, and a null domain_name fails the apply.
  dynamic "origin" {
    for_each = var.alb_origin_enabled ? [1] : []
    content {
      domain_name = var.regional_origins_by_domain[each.key][local.primary].alb_dns_name
      origin_id   = "alb-${local.primary}"

      # ... preserve the original block's remaining attributes verbatim
      # (custom_origin_config and any others present at main.tf:111-125).
    }
  }
```

- [ ] **Step 4: Make the two ALB cache behaviors conditional**

Wrap each `ordered_cache_behavior` that targets `alb-${local.primary}` the same way, preserving each body verbatim:

```hcl
  # /api/* -> ALB. Managed-AllViewer forwards the viewer Host header (so the
  # ALB's host-header listener rule still matches). Dropped while hibernated.
  dynamic "ordered_cache_behavior" {
    for_each = var.alb_origin_enabled ? [1] : []
    content {
      # ... preserve main.tf:141-150 verbatim
      target_origin_id = "alb-${local.primary}"
    }
  }

  # /health -> ALB (real app liveness), never cached. Dropped while hibernated.
  dynamic "ordered_cache_behavior" {
    for_each = var.alb_origin_enabled ? [1] : []
    content {
      # ... preserve main.tf:154-163 verbatim
      target_origin_id = "alb-${local.primary}"
    }
  }
```

Leave the S3 origin and the default cache behavior completely untouched — the distribution must keep serving the bucket at the root in both states, because that is what serves the maintenance page.

- [ ] **Step 5: Validate the module parses**

```bash
cd infra/terraform/modules/cloudfront/v1.0.0
terraform init -backend=false
terraform validate
```

Expected: `Success! The configuration is valid.`
If it reports `each.key` is not available, the `dynamic` block is at the wrong nesting level — the `for_each` on the resource itself must still be in scope. Move the `dynamic` inside the resource body, not outside it.

- [ ] **Step 6: Verify the two states differ in exactly the intended way**

There is no terraform unit-test harness in this repo, so prove it by formatting and reading rather than by asserting:

```bash
cd infra/terraform/modules/cloudfront/v1.0.0
terraform fmt -check
grep -c 'dynamic "origin"' main.tf
grep -c 'dynamic "ordered_cache_behavior"' main.tf
```

Expected: `fmt -check` silent (exit 0); one `dynamic "origin"`; two `dynamic "ordered_cache_behavior"`. The real two-state proof is the plan diff in Task 4 Step 6.

- [ ] **Step 7: Commit**

```bash
git add infra/terraform/modules/cloudfront/v1.0.0/variables.tf \
        infra/terraform/modules/cloudfront/v1.0.0/main.tf
git commit -m "feat(infra): make the CloudFront ALB origin conditional

alb_origin_enabled (default true) gates the ALB origin and its /api/* and
/health behaviors, so the ALB can be destroyed under a retained
distribution. Required because the network module's alb_dns_name output is
null when the ALB is disabled, and the consuming unit's try() does not
intercept null.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 3: Network module — retain the NAT EIP

`natgw.tf` gates the EIP, the gateway and the route all on `var.nat_gateway.enabled`, so disabling NAT releases the VoIP.ms-allowlisted address. Split the EIP's lifetime so it survives, unattached, at ~$3.60/mo.

**Files:**
- Modify: `infra/terraform/modules/network/v1.0.0/natgw.tf`
- Modify: `infra/terraform/modules/network/v1.0.0/variables.tf` (the `nat_gateway` object type)
- Modify: `infra/terraform/modules/network/v1.0.0/outputs.tf:51-54`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `nat_gateway.retain_eip` (bool, optional, default `false`) on the network module; `nat_eip_public_ip` output now emits whenever the EIP exists, not only when the gateway does.

- [ ] **Step 1: Confirm the current shape**

Run: `cat infra/terraform/modules/network/v1.0.0/natgw.tf` and `grep -n -A12 'variable "nat_gateway"' infra/terraform/modules/network/v1.0.0/variables.tf`
Confirm all three resources carry `count = var.nat_gateway.enabled ? 1 : 0` and note whether the variable is an `object({...})` with `optional()` attributes — the next step's syntax depends on it.

- [ ] **Step 2: Add `retain_eip` to the variable**

In `variables.tf`, inside the `nat_gateway` variable's object type, add:

```hcl
    # When true the Elastic IP is allocated even with enabled = false, so a
    # NAT teardown does not release the address. klanker-voice allowlists
    # this IP at VoIP.ms for the API relay and the CTF OTP endpoint; a fresh
    # IP does not fail at wake, it fails later and silently on the first SMS
    # or OTP call. ~$3.60/mo for an unattached allocation.
    retain_eip = optional(bool, false)
```

If the variable is not an `object(...)` with `optional()` support, add it as a plain attribute and give `network.hcl` an explicit value in Task 4 instead of relying on the default.

- [ ] **Step 3: Split the EIP's count from the gateway's**

In `natgw.tf`, change **only** the EIP resource's count line:

```hcl
# NAT Gateway needs an EIP -- and hibernation keeps the EIP after the
# gateway is gone (see nat_gateway.retain_eip), so this count is
# deliberately NOT the same expression as the gateway's below.
resource "aws_eip" "nat" {
  count  = var.nat_gateway.enabled || var.nat_gateway.retain_eip ? 1 : 0
  domain = "vpc"
  # ... tags unchanged
}
```

Leave `aws_nat_gateway.nat` and `aws_route.private_nat_gateway` gated on `var.nat_gateway.enabled` alone. The gateway's `allocation_id = aws_eip.nat[0].id` stays valid because whenever `enabled` is true the EIP count is also 1.

- [ ] **Step 4: Widen the EIP output**

In `outputs.tf`, replace the `nat_eip_public_ip` output:

```hcl
output "nat_eip_public_ip" {
  description = "Public IP of the NAT EIP (whenever the EIP exists, attached or retained)"
  value       = length(aws_eip.nat) > 0 ? aws_eip.nat[0].public_ip : null
}
```

Using `length(...)` rather than repeating the boolean expression keeps the output correct if the count expression changes again. Leave `nat_gateway_id` gated on `var.nat_gateway.enabled` — a retained EIP has no gateway, and the output should be null then.

- [ ] **Step 5: Validate**

```bash
cd infra/terraform/modules/network/v1.0.0
terraform init -backend=false
terraform validate
terraform fmt -check
```

Expected: valid, and `fmt -check` exits 0.

- [ ] **Step 6: Commit**

```bash
git add infra/terraform/modules/network/v1.0.0/natgw.tf \
        infra/terraform/modules/network/v1.0.0/variables.tf \
        infra/terraform/modules/network/v1.0.0/outputs.tf
git commit -m "feat(infra): retain the NAT EIP independently of the gateway

nat_gateway.retain_eip (default false) keeps aws_eip.nat allocated when the
gateway is disabled, so a hibernation does not release the
VoIP.ms-allowlisted egress IP. nat_eip_public_ip now emits whenever the EIP
exists, attached or not.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 4: Config plumbing — the `hibernated` flag

Wire the flag through the three config sites. This is the task where D-03 is honored: the service list goes empty, the unit stays enabled.

**Files:**
- Modify: `infra/terraform/live/site/site.hcl:164` (add `hibernated`), and the `ecs_services` block at `:166-180`
- Modify: `infra/terraform/live/site/region/us-east-1/network/network.hcl` (the `nat_gateway` and `alb` blocks)
- Modify: `infra/terraform/live/site/global/cloudfront/terragrunt.hcl` (pass `alb_origin_enabled`)

**Interfaces:**
- Consumes: `alb_origin_enabled` (Task 2), `nat_gateway.retain_eip` (Task 3).
- Produces: a `hibernated` boolean at the top level of `site.hcl`'s `locals`, readable by Task 1's `ReadHibernatedFlagFile`.

- [ ] **Step 1: Add the flag beside `paused`**

In `site.hcl`, directly below the existing `paused = false` line:

```hcl
  # Operator hibernate switch (kv hibernate / kv wake -- avoid editing by hand).
  # true => everything `paused` does, PLUS: the ECS services, their target
  # groups and listener rules are destroyed outright, and the NAT Gateway and
  # ALB are torn down. The NAT *EIP* is retained (unattached) so the VoIP.ms
  # allowlist stays valid. VPC, Route53, ACM, DynamoDB, the S3 ledger, the
  # cf-assets bucket and ECR all stay put, so wake needs no restore.
  # See docs/superpowers/specs/2026-09-10-hibernate-wake-design.md.
  hibernated = false
```

Keep the `=` alignment consistent with the surrounding block — the flag rewriter preserves whatever alignment it finds, but a human reads this file too.

- [ ] **Step 2: Empty the service list under hibernation**

In `site.hcl`'s `ecs_services` block, keep `enabled` as it is and make `services` conditional:

```hcl
  ecs_services = {
    # NOTE: this stays true under hibernation. Setting it false would make
    # ecs-service/terragrunt.hcl's `exclude { actions = ["all"] }` skip the
    # unit INCLUDING its destroy, orphaning the services -- still running,
    # still billing, and still holding the listener rules that block the ALB
    # delete. Emptying the list below is what actually removes them.
    enabled = true

    # Under hibernation the list goes empty: the module's for_each maps
    # collapse and terraform destroys the services, target groups and
    # listener rules in-graph.
    services = local.hibernated ? [] : [
      # ... the existing list body, unchanged
    ]
  }
```

- [ ] **Step 3: Gate NAT and ALB in `network.hcl`**

`network.hcl` already reads `site_vars`. Change the two blocks:

```hcl
    nat_gateway = {
      enabled = !local.site_vars.locals.hibernated
      # Keep the VoIP.ms-allowlisted egress IP across a hibernation.
      retain_eip = true
    }
```

```hcl
    alb = {
      enabled                    = !local.site_vars.locals.hibernated
      enable_deletion_protection = false
      ssl_policy                 = "ELBSecurityPolicy-TLS13-1-2-2021-06"
      logs_force_destroy         = true
    }
```

- [ ] **Step 4: Pass `alb_origin_enabled` from the CloudFront unit**

In `global/cloudfront/terragrunt.hcl`'s `inputs` block, beside the existing `waf_web_acl_arns = {}` line:

```hcl
  # Drop the ALB origin and its /api/*,/health behaviors while hibernated --
  # the ALB is destroyed, so its dns_name output is null and a null
  # domain_name would fail this apply.
  alb_origin_enabled = !local.site_vars.locals.hibernated
```

Confirm `local.site_vars` is already defined in that file's `locals` block (it is — it reads `find_in_parent_folders("site.hcl")` at the top). If the file binds only `_zone`/`_subs`/`_cf_doms`, add `site_vars` to the same `locals` block rather than introducing a second one.

- [ ] **Step 5: Check HCL formatting**

```bash
cd infra
terragrunt hcl format --check
```

Expected: exit 0.
**Trap:** `hcl format --check` validates *formatting only, not evaluation* — it passed cleanly against the `site.hcl` conditional-type defect that broke every terragrunt unit in Phase 16. A clean result here proves nothing about correctness. Step 6 is the real gate.

- [ ] **Step 6: Prove both states evaluate — the real gate**

Requires AWS SSO (`aws sso login --profile klanker-terraform`) and `set -a && . infra/.envrc && set +a` in the shell first.

```bash
set -a && . infra/.envrc && set +a
cd infra/terraform/live/site/region/us-east-1/network
terragrunt run -- plan | tee /tmp/plan-unhibernated.txt
```

Expected: `No changes.` — with `hibernated = false` the plan against the live stack must be empty. **Any proposed change here means the refactor altered the running configuration and must be fixed before going further.**

Then flip the flag locally (do not commit yet) and re-plan all three units:

```bash
sed -i '' 's/^  hibernated = false$/  hibernated = true/' infra/terraform/live/site/site.hcl
cd infra/terraform/live/site/region/us-east-1/network && terragrunt run -- plan | tee /tmp/plan-hibernated-network.txt
cd ../ecs-service && terragrunt run -- plan | tee /tmp/plan-hibernated-ecs.txt
cd ../../../global/cloudfront && terragrunt run -- plan | tee /tmp/plan-hibernated-cf.txt
```

Read each plan and confirm:
- **network:** `aws_lb.lb_public[0]` destroyed, `aws_nat_gateway.nat[0]` destroyed, `aws_route.private_nat_gateway[0]` destroyed, and **`aws_eip.nat[0]` NOT in the destroy list** — this is the D-04 proof.
- **ecs-service:** all three `aws_ecs_service`, their `aws_lb_target_group` and `aws_lb_listener_rule` destroyed. The plan must **not** be empty and must **not** say the unit was excluded — an excluded unit is the D-03 orphan failure.
- **cloudfront:** the distribution **updated in place**, with the ALB origin and two ordered behaviors removed. It must **not** be destroyed and recreated.

Restore the flag before committing: `sed -i '' 's/^  hibernated = true$/  hibernated = false/' infra/terraform/live/site/site.hcl`

- [ ] **Step 7: Confirm the flag reads back through Task 1's code**

```bash
cd kv && go run ./cmd/kv pause status 2>/dev/null | head -3 || true
```

This will not show the hibernated row until Task 8. For now just assert the file parses:

```bash
cd kv && cat > /tmp/flagcheck_test.go <<'EOF'
package cmd

import "testing"

func TestHibernatedFlagReadsFromRealSiteHCL(t *testing.T) {
	got, err := ReadHibernatedFlagFile("../../..")
	if err != nil {
		t.Fatalf("ReadHibernatedFlagFile: %v", err)
	}
	if got != false {
		t.Errorf("hibernated = %v, want false (the committed state)", got)
	}
}
EOF
cp /tmp/flagcheck_test.go internal/app/cmd/zz_flagcheck_test.go
go test ./internal/app/cmd/ -run TestHibernatedFlagReadsFromRealSiteHCL -v
rm internal/app/cmd/zz_flagcheck_test.go
```

Expected: PASS. This is a scaffold check, deliberately removed again — the permanent coverage is Task 1's fixture tests.

- [ ] **Step 8: Commit**

```bash
git add infra/terraform/live/site/site.hcl \
        infra/terraform/live/site/region/us-east-1/network/network.hcl \
        infra/terraform/live/site/global/cloudfront/terragrunt.hcl
git commit -m "feat(infra): add the hibernated flag and wire it through

site.hcl gains \`hibernated\`; under it the ecs_services list goes EMPTY
while the unit stays enabled (emptying removes the services in-graph;
disabling would make terragrunt exclude skip the destroy and orphan them),
NAT and ALB switch off with the EIP retained, and the CloudFront unit drops
its ALB origin.

Verified by plan: hibernated=false is a no-op against the live stack;
hibernated=true destroys the ALB, NAT and route but NOT aws_eip.nat,
destroys all three services with their target groups and listener rules,
and updates the CloudFront distribution in place.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 5: The two-phase sequential dispatch engine

The heart of the feature and the place D-07 is enforced. Terragrunt applies dependencies first, which is backwards for removal, so removal is two applies — and because both share `terragrunt-apply.yml`'s `cancel-in-progress` concurrency group, dispatching phase 2 early cancels phase 1 mid-destroy.

**Files:**
- Create: `kv/internal/app/cmd/lifecycle_phases.go`
- Test: `kv/internal/app/cmd/lifecycle_phases_test.go`

**Interfaces:**
- Consumes: `GHAPI` (`lifecycle_gh.go`), `TerragruntApplyWorkflow` (`lifecycle_gh.go:44`).
- Produces:
  - `type ApplyPhase struct { Name string; Modules string }`
  - `var HibernatePhases []ApplyPhase` / `var WakePhases []ApplyPhase`
  - `var ErrEmptyModuleList error`, `var ErrForbiddenModule error`
  - `func ValidatePhases(phases []ApplyPhase) error`
  - `func RunApplyPhases(ctx context.Context, gh GHAPI, ref string, phases []ApplyPhase, now func() time.Time, w io.Writer) ([]string, error)` — returns one run ID per completed phase.

- [ ] **Step 1: Write the failing test**

Create `kv/internal/app/cmd/lifecycle_phases_test.go`:

```go
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// recordingGH records every call in order so a test can assert not just
// what happened but the sequence -- which is the whole point of D-07.
type recordingGH struct {
	calls     []string
	watchErr  map[string]error
	runIDSeq  []string
	runIDNext int
}

func (g *recordingGH) AuthStatus(ctx context.Context) error { return nil }

func (g *recordingGH) DispatchWorkflow(ctx context.Context, workflow, ref string, inputs map[string]string) error {
	g.calls = append(g.calls, fmt.Sprintf("dispatch:%s", inputs["modules"]))
	return nil
}

func (g *recordingGH) LatestRunID(ctx context.Context, workflow, ref string, notBefore time.Time) (string, error) {
	id := g.runIDSeq[g.runIDNext]
	g.runIDNext++
	return id, nil
}

func (g *recordingGH) WatchRun(ctx context.Context, runID string, w io.Writer) error {
	g.calls = append(g.calls, "watch:"+runID)
	if err, ok := g.watchErr[runID]; ok {
		return err
	}
	return nil
}

func fixedNow() func() time.Time {
	t := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// D-07: each phase is dispatched, then watched to completion, before the
// next phase is dispatched at all.
func TestRunApplyPhases_DispatchesStrictlySequentially(t *testing.T) {
	gh := &recordingGH{runIDSeq: []string{"run-1", "run-2"}}

	ids, err := RunApplyPhases(context.Background(), gh, "main", HibernatePhases, fixedNow(), io.Discard)
	if err != nil {
		t.Fatalf("RunApplyPhases error: %v", err)
	}

	want := []string{
		"dispatch:ecs-service,cloudfront",
		"watch:run-1",
		"dispatch:network",
		"watch:run-2",
	}
	if len(gh.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", gh.calls, want)
	}
	for i := range want {
		if gh.calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, gh.calls[i], want[i])
		}
	}
	if len(ids) != 2 {
		t.Errorf("run ids = %v, want 2", ids)
	}
}

// D-07: a failed phase 1 aborts before phase 2 is dispatched. This is the
// test that matters most -- the failure it guards against is a cancelled
// run holding a half-completed destroy.
func TestRunApplyPhases_FailedPhaseAbortsBeforeNextDispatch(t *testing.T) {
	gh := &recordingGH{
		runIDSeq: []string{"run-1", "run-2"},
		watchErr: map[string]error{"run-1": errors.New("run failed")},
	}

	_, err := RunApplyPhases(context.Background(), gh, "main", HibernatePhases, fixedNow(), io.Discard)
	if err == nil {
		t.Fatal("RunApplyPhases error = nil, want a phase-1 failure")
	}
	if !strings.Contains(err.Error(), "ecs-service,cloudfront") {
		t.Errorf("error %q does not name the failing phase's modules", err)
	}

	for _, c := range gh.calls {
		if c == "dispatch:network" {
			t.Fatal("phase 2 was dispatched after phase 1 failed -- D-07 violated")
		}
	}
}

// Wake runs the same units in terragrunt's natural dependency order.
func TestWakePhases_ReverseHibernateOrder(t *testing.T) {
	if len(WakePhases) != 2 {
		t.Fatalf("WakePhases has %d phases, want 2", len(WakePhases))
	}
	if WakePhases[0].Modules != "network" {
		t.Errorf("wake phase 1 modules = %q, want \"network\"", WakePhases[0].Modules)
	}
	if WakePhases[1].Modules != "ecs-service,cloudfront" {
		t.Errorf("wake phase 2 modules = %q, want \"ecs-service,cloudfront\"", WakePhases[1].Modules)
	}
}

// D-08: an empty module list means apply-everything, which would apply the
// email unit and steal the account's active SES receipt rule set.
func TestValidatePhases_RefusesEmptyModuleList(t *testing.T) {
	err := ValidatePhases([]ApplyPhase{{Name: "bad", Modules: ""}})
	if !errors.Is(err, ErrEmptyModuleList) {
		t.Fatalf("error = %v, want ErrEmptyModuleList", err)
	}
}

// D-08: the email unit must never appear in a dispatch.
func TestValidatePhases_RefusesEmailModule(t *testing.T) {
	err := ValidatePhases([]ApplyPhase{{Name: "bad", Modules: "network,email"}})
	if !errors.Is(err, ErrForbiddenModule) {
		t.Fatalf("error = %v, want ErrForbiddenModule", err)
	}
}

// The shipped phase lists must themselves pass validation.
func TestShippedPhaseListsValidate(t *testing.T) {
	if err := ValidatePhases(HibernatePhases); err != nil {
		t.Errorf("HibernatePhases: %v", err)
	}
	if err := ValidatePhases(WakePhases); err != nil {
		t.Errorf("WakePhases: %v", err)
	}
}

// RunApplyPhases validates before dispatching anything at all.
func TestRunApplyPhases_ValidatesBeforeFirstDispatch(t *testing.T) {
	gh := &recordingGH{runIDSeq: []string{"run-1"}}

	_, err := RunApplyPhases(context.Background(), gh, "main",
		[]ApplyPhase{{Name: "bad", Modules: ""}}, fixedNow(), io.Discard)
	if !errors.Is(err, ErrEmptyModuleList) {
		t.Fatalf("error = %v, want ErrEmptyModuleList", err)
	}
	if len(gh.calls) != 0 {
		t.Errorf("calls = %v, want none -- validation must precede dispatch", gh.calls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd kv && go test ./internal/app/cmd/ -run 'TestRunApplyPhases|TestValidatePhases|TestWakePhases|TestShippedPhaseLists' -v`
Expected: FAIL — `undefined: RunApplyPhases`, `undefined: ApplyPhase`, `undefined: HibernatePhases`.

- [ ] **Step 3: Write the implementation**

Create `kv/internal/app/cmd/lifecycle_phases.go`:

```go
// Package cmd -- the two-phase sequential apply engine behind kv hibernate
// and kv wake (2026-09-10 spec §4).
//
// Terragrunt applies dependencies FIRST: network before ecs-service. That
// is right for creation and exactly backwards for removal -- a single apply
// would try to delete the ALB while ecs-service still holds
// aws_lb_listener_rule resources bound to its listener, and fail partway,
// leaving the stack half torn down.
//
// So removal is two applies, and they must be STRICTLY sequential.
// terragrunt-apply.yml declares
//
//	concurrency: { group: "${{ github.workflow }}-${{ github.ref }}",
//	               cancel-in-progress: true }
//
// and both phases dispatch against the same ref, so they land in the SAME
// concurrency group. Dispatching phase 2 while phase 1 is still running
// CANCELS PHASE 1 MID-APPLY -- with a half-completed destroy inside it.
// That is why RunApplyPhases watches each run to a terminal state before
// dispatching the next, and why no flag (including --yes) may relax it.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ApplyPhase is one dispatch of terragrunt-apply.yml: a human-readable
// name for the report, and the exact `modules` workflow input.
type ApplyPhase struct {
	Name    string
	Modules string
}

// HibernatePhases is the removal order. Phase 1 removes everything that
// references the ALB (the services with their target groups and listener
// rules, and CloudFront's ALB origin); only then can phase 2 delete the
// ALB and the NAT Gateway.
var HibernatePhases = []ApplyPhase{
	{Name: "services and CloudFront origin", Modules: "ecs-service,cloudfront"},
	{Name: "ALB and NAT Gateway", Modules: "network"},
}

// WakePhases is the creation order -- the reverse of HibernatePhases, and
// terragrunt's own natural dependency order, so wake carries no equivalent
// ordering hazard.
var WakePhases = []ApplyPhase{
	{Name: "ALB and NAT Gateway", Modules: "network"},
	{Name: "services and CloudFront origin", Modules: "ecs-service,cloudfront"},
}

// ErrEmptyModuleList is returned when a phase carries no modules.
// terragrunt-apply.yml documents its `modules` input as "empty = all", so
// an empty string is not a harmless default -- it applies EVERY unit,
// including `email`. See ErrForbiddenModule.
var ErrEmptyModuleList = errors.New("phase has an empty module list, which terragrunt-apply.yml treats as apply-all")

// ErrForbiddenModule is returned when a phase names a unit that must never
// be applied by these commands.
var ErrForbiddenModule = errors.New("phase names a forbidden module")

// forbiddenModules are units these commands must never apply. `email` owns
// aws_ses_active_receipt_rule_set, and SES allows exactly ONE active
// receipt rule set per account per region -- applying it steals the slot
// and silently kills klanker-maker's inbound mail. See
// docs/operators/ses-active-rule-set-fix.md; the underlying defect is
// unfixed, so this guard is load-bearing.
var forbiddenModules = map[string]bool{"email": true}

// ValidatePhases checks every phase before any dispatch happens, so a bad
// list fails before it can touch the account.
func ValidatePhases(phases []ApplyPhase) error {
	if len(phases) == 0 {
		return fmt.Errorf("%w: no phases", ErrEmptyModuleList)
	}
	for _, p := range phases {
		if strings.TrimSpace(p.Modules) == "" {
			return fmt.Errorf("%w: phase %q", ErrEmptyModuleList, p.Name)
		}
		for _, m := range strings.Split(p.Modules, ",") {
			if forbiddenModules[strings.TrimSpace(m)] {
				return fmt.Errorf("%w: phase %q names %q", ErrForbiddenModule, p.Name, strings.TrimSpace(m))
			}
		}
	}
	return nil
}

// RunApplyPhases dispatches each phase in order and watches it to a
// terminal state before dispatching the next, returning the run id of every
// completed phase. It returns on the first failing phase without
// dispatching any later one (D-07).
func RunApplyPhases(ctx context.Context, gh GHAPI, ref string, phases []ApplyPhase, now func() time.Time, w io.Writer) ([]string, error) {
	if err := ValidatePhases(phases); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}

	runIDs := make([]string, 0, len(phases))
	for i, p := range phases {
		fmt.Fprintf(w, "\nphase %d/%d: %s (modules=%s)\n", i+1, len(phases), p.Name, p.Modules)

		dispatchedAt := now()
		if err := gh.DispatchWorkflow(ctx, TerragruntApplyWorkflow, ref, map[string]string{"modules": p.Modules}); err != nil {
			return runIDs, fmt.Errorf("phase %d (%s) dispatch: %w", i+1, p.Modules, err)
		}
		runID, err := gh.LatestRunID(ctx, TerragruntApplyWorkflow, ref, dispatchedAt)
		if err != nil {
			return runIDs, fmt.Errorf("phase %d (%s) resolve run id: %w", i+1, p.Modules, err)
		}

		// The sequential gate. Nothing below dispatches until this returns.
		if err := gh.WatchRun(ctx, runID, w); err != nil {
			return runIDs, fmt.Errorf("phase %d (%s) run %s did not complete successfully: %w -- "+
				"later phases were NOT dispatched; the stack is part-way through and "+
				"`kv pause status` will show what actually applied", i+1, p.Modules, runID, err)
		}
		runIDs = append(runIDs, runID)
	}
	return runIDs, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `cd kv && go test ./internal/app/cmd/ -run 'TestRunApplyPhases|TestValidatePhases|TestWakePhases|TestShippedPhaseLists' -v`
Expected: PASS (7 tests).

- [ ] **Step 5: Commit**

```bash
git add kv/internal/app/cmd/lifecycle_phases.go kv/internal/app/cmd/lifecycle_phases_test.go
git commit -m "feat(kv): two-phase sequential apply engine for hibernate/wake

Removal needs the reverse of terragrunt's dependency order, so it is two
applies -- and because both share terragrunt-apply.yml's cancel-in-progress
concurrency group, dispatching phase 2 early would cancel phase 1 mid
destroy. RunApplyPhases watches each run to terminal state before
dispatching the next, and aborts without dispatching later phases on any
failure.

ValidatePhases refuses an empty module list (which the workflow treats as
apply-all) and refuses the email unit outright, whose apply steals the
account's single active SES receipt rule set.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 6: The maintenance page and its S3 swap

The SPA is published to S3 by CI, not terraform, so the page swap is an explicit operation. The real `index.html` is shadowed, never destroyed.

**Files:**
- Create: `apps/voice/client/public/maintenance.html`
- Create: `kv/internal/app/cmd/lifecycle_page.go`
- Test: `kv/internal/app/cmd/lifecycle_page_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type PageStoreAPI interface { CopyObject(ctx context.Context, bucket, srcKey, dstKey string) error; PutObject(ctx context.Context, bucket, key string, body []byte, cacheControl, contentType string) error; DeleteObject(ctx context.Context, bucket, key string) error; HeadObject(ctx context.Context, bucket, key string) (bool, error) }`
  - `const SPABackupKey = "index.spa.html"`, `const IndexKey = "index.html"`, `const IndexCacheControl = "no-cache, no-store, must-revalidate"`
  - `func SwapToMaintenance(ctx context.Context, api PageStoreAPI, bucket string, page []byte, w io.Writer) error`
  - `func RestoreSPA(ctx context.Context, api PageStoreAPI, bucket string, w io.Writer) error`

- [ ] **Step 1: Write the maintenance page**

Create `apps/voice/client/public/maintenance.html`. Keep the OG tags aligned with the real `index.html` — the unfurl requires a self-hosted, same-origin PNG (a GitHub-hosted card host is rejected by Signal), so reuse whatever image path `index.html` already references.

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>KlankerMaker Voice — on hold</title>

    <!-- Keep these in step with client/index.html. The og:image MUST be a
         self-hosted same-origin PNG; third-party card hosts are rejected by
         some clients, which is why the real index.html self-hosts it. -->
    <meta property="og:title" content="KlankerMaker Voice" />
    <meta property="og:description" content="The voice concierge is resting. Back when there's something worth demoing." />
    <meta property="og:type" content="website" />
    <meta property="og:url" content="https://voice.klankermaker.ai/" />
    <meta property="og:image" content="https://voice.klankermaker.ai/og-card.png" />
    <meta name="twitter:card" content="summary_large_image" />

    <style>
      :root { color-scheme: dark; }
      body {
        margin: 0; min-height: 100vh;
        display: grid; place-items: center;
        background: #0b0d10; color: #e6e8eb;
        font: 16px/1.6 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
        padding: 24px;
      }
      main { max-width: 30rem; text-align: center; }
      h1 { font-size: 1.5rem; font-weight: 600; margin: 0 0 0.75rem; letter-spacing: -0.01em; }
      p { margin: 0 0 0.5rem; color: #9aa3ad; }
      .dot {
        display: inline-block; width: 0.5rem; height: 0.5rem;
        border-radius: 50%; background: #f2b544; margin-right: 0.5rem;
        vertical-align: 0.05em;
      }
    </style>
  </head>
  <body>
    <main>
      <h1><span class="dot"></span>On hold</h1>
      <p>The KlankerMaker voice concierge is hibernating to keep the lights cheap.</p>
      <p>It'll be back when there's something worth demoing.</p>
    </main>
  </body>
</html>
```

Verify the `og:image` path matches the real one:

```bash
grep -o 'og:image" content="[^"]*"' apps/voice/client/index.html
```

If it differs, edit `maintenance.html` to match exactly — a broken unfurl is the specific failure this page exists to avoid.

- [ ] **Step 2: Write the failing test**

Create `kv/internal/app/cmd/lifecycle_page_test.go`:

```go
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd kv && go test ./internal/app/cmd/ -run 'TestSwapToMaintenance|TestRestoreSPA' -v`
Expected: FAIL — `undefined: SwapToMaintenance`, `undefined: IndexKey`.

- [ ] **Step 4: Write the implementation**

Create `kv/internal/app/cmd/lifecycle_page.go`:

```go
// Package cmd -- the maintenance-page swap behind kv hibernate / kv wake
// (2026-09-10 spec §7).
//
// IMPORTANT: unlike the site.hcl flags, this is NOT protected by the
// "config is the guard" property. The SPA is published to S3 by
// .github/workflows/build-voice.yml, not by terraform, so a build landing
// during hibernation would sync the real index.html straight back over the
// maintenance page with no failure anywhere. build-voice.yml carries a
// `hibernated` check for exactly that reason -- if that check is ever
// removed, this swap silently stops holding.
//
// The real SPA is shadowed, never destroyed: index.html is copied to
// index.spa.html first, and restored from there at wake.
package cmd

import (
	"context"
	"fmt"
	"io"
)

// Object keys and headers for the swap. IndexCacheControl mirrors what
// build-voice.yml uploads index.html with -- the shell is not
// content-hashed, so it must stay uncacheable or a stale shadowed SPA keeps
// being served from the edge.
const (
	IndexKey          = "index.html"
	SPABackupKey      = "index.spa.html"
	IndexCacheControl = "no-cache, no-store, must-revalidate"
	IndexContentType  = "text/html"
)

// PageStoreAPI is the narrow S3 seam the page swap runs through, so the
// orchestration is testable without touching the cloud -- matching how
// every other lifecycle seam in this package is shaped.
type PageStoreAPI interface {
	CopyObject(ctx context.Context, bucket, srcKey, dstKey string) error
	PutObject(ctx context.Context, bucket, key string, body []byte, cacheControl, contentType string) error
	DeleteObject(ctx context.Context, bucket, key string) error
	HeadObject(ctx context.Context, bucket, key string) (exists bool, err error)
}

// SwapToMaintenance saves the live SPA shell aside (unless a save is
// already there from an interrupted run) and puts the maintenance page in
// its place.
func SwapToMaintenance(ctx context.Context, api PageStoreAPI, bucket string, page []byte, w io.Writer) error {
	backupExists, err := api.HeadObject(ctx, bucket, SPABackupKey)
	if err != nil {
		return fmt.Errorf("head %s: %w", SPABackupKey, err)
	}

	if backupExists {
		// An earlier run already saved the real SPA. index.html now holds
		// the maintenance page, so copying it over the backup would destroy
		// the only remaining copy of the shell.
		fmt.Fprintf(w, "  %s already saved from an earlier run -- not re-copying\n", SPABackupKey)
	} else {
		if err := api.CopyObject(ctx, bucket, IndexKey, SPABackupKey); err != nil {
			return fmt.Errorf("save %s to %s: %w", IndexKey, SPABackupKey, err)
		}
		fmt.Fprintf(w, "  saved %s -> %s\n", IndexKey, SPABackupKey)
	}

	if err := api.PutObject(ctx, bucket, IndexKey, page, IndexCacheControl, IndexContentType); err != nil {
		return fmt.Errorf("put maintenance page at %s: %w", IndexKey, err)
	}
	fmt.Fprintf(w, "  put maintenance page at %s\n", IndexKey)
	return nil
}

// RestoreSPA puts the saved shell back and clears the shadow copy. With no
// saved copy present it is a reported no-op -- never a blanking write.
func RestoreSPA(ctx context.Context, api PageStoreAPI, bucket string, w io.Writer) error {
	backupExists, err := api.HeadObject(ctx, bucket, SPABackupKey)
	if err != nil {
		return fmt.Errorf("head %s: %w", SPABackupKey, err)
	}
	if !backupExists {
		fmt.Fprintf(w, "  no %s present -- leaving %s untouched\n", SPABackupKey, IndexKey)
		return nil
	}

	if err := api.CopyObject(ctx, bucket, SPABackupKey, IndexKey); err != nil {
		return fmt.Errorf("restore %s from %s: %w", IndexKey, SPABackupKey, err)
	}
	if err := api.DeleteObject(ctx, bucket, SPABackupKey); err != nil {
		return fmt.Errorf("delete %s: %w", SPABackupKey, err)
	}
	fmt.Fprintf(w, "  restored %s from %s\n", IndexKey, SPABackupKey)
	return nil
}
```

- [ ] **Step 5: Run the tests**

Run: `cd kv && go test ./internal/app/cmd/ -run 'TestSwapToMaintenance|TestRestoreSPA' -v`
Expected: PASS (5 tests).

Note the `RestoreSPA` copy re-puts the object without setting cache-control explicitly; S3's `CopyObject` carries the source object's metadata by default, and the source is the object originally uploaded by `build-voice.yml` with the right `no-cache` header. If the production adapter in Task 8 is found to drop metadata, add `MetadataDirective: COPY` there.

- [ ] **Step 6: Commit**

```bash
git add apps/voice/client/public/maintenance.html \
        kv/internal/app/cmd/lifecycle_page.go \
        kv/internal/app/cmd/lifecycle_page_test.go
git commit -m "feat(kv): maintenance page and its S3 shadow swap

index.html is copied to index.spa.html and shadowed by a committed
maintenance page, then restored at wake. The real SPA is never destroyed,
an interrupted run cannot clobber the saved copy, and a missing backup is a
no-op rather than a blanking write.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 7: Post-apply verification

A hibernate that leaves an orphaned service is the D-03 failure, and it is silent — the bill just keeps arriving. Verification makes it loud.

**Files:**
- Create: `kv/internal/app/cmd/lifecycle_verify.go`
- Test: `kv/internal/app/cmd/lifecycle_verify_test.go`

**Interfaces:**
- Consumes: `ECSAPI` (`lifecycle_ecs.go:24`), `ServicePosture` (`lifecycle_ecs.go`).
- Produces:
  - `type NetworkStateAPI interface { ALBExists(ctx context.Context, albARN string) (bool, error); NATGatewayExists(ctx context.Context, natID string) (bool, error); EIPAllocated(ctx context.Context, publicIP string) (bool, error) }`
  - `type HibernateVerification struct { ALBGone, NATGone, EIPRetained bool; RemainingServices []string; RetainedIP string }`
  - `func VerifyHibernated(ctx context.Context, ecsAPI ECSAPI, net NetworkStateAPI, cluster string, services []string, albARN, natID, eip string) (HibernateVerification, error)`
  - `func (v HibernateVerification) Err() error`

- [ ] **Step 1: Write the failing test**

Create `kv/internal/app/cmd/lifecycle_verify_test.go`:

```go
package cmd

import (
	"context"
	"strings"
	"testing"
)

type fakeNetState struct {
	alb, nat, eip bool
}

func (f *fakeNetState) ALBExists(ctx context.Context, arn string) (bool, error) { return f.alb, nil }
func (f *fakeNetState) NATGatewayExists(ctx context.Context, id string) (bool, error) {
	return f.nat, nil
}
func (f *fakeNetState) EIPAllocated(ctx context.Context, ip string) (bool, error) { return f.eip, nil }

type fakeVerifyECS struct{ postures []ServicePosture }

func (f *fakeVerifyECS) DescribeServices(ctx context.Context, cluster string, services []string) ([]ServicePosture, error) {
	return f.postures, nil
}
func (f *fakeVerifyECS) UpdateDesiredCount(ctx context.Context, cluster, service string, desired int32) error {
	return nil
}
func (f *fakeVerifyECS) ListRunningTasks(ctx context.Context, cluster, service string) ([]string, error) {
	return nil, nil
}
func (f *fakeVerifyECS) GetTaskProtection(ctx context.Context, cluster string, taskARNs []string) (int, error) {
	return 0, nil
}

// The clean hibernated state: ALB and NAT gone, EIP retained, no services.
func TestVerifyHibernated_CleanState(t *testing.T) {
	v, err := VerifyHibernated(context.Background(),
		&fakeVerifyECS{postures: nil},
		&fakeNetState{alb: false, nat: false, eip: true},
		"cluster", []string{"voice", "auth", "telephony-edge"},
		"alb-arn", "nat-id", "1.2.3.4")
	if err != nil {
		t.Fatalf("VerifyHibernated error: %v", err)
	}
	if verr := v.Err(); verr != nil {
		t.Fatalf("Err() = %v, want nil for a clean hibernated state", verr)
	}
}

// The D-03 orphan failure: a service still exists. This must be an error,
// never a warning -- an orphaned service keeps billing silently.
func TestVerifyHibernated_OrphanedServiceIsAnError(t *testing.T) {
	v, err := VerifyHibernated(context.Background(),
		&fakeVerifyECS{postures: []ServicePosture{{Name: "voice", Desired: 1, Running: 1}}},
		&fakeNetState{alb: false, nat: false, eip: true},
		"cluster", []string{"voice"}, "alb-arn", "nat-id", "1.2.3.4")
	if err != nil {
		t.Fatalf("VerifyHibernated error: %v", err)
	}
	verr := v.Err()
	if verr == nil {
		t.Fatal("Err() = nil, want an error naming the orphaned service")
	}
	if !strings.Contains(verr.Error(), "voice") {
		t.Errorf("error %q does not name the orphaned service", verr)
	}
}

// A released EIP is a failure: the VoIP.ms allowlist is now stale, and that
// fails silently later rather than loudly now.
func TestVerifyHibernated_ReleasedEIPIsAnError(t *testing.T) {
	v, err := VerifyHibernated(context.Background(),
		&fakeVerifyECS{postures: nil},
		&fakeNetState{alb: false, nat: false, eip: false},
		"cluster", []string{"voice"}, "alb-arn", "nat-id", "1.2.3.4")
	if err != nil {
		t.Fatalf("VerifyHibernated error: %v", err)
	}
	verr := v.Err()
	if verr == nil {
		t.Fatal("Err() = nil, want an error about the released EIP")
	}
	if !strings.Contains(strings.ToLower(verr.Error()), "eip") {
		t.Errorf("error %q does not mention the EIP", verr)
	}
}

// A surviving ALB means phase 2 did not do its job.
func TestVerifyHibernated_SurvivingALBIsAnError(t *testing.T) {
	v, err := VerifyHibernated(context.Background(),
		&fakeVerifyECS{postures: nil},
		&fakeNetState{alb: true, nat: false, eip: true},
		"cluster", []string{"voice"}, "alb-arn", "nat-id", "1.2.3.4")
	if err != nil {
		t.Fatalf("VerifyHibernated error: %v", err)
	}
	if v.Err() == nil {
		t.Fatal("Err() = nil, want an error about the surviving ALB")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd kv && go test ./internal/app/cmd/ -run 'TestVerifyHibernated' -v`
Expected: FAIL — `undefined: VerifyHibernated`.

- [ ] **Step 3: Write the implementation**

Create `kv/internal/app/cmd/lifecycle_verify.go`:

```go
// Package cmd -- post-apply verification for kv hibernate (2026-09-10 spec
// §8 step 9).
//
// Every check here exists because its failure is SILENT. An orphaned ECS
// service keeps billing with nothing to see; a released Elastic IP does not
// fail at wake, it fails weeks later on the first SMS relay or CTF OTP call
// because the VoIP.ms allowlist went stale. A verify that found an orphan
// and printed a warning would be worse than no verify at all, so every one
// of these is an error.
package cmd

import (
	"context"
	"fmt"
	"strings"
)

// NetworkStateAPI is the narrow seam for asking AWS what actually survived
// the network apply.
type NetworkStateAPI interface {
	ALBExists(ctx context.Context, albARN string) (bool, error)
	NATGatewayExists(ctx context.Context, natID string) (bool, error)
	EIPAllocated(ctx context.Context, publicIP string) (bool, error)
}

// HibernateVerification is what the post-apply checks observed.
type HibernateVerification struct {
	ALBGone           bool
	NATGone           bool
	EIPRetained       bool
	RemainingServices []string
	RetainedIP        string
}

// VerifyHibernated observes the post-apply state. It returns an error only
// for a failure to OBSERVE; a bad observed state is reported through Err()
// so the caller can print the full picture before failing.
func VerifyHibernated(ctx context.Context, ecsAPI ECSAPI, net NetworkStateAPI, cluster string, services []string, albARN, natID, eip string) (HibernateVerification, error) {
	v := HibernateVerification{RetainedIP: eip}

	postures, err := ecsAPI.DescribeServices(ctx, cluster, services)
	if err != nil {
		return v, fmt.Errorf("describe services: %w", err)
	}
	for _, p := range postures {
		v.RemainingServices = append(v.RemainingServices, p.Name)
	}

	albExists, err := net.ALBExists(ctx, albARN)
	if err != nil {
		return v, fmt.Errorf("check alb: %w", err)
	}
	v.ALBGone = !albExists

	natExists, err := net.NATGatewayExists(ctx, natID)
	if err != nil {
		return v, fmt.Errorf("check nat gateway: %w", err)
	}
	v.NATGone = !natExists

	allocated, err := net.EIPAllocated(ctx, eip)
	if err != nil {
		return v, fmt.Errorf("check eip: %w", err)
	}
	v.EIPRetained = allocated

	return v, nil
}

// Err reports the observed state as an error when it is not the clean
// hibernated state. Each problem is named, because each one needs a
// different operator response.
func (v HibernateVerification) Err() error {
	var problems []string

	if len(v.RemainingServices) > 0 {
		problems = append(problems, fmt.Sprintf(
			"ECS services still exist (%s) -- they are ORPHANED: still billing, and still "+
				"holding the listener rules that block the ALB delete. Check that site.hcl has "+
				"ecs_services.enabled = true with an EMPTY services list, not enabled = false "+
				"(which makes terragrunt exclude skip the destroy entirely)",
			strings.Join(v.RemainingServices, ", ")))
	}
	if !v.ALBGone {
		problems = append(problems, "the ALB still exists -- phase 2 did not complete")
	}
	if !v.NATGone {
		problems = append(problems, "the NAT Gateway still exists -- phase 2 did not complete")
	}
	if !v.EIPRetained {
		problems = append(problems, fmt.Sprintf(
			"the NAT EIP (%s) is NOT allocated -- it was released with the gateway. The VoIP.ms "+
				"API allowlist is now stale, which will not fail at wake but WILL fail silently on "+
				"the first SMS relay or CTF OTP call. Check nat_gateway.retain_eip = true",
			v.RetainedIP))
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("hibernate verification failed:\n  - %s", strings.Join(problems, "\n  - "))
}
```

- [ ] **Step 4: Run the tests**

Run: `cd kv && go test ./internal/app/cmd/ -run 'TestVerifyHibernated' -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add kv/internal/app/cmd/lifecycle_verify.go kv/internal/app/cmd/lifecycle_verify_test.go
git commit -m "feat(kv): post-apply verification for hibernate

Asserts the ALB and NAT Gateway are gone, the EIP is still allocated, and
no ECS service survives. Every check is an error rather than a warning
because every one of these failures is silent: an orphaned service just
keeps billing, and a released EIP fails weeks later on the first SMS relay
rather than at wake.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 8: Wire the `kv hibernate` / `kv wake` commands

Assemble the pieces into the spec §8 command flow, reusing the shipped pause skeleton.

**Files:**
- Create: `kv/internal/app/cmd/hibernate.go`
- Test: `kv/internal/app/cmd/hibernate_test.go`
- Modify: `kv/internal/app/cmd/root.go` (register both commands)
- Modify: `kv/internal/app/cmd/pause.go` (`printPauseStatus` grows a hibernated row)

**Interfaces:**
- Consumes: everything from Tasks 1, 5, 6, 7, plus the shipped `LifecycleDeps`, `PreflightLifecycle`, `DrainToZero`, `WaitForServicesRunning`, `WaitForTargetsHealthy`, `VoiceAndAuthTargetGroups`, `confirmAffirmative`, `ErrLifecycleCancelled`, `LifecycleBranch`.
- Produces: `func NewHibernateCmd(cfg *Config) *cobra.Command`, `func NewWakeCmd(cfg *Config) *cobra.Command`, `func RunHibernateFlip(ctx context.Context, deps HibernateDeps, opts HibernateOptions) error`.

- [ ] **Step 1: Write the failing test**

Create `kv/internal/app/cmd/hibernate_test.go`:

```go
package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSiteHCL builds a throwaway repo root holding a site.hcl with both
// flags, so the flip path can be exercised end to end against real files.
func writeSiteHCL(t *testing.T, paused, hibernated bool) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, filepath.Dir(SiteHCLRelPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := "locals {\n  paused = " + boolLit(paused) + "\n  hibernated = " + boolLit(hibernated) + "\n}\n"
	if err := os.WriteFile(filepath.Join(root, SiteHCLRelPath), []byte(src), 0o644); err != nil {
		t.Fatalf("write site.hcl: %v", err)
	}
	return root
}

func boolLit(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Already hibernated is a clean, idempotent no-op that touches nothing.
func TestRunHibernateFlip_AlreadyHibernatedIsNoOp(t *testing.T) {
	root := writeSiteHCL(t, false, true)
	gh := &recordingGH{runIDSeq: []string{"run-1", "run-2"}}
	var out bytes.Buffer

	deps := HibernateDeps{
		RepoRoot: root,
		Git:      &noopGit{clean: true, branch: "main", synced: true},
		GH:       gh,
		Out:      &out,
		Now:      fixedNow(),
	}
	err := RunHibernateFlip(context.Background(), deps, HibernateOptions{Want: true, Yes: true})
	if err != nil {
		t.Fatalf("RunHibernateFlip error: %v", err)
	}
	if len(gh.calls) != 0 {
		t.Errorf("calls = %v, want none for an idempotent no-op", gh.calls)
	}
	if !strings.Contains(out.String(), "no-op") {
		t.Errorf("output %q does not report a no-op", out.String())
	}
}

// D-12: --dry-run issues no dispatch at all.
func TestRunHibernateFlip_DryRunDispatchesNothing(t *testing.T) {
	root := writeSiteHCL(t, false, false)
	gh := &recordingGH{runIDSeq: []string{"run-1", "run-2"}}
	var out bytes.Buffer

	deps := HibernateDeps{
		RepoRoot: root,
		Git:      &noopGit{clean: true, branch: "main", synced: true},
		GH:       gh,
		Out:      &out,
		Now:      fixedNow(),
	}
	err := RunHibernateFlip(context.Background(), deps, HibernateOptions{Want: true, Yes: true, DryRun: true})
	if err != nil {
		t.Fatalf("RunHibernateFlip error: %v", err)
	}
	if len(gh.calls) != 0 {
		t.Errorf("calls = %v, want none under --dry-run", gh.calls)
	}
	// The flag file must be left exactly as found.
	got, err := ReadHibernatedFlagFile(root)
	if err != nil {
		t.Fatalf("ReadHibernatedFlagFile: %v", err)
	}
	if got != false {
		t.Error("--dry-run modified the hibernated flag")
	}
}

// A dirty tree is refused before anything is written.
func TestRunHibernateFlip_RefusesDirtyTree(t *testing.T) {
	root := writeSiteHCL(t, false, false)
	gh := &recordingGH{}
	deps := HibernateDeps{
		RepoRoot: root,
		Git:      &noopGit{clean: false, branch: "main", synced: true},
		GH:       gh,
		Out:      &bytes.Buffer{},
		Now:      fixedNow(),
	}
	err := RunHibernateFlip(context.Background(), deps, HibernateOptions{Want: true, Yes: true})
	if err == nil {
		t.Fatal("error = nil, want a dirty-tree refusal")
	}
	if len(gh.calls) != 0 {
		t.Errorf("calls = %v, want none after a preflight refusal", gh.calls)
	}
}

// The hibernate phase order must be the removal order, not terragrunt's.
func TestHibernateOptions_UsesHibernatePhasesForWantTrue(t *testing.T) {
	if phasesFor(true)[0].Modules != "ecs-service,cloudfront" {
		t.Error("hibernate must remove services and the CloudFront origin first")
	}
	if phasesFor(false)[0].Modules != "network" {
		t.Error("wake must create the network first")
	}
}

// noopGit is a minimal GitAPI whose preflight answers are configurable.
type noopGit struct {
	clean  bool
	branch string
	synced bool
}

func (g *noopGit) CurrentBranch(ctx context.Context) (string, error) { return g.branch, nil }
func (g *noopGit) IsClean(ctx context.Context) (bool, error)         { return g.clean, nil }
func (g *noopGit) SyncedWithOrigin(ctx context.Context, b string) (bool, error) {
	return g.synced, nil
}
func (g *noopGit) Diff(ctx context.Context, paths ...string) (string, error) { return "diff", nil }
func (g *noopGit) CommitPaths(ctx context.Context, m string, p ...string) error {
	return nil
}
func (g *noopGit) Push(ctx context.Context, branch string) error { return nil }
func (g *noopGit) HeadSHA(ctx context.Context) (string, error)   { return "sha", nil }

var _ = errors.New
var _ = time.Now
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd kv && go test ./internal/app/cmd/ -run 'TestRunHibernateFlip|TestHibernateOptions' -v`
Expected: FAIL — `undefined: HibernateDeps`, `undefined: RunHibernateFlip`, `undefined: phasesFor`.

- [ ] **Step 3: Write the command implementation**

Create `kv/internal/app/cmd/hibernate.go`:

```go
// Package cmd -- kv hibernate / kv wake, the third lifecycle tier
// (2026-09-10 spec §8). Sits below kv pause: where pause scales services to
// zero and leaves ~$60/mo of NAT Gateway and ALB running against no
// traffic, hibernate destroys them and lands at ~$14/mo, retaining the NAT
// EIP, DNS, certs and every durable store so wake needs no restore.
//
// Like kv pause, this command must never read, write, or reference the
// kill-switch (killswitch.go) -- they are orthogonal controls at different
// layers, and coupling them would produce a surprise at wake.
//
// Nothing here may automate a DID release: 725-404-8283 and the toll-free
// 855-916-INFO cannot be bought back once released.
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

// HibernateDeps carries everything RunHibernateFlip needs, injected so
// orchestration never constructs an AWS/git/gh client itself -- every
// dependency is a narrow interface a test can fake.
type HibernateDeps struct {
	RepoRoot     string
	Git          GitAPI
	GH           GHAPI
	ECS          ECSAPI
	Health       TargetHealthAPI
	Net          NetworkStateAPI
	Pages        PageStoreAPI
	Cluster      string
	Services     []string
	TargetGroups map[string]string
	AssetBucket  string
	ALBArn       string
	NATID        string
	NATEIP       string
	Now          func() time.Time
	In           io.Reader
	Out          io.Writer
}

// HibernateOptions parameterizes RunHibernateFlip for both hibernate
// (Want=true) and wake (Want=false).
type HibernateOptions struct {
	Want   bool
	Yes    bool
	Reason string
	DryRun bool
	Drain  DrainOptions
}

// Commit messages for the two directions.
const (
	HibernateCommitMessage = "ops(infra): hibernate (destroy NAT, ALB, ECS services)"
	WakeCommitMessage      = "ops(infra): wake (restore NAT, ALB, ECS services)"
)

// phasesFor returns the apply order for a direction: removal order when
// hibernating, terragrunt's natural creation order when waking.
func phasesFor(want bool) []ApplyPhase {
	if want {
		return HibernatePhases
	}
	return WakePhases
}

// RunHibernateFlip implements the spec §8 command flow in order: preflight,
// idempotent read, rewrite-and-confirm, commit and push, drain, the two
// sequential apply phases, the page swap, verify, and report.
func RunHibernateFlip(ctx context.Context, deps HibernateDeps, opts HibernateOptions) error {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	phases := phasesFor(opts.Want)

	// D-08: validate before anything at all, so a bad list cannot reach the
	// account even via an early failure path.
	if err := ValidatePhases(phases); err != nil {
		return err
	}

	if err := PreflightLifecycle(ctx, deps.Git, deps.GH.AuthStatus, LifecycleBranch); err != nil {
		return err
	}

	current, err := ReadHibernatedFlagFile(deps.RepoRoot)
	if err != nil {
		return err
	}
	action := "wake"
	if opts.Want {
		action = "hibernate"
	}
	if current == opts.Want {
		fmt.Fprintf(deps.Out, "kv %s: no-op, already hibernated=%t\n", action, current)
		return nil
	}

	if opts.DryRun {
		return printHibernateDryRun(deps.Out, action, phases, deps, opts)
	}

	path := filepath.Join(deps.RepoRoot, SiteHCLRelPath)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", SiteHCLRelPath, err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", SiteHCLRelPath, err)
	}
	if _, err := SetHibernatedFlagFile(deps.RepoRoot, opts.Want); err != nil {
		return err
	}
	restoreOriginal := func() error { return os.WriteFile(path, original, info.Mode()) }

	if !opts.Yes {
		diff, derr := deps.Git.Diff(ctx, SiteHCLRelPath)
		if derr != nil {
			_ = restoreOriginal()
			return derr
		}
		fmt.Fprintln(deps.Out, diff)
		fmt.Fprint(deps.Out, "Proceed? [y/N] ")
		if !confirmAffirmative(deps.In) {
			if rerr := restoreOriginal(); rerr != nil {
				return fmt.Errorf("restore %s after cancellation: %w", SiteHCLRelPath, rerr)
			}
			return ErrLifecycleCancelled
		}
	}

	message := WakeCommitMessage
	if opts.Want {
		message = HibernateCommitMessage
	}
	if opts.Reason != "" {
		message = message + "\n\n" + opts.Reason
	}
	if err := deps.Git.CommitPaths(ctx, message, SiteHCLRelPath); err != nil {
		return err
	}
	if err := deps.Git.Push(ctx, LifecycleBranch); err != nil {
		return err
	}

	// Phase 1 of a hibernate destroys services outright rather than scaling
	// them, so drain first -- voice tasks hold task-scale-in protection
	// while a session is live.
	if opts.Want && deps.ECS != nil {
		if _, derr := DrainToZero(ctx, deps.ECS, deps.Cluster, deps.Services, deps.Out, opts.Drain); derr != nil {
			return derr
		}
	}

	runIDs, err := RunApplyPhases(ctx, deps.GH, LifecycleBranch, phases, now, deps.Out)
	if err != nil {
		return err
	}

	if deps.Pages != nil && deps.AssetBucket != "" {
		fmt.Fprintln(deps.Out, "\nmaintenance page:")
		if opts.Want {
			page, rerr := os.ReadFile(filepath.Join(deps.RepoRoot, MaintenancePageRelPath))
			if rerr != nil {
				return fmt.Errorf("read maintenance page: %w", rerr)
			}
			if serr := SwapToMaintenance(ctx, deps.Pages, deps.AssetBucket, page, deps.Out); serr != nil {
				return serr
			}
		} else if rerr := RestoreSPA(ctx, deps.Pages, deps.AssetBucket, deps.Out); rerr != nil {
			return rerr
		}
	}

	if opts.Want {
		if deps.Net != nil {
			v, verr := VerifyHibernated(ctx, deps.ECS, deps.Net, deps.Cluster, deps.Services,
				deps.ALBArn, deps.NATID, deps.NATEIP)
			if verr != nil {
				return verr
			}
			if serr := v.Err(); serr != nil {
				return serr
			}
		}
		printHibernateReport(deps.Out, runIDs, deps.NATEIP)
		return nil
	}

	if deps.ECS != nil {
		if _, werr := WaitForServicesRunning(ctx, deps.ECS, deps.Cluster, deps.Services, deps.Out,
			opts.Drain.Timeout, opts.Drain.PollInterval); werr != nil {
			return werr
		}
		targetGroups, terr := VoiceAndAuthTargetGroups(deps.TargetGroups)
		if terr != nil {
			return terr
		}
		if herr := WaitForTargetsHealthy(ctx, deps.Health, targetGroups, deps.Out,
			opts.Drain.Timeout, opts.Drain.PollInterval); herr != nil {
			return herr
		}
	}
	printWakeReport(deps.Out, runIDs)
	return nil
}

// MaintenancePageRelPath is the repo-relative path to the committed page
// that shadows the SPA shell while hibernated.
const MaintenancePageRelPath = "apps/voice/client/public/maintenance.html"

// printHibernateDryRun reports exactly what would happen and mutates
// nothing (D-12).
func printHibernateDryRun(w io.Writer, action string, phases []ApplyPhase, deps HibernateDeps, opts HibernateOptions) error {
	fmt.Fprintf(w, "kv %s --dry-run: no dispatch, no S3 write, no invalidation.\n\n", action)
	fmt.Fprintf(w, "Would flip `hibernated` to %t in %s, commit to %s, then:\n", opts.Want, SiteHCLRelPath, LifecycleBranch)
	for i, p := range phases {
		fmt.Fprintf(w, "  phase %d: dispatch %s with modules=%q (%s)\n", i+1, TerragruntApplyWorkflow, p.Modules, p.Name)
		fmt.Fprintf(w, "           then WATCH it to terminal success before phase %d\n", i+2)
	}
	if opts.Want {
		fmt.Fprintf(w, "  then: copy s3://%s/%s -> %s and put the maintenance page\n", deps.AssetBucket, IndexKey, SPABackupKey)
		fmt.Fprintf(w, "  then: verify the ALB (%s) and NAT (%s) are gone and the EIP (%s) is retained\n",
			deps.ALBArn, deps.NATID, deps.NATEIP)
	} else {
		fmt.Fprintf(w, "  then: restore s3://%s/%s from %s\n", deps.AssetBucket, IndexKey, SPABackupKey)
		fmt.Fprintln(w, "  then: wait for voice and auth target groups to report healthy")
	}
	return nil
}

func printHibernateReport(w io.Writer, runIDs []string, eip string) {
	fmt.Fprintln(w, "\nkv hibernate: complete.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Cost posture: paused ~$60/mo -> hibernated ~$14/mo")
	fmt.Fprintln(w, "  (Fargate $0; NAT Gateway $0; ALB $0; retained EIP ~$3.60; misc floor ~$10.)")
	if eip != "" {
		fmt.Fprintf(w, "\nNAT EIP %s is retained (unattached) -- the VoIP.ms allowlist stays valid.\n", eip)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "While hibernated:")
	fmt.Fprintln(w, "  - voice.klankermaker.ai serves the maintenance page and still unfurls.")
	fmt.Fprintln(w, "  - auth.klankermaker.ai does not resolve to a healthy origin at all.")
	fmt.Fprintln(w, "  - the DIDs stay provisioned and BILLED by VoIP.ms; callers get a fast busy.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Manual follow-ups this command cannot do:")
	fmt.Fprintln(w, "  - The ElevenLabs subscription (Creator, $22/mo as of 2026-09-10) survives")
	fmt.Fprintln(w, "    hibernate, pause and destroy alike -- still ~1.5x the hibernated AWS bill.")
	fmt.Fprintln(w, "    It is kept deliberately, for the cloned voice; this line is a reminder,")
	fmt.Fprintln(w, "    not a recommendation to cancel.")
	fmt.Fprintln(w, "  - Releasing a DID is irreversible and stays outside this tool.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "The kill-switch was not touched -- it is a separate, application-layer")
	fmt.Fprintln(w, "mechanism (kv killswitch) and remains in whatever state it was already in.")
	if len(runIDs) > 0 {
		fmt.Fprintf(w, "\nApply runs: %v\n", runIDs)
	}
}

func printWakeReport(w io.Writer, runIDs []string) {
	fmt.Fprintln(w, "\nkv wake: complete.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Cost posture restored: hibernated ~$14/mo -> running ~$190/mo.")
	fmt.Fprintln(w, "Every ECS service is running and the voice and auth ALB target groups report healthy.")
	fmt.Fprintln(w, "The real SPA shell is back at index.html.")
	if len(runIDs) > 0 {
		fmt.Fprintf(w, "\nApply runs: %v\n", runIDs)
	}
}

// NewHibernateCmd builds "kv hibernate".
func NewHibernateCmd(cfg *Config) *cobra.Command {
	var yes, dryRun bool
	var reason string

	cmd := &cobra.Command{
		Use:   "hibernate",
		Short: "Destroy the ECS services, NAT Gateway and ALB (~$60/mo -> ~$14/mo)",
		Long: "kv hibernate flips the git-tracked `hibernated` boolean in\n" +
			"infra/terraform/live/site/site.hcl, commits and pushes it to main, then\n" +
			"dispatches TWO STRICTLY SEQUENTIAL terragrunt-apply runs: first\n" +
			"ecs-service,cloudfront (removing everything that references the ALB), then\n" +
			"network (deleting the ALB and NAT Gateway). The second is never dispatched\n" +
			"until the first has completed successfully -- they share the workflow's\n" +
			"cancel-in-progress concurrency group, so an eager dispatch would cancel a\n" +
			"half-completed destroy.\n\n" +
			"The NAT EIP, DNS, certs, DynamoDB, the S3 ledger, the cf-assets bucket and\n" +
			"ECR all survive, so `kv wake` needs no data restore. It never touches the\n" +
			"kill-switch and never releases a DID.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			deps, err := buildHibernateDeps(ctx, cfg, c)
			if err != nil {
				return err
			}
			return RunHibernateFlip(ctx, deps, HibernateOptions{
				Want: true, Yes: yes, Reason: reason, DryRun: dryRun,
				Drain: DrainOptions{Correct: true},
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the diff-and-confirm prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report the plan and mutate nothing")
	cmd.Flags().StringVar(&reason, "reason", "", "operator note recorded as a trailing line in the commit body")
	return cmd
}

// NewWakeCmd builds "kv wake".
func NewWakeCmd(cfg *Config) *cobra.Command {
	var yes, dryRun bool

	cmd := &cobra.Command{
		Use:   "wake",
		Short: "Rebuild the NAT Gateway, ALB and ECS services from a hibernated stack",
		Long: "kv wake mirrors kv hibernate, running the same two units in the reverse\n" +
			"order (network first, then ecs-service,cloudfront) -- which is terragrunt's\n" +
			"natural dependency order, so wake carries no ordering hazard. It restores\n" +
			"the real SPA shell over the maintenance page and does not report success\n" +
			"until the voice and auth ALB target groups report healthy.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			deps, err := buildHibernateDeps(ctx, cfg, c)
			if err != nil {
				return err
			}
			return RunHibernateFlip(ctx, deps, HibernateOptions{Want: false, Yes: yes, DryRun: dryRun})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the diff-and-confirm prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report the plan and mutate nothing")
	return cmd
}
```

- [ ] **Step 4: Write `buildHibernateDeps`**

Append to `hibernate.go`. It extends the shipped `buildLifecycleDeps` pattern with the S3, EC2 and ELBv2 lookups hibernate additionally needs. Follow `buildLifecycleDeps` in `pause.go` for the `ResolveECSPosture` call and client construction.

```go
// buildHibernateDeps resolves a repo root, wires the production seams, and
// resolves live resource identifiers via terragrunt outputs -- the same
// approach buildLifecycleDeps uses for kv pause.
func buildHibernateDeps(ctx context.Context, cfg *Config, c *cobra.Command) (HibernateDeps, error) {
	root, err := repoRoot()
	if err != nil {
		return HibernateDeps{}, err
	}
	ecsClient, err := cfg.ECSClient(ctx)
	if err != nil {
		return HibernateDeps{}, err
	}
	elbClient, err := cfg.ELBv2Client(ctx)
	if err != nil {
		return HibernateDeps{}, err
	}
	s3Client, err := cfg.S3Client(ctx)
	if err != nil {
		return HibernateDeps{}, err
	}

	cluster, services, targetGroups, err := ResolveECSPosture(ctx, NewTerragruntOutputReader(root))
	if err != nil {
		return HibernateDeps{}, fmt.Errorf("resolve ecs posture: %w", err)
	}

	return HibernateDeps{
		RepoRoot:     root,
		Git:          NewExecGit(root),
		GH:           NewExecGH(root),
		ECS:          NewECSAPI(ecsClient),
		Health:       NewTargetHealthAPI(elbClient),
		Pages:        NewPageStoreAPI(s3Client),
		Cluster:      cluster,
		Services:     services,
		TargetGroups: targetGroups,
		Now:          time.Now,
		In:           c.InOrStdin(),
		Out:          c.OutOrStdout(),
	}, nil
}
```

Then implement `NewPageStoreAPI(*s3.Client) PageStoreAPI` in `lifecycle_page.go`, adapting `CopyObject` (with `CopySource: bucket + "/" + srcKey` and `MetadataDirective: types.MetadataDirectiveCopy`), `PutObject`, `DeleteObject`, and `HeadObject` (treating a `NotFound`/`NoSuchKey` API error as `false, nil` rather than an error).

`AssetBucket`, `ALBArn`, `NATID` and `NATEIP` are resolved from terragrunt outputs and SSM the same way `ResolveECSPosture` resolves the cluster. Resolve `AssetBucket` from the SSM parameter `build-voice.yml` already reads: `/${SITE_LABEL}/cloudfront-assets/use1/voice/bucket_name`. Leave any that cannot be resolved empty — `RunHibernateFlip` already treats an empty `AssetBucket` and a nil `Net` as "skip that step", so a partial resolution degrades rather than crashes.

- [ ] **Step 5: Register the commands**

In `root.go`, beside the existing `NewPauseCmd` / `NewResumeCmd` registrations:

```go
	rootCmd.AddCommand(NewHibernateCmd(cfg))
	rootCmd.AddCommand(NewWakeCmd(cfg))
```

- [ ] **Step 6: Add the hibernated row to `kv pause status`**

In `pause.go`, change `printPauseStatus`'s signature and body, and its call site in the `status` subcommand:

```go
// printPauseStatus prints both lifecycle flags and each service's live
// desired/running counts side by side, so a flag-versus-reality divergence
// (a half-applied pause, or an orphaned service after a hibernate) is
// directly observable.
func printPauseStatus(w io.Writer, paused, hibernated bool, postures []ServicePosture) error {
	fmt.Fprintf(w, "paused flag     (site.hcl): %t\n", paused)
	fmt.Fprintf(w, "hibernated flag (site.hcl): %t\n", hibernated)
	if hibernated {
		fmt.Fprintln(w, "  (hibernated implies paused: the service list is empty, and the NAT Gateway and ALB are destroyed)")
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tDESIRED\tRUNNING")
	for _, p := range postures {
		fmt.Fprintf(tw, "%s\t%d\t%d\n", p.Name, p.Desired, p.Running)
	}
	return tw.Flush()
}
```

In the `status` subcommand's `RunE`, read the second flag and pass it through:

```go
			hibernated, err := ReadHibernatedFlagFile(deps.RepoRoot)
			if err != nil {
				return err
			}
			return printPauseStatus(c.OutOrStdout(), paused, hibernated, postures)
```

- [ ] **Step 7: Run the tests**

Run: `cd kv && go test ./internal/app/cmd/ -run 'TestRunHibernateFlip|TestHibernateOptions' -v`
Expected: PASS (4 tests).

- [ ] **Step 8: Run the whole package and build the binary**

```bash
cd kv && go build ./... && go test ./internal/app/cmd/
```
Expected: builds clean, all tests pass including every shipped pause test.

- [ ] **Step 9: Exercise the commands offline**

```bash
cd kv && go run ./cmd/kv hibernate --help
cd kv && go run ./cmd/kv wake --help
```
Expected: both render with `--yes`, `--dry-run`, and (hibernate only) `--reason`. No AWS call is made by `--help`.

- [ ] **Step 10: Commit**

```bash
git add kv/internal/app/cmd/hibernate.go kv/internal/app/cmd/hibernate_test.go \
        kv/internal/app/cmd/lifecycle_page.go kv/internal/app/cmd/root.go kv/internal/app/cmd/pause.go
git commit -m "feat(kv): add kv hibernate and kv wake

Assembles the flag engine, the two-phase sequential dispatch, the S3 page
swap and the post-apply verification into the spec §8 flow, reusing the
shipped kv pause preflight, drain and health-gate seams. kv pause status
grows a hibernated row.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 9: Guard the build against un-hibernating the page

The one place "config is the guard" does not hold. Without this, a `build-voice` run during hibernation silently restores a mic button that cannot work.

**Files:**
- Modify: `.github/workflows/build-voice.yml` (the "Publish client assets to S3 + invalidate CloudFront" step, lines 84-140)

**Interfaces:**
- Consumes: the `hibernated` flag in `site.hcl` (Task 4).
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Add the flag read and the guard**

In `build-voice.yml`, inside the publish step's `run:` block, immediately after `set -euo pipefail` and before the `BUCKET=` lookup:

```bash
          # The SPA is published here, not by terraform, so the "config is
          # the guard" property that protects the site.hcl lifecycle flags
          # does NOT extend to S3 object content. While hibernated,
          # index.html holds a maintenance page put there by `kv hibernate`
          # -- syncing the real shell back over it would silently restore a
          # mic button that cannot work, with no failure anywhere.
          # Hashed assets are harmless to sync either way.
          HIBERNATED=$(grep -E '^\s*hibernated\s*=\s*(true|false)\s*$' \
            infra/terraform/live/site/site.hcl | head -1 | grep -oE '(true|false)' || echo "false")
          echo "hibernated=$HIBERNATED"
```

- [ ] **Step 2: Gate the shell upload**

Change the `index.html` upload (currently `aws s3 cp "$TMP/dist/index.html" ...`) to:

```bash
          # The SPA shell is not hashed — upload no-cache so a deploy is picked
          # up immediately. Skipped while hibernated (see the guard above).
          if [ "$HIBERNATED" = "true" ]; then
            echo "## Client shell: skipped (hibernated)" >> "$GITHUB_STEP_SUMMARY"
            echo "site.hcl has hibernated = true, so index.html was NOT overwritten —" \
                 "the maintenance page stays in place. Run \`kv wake\` to restore the SPA." \
                 >> "$GITHUB_STEP_SUMMARY"
          else
            aws s3 cp "$TMP/dist/index.html" "s3://$BUCKET/index.html" \
              --cache-control 'no-cache, no-store, must-revalidate'
          fi
```

- [ ] **Step 3: Gate the invalidation**

Wrap the existing `create-invalidation` call so it only runs when not hibernated — an invalidation of `/` and `/index.html` while hibernated would flush the maintenance page from the edge for no reason:

```bash
          if [ -n "$DIST_ID" ] && [ "$DIST_ID" != "None" ] && [ "$HIBERNATED" != "true" ]; then
```

- [ ] **Step 4: Verify the guard expression against both states**

```bash
grep -E '^\s*hibernated\s*=\s*(true|false)\s*$' infra/terraform/live/site/site.hcl | head -1 | grep -oE '(true|false)'
```
Expected: `false` (the committed state).

Then prove it reads `true` too, without committing:
```bash
sed -i '' 's/^  hibernated = false$/  hibernated = true/' infra/terraform/live/site/site.hcl
grep -E '^\s*hibernated\s*=\s*(true|false)\s*$' infra/terraform/live/site/site.hcl | head -1 | grep -oE '(true|false)'
sed -i '' 's/^  hibernated = true$/  hibernated = false/' infra/terraform/live/site/site.hcl
git diff --exit-code infra/terraform/live/site/site.hcl
```
Expected: `true`, then a clean `git diff` proving the file was restored exactly.

- [ ] **Step 5: Validate the workflow YAML parses**

```bash
python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/build-voice.yml')); print('ok')"
```
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/build-voice.yml
git commit -m "fix(ci): do not overwrite the maintenance page while hibernated

The SPA is published to S3 by CI, not terraform, so the config-is-the-guard
property that protects the site.hcl lifecycle flags does not cover S3 object
content: a build landing during hibernation would sync the real index.html
back over the maintenance page with no failure anywhere. Reads the
hibernated flag and skips the shell upload and the invalidation.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 10: Operator runbook

`kv pause` has `docs/ops/pause-resume.md` and an entry in the operator manual. Hibernate needs the same, or it is findable only by reading the spec.

**Files:**
- Create: `docs/ops/hibernate-wake.md`
- Modify: `docs/operators/kv-cli-reference.md` (new command group + Command map rows)
- Modify: `docs/operators/README.md` (Start-here entry, pages-table row, "Turning it off" step)
- Modify: `scripts/verify-operator-docs.sh` (extend the `DOCS` array to link-check the new runbook)

**Interfaces:**
- Consumes: the shipped commands from Task 8.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write the runbook**

Create `docs/ops/hibernate-wake.md`, modelled on `docs/ops/pause-resume.md`. It must cover, each as its own section:

- The three tiers side by side (`kv pause` ~$60, `kv hibernate` ~$14, `kv destroy` ~$1) and when each is right.
- The `infra/.envrc` prerequisite: `set -a && . infra/.envrc && set +a`, with the same warning `pause-resume.md` carries — without it `terragrunt output` fails with an opaque backend error.
- The preflight refusal table (clean tree, on `main`, synced with origin, `gh` authed) and how to clear each.
- **The two-phase apply**, stating plainly that it means **two separate required-reviewer approvals** in the GitHub Actions UI, and that the command sits visibly waiting between them. This is the single most surprising operational fact about the command.
- What breaks while hibernated: `auth.klankermaker.ai` does not resolve to a healthy origin at all; `voice.klankermaker.ai` serves the maintenance page; DIDs stay provisioned, billed, and give callers a fast busy.
- That the ElevenLabs subscription (Creator, $22/mo since 2026-09-10, kept deliberately for
  the cloned voice) survives hibernation and is still ~1.5× the hibernated AWS bill.
- Recovery from a failed phase 1: nothing later was dispatched, `kv pause status` shows what actually applied, and re-running `kv hibernate` is safe because the flag is already committed and the command is idempotent.
- That `kv wake` needs no backup or restore — the distinguishing property versus `kv destroy`.

- [ ] **Step 2: Wire it into the operator manual**

Add to `docs/operators/kv-cli-reference.md` a `kv hibernate` / `kv wake` group directly after the `kv pause` group, with every flag verified against the real binary rather than from memory:

```bash
cd kv && go run ./cmd/kv hibernate --help
cd kv && go run ./cmd/kv wake --help
```

**Trap:** a `kv` on `PATH` may be stale and will report the wrong flags. Use `kv/bin/kv` or `go run ./cmd/kv`, as the 260826-ojd and 260826-i2c tasks both had to.

Add to `docs/operators/README.md`: a Start-here entry ("how do I take it down to almost nothing for months"), a pages-table row, and a `kv hibernate` step in "Turning it off" positioned after `kv pause`.

- [ ] **Step 3: Extend the doc verifier**

In `scripts/verify-operator-docs.sh`, add `docs/ops/hibernate-wake.md` to the `DOCS` array and bump the expected page count by one.

**Do not** add the new flags to the verifier's flag/default lists — those are sensitive to the stale-PATH-binary problem and would produce spurious failures.

- [ ] **Step 4: Run the verifier**

```bash
KV=kv/bin/kv bash scripts/verify-operator-docs.sh
```
Expected: clean run, zero `FAIL:` lines, page count one higher than before.

If `kv/bin/kv` does not exist, build it first: `cd kv && go build -o bin/kv ./cmd/kv`.

- [ ] **Step 5: Negative-test the link check**

Plant a broken relative link in the new runbook, confirm the verifier fails, then revert:

```bash
echo '[broken](./does-not-exist.md)' >> docs/ops/hibernate-wake.md
KV=kv/bin/kv bash scripts/verify-operator-docs.sh; echo "exit=$?"
git checkout docs/ops/hibernate-wake.md 2>/dev/null || sed -i '' '$d' docs/ops/hibernate-wake.md
```
Expected: a non-zero exit with a `FAIL:` line naming the broken link, then a clean tree. A verifier that passes here is not actually checking the new page.

- [ ] **Step 6: Commit**

```bash
git add docs/ops/hibernate-wake.md docs/operators/kv-cli-reference.md \
        docs/operators/README.md scripts/verify-operator-docs.sh
git commit -m "docs(ops): kv hibernate / kv wake runbook

Covers the three lifecycle tiers side by side, the two-phase apply and its
TWO required-reviewer approvals, what breaks while hibernated, recovery from
a failed phase, and that wake needs no restore. Wired into the operator
manual and the doc verifier.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Operator gate (not a task — a deliberate human decision)

`kv hibernate` is the first lifecycle command that destroys rather than scales, so like `kv destroy`'s 16-11 checkpoint it gets a dry run before it is ever used in earnest.

Before running the real command:

1. `kv hibernate --dry-run` and read the report line by line against what you believe exists.
2. Confirm the report names two phases in the removal order, with `ecs-service,cloudfront` first.
3. Confirm `email` appears nowhere in it.
4. Take a `kv backup` anyway. Hibernate does not require one, but it costs one command and the first run of a new destructive path is the wrong moment to discover an assumption was wrong.
5. Be ready to approve **two** separate `terraform-apply` runs in the GitHub Actions UI.

After the first real hibernation, measure the actual monthly cost in Cost Explorer and correct §9 of the spec and §12's first open item — the ~$10/mo misc floor is currently inferred from resource inventory, not measured.

---

## Self-Review

**Spec coverage:**

| Spec section | Task |
|---|---|
| §3 mechanism (`hibernated` flag, additive to `paused`) | 1, 4 |
| §3.1 second boolean not an enum | 1 (wrappers keep shipped paths intact) |
| §4 two-phase ordering | 5 |
| §4.1 concurrency trap | 5 (`RunApplyPhases` sequential gate + tests) |
| §4.2 drain before phase 1 | 8 (`RunHibernateFlip` drain step) |
| §5.1 conditional ALB origin | 2 |
| §5.2 retain the EIP | 3 |
| §5.3 flag plumbing | 4 |
| §6 the NAT EIP decision | 3, 7 (verification), 8 (report) |
| §7 maintenance page | 6 |
| §7.1 build guard | 9 |
| §8 command flow | 8 |
| §8.1 explicit module lists | 5 (`ValidatePhases`) |
| §8.2 `--dry-run` | 8 |
| §8.3 manual follow-ups in the report | 8 (`printHibernateReport`) |
| §10 testing | 1, 5, 6, 7, 8 |
| §11 rejected partial wake | not built, by design |
| §12 open items | operator gate section |

**Gaps deliberately left:** §12's CloudFront propagation measurement and the Cost Explorer confirmation are post-first-run operator observations, not code — they are in the operator gate section rather than a task. `auth.klankermaker.ai` having no maintenance page is an explicit spec non-goal.

**Type consistency checked:** `PageStoreAPI` methods match between the interface (Task 6), the fake (Task 6 test) and the adapter (Task 8 Step 4). `ApplyPhase.Modules` is the field name used in the phase lists, `ValidatePhases`, `RunApplyPhases`, and the dry-run printer. `ReadHibernatedFlagFile` / `SetHibernatedFlagFile` are the names used in Tasks 1, 4, 8 and 9. `HibernateVerification.Err()` is the method name in both Task 7's implementation and Task 8's call site. `ECSAPI` fakes in Tasks 7 and 8 implement all four shipped methods.

**One known rough edge:** Task 8's `buildHibernateDeps` leaves `ALBArn`, `NATID`, `NATEIP` and `AssetBucket` resolution described rather than coded, because the exact terragrunt output names must be read off the live units at implementation time. `RunHibernateFlip` degrades safely when any is empty (nil `Net` skips verification, empty `AssetBucket` skips the page swap), so a partial resolution reports less rather than crashing — but the implementer should resolve all four and confirm verification actually runs, since skipping it silently is precisely the failure mode Task 7 exists to prevent.
