package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/port"
)

// CommentMarker tags the one PR comment keelage keeps up to date.
const CommentMarker = "<!-- keelage:pr -->"

// CheckName is the check run keelage publishes on a PR head.
const CheckName = "keelage"

// PRBridge turns forge events into ledger records and back (spec §4.5):
// a PR shows the impact the daemon already pushed for its head commit
// (no server-side clone or computation), and a submitted review with a
// reason becomes a Judged record on that Change.
type PRBridge struct {
	Orgs     *Orgs
	Reporter port.PRReporter
	Installs port.InstallationMap
	Members  port.Membership
	Clock    port.Clock
}

// PRReport is what one delivery did.
type PRReport struct {
	Kind      string `json:"kind"`
	Org       string `json:"org,omitempty"`
	Change    string `json:"change,omitempty"`
	Commented bool   `json:"commented,omitempty"`
	Check     string `json:"check,omitempty"`
	Judged    bool   `json:"judged,omitempty"`
	Skipped   string `json:"skipped,omitempty"`
}

// HandleDelivery implements the webhook sink over a parser supplied by cmd.
type deliveryParser func(event, delivery string, body []byte) (port.RepoEvent, error)

// Sink adapts the bridge to httpapi.WebhookSink with the given parser.
func (b *PRBridge) Sink(parse func(event, delivery string, body []byte) (port.RepoEvent, error)) *PRSink {
	return &PRSink{bridge: b, parse: deliveryParser(parse)}
}

// PRSink is the webhook sink.
type PRSink struct {
	bridge *PRBridge
	parse  deliveryParser
}

// HandleDelivery parses and handles one delivery.
func (s *PRSink) HandleDelivery(ctx context.Context, event, delivery string, body []byte) (any, error) {
	ev, err := s.parse(event, delivery, body)
	if err != nil {
		return nil, err
	}
	return s.bridge.Handle(ctx, ev)
}

func (b *PRBridge) orgFor(ctx context.Context, ev port.RepoEvent) (string, error) {
	if b.Installs != nil && ev.Installation != 0 {
		org, err := b.Installs.OrgForInstallation(ctx, ev.Installation)
		if err == nil {
			return org, nil
		}
		if !errors.Is(err, port.ErrNotFound) {
			return "", err
		}
	}
	if ev.Owner != "" {
		return ev.Owner, nil
	}
	return "", errors.New("app: no org for delivery")
}

// Handle acts on one event.
func (b *PRBridge) Handle(ctx context.Context, ev port.RepoEvent) (PRReport, error) {
	rep := PRReport{Kind: ev.Kind}
	switch ev.Kind {
	case "installation":
		if b.Installs == nil || ev.Account == "" {
			rep.Skipped = "no installation map"
			return rep, nil
		}
		rep.Org = ev.Account
		return rep, b.Installs.LinkInstallation(ctx, ev.Installation, ev.Account)
	case "pr_opened", "pr_synchronize":
		return b.describe(ctx, ev, rep)
	case "pr_review":
		return b.review(ctx, ev, rep)
	}
	rep.Skipped = "ignored event"
	return rep, nil
}

// describe posts the impact comment and check for the head commit.
func (b *PRBridge) describe(ctx context.Context, ev port.RepoEvent, rep PRReport) (PRReport, error) {
	org, err := b.orgFor(ctx, ev)
	if err != nil {
		return rep, err
	}
	rep.Org = org
	o, err := b.Orgs.Get(ctx, org)
	if err != nil {
		return rep, err
	}
	o.mu.Lock()
	if err := o.catchUp(ctx); err != nil {
		o.mu.Unlock()
		return rep, err
	}
	ch, ok := o.Changes.ByRef(ev.HeadSHA)
	body, conclusion, title := renderPR(o, ch, ok, ev.HeadSHA)
	o.mu.Unlock()
	if ok {
		rep.Change = string(ch.ID)
	}
	if b.Reporter == nil {
		rep.Skipped = "no reporter"
		return rep, nil
	}
	if err := b.Reporter.UpsertComment(ctx, ev.Installation, ev.Owner, ev.Repo, ev.Number, CommentMarker, body); err != nil {
		return rep, err
	}
	rep.Commented = true
	if err := b.Reporter.Check(ctx, ev.Installation, ev.Owner, ev.Repo, ev.HeadSHA, CheckName, conclusion, title, body); err != nil {
		return rep, err
	}
	rep.Check = conclusion
	return rep, nil
}

// review records a submitted review as a judgment on the head commit's Change.
func (b *PRBridge) review(ctx context.Context, ev port.RepoEvent, rep PRReport) (PRReport, error) {
	if ev.Review == nil {
		rep.Skipped = "no review"
		return rep, nil
	}
	switch ev.Review.State {
	case "approved", "changes_requested":
	default:
		rep.Skipped = "review state " + ev.Review.State + " is not a judgment"
		return rep, nil
	}
	org, err := b.orgFor(ctx, ev)
	if err != nil {
		return rep, err
	}
	rep.Org = org
	if b.Members != nil {
		ok, err := b.Members.IsMember(ctx, org, strings.ToLower(ev.Review.Login))
		if err != nil {
			return rep, err
		}
		if !ok {
			rep.Skipped = ev.Review.Login + " is not a member of " + org
			return rep, nil
		}
	}
	o, err := b.Orgs.Get(ctx, org)
	if err != nil {
		return rep, err
	}
	// everything under the org lock is ledger work; GitHub I/O happens after
	var (
		body, conclusion, title string
		nudge                   string
	)
	o.mu.Lock()
	rep, judged, err := b.judge(ctx, o, ev, rep)
	if err == nil && rep.Change != "" {
		ch, _ := o.Changes.Get(core.ID(rep.Change))
		if judged {
			body, conclusion, title = renderPR(o, ch, true, ev.HeadSHA)
		} else if strings.Contains(rep.Skipped, "no-reason") {
			nudge = renderPRWithNote(o, ch, ev.HeadSHA, "A review on a change with impact needs a reason: add one to the review body so keelage can record the judgment.")
		}
	}
	o.mu.Unlock()
	if err != nil || b.Reporter == nil {
		return rep, err
	}
	if nudge != "" {
		_ = b.Reporter.UpsertComment(ctx, ev.Installation, ev.Owner, ev.Repo, ev.Number, CommentMarker, nudge)
		return rep, nil
	}
	if !judged {
		return rep, nil
	}
	if err := b.Reporter.UpsertComment(ctx, ev.Installation, ev.Owner, ev.Repo, ev.Number, CommentMarker, body); err != nil {
		return rep, err
	}
	rep.Commented = true
	if err := b.Reporter.Check(ctx, ev.Installation, ev.Owner, ev.Repo, ev.HeadSHA, CheckName, conclusion, title, body); err != nil {
		return rep, err
	}
	rep.Check = conclusion
	return rep, nil
}

