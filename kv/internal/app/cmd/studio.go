package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-voice/kv/internal/app/studio"
)

// defaultStudioPort matches the design spec's default local port for the
// operator console.
const defaultStudioPort = 7420

// didRouterFunc adapts a plain function to studio.DIDRouterAPI (the
// http.HandlerFunc pattern) — lets NewStudioCmd wire a closure over a
// *voipmsClient directly, without a named struct.
type didRouterFunc func(ctx context.Context, did string) error

func (f didRouterFunc) RouteDID(ctx context.Context, did string) error { return f(ctx, did) }

// buildVoipmsInjections resolves VoIP.ms API credentials via resolveCreds
// (production: cfg.resolveVoipmsCreds — env-first, SSM fallback; tests
// inject a canned func, mirroring TestResolveVoipmsCreds_*'s ssmFactory
// injection style so this stays testable with no real network call) and, on
// success, returns a DIDRouterAPI (routes an already-owned DID via
// routeVoipmsDidToPbx — DID-01 "add"; never number provisioning,
// 16-RESEARCH.md Pitfall 3) and an InboundDIDs lister (ListInboundDIDs,
// mapped into studio.InboundDID). Both are package-cmd closures injected
// into studio.ServerOptions — package studio never imports package cmd
// (16-RESEARCH.md Pitfall 4).
//
// On any credential-resolution failure both return values are nil,
// mirroring cmd/telephony.go's readInboundDIDs "never block the rest of the
// console" degradation: `kv studio` still starts and serves every other
// endpoint, and the DID handlers degrade gracefully (studio's
// mergedInboundDIDs / POST /api/dids' routingNote).
func buildVoipmsInjections(ctx context.Context, resolveCreds func(context.Context) (voipmsCreds, error)) (studio.DIDRouterAPI, func(context.Context) ([]studio.InboundDID, error)) {
	creds, err := resolveCreds(ctx)
	if err != nil {
		return nil, nil
	}
	vc := newVoipmsClient(creds)

	router := didRouterFunc(func(ctx context.Context, did string) error {
		return routeVoipmsDidToPbx(ctx, vc, did, "")
	})

	lister := func(ctx context.Context) ([]studio.InboundDID, error) {
		records, err := ListInboundDIDs(ctx, vc)
		if err != nil {
			return nil, err
		}
		out := make([]studio.InboundDID, 0, len(records))
		for _, rec := range records {
			out = append(out, studio.InboundDID{
				Did:     rec.DID,
				Label:   rec.Description,
				Region:  rec.POP,
				Routing: rec.Routing,
			})
		}
		return out, nil
	}

	return router, lister
}

// NewStudioCmd builds the "kv studio" command: a local, loopback-only HTTP
// server (STUD-01) serving the embedded kv studio web console over the
// operator's real, live voice-routing configuration — access codes, tiers,
// DIDs, knowledge packs, and gate-secret references assembled from
// DynamoDB, the repo config files, and SSM param names. It reuses the same
// --profile/--region/--table global flags and AWS-client construction path
// as `kv code`/`kv tier` — no separate credential story (STUD-03). Edits
// (rules, tier reassignment, gate mode, spoken unlocks, rule order, DIDs)
// write to those same stores; press Ctrl-C to stop.
func NewStudioCmd(cfg *Config) *cobra.Command {
	var (
		port   int
		noOpen bool
	)

	studioCmd := &cobra.Command{
		Use:   "studio",
		Short: "Start the local kv studio operator console (127.0.0.1 only)",
		Long: "kv studio starts a local, loopback-only HTTP server (never 0.0.0.0 --\n" +
			"there is no --host flag) and opens the browser to a self-contained,\n" +
			"embedded web console showing the operator's real, live voice-routing\n" +
			"configuration -- access codes, tiers, DIDs, knowledge packs, and gate\n" +
			"secret references -- assembled from DynamoDB, the repo config files,\n" +
			"and SSM param names, using the same --profile/--region as `kv code`/\n" +
			"`kv tier`. Edits write to those same stores; secret values are\n" +
			"revealed/rotated on demand only. Press Ctrl-C to stop.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()

			dynamoClient, err := cfg.DynamoClient(ctx)
			if err != nil {
				return err
			}
			// Constructed now (same client-building path as every other kv
			// command) so a bad --profile/--region fails fast. Phase 17
			// injects this into ServerOptions.SSM below, powering
			// POST /api/secret/reveal and POST /api/secret/rotate
			// (secret_reveal.go/secret_rotate.go) — the only places a
			// secret VALUE is ever read or written (spec §7).
			ssmClient, err := cfg.SSMClient(ctx)
			if err != nil {
				return err
			}

			root, err := repoRoot()
			if err != nil {
				return err
			}

			// DID-01/02: resolve the VoIP.ms-backed router/lister
			// best-effort. A resolution failure (creds unavailable) yields
			// nil/nil — studio degrades gracefully rather than failing
			// `kv studio` to start (mirrors readInboundDIDs' philosophy).
			didRouter, inboundDIDs := buildVoipmsInjections(ctx, cfg.resolveVoipmsCreds)

			// KNOW-03: the exact same subprocess `kv knowledge refresh`
			// shells (knowledge_rebuild.go's Rebuild), rooted at the same
			// repo root every other repo-file read/write in this command
			// uses. Without this injection POST /api/knowledge/rebuild
			// degrades to a structured 500 (errKnowledgeRebuildNotConfigured)
			// rather than ever running.
			knowledgeRebuild := studio.NewKnowledgeRebuildTrigger(root)

			srv := studio.NewServer(studio.ServerOptions{
				Dynamo:           dynamoClient,
				Table:            cfg.Table,
				Repo:             studio.RepoFiles{Root: root},
				Writer:           dynamoClient,
				DIDRouter:        didRouter,
				InboundDIDs:      inboundDIDs,
				SSM:              ssmClient,
				KnowledgeRebuild: knowledgeRebuild,
				Meta: studio.Meta{
					Region:  cfg.Region,
					Profile: cfg.Profile,
					Table:   cfg.Table,
				},
				Port: fmt.Sprintf("%d", port),
			})

			ln, err := srv.Listen()
			if err != nil {
				return err
			}

			url := fmt.Sprintf("http://%s", ln.Addr().String())
			fmt.Fprintf(c.OutOrStdout(), "kv studio serving %s\n", url)

			if !noOpen {
				if err := studio.OpenBrowser(url); err != nil {
					fmt.Fprintf(c.ErrOrStderr(), "kv studio: could not open browser automatically (%v) -- open %s manually\n", err, url)
				}
			}

			sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
			defer stop()

			return srv.Serve(sigCtx, ln)
		},
	}

	studioCmd.Flags().IntVar(&port, "port", defaultStudioPort, "TCP port to bind the local studio console to (127.0.0.1 only)")
	studioCmd.Flags().BoolVar(&noOpen, "no-open", false, "do not automatically open the browser")

	return studioCmd
}
