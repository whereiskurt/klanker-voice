# Phase 12: VoIP.ms Telephony — Inbound DID - Pattern Map

**Mapped:** 2026-07-12
**Files analyzed:** 16 new/modified files across auth app, voice service, kv CLI, infrastructure, and documentation
**Analogs found:** 14/16 (strong analogs for bypass /join machinery, PJSIP config, kv command structure, terraform service stubs)

---

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `apps/auth/webapp/src/entities/access-code.ts` | model | CRUD | same file (byBypassToken GSI) | exact |
| `apps/auth/webapp/src/lib/phone-normalization.ts` | utility | transform | `normalizeCode` in access-code.ts | exact |
| `apps/auth/webapp/src/app/tel/[e164]/route.ts` | route | request-response | `apps/auth/webapp/src/app/join/[token]/route.ts` | exact |
| `apps/auth/webapp/src/lib/bypass-token.ts` (modify for `/tel`) | utility | request-response | same file `mintAnonToken` | reuse |
| `apps/voice/asterisk/pjsip.conf` | config | request-response | same file (dev-softphone sections) | exact |
| `apps/voice/asterisk/render_configs.py` | utility | file-I/O | phase-11 render pattern | exact |
| `apps/voice/src/klanker_voice/telephony/controller.py` | controller | request-response | same file `on_stasis_start` | role-match |
| `apps/voice/src/klanker_voice/telephony/config.py` | config | CRUD | Phase-11 config pattern | role-match |
| `apps/voice/src/klanker_voice/config.py` | config | CRUD | same file (credential-field-name rejection) | reuse |
| `kv/internal/app/cmd/code.go` | CLI command | CRUD | same file (bypass subcommand) | exact |
| `kv/internal/app/cmd/voipms.go` | CLI command | request-response | `code.go` command structure | role-match |
| `kv/internal/app/electro/keys.go` | utility | CRUD | same file (AccessCodeGSI2PK template) | exact |
| `kv/internal/app/cmd/code_test.go` | test | CRUD | `bypass_test.go` pattern | exact |
| `kv/internal/app/cmd/voipms_test.go` | test | request-response | existing test patterns | role-match |
| `.planning/infra/terraform/live/site/services/telephony-edge/service.hcl` | config | CRUD | `services/voice/service.hcl` | exact |
| `docs/operators/voipms-provisioning-runbook.md` | documentation | batch | phase-11 runbook references | role-match |

---

## Pattern Assignments

### `apps/auth/webapp/src/entities/access-code.ts` (model, CRUD)

**Analog:** Same file, existing `byBypassToken` sparse GSI (lines 101–115)

**Sparse GSI pattern** (lines 101–115):
```typescript
// New byPhone GSI mirrors the byBypassToken pattern exactly
byPhone: {
  index: "gsi3pk-gsi3sk-index",  // Verify table has gsi3 defined
  pk: {
    field: "gsi3pk",
    casing: "none",  // Preserve E.164 digits (no transformation needed)
    composite: ["phone"],
    template: "phone#${phone}",  // e.g., "phone#+14165551234"
  },
  sk: {
    field: "gsi3sk",
    composite: [],
    template: "phone#",
    casing: "none",
  },
}
```

**Phone attribute** (new, non-required like bypassToken):
```typescript
// In attributes section
phone: {
  type: "string",
  // NOT required — bypass-less codes have no phone
},
phoneEnabled: {
  type: "boolean",
  default: false,
},
```

**Helper function** (mirrors `resolveBypassToken` at lines 188–211):
```typescript
export async function resolvePhoneToCode(
  normalizedPhone: string
): Promise<ResolvedAccessCode> {
  if (!normalizedPhone) {
    return { tierId: NO_ACCESS_TIER_ID, group: null };
  }
  try {
    const result = await AccessCode.query
      .byPhone({ phone: normalizedPhone })
      .go();
    if (result.data.length === 0) {
      return { tierId: NO_ACCESS_TIER_ID, group: null };
    }
    const code = result.data[0].code;
    return resolveAccessCode(code);  // Reuse existing resolver
  } catch (err) {
    console.error(`resolvePhoneToCode error: ${err}`);
    return { tierId: NO_ACCESS_TIER_ID, group: null };
  }
}
```

---

### `apps/auth/webapp/src/lib/phone-normalization.ts` (utility, transform)

**Analog:** `normalizeCode` in `apps/auth/webapp/src/entities/access-code.ts` (lines 128–132)

