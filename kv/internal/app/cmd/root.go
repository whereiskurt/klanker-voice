// Package cmd provides the Cobra command tree for the kv CLI — the
// klanker-voice operator tool for managing access codes and tiers directly
// against the kmv-auth-electro DynamoDB table (sibling to klanker-maker's
// km, mirroring its NewRootCmd/Execute structure).
package cmd

import (
	"context"
	"fmt"
	"log"
	"os"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/spf13/cobra"
)

// defaultTable matches apps/auth/webapp/src/entities/client.ts's
// ELECTRO_TABLE default so kv and the webapp talk to the same table without
// extra configuration in the common case.
const defaultTable = "kmv-auth-electro"

// defaultUsageTable matches apps/auth/webapp/src/entities/usage.ts's
// VOICE_USAGE_TABLE default / apps/voice/src/klanker_voice/quota.py's
// DEFAULT_USAGE_TABLE — the voice service's own table (kmv-voice-usage),
// distinct from the auth service's kmv-auth-electro table above. `kv usage`
// and `kv killswitch` target this table, not --table.
const defaultUsageTable = "kmv-voice-usage"

// Config carries the flags/env shared by every kv subcommand: which table to
// operate on, and (for local dev / tests against dynamodb-local) an optional
// endpoint override.
type Config struct {
	Table       string
	UsageTable  string
	EndpointURL string
	Region      string
	LogLevel    string
}

// DynamoClient builds an aws-sdk-go-v2 DynamoDB client from the Config,
// honoring EndpointURL for dynamodb-local (used by roundtrip_test.go and
// local operator testing) and Region otherwise deferring to the ambient AWS
// config/credential chain.
func (c *Config) DynamoClient(ctx context.Context) (*dynamodb.Client, error) {
	opts := []func(*awsconfig.LoadOptions) error{}
	if c.Region != "" {
		opts = append(opts, awsconfig.WithRegion(c.Region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if c.EndpointURL != "" {
			o.BaseEndpoint = &c.EndpointURL
		}
	}), nil
}

func tableFromEnv() string {
	if v := os.Getenv("AUTH_ELECTRO_DBNAME"); v != "" {
		return v
	}
	return defaultTable
}

// usageTableFromEnv resolves the voice usage table name from KMV_USAGE_TABLE
// (matching apps/voice/src/klanker_voice/quota.py's USAGE_TABLE_ENV_VAR),
// falling back to defaultUsageTable.
func usageTableFromEnv() string {
	if v := os.Getenv("KMV_USAGE_TABLE"); v != "" {
		return v
	}
	return defaultUsageTable
}

// NewRootCmd creates the root "kv" command with global flags and the code/
// tier subcommand trees attached.
func NewRootCmd() *cobra.Command {
	cfg := &Config{}

	root := &cobra.Command{
		Use:          "kv",
		Short:        "kv — klanker-voice operator CLI for access codes and tiers",
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVar(&cfg.LogLevel, "log-level", "info",
		"Log verbosity level (debug, info, warn, error)")
	root.PersistentFlags().StringVar(&cfg.Table, "table", tableFromEnv(),
		"DynamoDB table name (default from AUTH_ELECTRO_DBNAME env)")
	root.PersistentFlags().StringVar(&cfg.UsageTable, "usage-table", usageTableFromEnv(),
		"Voice usage DynamoDB table name, for usage/killswitch (default from KMV_USAGE_TABLE env)")
	root.PersistentFlags().StringVar(&cfg.EndpointURL, "endpoint-url", os.Getenv("AWS_ENDPOINT_URL_DYNAMODB"),
		"Override DynamoDB endpoint (for dynamodb-local dev/testing)")
	root.PersistentFlags().StringVar(&cfg.Region, "region", os.Getenv("AWS_REGION"),
		"AWS region (defaults to ambient AWS config/env)")

	root.AddCommand(NewCodeCmd(cfg))
	root.AddCommand(NewTierCmd(cfg))
	root.AddCommand(NewSmokeCmd(cfg))
	root.AddCommand(NewUsageCmd(cfg))

	return root
}

// Execute builds the command tree and runs the CLI, exiting with code 1 on
// any error. This is the one place os.Exit is called, keeping it outside
// RunE so any defers registered along the way have already run.
func Execute() {
	root := NewRootCmd()
	if err := root.Execute(); err != nil {
		log.SetFlags(0)
		log.Println("error:", err)
		os.Exit(1)
	}
}
