# klanker-maker (km) — KPH's deep knowledge pack

> Promoted from `.planning/phases/07-kph-knowledge-base/corpus/km-digest.md`
> (07-01), folding in the km sandbox architecture diagram legend and a few
> first-person facts/turns of phrase straight from Kurt's own transcripts.
> This is the SWAPPABLE deep pack (system[1]) the router loads when a
> visitor asks about klanker-maker — it never lives in the cached stable
> prefix (system[0]).

> Corrected one-liner (per the repo): klanker-maker is **an AI-agent runtime on your own AWS account** — a single Go CLI (`km`) that compiles a Kubernetes-style YAML "SandboxProfile" into a real, isolated AWS sandbox with kernel-level (eBPF) network enforcement, a token-metering MITM proxy, hard dollar budgets, and native Slack/GitHub/email integration. It is less "sandbox operations platform" in the abstract and more "declarative, self-hosted agent infrastructure with security rails." MIT-licensed personal project of Kurt Hundeck; explicitly not affiliated with any employer.

## What it is

**Elevator version:** Klanker Maker turns a YAML file into a locked-down AWS sandbox where an AI agent — Claude Code, OpenAI Codex, Goose, or a security tool — can run untrusted code safely. You declare what the sandbox is allowed to do (which hosts, which repos, how many dollars), and `km` builds the real infrastructure: scoped IAM role, security groups, eBPF network filters, a MITM proxy that meters every AI token, a Slack channel that talks back to the agent, and a budget ceiling that actually suspends compute when the money runs out.

**The honest version:** It's a fleet manager for disposable agent workstations on EC2/ECS. The core insight is "isolation is the product": every sandbox is default-deny on the network, and the platform assumes the agent inside will try to escape. It's built for security/engineering teams who need agents triaging vulns, reviewing PRs, and patching code across many repos without the investigation itself becoming the next breach. It's also a way to put agents in front of cloud-scale compute — from a ~$0.01/hr t3.medium spot instance up to 48xlarge GPU boxes serving 70B-class local models — instead of running them on a laptop. The repo positions it against AWS Bedrock AgentCore ("but you own the substrate"), Coder ("but for agents instead of humans"), and E2B ("but self-hosted with kernel-level controls").

Fun meta-fact the repo itself documents: klanker-maker was built almost entirely *by* Claude Code — the BURNT.md scoreboard records ~14 billion tokens, ~3,351 commits, and ~627K net lines of code over 81 days on a $200/month plan, with the human operator doing design, UAT, and ship/no-ship calls. Version at time of digest: 0.5.56, 68+ phases shipped across 11 sprints.

## How it works

**Architecture (four frames, per README):**

1. **The runtime.** A sandbox is a "compiled policy object." The SandboxProfile YAML (`apiVersion: klankermaker.ai/v1alpha2`, `kind: SandboxProfile`, with `extends` for profile inheritance) declares egress hosts, repos, regions, and spend; the compiler produces a Security Group, a scoped IAM role, EBS/EFS storage, a per-sandbox cgroup with eBPF programs attached, a transparent MITM proxy, and sidecar systemd services (dns-proxy, http-proxy, audit-log, OTEL tracing). Isolation is at the AWS-primitive layer — no shared multi-tenant runtime.

2. **The fleet manager.** A DynamoDB global table is the source of truth (`km list`, `km status`, alias/number lookups). EventBridge Scheduler drives deferred and recurring ops (`km at 'every thursday at 3pm' kill alice`). Lambda dispatchers handle remote create/destroy, email-to-create, GitHub token refresh, TTL expiry, spot interruption, and budget enforcement. Sandboxes can be paused (hibernated with RAM preserved), stopped, locked, cloned, baked into AMIs, or scheduled to resume.

3. **The integrations layer.** A Slack App gives each sandbox a bidirectional `#sb-{id}` channel with transcript streaming and 👀 ack reactions; inbound messages flow through signing-secret-verified webhooks to per-sandbox SQS FIFO queues and become Claude turns. A GitHub App issues short-lived, repo-allowlisted installation tokens. SES + Ed25519 gives every sandbox a signed email identity (`{id}@sandboxes.{domain}`). OTEL captures every prompt, tool call, and token to S3 for replay.

4. **The work envelope.** Profiles pick the substrate (EC2 spot/on-demand, ECS Fargate, local Docker; EKS planned), instance size, and storage shape. The same eBPF + MITM + budget layer wraps everything from a quick-fix t3.medium to GPU fleets.