**Phone normalization helper** (new file):
```typescript
/**
 * Normalize a phone number to canonical E.164 format for database storage.
 * Strips all non-digit characters, prepends country code if missing.
 * Used in both write paths (kv code phone --add) and lookup paths (controller → /tel).
 *
 * @param phone Raw phone input (may have spaces, dashes, parentheses, +, etc.)
 * @returns Canonical E.164 form: "+<country-code><number>" (digits only after +)
 */
export function normalizeE164(phone: string | null | undefined): string {
  const raw = String(phone ?? "").trim();
  if (!raw) {
    return "";
  }

  // Keep only digits and the leading +
  let cleaned = raw.replace(/[^\d+]/g, "");

  // Remove the leading + if present (we'll re-add it)
  if (cleaned.startsWith("+")) {
    cleaned = cleaned.substring(1);
  }

  // Remove leading zeros (trunk prefix)
  cleaned = cleaned.replace(/^0+/, "");

  // If the number doesn't start with a country code, assume +1 (North America)
  if (cleaned.length === 10 || (cleaned.length === 11 && cleaned.startsWith("1"))) {
    if (!cleaned.startsWith("1")) {
      cleaned = "1" + cleaned;
    }
  }

  return "+" + cleaned;
}

// Test cases (verify all work)
console.assert(normalizeE164("+1 (416) 555-1234") === "+14165551234");
console.assert(normalizeE164("416-555-1234") === "+14165551234");
console.assert(normalizeE164("+14165551234") === "+14165551234");
```

---

### `apps/auth/webapp/src/app/tel/[e164]/route.ts` (route, request-response)

**Analog:** `apps/auth/webapp/src/app/join/[token]/route.ts` (lines 1–77)

**Private `/tel` endpoint** (new file, mirrors /join):
```typescript
import { NextRequest, NextResponse } from "next/server";
import { normalizeE164 } from "@/lib/phone-normalization";
import { resolvePhoneToCode } from "@/entities/access-code";
import { mintAnonToken } from "@/lib/bypass-token";
import { config } from "@/config";

/**
 * Private endpoint for voice service to mint tokens from caller ID.
 * Only accessible from the telephony-edge (private network or shared bearer token).
 *
 * GET /use1/tel/+14165551234
 * Authorization: Bearer <TELEPHONY_ENDPOINT_AUTH_TOKEN> (if required)
 *
 * Response: { token: "eyJ0...", expiresIn: 3600 }
 * Error (all cases): { error: "not_found" } / 404 (no oracle)
 */

export async function GET(
  req: NextRequest,
  { params }: { params: { e164: string } }
) {
  const notFound = () =>
    new NextResponse("Not found", {
      status: 404,
      headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
    });

  try {
    // Verify private network access (optional; network ACL may suffice)
    const authHeader = req.headers.get("authorization");
    const expectedToken = process.env.TELEPHONY_ENDPOINT_AUTH_TOKEN;
    if (expectedToken && authHeader !== `Bearer ${expectedToken}`) {
      return NextResponse.json({ error: "unauthorized" }, { status: 401 });
    }

    // Normalize the phone from the URL path
    const normalized = normalizeE164(decodeURIComponent(params.e164));
    if (!normalized) {
      return notFound();
    }

    // Resolve phone → code → tier (returns no-access tier if not found)
    const resolved = await resolvePhoneToCode(normalized);

    // Mint token (same shape as bypass /join tokens)
    try {
      const minted = await mintAnonToken({
        code: resolved.code || "tel:" + normalized,
        tierId: resolved.tierId,
        group: resolved.group,
      });

      // Log: tel_phone_resolved tier=..., but never log phone/code directly
      console.info(`tel_phone_resolved call_id=<unknown> tier=${resolved.tierId}`);

      return NextResponse.json(minted, { status: 200 });
    } catch (err) {
      // Mint failure → same error as not found (no oracle)
      console.error(`tel_phone_mint_error: ${err}`);
      return notFound();
    }
  } catch {
    // Uniform failure — never leak whether the phone existed
    return notFound();
  }
}
```

---

### `apps/voice/asterisk/pjsip.conf` (config, request-response)

**Analog:** Same file, existing `dev-softphone` section (lines 37–73)

