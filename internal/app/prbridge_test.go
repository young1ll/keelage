package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/young1ll/keelage/internal/adapter/memory"
	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/core/harness"
	"github.com/young1ll/keelage/internal/port"
)

type fakeReporter struct {
	comments []string
	checks   []string // conclusion
}

func (f *fakeReporter) UpsertComment(_ context.Context, _ int64, _, _ string, _ int, marker, body string) error {
	if !strings.Contains(body, marker) {
		return context.Canceled
	}
	f.comments = append(f.comments, body)
	return nil
}

func (f *fakeReporter) Check(_ context.Context, _ int64, _, _, _, _, conclusion, _, _ string) error {
	f.checks = append(f.checks, conclusion)
	return nil
}

type installs map[int64]string

func (m installs) LinkInstallation(_ context.Context, id int64, org string) error {
	m[id] = org
	return nil
}
func (m installs) OrgForInstallation(_ context.Context, id int64) (string, error) {
	if o, ok := m[id]; ok {
		return o, nil
	}
	return "", port.ErrNotFound
}

func TestPRBridge_ImpactCommentAndReviewJudgment(t *testing.T) {
	ctx := context.Background()
	ledger := memory.NewLedger(nil)
	orgs := &Orgs{Open: func(context.Context, string) (port.OriginLedger, error) { return ledger, nil }, Codec: NewCodec(), Clock: memory.NewClock(t0)}
	rep := &fakeReporter{}
	inst := installs{}
	b := &PRBridge{Orgs: orgs, Reporter: rep, Installs: inst, Members: members{"acme/alice": true}, Clock: memory.NewClock(t0)}

	// the installation event binds installation 7 to the account's org
	if r, err := b.Handle(ctx, port.RepoEvent{Kind: "installation", Installation: 7, Account: "acme"}); err != nil || inst[7] != "acme" || r.Org != "acme" {
		t.Fatalf("installation: %+v %v %v", r, err, inst)
	}
	pr := port.RepoEvent{Kind: "pr_opened", Installation: 7, Owner: "acme", Repo: "api", Number: 12, HeadSHA: "abc123def456"}

	// no record for the head commit → neutral, explains what to run
	r, err := b.Handle(ctx, pr)
	if err != nil || r.Check != "neutral" || r.Change != "" || !strings.Contains(rep.comments[0], "No keelage record") {
		t.Fatalf("no record: %+v %v\n%s", r, err, rep.comments)
	}

	// the daemon pushed a Change for that commit, touching a verified constraint
	o, _ := orgs.Get(ctx, "acme")
	aliceA := core.ActorRef{Kind: core.ActorHuman, ID: "alice"}
	if _, err := o.Pipeline.Handle(ctx, aliceA, harness.DraftConstraint{ID: "c1", ConstraintKind: harness.KindRule, Scope: team, Body: "no retries in billing\nmore", Authored: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Pipeline.Handle(ctx, aliceA, harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: "c1"}}); err != nil {
		t.Fatal(err)
	}
	open := accountability.OpenChange{
		ChangeCmd: accountability.NewChangeCmd("ch1", "open-ch1"), ChangeKind: accountability.KindRealization, Mode: accountability.ModeManual, Scope: team,
		Impact:   accountability.Impact{Constraints: []accountability.ImpactRef{{Constraint: "c1", Relation: accountability.RelReferences}}, Anchors: []string{"code://src/fee.ts#calc"}},
		Proposal: &accountability.Proposal{Ref: "abc123def456", Actor: aliceA, At: t0}, NoIntent: true,
	}
	if _, err := o.Pipeline.Handle(ctx, aliceA, open); err != nil {
		t.Fatal(err)
	}
	r, err = b.Handle(ctx, port.RepoEvent{Kind: "pr_synchronize", Installation: 7, Owner: "acme", Repo: "api", Number: 12, HeadSHA: "abc123def456"})
	if err != nil || r.Change != "ch1" || r.Check != "neutral" || !r.Commented {
		t.Fatalf("with record: %+v %v", r, err)
	}
	last := rep.comments[len(rep.comments)-1]
	for _, want := range []string{"Change `ch1`", "`c1` rule [verified] — no retries in billing", "code://src/fee.ts#calc", "Judgment pending"} {
		if !strings.Contains(last, want) {
			t.Fatalf("comment lacks %q:\n%s", want, last)
		}
	}

	// a comment-only review is not a judgment; a non-member's approval is ignored
	review := func(state, body, login string, id int64) port.RepoEvent {
		e := pr
		e.Kind = "pr_review"
		e.Review = &port.ReviewInfo{ID: id, State: state, Body: body, Login: login, SubmittedAt: t0.Add(time.Hour)}
		return e
	}
	if r, _ := b.Handle(ctx, review("commented", "nice", "alice", 1)); r.Judged || r.Skipped == "" {
		t.Fatalf("comment review: %+v", r)
	}
	if r, _ := b.Handle(ctx, review("approved", "lgtm", "mallory", 2)); r.Judged || !strings.Contains(r.Skipped, "not a member") {
		t.Fatalf("outsider review: %+v", r)
	}
	// an approval without a reason on a change with impact is refused (and recorded) — the comment asks for one
	r, err = b.Handle(ctx, review("approved", "", "alice", 3))
	if err != nil || r.Judged || !strings.Contains(r.Skipped, "no-reason") {
		t.Fatalf("no reason: %+v %v", r, err)
	}
	if !strings.Contains(rep.comments[len(rep.comments)-1], "needs a reason") {
		t.Fatalf("no nudge:\n%s", rep.comments[len(rep.comments)-1])
	}
	// with a reason: Judged, comment refreshed, check success; replaying the same review changes nothing.
	// The reviewer login is case-insensitive.
	r, err = b.Handle(ctx, review("approved", "matches the billing rule", "Alice", 4))
	if err != nil || !r.Judged || r.Check != "success" {
		t.Fatalf("approve: %+v %v", r, err)
	}
	if ch, _ := o.Changes.Get("ch1"); ch.State != accountability.StateJudged || ch.Judgments != 1 {
		t.Fatalf("change after review: %+v", ch)
	}
	if r, _ := b.Handle(ctx, review("approved", "matches the billing rule", "alice", 4)); r.Judged {
		t.Fatalf("replayed review judged again: %+v", r)
	}
	envs, _ := ledger.Read(ctx, accountability.ChangeStream("ch1"), 0)
	if len(envs) != 2 || envs[1].Kind != "Judged" || envs[1].Idem != "gh-review:4" || envs[1].Actor.ID != "alice" {
		t.Fatalf("change stream: %+v", envs)
	}
	// changes_requested with a reason is a rejection
	if r, err := b.Handle(ctx, review("changes_requested", "breaks the retry rule", "alice", 5)); err != nil || !r.Judged {
		t.Fatalf("reject: %+v %v", r, err)
	}
	if ch, _ := o.Changes.Get("ch1"); ch.Judgments != 2 {
		envs, _ := ledger.Read(ctx, accountability.ChangeStream("ch1"), 0)
		for _, e := range envs {
			t.Logf("seq %d ver %d %s idem=%q body=%s", e.Seq, e.Ver, e.Kind, e.Idem, e.Body)
		}
		t.Fatalf("judgments: %+v", ch)
	}
	// an unmapped installation falls back to the repository owner as org
	if r, err := b.Handle(ctx, port.RepoEvent{Kind: "pr_opened", Installation: 99, Owner: "acme", Repo: "api", Number: 13, HeadSHA: "zzz"}); err != nil || r.Org != "acme" {
		t.Fatalf("fallback org: %+v %v", r, err)
	}
}
