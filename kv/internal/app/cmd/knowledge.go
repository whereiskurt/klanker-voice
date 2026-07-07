package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// repoRoot resolves the klanker-voice repo root via `git rev-parse
// --show-toplevel`, run from the operator's current working directory. kv
// stays a thin dispatcher for the knowledge refresh (07-04) -- it never
// reimplements distillation/doc-gen/indexing in Go -- so all it needs is a
// reliable way to find apps/voice/scripts/refresh_knowledge.py regardless of
// which directory `kv` was invoked from.
func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("resolve repo root (git rev-parse --show-toplevel): %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// NewKnowledgeCmd builds the "kv knowledge" parent command with a "refresh"
// subcommand (07-04, D-07). `refresh` shells out to
// apps/voice/scripts/refresh_knowledge.py via `uv run python
// scripts/refresh_knowledge.py` -- the actual manifest-gated distill +
// doc-gen-seam + chunk/index-build + advisory-lint pipeline lives entirely
// in that Python script; this command only locates it and forwards flags,
// following tier.go's Use/Short/RunE cobra-subcommand shape.
//
// `make -C apps/voice knowledge` (Makefile) is the primary/lower-friction
// home for this workflow (criterion 3 -- "a script run"); this `kv`
// subcommand mirrors the sibling `km`/`kv` operator-CLI structure for
// operators who prefer a single tool.
func NewKnowledgeCmd(_ *Config) *cobra.Command {
	knowledgeCmd := &cobra.Command{
		Use:   "knowledge",
		Short: "Regenerate KPH's curated packs + retrieval indexes (D-07)",
	}

	var (
		dryRun      bool
		skipDistill bool
		force       bool
	)

	refresh := &cobra.Command{
		Use:   "refresh",
		Short: "Run the offline knowledge refresh (shells to apps/voice/scripts/refresh_knowledge.py)",
		Long: "Regenerates KPH's curated per-topic packs AND its local BM25/FTS5 retrieval\n" +
			"chunk files from the checked-in manifest (D-01), refusing any source not\n" +
			"flagged public:true (D-02). Runs the swappable doc-generation seam over\n" +
			"code-heavy sources (Amendment 3.D/5) and flags do-not-say findings for the\n" +
			"D-09 git-diff human review -- never blocking the write (Amendment 3.E).\n\n" +
			"This is a thin dispatcher: the actual work happens in\n" +
			"apps/voice/scripts/refresh_knowledge.py, invoked here via `uv run python\n" +
			"scripts/refresh_knowledge.py`. Offline and deliberate only (Amendment 3.G) --\n" +
			"never run this during a live session.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			root, err := repoRoot()
			if err != nil {
				return err
			}
			appDir := filepath.Join(root, "apps", "voice")
			scriptPath := filepath.Join(appDir, "scripts", "refresh_knowledge.py")
			if _, err := os.Stat(scriptPath); err != nil {
				return fmt.Errorf("refresh script not found at %s: %w", scriptPath, err)
			}

			pyArgs := []string{"run", "python", "scripts/refresh_knowledge.py"}
			if dryRun {
				pyArgs = append(pyArgs, "--dry-run")
			}
			if skipDistill {
				pyArgs = append(pyArgs, "--skip-distill")
			}
			if force {
				pyArgs = append(pyArgs, "--force")
			}

			runCmd := exec.CommandContext(c.Context(), "uv", pyArgs...)
			runCmd.Dir = appDir
			runCmd.Stdout = c.OutOrStdout()
			runCmd.Stderr = c.ErrOrStderr()
			runCmd.Stdin = c.InOrStdin()
			return runCmd.Run()
		},
	}
	refresh.Flags().BoolVar(&dryRun, "dry-run", false,
		"write into a temp dir instead of the tracked knowledge/ tree, skip the LLM distillation pass")
	refresh.Flags().BoolVar(&skipDistill, "skip-distill", false,
		"skip the curated-pack distillation LLM pass (chunk/index build + advisory lint only)")
	refresh.Flags().BoolVar(&force, "force", false,
		"overwrite a topic's chunk file even if the new corpus has fewer chunks than what's committed")
	knowledgeCmd.AddCommand(refresh)

	return knowledgeCmd
}