**VoIP.ms PJSIP trunk sections** (add to pjsip.conf):
```ini
; VoIP.ms SIP trunk (registration-based, Phase 12 D-01)
; All passwords sourced from SSM at container start via render_configs.py

; Transport (existing, may need external_media_address update for Fargate public IP)
; See Phase-11 comment: external_media_address must be the Fargate task's public IP

; Auth for VoIP.ms registration (password from VOIPMS_SIP_PASSWORD env)
[voipms-auth](!)
type=auth
auth_type=userpass
username=${VOIPMS_SIP_USERNAME}
password=${VOIPMS_SIP_PASSWORD}

; Outbound registration to VoIP.ms Toronto POP
[voipms-registration]
type=registration
server_uri=sip:toronto.voip.ms
client_uri=sip:${VOIPMS_SIP_USERNAME}@voipms.net
outbound_auth=voipms-auth
retry_interval=300
expiration=3600
contact_user=klanker-pbx

; AOR (address-of-record) for VoIP.ms inbound calls
[voipms-aor](!)
type=aor
max_contacts=1

; VoIP.ms endpoint (context locked to from-klanker-inbound, D-01/§25.A)
[voipms-endpoint](softphone)
context=from-klanker-inbound
aors=voipms-aor
auth=voipms-auth
disallow=all
allow=ulaw
direct_media=no
force_rport=yes
rewrite_contact=yes
rtp_symmetric=yes

; Identify inbound SIP from VoIP.ms by IP (Toronto 1 example)
[voipms-identify]
type=identify
endpoint=voipms-endpoint
match=158.85.70.148

; (Add identify sections for other Toronto POPs: .149, .150, .151, .215.106, .215.114, .215.146, .213.210)
```

---

### `apps/voice/asterisk/render_configs.py` (utility, file-I/O)

**Analog:** Phase-11 render pattern for secrets (existing file)

**Extension pattern** (render VOIPMS_SIP_PASSWORD at container start):
```python
# In the render loop, add VOIPMS_SIP_PASSWORD to the substitution dict
import os

def render_asterisk_configs():
    """Render Asterisk config templates, substituting secrets from environment."""
    
    # Existing environment variables
    telephony_media_address = os.environ.get("TELEPHONY_MEDIA_ADDRESS", "127.0.0.1")
    softphone_password = os.environ.get("SOFTPHONE_SIP_PASSWORD", "")
    
    # Phase 12: new VoIP.ms secrets (from SSM via ECS valueFrom)
    voipms_sip_username = os.environ.get("VOIPMS_SIP_USERNAME", "")
    voipms_sip_password = os.environ.get("VOIPMS_SIP_PASSWORD", "")
    
    # Build substitution dict
    subs = {
        "TELEPHONY_MEDIA_ADDRESS": telephony_media_address,
        "SOFTPHONE_SIP_PASSWORD": softphone_password,
        "VOIPMS_SIP_USERNAME": voipms_sip_username,
        "VOIPMS_SIP_PASSWORD": voipms_sip_password,
    }
    
    # Render templates (pseudocode)
    for template_file in ["pjsip.conf.tmpl", "extensions.conf.tmpl", "ari.conf.tmpl"]:
        with open(f"/etc/asterisk/{template_file}", "r") as f:
            content = f.read()
        for key, value in subs.items():
            content = content.replace(f"${{{key}}}", value)
        with open(f"/etc/asterisk/.rendered/{template_file.replace('.tmpl', '')}", "w") as f:
            f.write(content)
```

---

### `apps/voice/src/klanker_voice/telephony/controller.py` (controller, request-response)

**Analog:** Same file, `on_stasis_start` method (lines 1–250)

**Caller-ID normalization + `/tel` mint call** (in `on_stasis_start`, after line ~250):
```python
# In on_stasis_start, after extracting raw caller ID from ARI event:

def _normalize_e164(self, raw: Any) -> str:
    """Best-effort E.164 normalization for caller ID (matches auth-app helper)."""
    if raw is None:
        return ""
    phone_str = str(raw).strip()
    # Remove all non-digit characters except leading +
    cleaned = "".join(c for c in phone_str if c.isdigit() or c == "+")
    if not cleaned:
        return ""
    if cleaned.startswith("+"):
        cleaned = cleaned[1:]
    cleaned = cleaned.lstrip("0")
    if len(cleaned) == 10 or (len(cleaned) == 11 and cleaned[0] == "1"):
        if not cleaned.startswith("1"):
            cleaned = "1" + cleaned
    return "+" + cleaned

async def _mint_token_from_phone(self, normalized_caller_id: str) -> str | None:
    """Call the private /tel endpoint to mint an OIDC token from caller ID."""
    if not normalized_caller_id:
        return None
    
    auth_token = os.getenv("TELEPHONY_ENDPOINT_AUTH_TOKEN", "")
    headers = {"Authorization": f"Bearer {auth_token}"} if auth_token else {}
    
    try:
        # Assumes /tel endpoint is at auth.klankermaker.ai/use1/tel/<e164>
        response = await asyncio.to_thread(
            requests.get,
            f"https://auth.klankermaker.ai/use1/tel/{urllib.parse.quote(normalized_caller_id)}",
            headers=headers,
            timeout=5,
        )
        if response.status_code == 200:
            result = response.json()
            return result.get("token")
        else:
            logger.warning(f"tel_endpoint_failed status={response.status_code}")
            return None
    except Exception as err:
        logger.error(f"tel_endpoint_error: {err}")
        return None

# In on_stasis_start, construct CallIdentity from minted token:
raw_caller_id = event.channel.caller.number or "unknown"
normalized_caller_id = self._normalize_e164(raw_caller_id)
logger.info(f"StasisStart call_id={call_id} raw={raw_caller_id} normalized={normalized_caller_id}")

# Mint token from caller ID
token = await self._mint_token_from_phone(normalized_caller_id)

# Build identity from token (or minimal if no token)
if token:
    identity = CallIdentity(
        subject=f"tel:{normalized_caller_id}",  # Extract from token's sub claim
        authenticated=True,
        auth_method="tel",
        # tier_id comes from the minted token's tier_id claim
    )
else:
    identity = CallIdentity(
        subject=f"tel:{normalized_caller_id}",
        authenticated=False,
        auth_method="tel",
        # Minimal/no-access tier
    )
```

