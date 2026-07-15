package studio

import (
	"reflect"
	"testing"
)

// diffFixtureLiveView returns a fully-populated live ConfigView exercising
// every surface a changeset covers — every table-driven case below mutates
// either its returned SOPDoc projection (via diffFixtureSOPDoc) or a fresh
// copy of this view, so each case is isolated to the one surface/field it
// claims to test.
func diffFixtureLiveView() ConfigView {
	return ConfigView{
		Rules: []Rule{
			{
				ID:      "greenhouse",
				Who:     WhoSpec{Type: "known", Numbers: []string{"+15555550100"}},
				Secret:  SecretSpec{Mode: "passphrase", Ref: telephonyAccessPinParam},
				Unlocks: []Unlock{{Phrase: "resume", Add: []string{"resume-pack"}}},
				Grant:   GrantSpec{Minutes: 30, PeriodMin: 60, Concurrency: 4, TierID: "recruiting-30"},
			},
			{
				ID:      "walkin",
				Who:     WhoSpec{Type: "any", Numbers: []string{}},
				Secret:  SecretSpec{Mode: "passphrase", Ref: telephonyAccessPinParam},
				Unlocks: []Unlock{{Phrase: "resume", Add: []string{"resume-pack"}}},
				Grant:   GrantSpec{Minutes: 3, PeriodMin: 10, Concurrency: 4, TierID: "public-3"},
			},
		},
		Knowledge: []KnowledgePack{
			{ID: "resume-pack", SpokenName: "Kurt's resume", Pack: "resume.md", Sources: []KnowledgeSource{}},
		},
		InboundDIDs: []InboundDID{
			{Did: "+16135551234", Label: "Ottawa", Region: "ON", DefaultRule: "greenhouse", Greeting: "Hi there"},
		},
	}
}

// diffFixtureSOPDoc returns the SOPDoc a fresh, unmodified
// diffFixtureLiveView() projects to — the "matches live" baseline every
// case starts from and mutates one surface of.
func diffFixtureSOPDoc() SOPDoc {
	doc := ToSOPDoc(diffFixtureLiveView())
	doc.Name = "conference-2026"
	doc.CreatedAt = "2026-07-15T10:00:00Z"
	return doc
}

func TestDiffChangeset_UnchangedIsEmpty(t *testing.T) {
	got := DiffChangeset(diffFixtureSOPDoc(), diffFixtureLiveView())
	if len(got) != 0 {
		t.Fatalf("expected an empty changeset for an unchanged SOP/live pair, got %d entries: %#v", len(got), got)
	}
}

func TestDiffChangeset(t *testing.T) {
	tests := []struct {
		name string
		doc  func() SOPDoc
		live func() ConfigView
		want []ChangesetEntry
	}{
		{
			name: "added tier",
			doc: func() SOPDoc {
				d := diffFixtureSOPDoc()
				d.Tiers = append(d.Tiers, SOPTier{TierID: "vip-60", SessionMaxSeconds: 3600, PeriodMaxSeconds: 7200, MaxConcurrent: 2})
				return d
			},
			live: diffFixtureLiveView,
			want: []ChangesetEntry{
				{Surface: "tier", Kind: "added", Key: "vip-60"},
			},
		},
		{
			name: "changed tier field",
			doc: func() SOPDoc {
				d := diffFixtureSOPDoc()
				d.Tiers[0].SessionMaxSeconds = 1200
				return d
			},
			live: diffFixtureLiveView,
			want: []ChangesetEntry{
				{Surface: "tier", Kind: "changed", Key: "recruiting-30", Field: "sessionMaxSeconds", From: int64(1800), To: int64(1200)},
			},
		},
		{
			name: "removed live-only tier",
			doc: func() SOPDoc {
				d := diffFixtureSOPDoc()
				d.Tiers = d.Tiers[:1] // drop public-3; rules still reference it, isolating this to the tier surface
				return d
			},
			live: diffFixtureLiveView,
			want: []ChangesetEntry{
				{Surface: "tier", Kind: "removed", Key: "public-3"},
			},
		},
		{
			name: "added rule",
			doc: func() SOPDoc {
				d := diffFixtureSOPDoc()
				d.Rules = append(d.Rules, SOPRule{
					Code:   "vip",
					Who:    WhoSpec{Type: "any", Numbers: []string{}},
					TierID: "recruiting-30",
				})
				return d
			},
			live: diffFixtureLiveView,
			want: []ChangesetEntry{
				{Surface: "rule", Kind: "added", Key: "vip"},
			},
		},
		{
			name: "changed rule tier (destructive: moved to block/no-access)",
			doc: func() SOPDoc {
				d := diffFixtureSOPDoc()
				d.Rules[1].TierID = blockTierID
				return d
			},
			live: diffFixtureLiveView,
			want: []ChangesetEntry{
				{Surface: "rule", Kind: "changed", Key: "walkin", Field: "tierId", From: "public-3", To: blockTierID},
			},
		},
		{
			name: "added did",
			doc: func() SOPDoc {
				d := diffFixtureSOPDoc()
				d.Dids = append(d.Dids, DIDMeta{Did: "+17145550100", Label: "Anaheim", Region: "CA", DefaultRule: "walkin", Greeting: "Howdy"})
				return d
			},
			live: diffFixtureLiveView,
			want: []ChangesetEntry{
				{Surface: "did", Kind: "added", Key: "+17145550100"},
			},
		},
		{
			name: "added unlock keyword",
			doc: func() SOPDoc {
				d := diffFixtureSOPDoc()
				d.Unlocks = append(d.Unlocks, Unlock{Phrase: "defcon", Add: []string{"defcon-pack"}})
				return d
			},
			live: diffFixtureLiveView,
			want: []ChangesetEntry{
				{Surface: "unlock", Kind: "added", Key: "defcon"},
			},
		},
		{
			name: "added knowledge source",
			doc: func() SOPDoc {
				d := diffFixtureSOPDoc()
				d.Knowledge[0].Sources = append(d.Knowledge[0].Sources, KnowledgeSource{Path: "docs/resume.pdf", Kind: "doc", Public: true})
				return d
			},
			live: diffFixtureLiveView,
			want: []ChangesetEntry{
				{Surface: "knowledge", Kind: "changed", Key: "resume-pack", Field: "sources",
					From: []KnowledgeSource{},
					To:   []KnowledgeSource{{Path: "docs/resume.pdf", Kind: "doc", Public: true}}},
			},
		},
		{
			name: "changed gate mode",
			doc: func() SOPDoc {
				d := diffFixtureSOPDoc()
				d.Gate.Mode = "dtmf"
				return d
			},
			live: diffFixtureLiveView,
			want: []ChangesetEntry{
				{Surface: "gate", Kind: "changed", Key: "gate", Field: "mode", From: "passphrase", To: "dtmf"},
			},
		},
		{
			name: "reordered rule list",
			doc: func() SOPDoc {
				d := diffFixtureSOPDoc()
				d.Order = []string{"walkin", "greenhouse"}
				return d
			},
			live: diffFixtureLiveView,
			want: []ChangesetEntry{
				{Surface: "order", Kind: "changed", Key: "order",
					From: []string{"greenhouse", "walkin"}, To: []string{"walkin", "greenhouse"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DiffChangeset(tt.doc(), tt.live())
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("DiffChangeset mismatch:\n  got:  %#v\n  want: %#v", got, tt.want)
			}
		})
	}
}
