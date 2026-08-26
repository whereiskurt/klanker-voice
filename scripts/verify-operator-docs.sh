#!/usr/bin/env bash
# Verify the operator manual against reality.
#
# A manual full of commands is only useful while its commands are still true.
# This checks the mechanical claims — links, images, flags, defaults, resource
# names, and that no secret leaked into a page that syncs to a public wiki —
# so drift shows up as a failing check rather than as a confused operator.
#
#   scripts/verify-operator-docs.sh            # uses `kv` from PATH
#   KV=kv/bin/kv scripts/verify-operator-docs.sh
#
# Not checked here (needs live AWS): that the live account still matches the
# inventory in infrastructure.md. Re-capture with the commands in
# docs/operators/README.md#conventions when it drifts.

set -uo pipefail
cd "$(git rev-parse --show-toplevel)" || exit 1

KV="${KV:-kv}"
DOCS=(
  docs/operators/README.md
  docs/operators/kv-cli-reference.md
  docs/operators/phone-number-inventory.md
  docs/operators/access-codes-and-tiers.md
  docs/operators/phone-games-runbook.md
  docs/operators/pbx-lifecycle.md
  docs/operators/infrastructure.md
  docs/operators/incident-runbook.md
  docs/ops/pause-resume.md
  docs/ops/backup-restore.md
)
TOML=apps/voice/configs/telephony.toml
fails=0

section() { printf '\n\033[1m%s\033[0m\n' "$1"; }
bad() { printf '  FAIL: %s\n' "$1"; fails=$((fails + 1)); }
ok() { printf '  ok: %s\n' "$1"; }

# --------------------------------------------------------------------------
section "Links and images resolve"

for f in "${DOCS[@]}"; do
  d=$(dirname "$f")
  # Every relative markdown target (link or image) must exist on disk.
  grep -o '](\([^)]*\))' "$f" | sed 's/^](//; s/)$//; s/#.*//' | sort -u |
    while read -r t; do
      [ -z "$t" ] && continue
      case "$t" in http*|mailto*|\<*) continue ;; esac
      [ -e "$d/$t" ] || printf '  FAIL: %s -> %s\n' "$f" "$t"
    done
done
ok "checked $((${#DOCS[@]})) pages"

# --------------------------------------------------------------------------
section "Captures are rendered and paired"