---

### `apps/voice/src/klanker_voice/telephony/config.py` (config, CRUD)

**Analog:** Same file type; Phase-11 credential-name rejection pattern (from `config.py`)

**Telephony config** (ensure no VOIPMS/credential fields leak into TOML):
```python
# Extend credential-name rejection in TelephonyConfig if needed
# (Most secrets are rendered into Asterisk config at container start, not loaded by Python)
```

---

### `apps/voice/src/klanker_voice/config.py` (config, CRUD)

**Analog:** Same file, `_CREDENTIAL_FIELD_RE` pattern (lines 49–52)

**Extend credential-field rejection** (lines 49–52):
```python
# Existing regex
_CREDENTIAL_FIELD_RE = re.compile(
    r"(?:^|_)(?:api_?key|key|keys|secret|secrets|token|tokens|password|passwd|"
    r"credential|credentials|bearer|auth|pin|passphrase|pass_?word)(?:_|$)|apikey|^words$",
    re.IGNORECASE,
)

# Phase 12 extension (same regex pattern — no changes needed if voipms_sip_password 
# matches the "password" stem already):
# The regex already catches: voipms_sip_password (contains "password" stem)
# So the existing validation is sufficient.

def load_config(...) -> PipelineConfig:
  # Existing validation (lines 54+ in config.py):
  # Verify no credential-looking fields in the TOML
  # Phase 12 adds no new fields that bypass this check
```

---

### `kv/internal/app/cmd/code.go` (CLI command, CRUD)

**Analog:** Same file, `bypass` subcommand (lines 327–377)

**`kv code phone` subcommand** (add to `NewCodeCmd`, mirrors `bypass` structure):
```go
var (
    phoneAdd    string
    phoneRemove bool
)
phone := &cobra.Command{
    Use:   "phone <code>",
    Short: "Manage phone number mapping for a code (caller-ID access)",
    Long: "Manage the per-code phone number mapping for caller-ID-based access.\n\n" +
        "  kv code phone <code> --add <e164>       add a phone number mapping (enables caller-ID mint)\n" +
        "  kv code phone <code> --remove           remove the phone mapping (disables caller-ID mint)\n\n" +
        "The phone number is normalized to E.164 (+<country-code><number>).",
    Args: cobra.ExactArgs(1),
    RunE: func(c *cobra.Command, args []string) error {
        if phoneAdd != "" && phoneRemove {
            return fmt.Errorf("--add and --remove are mutually exclusive")
        }
        if phoneAdd == "" && !phoneRemove {
            return fmt.Errorf("--add <e164> or --remove is required")
        }
        
        client, err := cfg.DynamoClient(c.Context())
        if err != nil {
            return err
        }
        code := args[0]
        
        if phoneRemove {
            if err := RemovePhoneMapping(c.Context(), client, cfg.Table, code); err != nil {
                return err
            }
            fmt.Fprintf(c.OutOrStdout(), "removed phone mapping for code %q\n", electro.NormalizeCode(code))
            return nil
        }
        
        // Add (or update) phone mapping
        normalized, err := normalizeE164(phoneAdd)
        if err != nil {
            return fmt.Errorf("invalid phone number: %w", err)
        }
        if err := AddPhoneMapping(c.Context(), client, cfg.Table, code, normalized); err != nil {
            return err
        }
        fmt.Fprintf(c.OutOrStdout(), "added phone mapping for code %q: %s\n", electro.NormalizeCode(code), normalized)
        return nil
    },
}
phone.Flags().StringVar(&phoneAdd, "add", "", "E.164 phone number to map (e.g., +14165551234)")
phone.Flags().BoolVar(&phoneRemove, "remove", false, "remove the phone mapping")
codeCmd.AddCommand(phone)

// Helper functions (similar to EnableBypass / DisableBypass)
func AddPhoneMapping(ctx context.Context, client *dynamodb.Client, table, code, normalizedPhone string) error {
    if err := validateCodeCharset(code); err != nil {
        return err
    }
    _, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
        TableName: aws.String(table),
        Key: map[string]types.AttributeValue{
            "pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
            "sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
        },
        UpdateExpression: aws.String(
            "SET phone = :phone, phoneEnabled = :t, gsi3pk = :g3pk, gsi3sk = :g3sk",
        ),
        ExpressionAttributeValues: map[string]types.AttributeValue{
            ":phone": &types.AttributeValueMemberS{Value: normalizedPhone},
            ":t":     &types.AttributeValueMemberBOOL{Value: true},
            ":g3pk":  &types.AttributeValueMemberS{Value: electro.AccessCodeGSI3PK(normalizedPhone)},
            ":g3sk":  &types.AttributeValueMemberS{Value: electro.AccessCodeGSI3SK()},
        },
        ConditionExpression: aws.String("attribute_exists(pk)"),
    })
    if err != nil {
        return fmt.Errorf("add phone mapping for code %q: %w", code, err)
    }
    return nil
}

func RemovePhoneMapping(ctx context.Context, client *dynamodb.Client, table, code string) error {
    if err := validateCodeCharset(code); err != nil {
        return err
    }
    _, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
        TableName: aws.String(table),
        Key: map[string]types.AttributeValue{
            "pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
            "sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
        },
        UpdateExpression:    aws.String("REMOVE phone, phoneEnabled, gsi3pk, gsi3sk"),
        ConditionExpression: aws.String("attribute_exists(pk)"),
    })
    if err != nil {
        return fmt.Errorf("remove phone mapping for code %q: %w", code, err)
    }
    return nil
}
```

