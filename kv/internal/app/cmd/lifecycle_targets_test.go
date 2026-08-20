package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeOutputReader is a canned-JSON TerraformOutputReader: outputs maps a
// unit directory to its raw output-JSON fixture (the same envelope shape
// OutputJSON's real implementation returns), and errs maps a unit
// directory to an error OutputJSON should return instead. No method here
// shells out to anything.
type fakeOutputReader struct {
	outputs map[string][]byte
	errs    map[string]error
}

func (f *fakeOutputReader) OutputJSON(_ context.Context, unitDir string) ([]byte, error) {
	if err, ok := f.errs[unitDir]; ok {
		return nil, err
	}
	b, ok := f.outputs[unitDir]
	if !ok {
		return nil, errors.New("fakeOutputReader: no fixture registered for unit " + unitDir)
	}
	return b, nil
}

const fixtureDynamoDB = `{
  "tables": {
    "value": {
      "kmv-auth-electro": {"table_name": "kmv-auth-electro"},
      "kmv-auth-authjs": {"table_name": "kmv-auth-authjs"},
      "kmv-voice-usage": {"table_name": "kmv-voice-usage"}
    }
  }
}`

const fixtureLedger = `{"bucket_name": {"value": "kmv-ledger-abc123"}}`

const fixtureLedgerAlt = `{"bucket_name": {"value": "kmv-ledger-xyz789"}}`

const fixtureECSService = `{
  "services": {
    "value": {
      "voice": {"service_name": "voice"},
      "auth": {"service_name": "auth"},
      "telephony-edge": {"service_name": "telephony-edge"}
    }
  },
  "target_groups": {
    "value": {
      "voice": {"arn": "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/voice/aaa"},
      "auth": {"arn": "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/auth/bbb"}
    }
  }
}`

const fixtureECSCluster = `{
  "clusters": {
    "value": {
      "app": {"cluster_name": "app-kmv"}
    }
  }
}`

const fixtureNetwork = `{"nat_eip_public_ip": {"value": "203.0.113.42"}}`

func fullFixtureReader() *fakeOutputReader {
	return &fakeOutputReader{
		outputs: map[string][]byte{
			tfUnitDynamoDB:   []byte(fixtureDynamoDB),
			tfUnitLedger:     []byte(fixtureLedger),
			tfUnitECSService: []byte(fixtureECSService),
			tfUnitECSCluster: []byte(fixtureECSCluster),
			tfUnitNetwork:    []byte(fixtureNetwork),
		},
	}
}

