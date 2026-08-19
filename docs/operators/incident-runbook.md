# Incident runbook

What to do when it's on fire, and what to do before it is.

Everything here is written for the case where you have five minutes, a phone in
your hand, and a room full of people. The reasoning is at the bottom; the
commands are at the top.

---

## The brake

```bash
kv killswitch on --reason "explain yourself here"
```

Every **new** voice session — browser and phone alike — is refused site-wide,
effective on the next session. No deploy, no restart, no cache. Sessions already
in progress are not killed.

```bash
kv killswitch status     # what is it doing, and why
kv killswitch off        # release, and clear the reason
```

Both `on` and `off` are idempotent: flipping to the state it is already in
prints `(no-op)` and is not an error.

**Always set `--reason`.** It is what tells you, later, whether a human pulled
the brake or the automatic ceiling did.

---

## It trips itself

Two ceilings engage the kill-switch with no human involved, checked as usage is
recorded:

| Ceiling | Value | Meaning |
|---|---|---|
| `auto_trip_ceiling_seconds` | 7200 | two hours of total site-wide conversation today |
| `auto_trip_ceiling_dollars` | 40 | forty estimated dollars today |

Either one crossing engages the switch with `reason: auto-trip`. The dollar
figure is derived from `est_cost_per_second` (0.005) — a tripwire, not a bill.

```bash
kv killswitch status     # ENGAGED true, REASON auto-trip
kv usage today           # how much, and how many sessions
```

> **Turning it off without changing anything means it trips again**, usually
> within minutes, because the day's total does not reset when you release the
> brake. Decide first: is the traffic legitimate (raise the ceilings and
> redeploy) or not (find the source, then release)?

To raise the ceilings you edit `auto_trip_ceiling_*` in the pipeline config and
deploy. There is no runtime override. The fast lever that needs *no* deploy is
tightening tiers instead:

```bash
kv tier define pstn-public-tier --session-max 120 --period-max 900 \
  --max-concurrent 4 --group pstn
```

Tier changes take effect on the next session. Halving `--session-max` halves the
burn rate without turning anyone away — usually the right first move mid-event.

> `tier define` is a full replace. Pass every flag; omitted ones revert to
> defaults, and `--max-concurrent` defaults to `1`.

---

## Diagnostic tree

### "The phone number doesn't work"

Work down. Each step is cheaper than the one after it.

**1. Is the brake on?**
```bash
kv killswitch status
```
Engaged means every call is refused. That is the whole answer.

**2. Is there money on the account?**
```bash
kv voipms balance
```
Auto-recharge is deliberately off, so a drained balance stops inbound routing —
failing closed, silently, indistinguishable from a broken number. This is the
single most likely cause of "it just stopped working overnight".

**3. Is the edge running?**
```bash
aws ecs describe-services --cluster app-use1-kmv --services telephony-edge-use1 \
  --query 'services[0].{d:desiredCount,r:runningCount,s:deployments[0].rolloutState}'
```
`runningCount: 0` with `FAILED` almost always means a `valueFrom` in the task
definition points at an SSM parameter that does not exist. Check the recent
`service.hcl` diff against what is actually seeded:
```bash
aws ssm describe-parameters \
  --parameter-filters "Key=Name,Option=BeginsWith,Values=/kmv/secrets/use1/ctf" \
  --query 'Parameters[].Name' --output text
```

**4. Are calls arriving at all?**
```bash
kv telephony calls --since 1h --view calls
aws logs tail /ecs/telephony-edge-telephony-edge-use1-kmv --since 15m --follow
```
Nothing at all means the call is not reaching the edge: a carrier problem, a POP
mismatch, or the security group. Calls arriving means routing is fine and the
problem is downstream.

**5. Is the number still routed and tagged?**
```bash
kv telephony list
kv voipms set-cid-prefix <did> <TAG>     # idempotent; verifies by readback
```
Re-asserting the prefix is safe and tells you the truth — the command reads the
DID back and fails if routing changed or the prefix did not land.

**6. Did the POP IPs change?**