---

### `kv/internal/app/cmd/voipms.go` (CLI command, request-response)

**Analog:** `code.go` command structure (lines 237–377)

**`kv voipms` command family** (new file):
```go
package cmd

import (
    "context"
    "fmt"
    
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/ssm"
    "github.com/spf13/cobra"
)

// NewVoipmsCmd builds the "kv voipms" parent command with sub-commands
// for provisioning and managing the VoIP.ms account.
func NewVoipmsCmd(cfg *Config) *cobra.Command {
    voipmsCmd := &cobra.Command{
        Use:   "voipms",
        Short: "Manage VoIP.ms account and DID provisioning",
        Long: "VoIP.ms account management: read balance, set caps, route DIDs.\n" +
            "Portal-only steps (2FA, restrictions) are documented separately.",
    }

    // kv voipms balance
    balance := &cobra.Command{
        Use:   "balance",
        Short: "Read current VoIP.ms account balance",
        Args:  cobra.NoArgs,
        RunE: func(c *cobra.Command, args []string) error {
            // Call VoIP.ms REST API: general.getBalance
            // Requires VOIPMS_API_USERNAME + VOIPMS_API_PASSWORD from SSM
            bal, err := getVoipmsBalance(c.Context())
            if err != nil {
                return err
            }
            fmt.Fprintf(c.OutOrStdout(), "VoIP.ms balance: $%.2f\n", bal)
            return nil
        },
    }
    voipmsCmd.AddCommand(balance)

    // kv voipms route-did <did> <route-to>
    var routeDid string
    routeCmd := &cobra.Command{
        Use:   "route-did <did>",
        Short: "Route a DID to the klanker-pbx subaccount",
        Args:  cobra.ExactArgs(1),
        RunE: func(c *cobra.Command, args []string) error {
            did := args[0]
            // Call VoIP.ms REST API: dids.setDIDRouting
            if err := routeVoipmsDidToPbx(c.Context(), did); err != nil {
                return err
            }
            fmt.Fprintf(c.OutOrStdout(), "routed DID %s to klanker-pbx\n", did)
            return nil
        },
    }
    voipmsCmd.AddCommand(routeCmd)

    // kv voipms set-caps
    var maxCallDuration int
    capsCmd := &cobra.Command{
        Use:   "set-caps",
        Short: "Set per-call max duration and other caps",
        Args:  cobra.NoArgs,
        RunE: func(c *cobra.Command, args []string) error {
            // Call VoIP.ms REST API: general.setMaxCallDuration (or similar)
            if err := setVoipmsCallDuration(c.Context(), maxCallDuration); err != nil {
                return err
            }
            fmt.Fprintf(c.OutOrStdout(), "set max call duration to %d seconds\n", maxCallDuration)
            return nil
        },
    }
    capsCmd.Flags().IntVar(&maxCallDuration, "max-duration", 600, "max seconds per call (default 10 min)")
    voipmsCmd.AddCommand(capsCmd)

    return voipmsCmd
}

// getVoipmsBalance reads the VoIP.ms account balance via the REST API.
// Credentials come from SSM (VOIPMS_API_USERNAME, VOIPMS_API_PASSWORD).
func getVoipmsBalance(ctx context.Context) (float64, error) {
    // Placeholder: the actual implementation calls VoIP.ms REST API via net/http
    // Base URL: https://voip.ms/api/v1/rest.php
    // Method: general.getBalance
    // Auth: api_username + api_password query params
    return 0.0, nil
}

// routeVoipmsDidToPbx routes a DID to the klanker-pbx subaccount.
func routeVoipmsDidToPbx(ctx context.Context, did string) error {
    // Placeholder: VoIP.ms REST API dids.setDIDRouting call
    return nil
}

// setVoipmsCallDuration sets the per-call max duration.
func setVoipmsCallDuration(ctx context.Context, durationSeconds int) error {
    // Placeholder: VoIP.ms REST API call
    return nil
}
```

