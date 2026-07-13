# Data-only stub: the eight Toronto VoIP.ms POP IPs the telephony-edge
# security group (network/v1.0.0's dedicated `aws_security_group.telephony_edge`
# resource) locks SIP/RTP ingress to. Read by network.hcl (this directory)
# and merged into the network module's `telephony_edge_pop_cidrs` input —
# NEVER 0.0.0.0/0 (D-01, T-12-07-01).
#
# Source: 12-RESEARCH.md "Security Group for VoIP.ms Trunk" (verified
# 2026-07-12 against wiki.voip.ms/article/Servers, the Toronto POP cluster).
# 12-04's Asterisk `[voipms-identify]` pjsip.conf section matches all eight
# of the SAME IPs (VoIP.ms may deliver inbound traffic from any POP in the
# Toronto cluster even when registration targets one specific host) — the
# two lists MUST stay in sync.
#
# OPERATOR RUNBOOK — 6-month POP re-verification procedure:
#   1. Re-check the current Toronto POP list at
#      https://wiki.voip.ms/article/Servers (or VoIP.ms support if the page
#      moves) every ~6 months, or immediately if inbound calls start
#      failing with no application-layer error (a POP IP change is the
#      first thing to suspect).
#   2. If the list changed: update BOTH this file's `voipms_toronto_pop_cidrs`
#      AND apps/voice/asterisk/pjsip.conf's `[voipms-identify]` `match=`
#      lines together, in the same commit — the SG and the Asterisk-level
#      identify list are a single security boundary split across two
#      files, and drifting them apart re-opens (SG too narrow, calls drop)
#      or under-protects (SG too wide, an old POP IP now reassigned to a
#      different tenant) the edge.
#   3. `terragrunt apply` the network unit (SG change only, no service
#      restart required) after updating this file.
locals {
  # Live values from getServersInfo, 2026-07-12 (the research-era
  # 158.85.70.x/184.75.21x.x list was stale — those IPs no longer appear in
  # the API's server registry). toronto.voip.ms resolves to toronto1
  # (208.100.60.50, POP 45) — the registration target and the DID's POP.
  voipms_toronto_pop_cidrs = [
    "208.100.60.50/32", # toronto1 (POP 45 — registration target)
    "208.100.60.51/32", # toronto2 (POP 99)
    "208.100.60.52/32", # toronto3 (POP 98)
    "208.100.60.53/32", # toronto4 (POP 92)
    "208.100.60.54/32", # toronto5 (POP 12)
    "208.100.60.55/32", # toronto6 (POP 38)
    "208.100.60.56/32", # toronto7 (POP 61)
    "208.100.60.57/32", # toronto8 (POP 62)
    "208.100.60.58/32", # toronto9 (POP 63)
    "208.100.60.59/32", # toronto10 (POP 6)
  ]
}
