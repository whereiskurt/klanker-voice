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
  voipms_toronto_pop_cidrs = [
    "158.85.70.148/32",  # Toronto 1
    "158.85.70.149/32",  # Toronto 2
    "158.85.70.150/32",  # Toronto 3
    "158.85.70.151/32",  # Toronto 4
    "184.75.215.106/32", # Toronto 5
    "184.75.215.114/32", # Toronto 6
    "184.75.215.146/32", # Toronto 7
    "184.75.213.210/32", # Toronto 8
  ]
}