---

### `kv/internal/app/electro/keys.go` (utility, CRUD)

**Analog:** Same file, `AccessCodeGSI2PK` pattern (lines 86–99)

**Phone GSI key templates** (add to keys.go, mirrors GSI2 pattern):
```go
// --- AccessCode phone mapping (byPhone sparse GSI, Phase 12) ---

// AccessCodeGSI3PK builds the AccessCode gsi3 (byPhone) partition key:
// "phone#${phone}". The phone number is already normalized to E.164 (digits only),
// so no case transform is needed.
func AccessCodeGSI3PK(phone string) string {
    return "phone#" + phone
}

// AccessCodeGSI3SK is the AccessCode gsi3 sort key: the constant "phone#"
// (empty composite in the ElectroDB template).
func AccessCodeGSI3SK() string {
    return "phone#"
}
```

---

### `kv/internal/app/cmd/code_test.go` (test, CRUD)

**Analog:** `bypass_test.go` pattern (Phase-11 test structure)

**Phone mapping tests** (mirror the bypass test structure):
```go
package cmd

import (
    "context"
    "testing"
)

func TestAddPhoneMapping(t *testing.T) {
    // Test setup: create a code, add a phone mapping, verify gsi3pk/gsi3sk
    // Mirror TestEnableBypass logic
}

func TestRemovePhoneMapping(t *testing.T) {
    // Test that phone/phoneEnabled/gsi3pk/gsi3sk are REMOVED
}

func TestNormalizeE164(t *testing.T) {
    // Test E.164 normalization: various input formats → canonical form
    tests := []struct {
        input    string
        expected string
    }{
        {"+1 (416) 555-1234", "+14165551234"},
        {"1-416-555-1234", "+14165551234"},
        {"416-555-1234", "+14165551234"},
        {"+14165551234", "+14165551234"},
        {"", ""},
    }
    for _, tt := range tests {
        got, err := normalizeE164(tt.input)
        if got != tt.expected {
            t.Errorf("normalizeE164(%q) = %q, want %q", tt.input, got, tt.expected)
        }
    }
}
```

---

### `.planning/infra/terraform/live/site/services/telephony-edge/service.hcl` (config, CRUD)

**Analog:** `services/voice/service.hcl` (lines 1–150)

