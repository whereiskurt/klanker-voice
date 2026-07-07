# klanker-voice — CloudFront + S3 static-asset front (design)

**Date:** 2026-07-07
**Status:** Approved (topology + module layout), implementation in progress
**Author:** brainstormed with Kurt (KPH)
**Relates to:** the 2026-07-07 black-screen incident (multi-build asset skew); durable fix.

## Problem

The klanker-voice SPA (`apps/voice/client`, Vite `base:"/"`) is built to `dist/`, **COPY'd
into the Docker image**, and served by the FastAPI app via `StaticFiles(html=True)` at `/`
with a 404→`index.html` fallback. Because each ECS task carries its own image, and Vite emits
**content-hashed** asset filenames, a deploy window with two tasks (old + new) serves two
different bundles. `index.html` from task A references `/assets/index-<hashA>.js`; if that
request round-robins to task B (which only has `<hashB>`), it 404s → the SPA 404-fallback
returns `index.html` **in place of** the JS/CSS → React never mounts → **black screen**. (The
proximate 2026-07-07 incident was compounded by an ECS scale-in-protection leak that stranded
the old task; that bug is separately fixed and deployed in `d25dd3c`.)

The durable fix: serve the static client from a **retained S3 bucket behind CloudFront** so
old and new hashed bundles coexist across deploy windows — a mismatched hash can never 404.

## Decision (approved)

**Full-front CloudFront** — `voice.klankermaker.ai` moves onto a single CloudFront
distribution with two origins, mirroring defcon.run.34's `run.human` shape but **flat
single-domain routing** (no per-region path prefix).

### Topology

```
Route53  voice.klankermaker.ai  (A / alias)
                 │
                 ▼
          CloudFront distribution
          ├─ /api/*   ─┐
          ├─ /health  ─┴──▶ ALB origin ──▶ ECS task   (signaling + liveness)
          └─ /*  (default) ──▶ S3 asset bucket (private, OAC)   (SPA shell + hashed assets)

   WebRTC media:  browser ──UDP──▶ task public IP   (direct; does NOT traverse CloudFront)
```

### Cache behaviors (ordered)

| Path pattern | Origin | Cache policy | Origin-request policy | Notes |
|---|---|---|---|---|
| `/api/*` | ALB | Managed-CachingDisabled (`4135ea2d-…`) | Managed-AllViewerExceptHostHeader (`b689b0a8-…`) | Forwards `Authorization`; SDP offer POST. Never cache. |
| `/health` | ALB | Managed-CachingDisabled | AllViewerExceptHostHeader | Real app liveness. |
| `/*` (default) | S3 (OAC) | Managed-CachingOptimized (`658327ea-…`) | — | Hashed assets long-cache; `index.html` served no-cache. |

- **Deep-link / SPA routing:** CloudFront `custom_error_response` maps S3 403/404 → `/index.html`
  with HTTP 200. The app-side 404→index fallback remains as a defense-in-depth backstop.
- **WebRTC media is unaffected** — it is direct UDP to the task's public IP; CloudFront only
  fronts the one-shot `/api/offer` POST and the static shell.
- **ALB host-header rule:** the ALB origin sends a custom header (e.g. `X-Origin-Region`) and
  the voice listener rule continues to match `host_headers=["voice.klankermaker.ai"]`; CloudFront
  forwards the Host via AllViewerExceptHostHeader semantics as run.human does.

### Region-path scheme: FLAT (no `/use1/` prefix)

defcon.run.34 serves each region under a `/{region}/…` path prefix (Next.js `basePath`). We
**do not** adopt that. Rationale:

- The prefix only earns its keep when a *second live region* serves the same domain with its
  own ALB — a future multi-region voice deploy with its own design.
- Adopting it now would churn the live path (Vite `base`, OIDC redirect URI, `/api/offer` →
  `/use1/api/offer`, SPA mount) for zero present benefit. YAGNI.
- The client already calls `/api/offer` as a **relative** path and Vite is `base:"/"`, so
  full-front requires **no client code change** for the endpoint.

We still adopt run.human's **global-resources + skip/mock structure** (below) so additional
regions can be lit up later without restructuring.

## Terraform structure

