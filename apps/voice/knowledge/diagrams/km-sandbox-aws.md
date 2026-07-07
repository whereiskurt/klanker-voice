# Klanker-Maker Sandbox — AWS Services, Apps & Integrations

> Structured text legend of Kurt's architecture diagram (source of truth: hand-drawn
> Excalidraw). Ingested as searchable text per Phase-7 Amendment 3-D (km diagram → text).
> Roles annotated from Kurt's transcripts where he described them; unannotated entries are
> named in the diagram but not yet narrated.

**One-line frame:** Two AWS accounts. A **Management Account** holds identity + DNS; a
locked-down **Sandbox Account** holds everything a klanker runs on. **Each EC2 is one sandbox
instance, fully configured by a YAML file.**

## Management Account
- **AWS Console** — operator entry point.
- **SSO** — identity / sign-in.
- **Route53** — DNS.

## Sandbox Account (per-account AWS services)
- **AWS Console** — account entry point.
- **SES** — Simple Email Service. Every sandbox gets its own keys + email address; sandboxes
  exchange messages/files over email, each message landing as an S3 object (built-in txn log).
- **EventBridge** — schedules the Lambda that refreshes GitHub-app credentials (~every 45 min,
  before the 1-hour token expires) and drives interval "checks".
- **Lambda** — bridges + checks: GitHub/HackerOne webhook bridges, interval checks, the Slack
  webhook receiver (pushes to SQS), the GitHub-credential refresher.
- **ECR** — container registry.
- **Bedrock** — optional in-instance access to foundational models; token spend rolls into the
  AWS bill (no separate Anthropic key). Pin to Haiku to bound cost.
- **Route53** — DNS.
- **KMS** — encryption keys; regional (SOPS needs a KMS key in the executing region).
- **SSM** — where per-sandbox credentials surface; also the SSH/VS Code tunnel transport
  (reverse tunnel over SSM, no public ports). git-askpass reads GitHub creds from SSM.
- **DynamoDB** — state store; MeshTK uses it for cached MQTT credentials.
- **VPC** — network isolation.
- **IAM** — per-sandbox instance profiles + resource policies; each sandbox reaches only its
  own credentials. IRSA maps k8s service accounts → IAM roles (run klanker-maker from k8s).
- **SQS** — Slack webhook → Lambda → SQS; the EC2 polls the queue for its next `claude -p` turn.
- **S3** — internal buckets (no public access; presigned URLs only); check results + email objects.
- **EBS** — block storage for instances.
- **EFS** — regional NFS; sandboxes in the same region share EFS mounts to swap files.

## Sandbox (EC2), zoomed — "Each EC2 is a sandbox instance configured by a YAML"
Runs on a hardened EC2 instance:
- **eBPF** — host-based egress firewall / layer-7 MITM inspection; cannot be disabled even as
  root (breaking it self-destructs the box). Allow-lists repos/branches, inspects tunnels.
- **Claude Code**, **Codex**, **goose** — coding agents (device-flow login).
- **VS Code** — remote dev over the SSM reverse tunnel.
- **tmux** — terminal multiplexer.

## Apps & Integrations
- **Group Chat: Slack** — per-sandbox channel; klanker has NO Slack creds (mediated by Lambdas;
  `km slack send`). Rich text/tables/unfurls; reactions ack messages.
- **Source Code: GitHub** — via a GitHub App (private key → 1-hour tokens), not PATs; creds float
  through SSM via git-askpass.
- **Email: ProtonMail / Gmail / Outlook** — external email endpoints the SES transport interoperates with.

## Cross-checks vs. transcripts
Diagram corroborates: SES email transport + EFS/S3 (`AWS.services`), the GitHub-app credential
float via SSM/EventBridge/Lambda (`github.slack.app`), Slack webhook→Lambda→SQS→EC2 poll +
Bedrock (`more.klanker`), eBPF firewall depth (`irsa`, `let's.it.going`), IRSA k8s→IAM
(`more.1`), DynamoDB-cached MQTT creds (`alrighty`). No contradictions found.
