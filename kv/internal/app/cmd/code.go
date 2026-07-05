package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewCodeCmd builds the "kv code" parent command with create/list/expire
// subcommands (KV-01). RunE bodies are stubbed here (Task 1 — command tree
// scaffolding); Task 2 wires them to real DynamoDB reads/writes against the
// electro table.
func NewCodeCmd(cfg *Config) *cobra.Command {
	codeCmd := &cobra.Command{
		Use:   "code",
		Short: "Manage access codes (create, list, expire)",
	}

	var (
		tier           string
		group          string
		expiresRFC3339 string
		maxRedemptions int
	)

	create := &cobra.Command{
		Use:   "create <code>",
		Short: "Create an access code mapped to a tier",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented yet")
		},
	}
	create.Flags().StringVar(&tier, "tier", "", "tier id this code grants (required)")
	create.Flags().StringVar(&group, "group", "", "optional group label")
	create.Flags().StringVar(&expiresRFC3339, "expires", "", "optional expiry, RFC3339 (e.g. 2026-12-31T00:00:00Z)")
	create.Flags().IntVar(&maxRedemptions, "max", 0, "optional max unique-user redemptions (unlimited if unset)")
	codeCmd.AddCommand(create)

	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List access codes",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented yet")
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	codeCmd.AddCommand(list)

	expire := &cobra.Command{
		Use:   "expire <code>",
		Short: "Soft-expire an access code (sets expiresAt = now)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented yet")
		},
	}
	codeCmd.AddCommand(expire)

	return codeCmd
}
