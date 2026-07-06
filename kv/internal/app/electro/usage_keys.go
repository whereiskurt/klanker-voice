// Usage entity key reproduction — mirrors
// apps/auth/webapp/src/entities/usage.ts and
// apps/voice/src/klanker_voice/quota.py's key templates byte-for-byte
// (04-04's "kv <-> webapp/voice compat" discipline, same as
// AccessCode/Tier above).
//
// FINAL key templates (source of truth: usage.ts, verified against
// quota.py's own `_heartbeat_pk`/`_daily_pk`/`ROLLUP_PK`/`CONTROL_PK`
// constants):
//
//	UsageHeartbeat  primary pk: "session#${userId}"   sk: "heartbeat#${sessionId}"
//	UsageDaily      primary pk: "user#${userId}"      sk: "day#${day}"        (day = yyyy-mm-dd, UTC)
//	UsageRollup     primary pk: "rollup#"              sk: "day#${day}"        (day = yyyy-mm-dd, UTC)
//	UsageControl    primary pk: "control#"             sk: "killswitch#"
//
// None of these four entities declare a GSI (unlike AccessCode/Tier) — every
// kv usage/killswitch access pattern is a direct GetItem/Query on the
// primary index, no table scan (KV-03, D-10).
package electro

import "time"

const (
	UsageHeartbeatEntityName = "UsageHeartbeat"
	UsageDailyEntityName     = "UsageDaily"
	UsageRollupEntityName    = "UsageRollup"
	UsageControlEntityName   = "UsageControl"

	// UsageDayFormat is the yyyy-mm-dd (UTC) day-key format shared by
	// UsageDaily and UsageRollup sort keys.
	UsageDayFormat = "2006-01-02"
)

// UsageDayString formats t as the yyyy-mm-dd (UTC) day key used by
// UsageDaily/UsageRollup sort keys — callers should pass time.Now().UTC()
// for "today".
func UsageDayString(t time.Time) string {
	return t.UTC().Format(UsageDayFormat)
}

// --- UsageHeartbeat key templates ---

// UsageHeartbeatPK builds the UsageHeartbeat primary partition key:
// "session#${userId}".
func UsageHeartbeatPK(userID string) string {
	return "session#" + userID
}

// UsageHeartbeatSK builds the UsageHeartbeat primary sort key:
// "heartbeat#${sessionId}".
func UsageHeartbeatSK(sessionID string) string {
	return "heartbeat#" + sessionID
}

// --- UsageDaily key templates ---

// UsageDailyPK builds the UsageDaily primary partition key: "user#${userId}".
func UsageDailyPK(userID string) string {
	return "user#" + userID
}

// UsageDailySK builds the UsageDaily primary sort key: "day#${day}"
// (day = yyyy-mm-dd, UTC).
func UsageDailySK(day string) string {
	return "day#" + day
}

// --- UsageRollup key templates ---

// UsageRollupPK is the UsageRollup primary partition key: the constant
// "rollup#" (every day's rollup lives in the same partition, one item per
// day — an O(1) GetItem for "today").
func UsageRollupPK() string {
	return "rollup#"
}

// UsageRollupSK builds the UsageRollup primary sort key: "day#${day}"
// (day = yyyy-mm-dd, UTC).
func UsageRollupSK(day string) string {
	return "day#" + day
}

// --- UsageControl key templates ---

// UsageControlPK is the UsageControl primary partition key: the constant
// "control#".
func UsageControlPK() string {
	return "control#"
}

// UsageControlSK is the UsageControl primary sort key: the constant
// "killswitch#" — a single item, read on every /api/offer start gate (D-08).
func UsageControlSK() string {
	return "killswitch#"
}