// judge records the review as a judgment (caller holds o.mu). judged is
// true when a new Judged record was written.
func (b *PRBridge) judge(ctx context.Context, o *Org, ev port.RepoEvent, rep PRReport) (PRReport, bool, error) {
	if err := o.catchUp(ctx); err != nil {
		return rep, false, err
	}
	ch, ok := o.Changes.ByRef(ev.HeadSHA)
	if !ok {
		rep.Skipped = "no change for " + ev.HeadSHA
		return rep, false, nil
	}
	rep.Change = string(ch.ID)
	decision := accountability.DecisionAccept
	if ev.Review.State == "changes_requested" {
		decision = accountability.DecisionReject
	}
	reviewer := core.ActorRef{Kind: core.ActorHuman, ID: core.ID(strings.ToLower(ev.Review.Login))}
	at := ev.Review.SubmittedAt
	if at.IsZero() {
		at = b.Clock.Now()
	}
	rid := strconv.FormatInt(ev.Review.ID, 10)
	cmd := accountability.Judge{
		ChangeCmd: accountability.NewChangeCmd(ch.ID, "gh-review:"+rid),
		Judgment: accountability.Judgment{
			ID: core.ID("ghr-" + rid), Decision: decision, Reason: strings.TrimSpace(ev.Review.Body), By: reviewer, At: at, Source: accountability.SourceReview,
		},
	}
	res, err := o.Pipeline.Handle(ctx, reviewer, cmd)
	if err != nil {
		if r, isRej := core.AsRejection(err); isRej {
			rep.Skipped = "review not recorded: " + r.Error()
			return rep, false, nil
		}
		return rep, false, err
	}
	rep.Judged = !res.Replayed
	return rep, rep.Judged, nil
}

func renderPRWithNote(o *Org, ch ChangeSummary, sha, note string) string {
	body, _, _ := renderPR(o, ch, true, sha)
	return body + "\n> " + note + "\n"
}

// renderPR is the comment body, the check conclusion and its title.
// Conclusions: success = judged or no impact; neutral = judgment pending or
// no record (the check never fails a PR in v0: the harness decides, not CI).
func renderPR(o *Org, ch ChangeSummary, ok bool, sha string) (body, conclusion, title string) {
	var sb strings.Builder
	sb.WriteString(CommentMarker + "\n")
	if !ok {
		sb.WriteString("### keelage\n\nNo keelage record for `" + short(sha) + "`.\n\n")
		sb.WriteString("The daemon records a Change per commit (`keelage change derive <sha>`), and `keelage sync push` shares it; the Action does the same headlessly.\n")
		return sb.String(), "neutral", "no keelage record for " + short(sha)
	}
	fmt.Fprintf(&sb, "### keelage · Change `%s`\n\n", ch.ID)
	fmt.Fprintf(&sb, "| | |\n|---|---|\n| kind | %s (%s) |\n| state | **%s** |\n| autonomy applied | %s |\n| judgments | %d |\n| sessions | %d |\n\n",
		ch.Kind, ch.Mode, ch.State, ch.AutonomyApplied, ch.Judgments, len(ch.Sessions))
	if len(ch.Constraints) > 0 {
		sb.WriteString("**Constraints touched**\n\n")
		for _, id := range ch.Constraints {
			if c, found := o.Constraints.Get(id); found {
				fmt.Fprintf(&sb, "- `%s` %s [%s] — %s\n", c.ID, c.Kind, c.State, firstLine(c.Body))
			} else {
				fmt.Fprintf(&sb, "- `%s`\n", id)
			}
		}
		sb.WriteString("\n")
	}
	if len(ch.Anchors) > 0 {
		sb.WriteString("**Anchors**\n\n")
		for _, a := range ch.Anchors {
			state := ""
			if s, found := o.Anchors.Get(a); found {
				state = " [" + string(s.State) + "]"
			}
			fmt.Fprintf(&sb, "- `%s`%s\n", a, state)
		}
		sb.WriteString("\n")
	}
	switch {
	case ch.State == accountability.StateProposed && len(ch.Constraints) > 0:
		sb.WriteString("_Judgment pending: an approving or changes-requested review **with a reason** is recorded as the judgment._\n")
		return sb.String(), "neutral", "judgment pending on " + string(ch.ID)
	case ch.State == accountability.StateProposed:
		sb.WriteString("_No constraint impact: settles without a judgment._\n")
		return sb.String(), "success", "no constraint impact"
	default:
		return sb.String(), "success", string(ch.State) + " · " + string(ch.ID)
	}
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 120 {
		s = string(r[:117]) + "…"
	}
	return s
}