**Security model (docs/security-model.md):** four principles — explicit allowlists everywhere, deny by default, defense in depth, assume agent compromise. A 3-account AWS model (Management / Terraform / Application) contains blast radius. Network egress is enforced at up to four layers: Security Groups (L3/L4), cgroup eBPF programs (kernel-level — even root can't bypass), a DNS proxy returning NXDOMAIN for non-allowlisted domains, and an HTTP proxy returning 403 for non-allowlisted hosts. A 6-statement Service Control Policy backstops everything (deny IAM escalation, SSM pivot, org discovery, out-of-region actions, infra mutation).

**The `km` CLI command surface** (from docs/user-manual.md and internal/app/cmd/): `init`, `bootstrap`/`unbootstrap`, `validate`, `create` (incl. `--remote`), `clone`, `destroy`, `pause`/`resume`/`stop`, `lock`/`unlock`, `extend`, `list`, `status`, `logs`, `doctor` (a large diagnostic suite), `shell` (with `--learn` and `--ami`), `agent` (interactive) + `agent run`/`attach`/`results`/`list`, `budget add`, `ami`, `vscode`, `desktop`, `otel` (`--timeline` session replay), `email send`/`read`, `info`, `rsync`, `at`/`schedule` (+ `list`/`cancel`), `configure` (+ `configure github`), `github`, `h1`, `slack`, `cluster`, `capacity`, `model`, `roll creds`, `uninit`, `completion`. Sandboxes are addressed by alias, list number (`km agent 1 --claude`), or raw `sb-xxxx` ID.

**Companion binaries** (cmd/): budget-enforcer, create-handler, email-create-handler, github-token-refresher, ttl-handler (Lambdas); km-slack / km-slack-bridge, km-github / km-github-bridge, km-h1 / km-h1-bridge, km-send/km-recv (sandbox-side email), km-presence, km-quota-alerter, and a configui.

## Architecture at a glance (Kurt's diagram, ingested as text)

**One-line frame:** Two AWS accounts. A **Management Account** holds identity + DNS; a locked-down **Sandbox Account** holds everything a klanker runs on. **Each EC2 is one sandbox instance, fully configured by a YAML file.**

- **Management Account** — AWS Console (operator entry point), SSO (identity/sign-in), Route53 (DNS).
- **Sandbox Account (per-account AWS services):**
  - **SES** — every sandbox gets its own keys + email address; sandboxes exchange messages/files over email, each message landing as an S3 object (a built-in transaction log).
  - **EventBridge** — schedules the Lambda that refreshes GitHub-app credentials (~every 45 min, before the 1-hour token expires) and drives interval "checks".
  - **Lambda** — bridges + checks: GitHub/HackerOne webhook bridges, interval checks, the Slack webhook receiver (pushes to SQS), the GitHub-credential refresher.
  - **ECR** — container registry. **Bedrock** — optional in-instance access to foundational models, billed through the AWS account (pin to Haiku to bound cost). **KMS** — regional encryption keys (SOPS needs one in the executing region).
  - **SSM** — where per-sandbox credentials surface, plus the SSH/VS Code tunnel transport (a reverse tunnel over SSM, no public ports); git-askpass reads GitHub creds from SSM.
  - **DynamoDB** — state store; MeshTK also uses it for cached MQTT credentials. **VPC** — network isolation.
  - **IAM** — per-sandbox instance profiles + resource policies (each sandbox reaches only its own credentials); IRSA maps k8s service accounts to IAM roles, letting klanker-maker run from Kubernetes.
  - **SQS** — the Slack webhook lands via Lambda into SQS; the EC2 polls the queue for its next `claude -p` turn. **S3** — internal buckets, presigned URLs only, no public access. **EBS/EFS** — block storage plus a regional NFS same-region sandboxes can share to swap files.
- **Each sandbox (EC2), zoomed in** — runs on a hardened instance: **eBPF** (host-based egress firewall + layer-7 MITM inspection, cannot be disabled even as root — breaking it self-destructs the box, allowlists repos/branches, inspects tunnels), **Claude Code / Codex / goose** (device-flow login), **VS Code** (remote dev over the SSM reverse tunnel), **tmux**.
- **Apps & integrations** — **Slack** (per-sandbox channel, klanker has NO Slack creds — mediated by Lambdas, `km slack send`; rich text/tables/unfurls, reactions ack messages), **GitHub** (via a GitHub App — private key to 1-hour tokens, not PATs; creds float through SSM via git-askpass), **Email** (ProtonMail/Gmail/Outlook interoperate with the SES transport).

## In Kurt's own words (verbatim, from the recorded transcripts)

- On why he built it: *"I decided, like, securely because folks were doing crazy things... I thought at that time that sandboxing was gonna be the mechanism which is going to secure our agents, our klankers. We need to put them in little boxes and isolate them... and let them do whatever they need to do in that box."*
- On the eBPF depth: *"What's also nice about the sandbox and the eBPF is that I can allow privilege execution so you could run as root on that system and you still can't break out. There's basically no way to break out of that system... I can just let them rip on that system."*
- On the MITM proxy (a favorite demo bit): *"I have an example where I man-in-the-middle a google.com request and replace it with a rickroll YouTube."* — and a companion "learn mode" that, at the end of a session, prints every network address you actually visited, so a security profile can be generated from real observed traffic instead of guesswork.
- On Slack as a first-class interface: *"Slack was a big driving force when I first built this. People wanted to bring Claude into their Slack conversations, or walk away from their computer and have a Slack message say something to them... and be able to react to their klankers."* The klanker itself never holds Slack credentials — everything is mediated through Lambdas in the middle (`km slack send`), so a compromised sandbox still can't call the Slack API directly.
- On Bedrock vs. a direct Anthropic key: *"If I enable Bedrock, it'll allow the instance profile for the klanker itself to talk to Bedrock... all of the billing for your tokens is rolled up into your account billing. So effectively you don't see a bill from Anthropic, but it's rolled up into your AWS bill."* Pinning to a lightweight model like Haiku keeps that rolled-up spend to fractional pennies per session.
- On the project's own scale, mid-build (numbers below are as of that recording — see the BURNT.md figures above for the current, larger total): *"this extremely security-focused AWS sandbox project that I've burnt about a billion-and-a-half Claude tokens on."*

## Topic map

### Budgets — hard dollar ceilings
- Every sandbox has two independent spend pools: compute (spot/Fargate time × rate) and AI (Bedrock tokens × model rate), tracked in a DynamoDB global table.
- At 80% you get a warning email plus a Slack ping; at 100% the proxy starts returning 403 on AI calls and a Lambda revokes Bedrock IAM permissions — dual-layer enforcement. Compute exhaustion suspends the instance rather than destroying it, and `km budget add` tops up and resumes.
- Source pointers: `docs/budget-guide.md`, `cmd/budget-enforcer/`, `internal/app/cmd/budget.go`

### Network enforcement — eBPF and MITM proxies
- Three modes per profile: `proxy` (iptables DNAT into MITM sidecars), `ebpf` (cgroup-attached BPF programs filtering connects in the kernel), or `both`. In eBPF mode even a root user inside the sandbox can't bypass the allowlist.
- BPF programs hook cgroup/connect4, sendmsg4 (DNS redirect), sockops, and cgroup_skb/egress as a packet-level backstop; there's also SSL uprobe plaintext capture for observability.
- Source pointers: `docs/ebpf.md`, `docs/security-model.md` §5, `sidecars/dns-proxy/`, `sidecars/http-proxy/`

### SandboxProfiles — the YAML contract
- A profile is a Kubernetes-style YAML (kind: SandboxProfile) declaring lifecycle (TTL, idle timeout), runtime (substrate, spot, instance type, AMI, EFS), execution (init commands, env, rsync), source access (allowed GitHub repos/refs), network egress allowlists, IAM scoping, budget, and notifications. Profiles compose via `extends` with deep-merge inheritance.
- The `sealed` built-in profile is the extreme case: empty allowlists, zero network egress. Other built-ins include goose, codex, hardened, learn, and ao.
- Source pointers: `docs/profile-reference.md`, `profiles/` (incl. `profiles/base/`), `internal/app/cmd/validate.go`

### Learn mode — policy from observation
- `km shell --learn` records the DNS queries, TLS connections, GitHub repos, and shell commands from a live session, then generates a SandboxProfile YAML on exit — so you derive the allowlist from real traffic instead of guessing. Add `--ami` to also snapshot the tuned box into a private AMI referenced in the generated profile.
- Source pointers: `docs/user-manual.md` (§ km shell), `profiles/learner.yaml`

### Running agents
- `km agent <id> --claude` (or `--codex`) opens an interactive session over SSM; `km agent run <id> --prompt "..." --wait` fires a non-interactive turn in a persistent tmux session that survives disconnects; `km agent attach`, `results`, and `list` round out the loop. `--no-bedrock` switches from Bedrock to the direct Anthropic API.
- Results land as JSON (with `total_cost_usd`) in S3 and on sandbox disk.
- Source pointers: `docs/user-manual.md` (§ km agent), `internal/app/cmd/agent.go`

### Slack integration
- Each sandbox can get its own `#sb-{id}` channel with bidirectional chat: outbound status via an Ed25519-signed bridge Lambda (the bot token never leaves AWS), inbound Slack messages verified and dispatched via SQS FIFO to a sandbox poller that turns them into Claude turns, with per-turn transcript streaming and 👀 ack reactions. Later phases added mention-only "polite-bot" mode and a federated relay so one Slack App can serve many km installs.
- Source pointers: `docs/slack-notifications.md`, `docs/slack-app-permissions.md`, `cmd/km-slack-bridge/`, `skills/slack/`

### GitHub and HackerOne bridges
- The GitHub bridge makes `@km-bot review this PR` in a pull-request comment dispatch the full diff to a Claude agent in a km sandbox, which posts a structured review back — dormant by default, activated via `km github init`. Tokens are short-lived GitHub App installation tokens scoped to allowlisted repos, refreshed by Lambda, never written to env.
- The HackerOne bridge (`km-h1-bridge`) is the direct analog: a program webhook HMAC-verifies, dedupes, and dispatches a report to a sandbox agent, which replies through the HackerOne customer API.
- Source pointers: `docs/github-bridge.md`, `docs/github-app-permissions.md`, `docs/h1-bridge.md`

### Multi-agent email
- Every sandbox gets exactly one address, `{sandbox-id}@sandboxes.{domain}`, and an Ed25519 keypair; inter-sandbox mail is signed, verified against a `km-identities` table, and optionally NaCl-box encrypted. Email is deliberately the *only* cross-sandbox communication path — no VPC peering, no shared databases.
- There's also an operator inbox with a Haiku AI interpreter: a sandbox agent can email `operator@sandboxes.{domain}` a natural-language request ("schedule an agent run in 30 minutes") and the Lambda maps it to real `km` commands, gated by a KM-AUTH safe phrase.
- Source pointers: `docs/multi-agent-email.md`, `skills/email/`, `skills/operator/`, `cmd/email-create-handler/`

### Scheduling — km at
- `km at '10pm tomorrow' create profiles/goose.yaml` or `km at 'every thursday at 3pm' kill alice` — natural-language time expressions compile to EventBridge Scheduler rules targeting Lambdas; supports create, destroy, kill, stop, pause, resume, extend, budget-add, and recurring agent runs. Recurring crons run in the operator's local timezone.
- Source pointers: `docs/user-manual.md` (§ km at / km schedule), `internal/app/cmd/at.go`, `cmd/ttl-handler/`

### GPU model serving
- Phase 122 adds profiles that serve 70B-class local LLMs (Qwen 2.5-72B, Llama 3.3-70B, GLM-4.5-Air/4.6, Kimi-Dev-72B) on g6e.12xlarge/48xlarge via vLLM behind an on-box Bifrost gateway, reachable through VS Code, Slack, on-box codex, or a `km model start` laptop port-forward. As of the doc, live GPU UAT was pending an AWS quota increase — code-complete, not fully hardware-verified.
- Source pointers: `docs/gpu-model-serving.md`, `profiles/gpu-*.yaml`, `profiles/base/gpu/`

### Remote access — VS Code and desktop
- `km vscode start` opens an SSM port-forward and writes a managed SSH config block so desktop VS Code Remote-SSH lands in `/workspace` — no public IP, no bastion, per-sandbox ed25519 keys. `km desktop start` does the same trust model for a graphical browser session via KasmVNC (kiosk or full XFCE mode).
- Source pointers: `docs/vscode.md`, `docs/desktop.md`, `skills/vscode/`, `skills/desktop/`

### Observability and audit
- An OTEL Collector sidecar captures Claude Code prompts, tool calls, API requests, token usage, and cost per turn to S3; `km otel --timeline` replays a session. Audit logs redact secrets; `km logs` streams audit and network logs.
- Source pointers: `docs/security-model.md` §11, `sidecars/tracing/`, `sidecars/audit-log/`, `internal/app/cmd/otel.go`

### Economics — spot-first
- A t3.medium spot instance is about a cent an hour in us-east-1; the README's pitch is "run 10 sandboxes for a workday for under a dollar." Spot interruption handlers upload artifacts on the 2-minute warning.
- Source pointers: `README.md`, `docs/security-model.md` §13, `internal/app/cmd/spot_rate.go`

### The BURNT story — built by the agent it hosts
- BURNT.md is a living token scoreboard: ~14.01 billion tokens, ~3,351 commits, ~627K net lines over 81 days, zero hand-typed code — Claude Code on Opus 4.7 wrote it while the operator designed profiles, ran UAT, and made ship calls. Great booth story.
- Source pointers: `docs/BURNT.md`, `docs/BURNT.process.md`, `docs/RELEASE-HIGHLIGHTS.md`

## Cross-links

- **klanker-voice (this project):** `kv` is designed as the structural sibling of `km` — same cobra command-tree conventions, same terraform/terragrunt style. klanker-voice's quota/kill-switch philosophy ("dollar ceilings that actually stop burn") is km's budget model applied to voice APIs. The KlankerMaker concierge (KPH) itself exists to explain km.
- **defcon.run.34:** km profiles use `whereiskurt/defcon.run.34` as a canonical allowedRepos example; klanker-voice's auth service is a port of defcon.run's run.auth, and its infra conventions follow defcon.run.34's terragrunt pins. Same operator, shared IaC DNA.
- **meshtk:** appears alongside defcon.run.34 in km's example repo allowlists (`whereiskurt/meshtk`) — one of Kurt's repos that km sandboxes are typically pointed at.
- **tiogo / kvmlab:** no mentions found in the km repo's docs or code — I can't attest to a concrete link from this codebase. (tiogo is historically Kurt's Tenable.io Go CLI; kvmlab presumably local virtualization — but that's outside this repo, so KPH should hedge.)
- **klankermaker.ai domain:** shared brand across the family — km's sandbox email lives at `sandboxes.klankermaker.ai`, klanker-voice at `voice.klankermaker.ai`, auth at `auth.klankermaker.ai`.

## Sample Q→A

1. **Q: What is Klanker Maker?**
   A: It's Kurt's agent runtime for AWS — a Go CLI called `km` that compiles a YAML profile into an isolated cloud sandbox where AI agents like Claude Code can run untrusted code, with kernel-level network filtering, a hard dollar budget, and its own Slack channel.

2. **Q: Why not just run Claude Code on a laptop?**
   A: Two reasons: safety and scale. A sandbox contains the blast radius when an agent touches malicious code, and AWS lets the agent sit next to real compute — anything from a one-cent-an-hour spot instance to a GPU box serving a 70-billion-parameter model.

3. **Q: How does the budget enforcement actually work?**
   A: Each sandbox has separate compute and AI dollar pools in DynamoDB. At 80% you get warned by email and Slack; at 100% the proxy starts rejecting AI calls with a 403 and a Lambda revokes the Bedrock permissions. The sandbox is suspended, not destroyed — `km budget add` tops it up and resumes.

4. **Q: What stops an agent from escaping the sandbox?**
   A: Defense in depth: security groups, eBPF programs attached to the sandbox's cgroup that filter connections in the kernel — even root can't bypass them — plus DNS and HTTP proxies that deny anything not allowlisted, and an AWS Service Control Policy as the org-level backstop. The design assumes the agent *will* try to escape. Kurt's own line on it: he can let an agent run as root and it still can't break out — "let them rip."

5. **Q: How do you talk to a running agent?**
   A: Lots of ways. Every sandbox can get its own Slack channel with bidirectional chat — you message the channel and it becomes a Claude turn. Or `km agent run` from the terminal, or VS Code over SSM, or even email: each sandbox has its own signed email address.

6. **Q: What's the coolest command?**
   A: Probably `km shell --learn`. You open a shell, do your work, and when you exit, km generates a security profile from the traffic it observed — the DNS lookups, TLS connections, and repos you actually touched become the allowlist. Policy from observation instead of guesswork. Kurt's demo bit for the inspection layer: he's man-in-the-middled a google.com request and swapped in a rickroll.

7. **Q: Can agents talk to each other?**
   A: Yes, but only by email — that's deliberate. Each sandbox gets an Ed25519 keypair and a unique address; messages are cryptographically signed and verified, optionally encrypted. No shared network, no shared database — just verifiable mail between isolated agents.

8. **Q: What agents does it support?**
   A: Claude Code and OpenAI Codex are first-class via `km agent`, Goose has a built-in profile, and it can serve open models like Qwen and Llama locally on GPU sandboxes through vLLM. Claude can go through Bedrock or the direct Anthropic API.

9. **Q: What does a sandbox cost?**
   A: It's spot-first: a t3.medium spot instance is around a penny an hour in us-east-1. The README's line is you can run ten sandboxes for a full workday for under a dollar — before AI tokens, which have their own metered budget.

10. **Q: Can GitHub trigger an agent?**
    A: Yes — comment `@km-bot review this PR` on an allowlisted repo and a bridge Lambda reacts with an eyes emoji, ships the diff to a Claude agent in a sandbox, and the agent posts a structured review back to the PR. There's an equivalent bridge for HackerOne reports.

11. **Q: Who built it?**
    A: Kurt designed it and ran the ship decisions, but nearly every line of code was written by Claude Code — the repo keeps a scoreboard called BURNT: about 14 billion tokens and 627 thousand lines of code over 81 days, on a 200-dollar-a-month plan, with zero hand-typed code.

12. **Q: How does it relate to this voice demo?**
    A: Same family. The voice project's CLI, `kv`, is the deliberate sibling of `km` — same command style, same infrastructure conventions — and the voice service borrows km's core idea: give an AI a real budget ceiling that actually enforces itself.

13. **Q: How is it different from something like E2B or Bedrock AgentCore?**
    A: The repo's framing: it's AgentCore but you own the substrate, Coder but for agents instead of humans, and E2B but self-hosted with kernel-level controls — real cloud credentials, real storage, real GitHub repos, and a real budget, all in your own AWS account.

14. **Q: Can I schedule things?**
    A: Yes — `km at` takes natural language: "km at 10pm tomorrow create," or "every Thursday at 3pm kill alice." It compiles to EventBridge Scheduler rules, so schedules fire even with your laptop closed. You can even email the operator inbox in plain English and a Haiku model translates it into commands.

15. **Q: Is it open source?**
    A: Yes — MIT licensed on GitHub under whereiskurt, as a personal project. The docs are careful to say it's not a commercial product and it provisions real AWS infrastructure on your bill, so use at your own risk.

## Landmines / do-not-say

- **`km-config.yaml` (repo root) and `km-config.yaml.bak`:** a real operator config file — likely contains AWS account IDs, bucket names, Lambda ARNs, Slack/GitHub identifiers, and domain wiring. Do NOT surface its contents. (Not read into this pack; only its existence noted.)
- **`profiles/secrets/*.enc.yaml`:** SOPS-encrypted secrets (HF tokens etc.). Encrypted at rest, but do not quote filenames-to-purpose mappings beyond the generic pattern, and never contents.
- **`.planning/` tree (~hundreds of files):** internal GSD planning artifacts, phase plans, research, and unreleased-feature context (e.g., EKS substrate "planned", Phase 117 profile inheritance work, federated relay internals). Treat as private roadmap — KPH should only speak to what's in README/docs.
- **Internal hostnames/emails:** `operator@sandboxes.klankermaker.ai` and the `sb-*@sandboxes.klankermaker.ai` pattern appear in public docs, so the *pattern* is speakable, but KPH should not enumerate real sandbox IDs, channel names, or live schedule names, and should never encourage strangers to email the operator inbox (it executes platform actions).
- **KM-AUTH safe phrase mechanism:** fine to say "authenticated requests," but do not describe how to forge or where the phrase lives.
- **AWS specifics:** never state account IDs, role ARNs, bucket names, Function URLs, or webhook endpoints even if found in docs/tests/diagrams.
- **Pending/unverified claims:** GPU serving live UAT and the HackerOne reply-visibility UAT were pending at doc time — KPH should say "designed and code-complete" rather than overclaim.
- **`scratchpad/`, `testdata/`, `dist/`, `bin/`:** build/test artifacts; not knowledge, possibly containing captured traffic or tokens — excluded from this pack.

## PACK COMPLETE — klanker-maker