for s in docs/assets/terminal/*.session; do
  [ -e "${s%.session}.svg" ] || bad "unrendered: $s (run scripts/render-terminal-svg.py)"
done
for v in docs/assets/terminal/*.svg; do
  [ -e "${v%.svg}.session" ] || bad "orphan svg with no transcript: $v"
done
ok "every transcript has an SVG and vice versa"

# --------------------------------------------------------------------------
section "kv flags and defaults still exist"

if ! command -v "$KV" >/dev/null 2>&1 && [ ! -x "$KV" ]; then
  printf '  SKIP: kv not found (set KV=path/to/kv)\n'
else
  # Flags the reference names, as "<subcommand>:<flag>".
  for pair in \
    "code list:--json" "code create:--tier" "code create:--group" \
    "code create:--expires" "code create:--max" \
    "code bypass:--rotate" "code bypass:--disable" \
    "code phone:--add" "code phone:--remove" \
    "tier define:--session-max" "tier define:--period-max" \
    "tier define:--max-concurrent" \
    "usage today:--user-id" "usage history:--days" "killswitch on:--reason" \
    "telephony list:--show-secrets" "telephony list:--config" \
    "telephony stats:--since" "telephony stats:--did" "telephony stats:--log-group" \
    "telephony calls:--view" "telephony calls:--new-within" "telephony calls:--caller" \
    "voipms search-dids:--state" "voipms search-dids:--ratecenter" \
    "voipms search-tollfree:--query" "voipms search-tollfree:--type" \
    "voipms search-tollfree:--usa-only" \
    "voipms order-did:--routing" "voipms order-did:--pop" \
    "voipms order-did:--dialtime" "voipms order-did:--cnam" \
    "voipms order-did:--billing-type" \
    "voipms route-did:--subaccount" "voipms cancel-did:--yes" \
    "voipms create-subaccount:--username" "voipms create-subaccount:--password" \
    "voipms create-subaccount:--allowed-ip" \
    "knowledge refresh:--dry-run" "knowledge refresh:--skip-distill" \
    "knowledge refresh:--force" \
    "smoke:--endpoint" "studio:--port" "studio:--no-open"; do
    cmd="${pair%%:*}"; flag="${pair##*:}"
    # shellcheck disable=SC2086
    $KV $cmd --help 2>&1 | grep -q -- "      $flag" || bad "kv $cmd has no $flag"
  done
  ok "every documented flag exists"

  # Defaults quoted in the reference. Anchored on the flag's own help ROW
  # (leading whitespace + flag) so prose mentioning another flag can't match.
  check_default() {
    # shellcheck disable=SC2086
    row=$($KV $1 --help 2>&1 | grep -E "^ +$2 ")
    echo "$row" | grep -q "default $3" || bad "kv $1 $2: expected default $3, got: ${row:-<no row>}"
  }
  check_default "telephony stats" "--since" "24h0m0s"
  check_default "telephony calls" "--view" '"callers"'
  check_default "telephony calls" "--new-within" "1h0m0s"
  check_default "usage history" "--days" "7"
  check_default "tier define" "--max-concurrent" "1"
  check_default "voipms order-did" "--pop" '"45"'
  check_default "voipms order-did" "--cnam" '"0"'
  check_default "voipms order-did" "--dialtime" '"60"'
  check_default "voipms search-tollfree" "--type" '"contains"'
  check_default "studio" "--port" "7420"
  check_default "smoke" "--endpoint" '"https://voice.klankermaker.ai"'
  ok "every quoted default matches --help"
fi

# --------------------------------------------------------------------------
section "Config values quoted in the manual match telephony.toml"

check_toml() { grep -q "$1" "$TOML" || bad "$TOML no longer has: $1"; }
check_toml 'gate_window_seconds = 12'
check_toml 'gate_cue_lead_max_seconds = 8.0'
check_toml 'unlock_tier_id = "pstn-public-tier"'
check_toml 'max_concurrent_calls = 4'
check_toml 'auto_trip_ceiling_seconds = 7200'
check_toml 'auto_trip_ceiling_dollars = 40'
check_toml 'est_cost_per_second = 0.005'
for tag in KVD3234 KVD3283 KVD8283 KVD1800; do check_toml "\"$tag\""; done
ok "gate, ceiling and DID-tag claims match config"

# --------------------------------------------------------------------------
section "Referenced repo paths exist"

for p in \
  "$TOML" \
  infra/terraform/live/site/services/telephony-edge/service.hcl \
  infra/terraform/live/site/services/auth/service.hcl \
  infra/terraform/live/site/region/us-east-1/network/telephony-sg.hcl \
  apps/voice/configs/studio/dids.yaml \
  apps/voice/scripts/refresh_knowledge.py \
  apps/voice/asterisk/Dockerfile \
  scripts/render-terminal-svg.py scripts/sync-wiki.py infra/.envrc; do
  [ -e "$p" ] || bad "referenced path is gone: $p"
done
ok "every referenced path exists"

# --------------------------------------------------------------------------
section "No secret or PII leaked into a page that syncs public"

# This script is itself public, so it must not carry a literal deny-list of
# the values it is looking for. Instead: (a) a structural rule that needs no
# list, and (b) a live deny-list pulled from the account when credentials are
# available.

# (a) Structural. Any phone-like number in the manual must be either a
# publicly-known DID (the ones already in the committed telephony config), a
# 555 documentation number, or an obvious placeholder. Anything else is a
# caller number that escaped redaction.
mapfile -t PUBLIC_DIDS < <(
  { grep -oE '"[0-9]{10}"' "$TOML" | tr -d '"'
    sed 's/#.*//' docs/operators/.public-numbers 2>/dev/null | grep -oE '[0-9]{10}'
  } | sort -u
)
allowed_number() {
  case "$1" in *555????) return 0 ;; esac          # 555 documentation range
  for d in "${PUBLIC_DIDS[@]}"; do [ "$1" = "$d" ] && return 0; done
  return 1
}
leaked_numbers=0
while read -r n; do
  allowed_number "$n" || { bad "unredacted phone number in the manual: $n"; leaked_numbers=1; }
done < <(grep -rhoE '\b[0-9]{10}\b' "${DOCS[@]}" docs/assets/terminal/*.session 2>/dev/null |
         grep -vE '^(1000000|052251888500)$' | sort -u)
[ "$leaked_numbers" -eq 0 ] && ok "every phone number in the manual is a public DID or a placeholder"

# (b) Live. Access-code values are the credential a stranger would type, so
# assert that no current code string appears anywhere in docs/.
if command -v "$KV" >/dev/null 2>&1 || [ -x "$KV" ]; then
  codes=$("$KV" code list --json 2>/dev/null |
          sed -n 's/.*"code": *"\([^"]*\)".*/\1/p' | sort -u)
  if [ -z "$codes" ]; then
    printf '  SKIP: could not read live access codes (no AWS session?)\n'
  else
    leaked_codes=0
    while read -r c; do
      [ -z "$c" ] && continue
      if grep -rqiw -- "$c" "${DOCS[@]}" docs/assets/terminal/*.session 2>/dev/null; then
        bad "a live access-code value appears in the manual (redact it)"
        leaked_codes=1
      fi
    done <<< "$codes"
    [ "$leaked_codes" -eq 0 ] && ok "no live access-code value appears in the manual"
  fi
fi

# The gate/game secrets must only ever be referenced by SSM path, never value.
if grep -rEq '(access_pin|passphrase_words|announcement_code_[a-z0-9]+) *[:=] *[^ <"`]' \
     "${DOCS[@]}" 2>/dev/null; then
  bad "a gate or game secret looks like it has a value attached"
else
  ok "gate and game secrets referenced by path only"
fi

# --------------------------------------------------------------------------
if [ "$fails" -eq 0 ]; then
  printf '\n\033[32mAll operator-manual checks passed.\033[0m\n'
else
  printf '\n\033[31m%d check(s) failed.\033[0m\n' "$fails"
fi
exit "$fails"