If it worked for months and then stopped with no deploy, suspect a VoIP.ms POP IP
change. The security group allows only ten `/32`s. Re-verify against
[wiki.voip.ms/article/Servers](https://wiki.voip.ms/article/Servers) and update
both `telephony-sg.hcl` and the runbook table together.

### "Callers can't get through the gate"

```bash
kv telephony stats --since 2h
```

![kv telephony stats](../assets/terminal/telephony-stats.svg)

Read the outcome mix:

| What you see | What it means |
|---|---|
| mostly `gate_timeout`, all at the same second | the window is closing on people — see below |
| `gate_timeout` with partial `DIGITS` counts (2, 4, 5…) | the window is closing **mid-code**; widen it |
| mostly `early_hangup` | people are hanging up before trying — cue or greeting problem |
| `announcement_code` present and healthy | the line works; the puzzle is just hard |
| `error` | pipeline failures; check the logs |

The window is `gate_window_seconds` (currently 12), measured from the *end* of
the ~4.3s pickup cue. It has been tuned on real telemetry before: at 8s, even the
operator — who knows the code — had a median unlock at 6.0s and a max at 12.1s,
with 3 of 24 successful unlocks already over 8s, while six external callers timed
out at exactly 8.1s having entered partial codes. Widening to 12s **and** making
the timer cue-relative took the real dialling budget from ~4s to ~10s.

If it is only *spoken* triggers failing, remember Deepgram needs ~2.8s to finalise
an utterance on top of the caller's speaking time.

To see what speech actually heard on failed attempts, `gate_debug_log_heard`
logs one line per fail-closed call with the caller's number and the tokens STT
heard. It is opt-in, deliberately fail-path only, never logs the passphrase or
the PIN, and should be flipped back off once you have your answer.

`gate_debug_log_dtmf` similarly logs *that* a digit arrived and a running count —
never the value — which is how the "twelve calls, zero digits registered" mystery
was settled. (Answer: the caller genuinely pressed nothing. The instrument was
fine.)

### "The bill is spiking"

```bash
kv usage today                    # site total, session count, estimated cost
kv telephony calls --since 6h --view callers
kv telephony calls --since 6h --view new
```

The `callers` view is a per-caller rollup with a DID breakdown; `new` surfaces
first-seen callers inside the window. One number with an implausible call count
is your answer.

Escalation ladder, cheapest first:

1. **Tighten the tier.** `kv tier define pstn-public-tier --session-max 120 …` —
   no deploy, next session.
2. **Cut off one caller.** If it is a mapped caller-ID:
   `kv code phone <code> --remove`. If it is an access code doing the damage:
   `kv code expire <code>` and `kv code bypass <code> --disable`.
3. **Pull the brake.** `kv killswitch on --reason "cost spike, investigating"`.
4. **Stop the phones only.** Scale `telephony-edge-use1` to zero — leaves the
   browser side up.

### "The browser side is broken"

```bash
kv smoke
```

![kv smoke](../assets/terminal/smoke.svg)

- `FAIL` with `ICE-STATE` not `connected` → the media path is broken. Check the
  voice service is running and the WebRTC UDP security group is intact.
- `FAIL` with ICE connected but `RTP-PACKETS 0` → signalling works and the
  pipeline is not speaking. Provider keys or a pipeline error; check
  `/ecs/voice-app-voice-use1-kmv`.
- An auth rejection → `KMV_SMOKE_SERVICE_TOKEN` is wrong or unset.

### "Nobody can log in"

The auth service has no public IP and sits behind the ALB.

```bash
aws ecs describe-services --cluster app-use1-kmv --services auth-use1 \
  --query 'services[0].{d:desiredCount,r:runningCount,s:deployments[0].rolloutState}'
aws logs tail /ecs/auth-app-auth-use1-kmv --since 15m
```

Auth being down takes **phone calls** with it: the caller-ID mint path and the
CTF OTP/SMS routes both live in the auth app. A phone outage with a healthy
telephony-edge is worth an auth check.

---

## Before an event

Five minutes, in this order:

```bash
kv killswitch status                    # disengaged?
kv voipms balance                       # funded?
kv smoke                                # pipeline carries audio?
kv telephony list                       # numbers routed, tagged, games armed?
kv tier list                            # public tier exists and is sane?
kv usage today                          # clean slate?
```

Then **call each number** and play each game. Nothing else proves the chain end
to end: prefix set at the carrier, prefix arriving in the caller-ID name, tag
matching the TOML, code resolving from SSM, script playing, SMS delivering. Every
one of those links fails silently.

Confirm `pstn-public-tier` appears in `kv tier list`. If that row is missing,
every public call fails closed with no error anywhere — the tier is referenced by
name from `configs/telephony.toml`, and an absent tier is treated as no-access.

---

## After an event

```bash
kv telephony stats --since 72h          # how did each line do?
kv telephony calls --since 72h --view callers
kv usage today
kv telephony list                       # audit the mint mappings
```

Then clean up the standing access you granted:

```bash
kv code expire <event-code>
kv code bypass <event-code> --disable
kv code phone <code> --remove
```

An event code without an expiry, or a bypass link still live, is a free pass that
outlives the event by however long it takes someone to notice.

---

## What the brake does and does not do

**Does:** refuse every new voice session site-wide — browser and phone — on the
next session start, with no deploy and no restart, because the session-start gate
reads that one control item on every session.

**Does not:**

- kill sessions already in progress;
- stop calls from *arriving* (the phone still answers, then declines);
- stop VoIP.ms per-minute charges for those arriving calls;
- reset the day's usage totals, so an auto-trip re-arms the moment you release it;
- affect the auth service, magic links, or `/admin`.

To stop calls arriving at all, scale `telephony-edge-use1` to zero — with the
caveat that Terraform owns `desired_count`, so the next apply of the ECS service
unit undoes it.

---

## Related

- [`kv` CLI reference](kv-cli-reference.md) — every command above
- [Infrastructure](infrastructure.md) — what each layer is, and health checks
- [Access codes & tiers](access-codes-and-tiers.md) — the quota model
- [Phone games](phone-games-runbook.md) — game-specific failure modes
- [PBX lifecycle](pbx-lifecycle.md) — carrier-side recovery
