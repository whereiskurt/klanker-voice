// Package cmd -- the `hibernated` flag accessors. The scanning, comment
// preservation, decoy rejection and ambiguity refusal all live in the
// generalized engine in lifecycle_pauseflag.go; this file exists only so
// the hibernate call sites read as clearly as the pause ones do.
//
// `hibernated` is the deeper of the two operator switches in site.hcl. It
// implies `paused` (2026-09-10 spec D-02): when it is true the service list
// goes empty, so the paused overrides become moot. kv hibernate sets only
// this flag and never touches `paused`, so an operator who hibernates from
// a paused stack and later wakes lands back in the paused state they
// started from.
package cmd

// ReadHibernatedFlagFile reports the `hibernated` flag's value in site.hcl.
func ReadHibernatedFlagFile(repoRoot string) (bool, error) {
	return ReadLifecycleFlagFile(repoRoot, HibernatedFlagName)
}

// SetHibernatedFlagFile flips the `hibernated` flag in site.hcl to want.
func SetHibernatedFlagFile(repoRoot string, want bool) (changed bool, err error) {
	return SetLifecycleFlagFile(repoRoot, HibernatedFlagName, want)
}