func TestResolveLiveTargets(t *testing.T) {
	ctx := context.Background()

	// Test 1: given canned output-JSON fixtures for all five units,
	// ResolveLiveTargets returns the three physical table names
	// keyed by logical name, the ledger bucket, the cluster name, three
	// service names, the target-group ARNs, and the NAT EIP.
	t.Run("AllFiveUnits", func(t *testing.T) {
		targets, err := ResolveLiveTargets(ctx, fullFixtureReader())
		if err != nil {
			t.Fatalf("ResolveLiveTargets() error = %v", err)
		}
		wantTables := map[string]string{
			"auth-electro": "kmv-auth-electro",
			"auth-authjs":  "kmv-auth-authjs",
			"voice-usage":  "kmv-voice-usage",
		}
		for logical, want := range wantTables {
			if got := targets.TableNames[logical]; got != want {
				t.Errorf("TableNames[%q] = %q, want %q", logical, got, want)
			}
		}
		if targets.LedgerBucket != "kmv-ledger-abc123" {
			t.Errorf("LedgerBucket = %q, want kmv-ledger-abc123", targets.LedgerBucket)
		}
		if targets.ClusterName != "app-kmv" {
			t.Errorf("ClusterName = %q, want app-kmv", targets.ClusterName)
		}
		if len(targets.ServiceNames) != 3 {
			t.Errorf("len(ServiceNames) = %d, want 3", len(targets.ServiceNames))
		}
		if targets.NATEIP != "203.0.113.42" {
			t.Errorf("NATEIP = %q, want 203.0.113.42", targets.NATEIP)
		}
		if len(targets.TargetGroupARNs) != 2 {
			t.Errorf("len(TargetGroupARNs) = %d, want 2", len(targets.TargetGroupARNs))
		}
		if targets.TargetGroupARNs["voice"] == "" {
			t.Error(`TargetGroupARNs["voice"] is empty`)
		}
	})

	// Test 2: a unit whose output JSON is missing the expected key yields
	// a non-nil error naming the unit directory and the missing output.
	t.Run("MissingOutputKey", func(t *testing.T) {
		reader := fullFixtureReader()
		reader.outputs[tfUnitDynamoDB] = []byte(`{}`)

		_, err := ResolveLiveTargets(ctx, reader)
		if err == nil {
			t.Fatal("ResolveLiveTargets() error = nil, want non-nil for a unit missing its expected output key")
		}
		if !strings.Contains(err.Error(), tfUnitDynamoDB) {
			t.Errorf("error %q does not name the unit directory %q", err.Error(), tfUnitDynamoDB)
		}
		if !strings.Contains(err.Error(), "tables") {
			t.Errorf("error %q does not name the missing output %q", err.Error(), "tables")
		}
	})

	// Test 3: a reader that errors for one unit yields a non-nil error
	// that wraps the reader error and names the unit directory.
	t.Run("ReaderError", func(t *testing.T) {
		reader := fullFixtureReader()
		reader.errs = map[string]error{tfUnitLedger: errors.New("boom: no such unit")}

		_, err := ResolveLiveTargets(ctx, reader)
		if err == nil {
			t.Fatal("ResolveLiveTargets() error = nil, want non-nil when a unit's reader errors")
		}
		if !strings.Contains(err.Error(), "boom: no such unit") {
			t.Errorf("error %q does not wrap the underlying reader error", err.Error())
		}
		if !strings.Contains(err.Error(), tfUnitLedger) {
			t.Errorf("error %q does not name the unit directory %q", err.Error(), tfUnitLedger)
		}
	})

	// Test 4: ledger bucket resolution reads only the ledger unit's
	// output — a fake reader that fails when asked for any other unit
	// still resolves the ledger bucket via the ledger-only entry point.
	t.Run("LedgerOnlyEntryPoint", func(t *testing.T) {
		reader := &fakeOutputReader{
			outputs: map[string][]byte{tfUnitLedger: []byte(fixtureLedger)},
			errs: map[string]error{
				tfUnitDynamoDB:   errors.New("must not be called"),
				tfUnitECSService: errors.New("must not be called"),
				tfUnitECSCluster: errors.New("must not be called"),
				tfUnitNetwork:    errors.New("must not be called"),
			},
		}
		bucket, err := ResolveLedgerBucket(ctx, reader)
		if err != nil {
			t.Fatalf("ResolveLedgerBucket() error = %v", err)
		}
		if bucket != "kmv-ledger-abc123" {
			t.Errorf("bucket = %q, want kmv-ledger-abc123", bucket)
		}
	})

	// Test 5: ResolveLiveTargets returns a distinct bucket name when the
	// canned ledger output changes, proving nothing is cached or
	// hardcoded (the random_id recreate case).
	t.Run("DistinctBucketOnChange", func(t *testing.T) {
		first := fullFixtureReader()
		firstTargets, err := ResolveLiveTargets(ctx, first)
		if err != nil {
			t.Fatalf("ResolveLiveTargets() (first) error = %v", err)
		}

		second := fullFixtureReader()
		second.outputs[tfUnitLedger] = []byte(fixtureLedgerAlt)
		secondTargets, err := ResolveLiveTargets(ctx, second)
		if err != nil {
			t.Fatalf("ResolveLiveTargets() (second) error = %v", err)
		}

		if firstTargets.LedgerBucket == secondTargets.LedgerBucket {
			t.Errorf("LedgerBucket did not change: both resolved to %q", firstTargets.LedgerBucket)
		}
		if secondTargets.LedgerBucket != "kmv-ledger-xyz789" {
			t.Errorf("second LedgerBucket = %q, want kmv-ledger-xyz789", secondTargets.LedgerBucket)
		}
	})
}
