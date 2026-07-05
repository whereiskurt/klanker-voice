package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewTierCmd builds the "kv tier" parent command with define/list
// subcommands (KV-02). RunE bodies are stubbed here (Task 1 — command tree
// scaffolding); Task 2 wires them to real DynamoDB reads/writes against the
// electro table.
func NewTierCmd(cfg *Config) *cobra.Command {
	tierCmd := &cobra.Command{
		Use:   "tier",
		Short: "Manage tiers (session/period/concurrency limits)",
	}

	var (
		group         string
		sessionMax    int64
		periodMax     int64
		maxConcurrent int64
	)

	define := &cobra.Command{
		Use:   "define <tierId>",
		Short: "Define (create or replace) a tier",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented yet")
		},
	}
	define.Flags().StringVar(&group, "group", "", "optional group label")
	define.Flags().Int64Var(&sessionMax, "session-max", 0, "max seconds per session (required)")
	define.Flags().Int64Var(&periodMax, "period-max", 0, "max seconds per rolling period (required)")
	define.Flags().Int64Var(&maxConcurrent, "max-concurrent", 1, "max concurrent sessions")
	tierCmd.AddCommand(define)

	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List tiers",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented yet")
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	tierCmd.AddCommand(list)

	return tierCmd
}