**Telephony-edge service stub** (new file, mirrors voice service structure):
```hcl
# Data-only service stub for the telephony-edge (Asterisk PSTN gateway).
# site.hcl reads this at parse time for every unit.

locals {
  # ECR repository for the Asterisk edge
  ecr_repositories = [
    {
      name                 = "asterisk-edge"
      regions              = ["us-east-1"]
      image_tag_mutability = "IMMUTABLE"
      lifecycle_policy = {
        max_image_count = 5
        expire_days     = 30
      }
    }
  ]

  # Minimal task role IAM: SSM GetParameters (for secrets), CloudWatch (metrics, Phase 14)
  task_role_iam_statements = [
    {
      sid     = "SecretRetrieval"
      actions = ["ssm:GetParameters", "ssm:GetParameter"]
      resources = [
        "arn:aws:ssm:*:*:parameter/kmv/secrets/use1/voipms/*",
        "arn:aws:ssm:*:*:parameter/kmv/secrets/use1/asterisk/*",
        "arn:aws:ssm:*:*:parameter/kmv/secrets/use1/telephony/*",
      ]
    },
    {
      sid     = "KmsDecrypt"
      actions = ["kms:Decrypt"]
      resources = ["*"]
      condition = {
        test     = "StringEquals"
        variable = "kms:ViaService"
        values   = ["ssm.us-east-1.amazonaws.com"]
      }
    },
  ]

  # Asterisk edge task definition
  task = {
    name         = "asterisk-edge"
    regions      = ["us-east-1"]
    cluster_name = "app"
    task_cpu     = 512
    task_memory  = 1024

    task_role_policy_statements = local.task_role_iam_statements

    containers = [
      {
        name      = "asterisk-edge"
        image     = "asterisk-edge:${get_env("TF_VAR_ASTERISK_IMAGE_TAG", "latest")}"
        cpu       = 512
        memory    = 1024
        essential = true

        # Asterisk binds on UDP 5060 (SIP) + 20000-20100 (RTP)
        portMappings = [
          {
            containerPort = 5060
            protocol      = "udp"
          },
          {
            containerPort = 20000
            hostPort      = 20000
            protocol      = "udp"
          }
          # (add 20001–20100 if needed; one-per-call for the RTP port range)
        ]

        # Phase 12: inject VoIP.ms + Asterisk secrets from SSM
        secrets = [
          {
            name      = "VOIPMS_SIP_USERNAME"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/voipms/sip_username"
          },
          {
            name      = "VOIPMS_SIP_PASSWORD"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/voipms/sip_password"
          },
          {
            name      = "ASTERISK_ARI_USERNAME"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/asterisk/ari_username"
          },
          {
            name      = "ASTERISK_ARI_PASSWORD"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/asterisk/ari_password"
          },
          {
            name      = "TELEPHONY_ENDPOINT_AUTH_TOKEN"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/telephony/endpoint_auth_token"
          },
          {
            name      = "TELEPHONY_ACCESS_PIN"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/telephony/access_pin"
          },
          {
            name      = "TELEPHONY_PASSPHRASE_WORDS"
            valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/telephony/passphrase_words"
          }
        ]

        # Asterisk ARI is private-network-only (no internet exposure, D-01)
        environment = [
          {
            name  = "ASTERISK_ARI_URL"
            value = "http://localhost:8088"  # ARI server inside container
          },
        ]

        logConfiguration = {
          logDriver = "awslogs"
          options = {
            "awslogs-group"         = "asterisk-edge"
            "awslogs-region"        = "us-east-1"
            "awslogs-stream-prefix" = "ecs"
          }
        }
      }
    ]
  }

  # Service definition: public IP (for registration trunk), isolated SG
  service = {
    name                   = "asterisk-edge"
    cluster_name           = "app"
    task_definition_family = "asterisk-edge"
    desired_count          = 1
    deployment_maximum_percent = 100
    deployment_minimum_healthy_percent = 0  # Allow rolling restart

    # Fargate launch type
    launch_type = "FARGATE"

    # Public subnet + public IP assignment (registration trunk needs public IP)
    network_configuration = {
      assign_public_ip = true
      subnets          = ["subnet-public-1"]  # Reference to public subnet
      security_groups  = ["sg-asterisk-edge"]  # See network module
    }

    # No load balancer; ARI is private-only
  }

  # Security group for the edge: inbound from VoIP.ms POPs only
  # (defined in the network module, not here — this is data-only)
}
```

---

### Network Security Group (terraform region module)

**Analog:** Existing voice service security group

**VoIP.ms POP allow-list** (in network module, new SG for telephony-edge):
```hcl
# infra/terraform/live/site/region/us-east-1/network/telephony-edge.hcl

resource "aws_security_group" "asterisk_edge" {
  name        = "asterisk-edge-sg"
  description = "Asterisk edge: inbound SIP/RTP from VoIP.ms POPs only (D-01)"
  vpc_id      = var.vpc_id

  # Inbound SIP from Toronto POPs (8 IPs)
  dynamic "ingress" {
    for_each = local.voipms_toronto_pop_cidrs
    content {
      from_port   = 5060
      to_port     = 5060
      protocol    = "udp"
      cidr_blocks = [ingress.value]
    }
  }

  # Inbound RTP from Toronto POPs (RTP range)
  dynamic "ingress" {
    for_each = local.voipms_toronto_pop_cidrs
    content {
      from_port   = 20000
      to_port     = 20100
      protocol    = "udp"
      cidr_blocks = [ingress.value]
    }
  }

  # Outbound: allow all (registration, RTP, etc.)
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "asterisk-edge-sg" }
}

locals {
  # Toronto POP IP addresses (verified 2026-07-12)
  voipms_toronto_pop_cidrs = [
    "158.85.70.148/32",   # Toronto 1
    "158.85.70.149/32",   # Toronto 2
    "158.85.70.150/32",   # Toronto 3
    "158.85.70.151/32",   # Toronto 4
    "184.75.215.106/32",  # Toronto 5
    "184.75.215.114/32",  # Toronto 6
    "184.75.215.146/32",  # Toronto 7
    "184.75.213.210/32",  # Toronto 8
  ]
}
```

---

## Shared Patterns

### Authentication & Token Minting (Auth app & Voice service)

**Source:** `apps/auth/webapp/src/lib/bypass-token.ts` (lines 74–97)

