package cmd

import (
	"context"
	"errors"
	"testing"
)

// TestNewStudioCmd_FlagsAndDefaults asserts the "studio" command builds with
// --port (default 7420) and --no-open (default false) — no live server is
// started; this is wiring-only coverage (RunE binds a real loopback
// listener + AWS clients, exercised by the manual smoke, not this test).
func TestNewStudioCmd_FlagsAndDefaults(t *testing.T) {
	cmd := NewStudioCmd(&Config{})
	if cmd.Use != "studio" {
		t.Fatalf("Use = %q, want \"studio\"", cmd.Use)
	}

	portFlag := cmd.Flags().Lookup("port")
	if portFlag == nil {
		t.Fatal("--port flag not registered")
	}
	if portFlag.DefValue != "7420" {
		t.Errorf("--port default = %q, want \"7420\"", portFlag.DefValue)
	}

	noOpenFlag := cmd.Flags().Lookup("no-open")
	if noOpenFlag == nil {
		t.Fatal("--no-open flag not registered")
	}
	if noOpenFlag.DefValue != "false" {
		t.Errorf("--no-open default = %q, want \"false\"", noOpenFlag.DefValue)
	}
}

// TestNewRootCmd_RegistersStudio asserts "kv studio" is reachable from the
// root command tree.
func TestNewRootCmd_RegistersStudio(t *testing.T) {
	root := NewRootCmd()
	found, _, err := root.Find([]string{"studio"})
	if err != nil {
		t.Fatalf("root.Find([\"studio\"]) error: %v", err)
	}
	if found == nil || found.Name() != "studio" {
		t.Fatalf("root.Find([\"studio\"]) = %+v, want the studio subcommand", found)
	}
}

// --------------------------------------------------------------------------
// buildVoipmsInjections (Plan 16-04: DID-01/02's injected DIDRouterAPI +
// InboundDIDs lister). Tests inject a canned resolveCreds func so no real
// network/SSM call is ever made — mirrors TestResolveVoipmsCreds_*'s
// ssmFactory-injection style in voipms_test.go.

func TestStudioCmd_BuildVoipmsInjections_DegradesGracefullyOnCredsError(t *testing.T) {
	router, lister := buildVoipmsInjections(context.Background(), func(ctx context.Context) (voipmsCreds, error) {
		return voipmsCreds{}, errors.New("no VoIP.ms creds available")
	})
	if router != nil {
		t.Error("router is non-nil, want nil when credential resolution fails")
	}
	if lister != nil {
		t.Error("lister is non-nil, want nil when credential resolution fails")
	}
}

func TestStudioCmd_BuildVoipmsInjections_WiresRouterAndListerOnSuccess(t *testing.T) {
	router, lister := buildVoipmsInjections(context.Background(), func(ctx context.Context) (voipmsCreds, error) {
		return voipmsCreds{Username: "u", Password: "p"}, nil
	})
	if router == nil {
		t.Fatal("router = nil, want a non-nil DIDRouterAPI when credentials resolve")
	}
	if lister == nil {
		t.Fatal("lister = nil, want a non-nil InboundDIDs lister when credentials resolve")
	}
}
