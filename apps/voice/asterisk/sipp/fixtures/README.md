# SIPp pcap fixtures (Phase 11 Plan 07, D-07/R4)

`gate-pass.xml` replays a short, pre-recorded RTP/PCMU capture through
SIPp's `play_pcap_audio` action to exercise the §24 answer-gate's real
audio path (Asterisk -> External Media -> the Klanker RTP listener -> STT)
end-to-end. Neither fixture is committed to the repo (recorded audio
containing the real passphrase/PIN is exactly the kind of thing that
shouldn't sit in git history next to the config that defines it) -- both
are generated locally, on demand, following the recipe below.

## Why not commit them

- `gate-pass-passphrase.pcap` embeds whatever `TELEPHONY_PASSPHRASE_WORDS`
  your local `.env` is configured with (D-09: secrets stay out of the
  repo). A committed fixture would either go stale the moment you rotate
  the passphrase, or -- worse -- leak it.
- `gate-pass-dtmf.pcap` is smaller and less sensitive (DTMF tones for
  `TELEPHONY_ACCESS_PIN`) but has the same staleness problem.

If your setup genuinely never rotates these values and you want a stable
fixture checked in, that's a discretionary local choice -- just don't
commit real production secrets under any fixture filename.

## Recording recipe

Two practical ways to produce a `play_pcap_audio`-compatible file (SIPp
expects a `.pcap` containing one RTP audio stream, PCMU/8000 to match
`rtp.conf`/Phase 10's codec):

### Option A -- record live with a softphone + Asterisk, capture with tcpdump

1. Bring up the compose harness (`docker compose up`, see `../README.md`).
2. Point a real SIP softphone (Linphone/baresip) at the `dev-softphone`
   endpoint, place a call, and while `tcpdump`/Wireshark is capturing on
   the loopback/rtp.conf port range, speak the 4 passphrase words (or key
   the DTMF PIN).
3. Filter the capture down to just the RTP stream for that call
   (Wireshark: `Telephony -> RTP -> Stream Analysis`, then
   `File -> Export Specified Packets`) and save as
   `fixtures/gate-pass-passphrase.pcap` (or `-dtmf.pcap`).
4. Confirm the codec is PCMU (payload type 0) -- `rtp.conf`/the PJSIP
   endpoint only negotiate ulaw (Phase 10 §9), and `gate-pass.xml`'s SDP
   offer only advertises payload type 0 (+ 101 for DTMF telephone-events).

### Option B -- synthesize offline with `sox` + `text2wave`/ElevenLabs, then wrap as RTP

1. Render a short WAV of the 4 passphrase words at 8kHz mono (any TTS is
   fine for a test fixture -- this never needs to be the production KPH
   voice): e.g. `say -o words.aiff "purple falcon midnight compass"` (macOS)
   or ElevenLabs via `make -C apps/voice say TEXT="purple falcon midnight
   compass"` (writes to whatever the `say.py` script's default output is)
   then `sox` to 8kHz mono PCMU: `sox words.wav -r 8000 -c 1 -e mu-law
   words.ulaw`.
2. Wrap the raw ulaw payload as an RTP stream inside a pcap. SIPp ships
   a `pcapplay` doc example (`sipp.readthedocs.io/en/latest/media.html`)
   with a small Python/scapy snippet for exactly this — build one RTP
   packet per 20ms frame (matches `packet_ms=20`, Phase 10/11's fixed
   packetization interval), PT=0, and write the packets to a pcap file
   with `scapy`'s `wrpcap`.

### DTMF fixture (`gate-pass-dtmf.pcap`)

Same shape, but the audio is RFC2833 `telephone-event` RTP packets (PT=101,
matching the SDP offer in `gate-pass.xml`) encoding the digits of
`TELEPHONY_ACCESS_PIN`, not spoken audio. A softphone's own DTMF keypad
(Option A) is the simplest way to generate this — most SIP softphones send
RFC2833 by default.

## Validating a fixture before wiring it into a CI-facing run

```bash
# Confirms the file parses as a valid pcap and inspect the RTP stream:
tcpdump -r fixtures/gate-pass-passphrase.pcap -n

# Dry-run the scenario against the compose Asterisk instance:
docker compose --profile integration run --rm sipp
```

A missing fixture makes `gate-pass.xml`'s `play_pcap_audio` action fail
loudly (SIPp errors on the missing file) rather than silently skipping --
this is intentional so a stale/absent fixture is never mistaken for a
passing run.

**This local SIPp + real-fixture run is opt-in, local-only tooling** — the
CI-required artifact is the deterministic, fixture-free
`tests/test_telephony_integration.py` (fake AriClient + fake RTP media
session, synthetic `TranscriptionFrame`s standing in for the STT output a
real pcap replay would eventually produce). See that file's own module
docstring and `../README.md`'s "Manual §19-C softphone proof" section for
the full CI-vs-manual boundary.
