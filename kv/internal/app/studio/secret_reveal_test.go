package studio

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// fakeSSMRevealAPI is an in-memory SSMRevealAPI: it never dials AWS, records
// every call it receives (for the allow-list zero-call assertion), and
// returns whatever value/err was configured.
type fakeSSMRevealAPI struct {
	value   string
	err     error
	calls   int
	lastReq *ssm.GetParameterInput
}

func (f *fakeSSMRevealAPI) GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.calls++
	f.lastReq = params
	if f.err != nil {
		return nil, f.err
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{Value: &f.value},
	}, nil
}

func TestRevealSecret_AllowedName_ReturnsDecryptedValue(t *testing.T) {
	fake := &fakeSSMRevealAPI{value: "sentinel-decrypted-value"}

	got, err := RevealSecret(context.Background(), fake, telephonyAccessPinParam)
	if err != nil {
		t.Fatalf("RevealSecret() error = %v", err)
	}
	if got.Status != "set" || got.Value != "sentinel-decrypted-value" {
		t.Errorf("RevealSecret() = %+v, want Status=set Value=sentinel-decrypted-value", got)
	}
	if fake.calls != 1 {
		t.Errorf("fake.calls = %d, want 1", fake.calls)
	}
	if fake.lastReq.WithDecryption == nil || !*fake.lastReq.WithDecryption {
		t.Error("GetParameterInput.WithDecryption not set true")
	}
}

// TestReveal_AllowList asserts a non-allowlisted param name (here, one of
// the auth app's JWT signing secrets living in the SAME /kmv/secrets/use1/*
// prefix) is rejected AND that rejection happens before any AWS call is
// made — the fake records zero GetParameter calls (T-17-01).
func TestReveal_AllowList(t *testing.T) {
	fake := &fakeSSMRevealAPI{value: "should-never-be-returned"}

	_, err := RevealSecret(context.Background(), fake, "/kmv/secrets/use1/jwt/signing_key")
	if err == nil {
		t.Fatal("RevealSecret() error = nil, want a rejection for a non-allowlisted name")
	}
	if !errors.Is(err, errSecretNotAllowed) {
		t.Errorf("RevealSecret() error = %v, want errSecretNotAllowed", err)
	}
	if fake.calls != 0 {
		t.Errorf("fake.calls = %d, want 0 (allow-list must be checked before any AWS call)", fake.calls)
	}
}

func TestRevealSecret_ParameterNotFound_YieldsNotSetStatus(t *testing.T) {
	fake := &fakeSSMRevealAPI{err: &ssmtypes.ParameterNotFound{}}

	got, err := RevealSecret(context.Background(), fake, telephonyPassphraseWordsParam)
	if err != nil {
		t.Fatalf("RevealSecret() error = %v, want nil (not-set is not a Go error)", err)
	}
	if got.Status != "not set" || got.Value != "" {
		t.Errorf("RevealSecret() = %+v, want Status=\"not set\" Value=\"\"", got)
	}
}

func TestRevealSecret_OtherAWSError_YieldsShortNonSensitiveNote(t *testing.T) {
	fake := &fakeSSMRevealAPI{err: errors.New("AccessDenied: user arn:aws:iam::123456789012:user/ops is not authorized to perform ssm:GetParameter on resource arn:aws:ssm:us-east-1:123456789012:parameter/kmv/secrets/use1/telephony/access_pin")}

	got, err := RevealSecret(context.Background(), fake, telephonyAccessPinParam)
	if err != nil {
		t.Fatalf("RevealSecret() error = %v, want nil (classified into Status, not a Go error)", err)
	}
	if got.Value != "" {
		t.Errorf("RevealSecret().Value = %q, want empty on error", got.Value)
	}
	if strings.Contains(got.Status, "arn:aws:iam") || strings.Contains(got.Status, "123456789012") {
		t.Errorf("RevealSecret().Status = %q leaks raw AWS error internals, want a short non-sensitive note", got.Status)
	}
}

func TestReadSecretRefs_IncludesEndpointAuthToken(t *testing.T) {
	refs := ReadSecretRefs("either")
	found := false
	for _, r := range refs {
		if r.Name == telephonyEndpointAuthTokenParam {
			found = true
		}
	}
	if !found {
		t.Errorf("ReadSecretRefs() = %+v, want a ref for %s", refs, telephonyEndpointAuthTokenParam)
	}
	if len(refs) != 3 {
		t.Errorf("len(ReadSecretRefs()) = %d, want 3", len(refs))
	}
}

// TestReveal_NeverPersisted reveals a sentinel value inside a temp git
// worktree, then walks the whole tree AND `git log -p` and asserts the
// sentinel string appears nowhere on disk or in git — RevealSecret's only
// output is its function return value; it must never write a file, a log
// line, or a commit (SEC-01 / T-17-02).
func TestReveal_NeverPersisted(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	tmp := t.TempDir()
	runGit(t, tmp, "init", "-q")
	runGit(t, tmp, "config", "user.email", "test@example.com")
	runGit(t, tmp, "config", "user.name", "test")
	// A tracked file + commit BEFORE the reveal, so `git log -p` has
	// something to walk and this test isn't vacuously trivial.
	seedPath := filepath.Join(tmp, "README.md")
	if err := os.WriteFile(seedPath, []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGit(t, tmp, "add", "README.md")
	runGit(t, tmp, "commit", "-q", "-m", "seed")

	const sentinel = "kv-reveal-sentinel-9f3a2b7c-never-persisted"
	fake := &fakeSSMRevealAPI{value: sentinel}

	got, err := RevealSecret(context.Background(), fake, telephonyAccessPinParam)
	if err != nil {
		t.Fatalf("RevealSecret() error = %v", err)
	}
	if got.Value != sentinel {
		t.Fatalf("RevealSecret().Value = %q, want %q (sanity check the fake wired correctly)", got.Value, sentinel)
	}

	// Walk every file in the worktree (including any hidden/untracked
	// files RevealSecret might have introduced) and assert the sentinel
	// is absent.
	err = filepath.WalkDir(tmp, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil // best-effort; unreadable files (e.g. git internals) aren't a leak vector
		}
		if strings.Contains(string(data), sentinel) {
			t.Errorf("sentinel found on disk at %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk temp worktree: %v", err)
	}

	// git log -p over the full history (there is only the "seed" commit —
	// RevealSecret must never have added a commit either).
	logOut := runGitOutput(t, tmp, "log", "-p", "--all")
	if strings.Contains(logOut, sentinel) {
		t.Error("sentinel found in `git log -p --all` output")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