**Reused `mintAnonToken` signature** — identical for both `/join` and `/tel`:
```typescript
interface MintAnonTokenInput {
  code: string;
  tierId: string;
  group: string | null;
}

// Both endpoints call:
const minted = await mintAnonToken({
  code: resolved.code,
  tierId: resolved.tierId,
  group: resolved.group,
});

// Returns { token: "<jwt>", expiresIn: 3600 }
// Token is valid in voice service without any changes (same issuer/aud/jwks/kid)
```

**Apply to:** `/join` route (reused) + new `/tel` route (Phase 12)

---

### No-Oracle Failure Contract

**Source:** `apps/auth/webapp/src/app/join/[token]/route.ts` (lines 37–41, 73–76)

**Uniform failure response pattern** — never leak detailed error info:
```typescript
// All failure modes → same response
const notFound = () =>
  new NextResponse("Not found", {
    status: 404,
    headers: { "cache-control": "no-store" },
  });

// Unknown token, bypass-disabled code, expired code, mint error → all return notFound()
// This pattern is mirrored in the new `/tel` endpoint
```

**Apply to:** Both `/join` (existing) and `/tel` (Phase 12)

---

### Sparse GSI Pattern

**Source:** `apps/auth/webapp/src/entities/access-code.ts` (lines 101–115)

**Sparse indexing** — optional composite attributes are omitted from the GSI entirely:
```typescript
byBypassToken: {
  index: "gsi2pk-gsi2sk-index",
  pk: {
    field: "gsi2pk",
    casing: "none",  // Opaque secrets preserve casing
    composite: ["bypassToken"],
    template: "bypass#${bypassToken}",
  },
  sk: { field: "gsi2sk", composite: [], template: "bypass#", casing: "none" },
}

// New byPhone GSI follows the same pattern exactly
// Only codes with phone != null are indexed on gsi3pk/gsi3sk
```

**Apply to:** `byPhone` GSI (Phase 12) — mirrors `byBypassToken` (Phase 3/5)

---

### Secrets in SSM (ECS `valueFrom`)

**Source:** `infra/terraform/live/site/services/voice/service.hcl` (lines 132–149)

**Pattern for injecting secrets at container runtime:**
```hcl
secrets = [
  {
    name      = "ENVIRONMENT_VARIABLE_NAME"
    valueFrom = "arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/<service>/<secret_name>"
  },
  # ...
]
```

**Apply to:** All SSM-backed secrets (Phase 12: VOIPMS_SIP_*, ASTERISK_ARI_*, TELEPHONY_* — move from local env to SSM)

---

### Secret Rendering into Asterisk Config

**Source:** Phase-11 `render_configs.py` pattern (mentioned in controller.py lines 58–65)

**At-container-start substitution** (never commit secrets to git):
```python
# Read ${PLACEHOLDER} from environment (injected by ECS from SSM)
# Render into config file
# Write to gitignored directory
```

**Apply to:** `pjsip.conf` password rendering (Phase 12: VOIPMS_SIP_PASSWORD)

---

### kv CLI Command Structure

**Source:** `kv/internal/app/cmd/code.go` (lines 237–377)

**Cobra command shape** (reusable for all `kv` subcommands):
```go
func NewSomeCmd(cfg *Config) *cobra.Command {
  cmd := &cobra.Command{
    Use:   "subcommand",
    Short: "...",
    Args:  cobra.ExactArgs(n),
    RunE: func(c *cobra.Command, args []string) error {
      // Get DynamoDB client
      client, err := cfg.DynamoClient(c.Context())
      // Do work
      // Print output
      return nil
    },
  }
  cmd.Flags().StringVar(&var, "flag", "default", "help")
  return cmd
}
```

**Apply to:** `kv code phone` + `kv voipms` (Phase 12)

---

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `docs/operators/voipms-provisioning-runbook.md` | documentation | batch | Operator runbook is new; no codebase precedent (mirrors Phase-11 runbook structure but is operator-specific) |

---

## Metadata

**Analog search scope:** 
- `apps/auth/webapp/src/` (entities, lib, app routes)
- `apps/voice/src/klanker_voice/` (config, controller, services)
- `apps/voice/asterisk/` (pjsip.conf, config rendering)
- `kv/internal/app/cmd/` (CLI command structure)
- `kv/internal/app/electro/` (key templates)
- `infra/terraform/live/site/services/` (service.hcl stubs)

**Files scanned:** 15+ source files across 4 top-level dirs

**Pattern extraction date:** 2026-07-12

**Key finding:** The bypass `/join` machinery (Phase 3/5) is the **single source of truth** for Phase 12's caller-ID mint path. Every pattern (sparse GSI, no-oracle contract, token minting, kv command shape) has a direct analog in the codebase, ensuring consistency and minimal new code surface.