Our `infra/terraform` already mirrors defcon.run.34 (providers/, live/site/{global,region,
services}, modules/*/v1.0.0). `site.hcl` already carries a stubbed `cloudfront` block,
`skip_regions=["ap-southeast-1","ca-central-1"]`, and an IAM grant of `cloudfront:*`.

### New modules

- **`modules/cloudfront-assets/v1.0.0`** (regional) — the private S3 asset bucket
  `cf-assets-voice-{region.label}-{site}-{rnd}`: versioning, AES256 SSE, full
  public-access-block, CORS GET/HEAD, lifecycle (expire noncurrent 90d, abort MPU 7d).
  Writes SSM discovery params `/{site}/cloudfront-assets/{region.label}/voice/bucket_name`
  (+ `bucket_arn`, `bucket_regional_domain_name`). **No bucket policy here** — the global
  module owns it (avoids the cross-region provider circular dependency).

- **`modules/cloudfront/v1.0.0`** (global, us-east-1) — `aws_cloudfront_distribution`
  (alias `voice.klankermaker.ai`), `aws_cloudfront_origin_access_control` (sigv4), ALB +
  S3 origins, the three ordered behaviors, `custom_error_response` 403/404→`/index.html`,
  `viewer_certificate` from the us-east-1 ACM cert, `logging_config` → a CF logs bucket,
  and the **asset-bucket `aws_s3_bucket_policy`** applied here via the regional provider
  alias (`aws.use1`) with `Condition StringEquals AWS:SourceArn=<dist arn>` + a DenyNonHTTPS
  statement. `route53.tf`: A-alias → distribution.

### New live units

- `live/site/region/us-east-1/cloudfront/terragrunt.hcl` → `modules/cloudfront-assets`.
- `live/site/global/cloudfront/terragrunt.hcl` → `modules/cloudfront`. Declares `dependency`
  blocks for **use1 + cac1 + apse1** cloudfront-assets units + use1 network (ALB) + use1
  certs. cac1/apse1 are skip-excluded → fall back to `mock_outputs` (with
  `mock_outputs_allowed_terraform_commands` including `"apply"`). Bucket-policy `for_each`
  guards `!contains(skipped_region_labels, …)` and `!startswith(bucket_id,"mock-")` so no
  policy ever touches a mock bucket.

### Config wiring (`site.hcl`)

Flip the stub: `cloudfront = { enabled=true, domains=["voice"], regions=["us-east-1"],
price_class="PriceClass_100", logging={…} }`. `skip_regions` already lists cac1/apse1. The
`region/skip.hcl` exclude triad + mock pattern is added/confirmed as part of this work.

### Certificate

CloudFront requires a **us-east-1** ACM cert for the `voice.klankermaker.ai` alias. We have a
`certs` module in `region/us-east-1`; add the `voice.klankermaker.ai` SAN there if not already
covered, DNS-validated via Route53. (The ALB's own cert is separate and unchanged.)

## Build / deploy changes

Move the client from "baked into the image" to "synced to S3":

1. **Build** `apps/voice/client` → `dist/` (in CI, as today).
2. **Read** the target bucket from SSM:
   `/{site}/cloudfront-assets/us-east-1/voice/bucket_name`.
3. **Sync** to S3:
   - `aws s3 sync dist/assets → s3://$BUCKET/assets` with
     `cache-control: public,max-age=31536000,immutable` (hashed → safe to long-cache).
   - `aws s3 cp dist/index.html → s3://$BUCKET/index.html` with `cache-control: no-cache`.
   - Other top-level static files (favicon, etc.) synced accordingly.
4. **Invalidate** CloudFront: find the distribution by alias, `create-invalidation --paths
   "/index.html" "/"` (hashed assets need no invalidation; only the no-cache shell).

**ECS `StaticFiles` mount:** retained as a harmless backstop (and for local dev), but it is no
longer the production serving path once DNS points at CloudFront. Removing it is optional and
deferred.

## Cutover plan (executed later, NOT during a live demo window)

Ordered so the live site never breaks:

1. Apply `region/us-east-1/certs` (add voice SAN) — DNS-validated, no user impact.
2. Apply `region/us-east-1/cloudfront` (asset bucket + SSM). No user impact.
3. Run the build→S3 sync so the bucket is populated **before** any traffic is pointed at it.
4. Apply `global/cloudfront` (distribution). It comes up serving the populated bucket + ALB
   origin; still not referenced by DNS. Smoke-test via the distribution domain name.
5. **DNS cutover:** repoint `voice.klankermaker.ai` A-alias from ALB → CloudFront. This is the
   only user-visible step; instantly reversible by pointing the alias back at the ALB.
6. Verify: SPA loads, `/api/offer` handshake succeeds through CloudFront, a full voice session
   works (media direct UDP), deep links resolve via the custom-error-response.

**Rollback:** repoint the Route53 alias back to the ALB. The ECS `StaticFiles` mount still
serves the SPA, so rollback is a single DNS change with no redeploy.

## Testing

- `terraform validate` / `terragrunt hclfmt` on the new modules and units.
- `terragrunt plan` on each new unit (asset bucket, distribution) reviewed by human before apply.
- Post-apply smoke: `curl` the distribution domain for `index.html` + a hashed asset (S3
  origin) and `/health` (ALB origin); confirm `/api/offer` returns a typed rejection (401
  without token) proving the ALB behavior forwards `Authorization`.
- End-to-end: a real browser voice session through the CloudFront-fronted domain.

## Out of scope (YAGNI / future)

- Per-region `/use1/` path prefix and multi-region live voice stacks (cac1/apse1) — pre-wired
  as mock buckets only.
- `s3-uploads` / `cloudfront_access` upload buckets (the transcript-ledger use case) — separate
  work.
- Removing the ECS `StaticFiles` mount.
