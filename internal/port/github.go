package port

import (
	"context"
	"time"
)

// RepoEvent is a normalized delivery from the forge (GitHub App webhook,
// spec §4.5). The adapter parses the provider payload; app decides.
type RepoEvent struct {
	// Kind: installation | pr_opened | pr_synchronize | pr_review | ignored
	Kind         string
	Delivery     string
	Installation int64
	// Account is the installation's account login (installation events).
	Account string
	Owner   string
	Repo    string
	Number  int
	HeadSHA string
	Title   string
	URL     string
	Review  *ReviewInfo
}

// ReviewInfo is a submitted pull request review.
type ReviewInfo struct {
	ID          int64
	State       string // approved | changes_requested | commented | dismissed
	Body        string
	Login       string
	SubmittedAt time.Time
}

// PRReporter writes back to the forge on behalf of the app installation.
type PRReporter interface {
	// UpsertComment posts the comment, or edits the earlier one carrying marker.
	UpsertComment(ctx context.Context, installation int64, owner, repo string, number int, marker, body string) error
	// Check publishes a completed check run on the head commit.
	Check(ctx context.Context, installation int64, owner, repo, headSHA, name, conclusion, title, summary string) error
}

// InstallationMap binds app installations to orgs (spec §4.2: the
// installation is how a user ↔ org mapping starts).
type InstallationMap interface {
	LinkInstallation(ctx context.Context, id int64, org string) error
	OrgForInstallation(ctx context.Context, id int64) (string, error)
}
