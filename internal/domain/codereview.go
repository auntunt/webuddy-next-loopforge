package domain

import "time"

// CodeReviewPart records one batched slice of a complete unified diff review.
type CodeReviewPart struct {
	Part       int    `json:"part"`
	File       string `json:"file"`
	Characters int    `json:"characters"`
	SHA256     string `json:"sha256"`
	Verdict    string `json:"verdict"` // pass | fail | unknown
}

// CodeReviewAC records evidence for one acceptance criterion.
type CodeReviewAC struct {
	ID       string `json:"id"`
	Result   string `json:"result"` // pass | fail | unverified
	Evidence string `json:"evidence"`
}

// CodeReviewScope tracks new requirements, regressions, invariants, and gaps.
type CodeReviewScope struct {
	New        []string `json:"new,omitempty"`
	Regression []string `json:"regression,omitempty"`
	Invariants []string `json:"invariants,omitempty"`
	Gaps       []string `json:"gaps,omitempty"`
}

// CodeReviewRecord is a complete, tamper-evident diff review record ported from
// Webuddy's code_review.py core.
type CodeReviewRecord struct {
	ID                string           `json:"id"`
	ProjectID         string           `json:"projectId"`
	IssueID           string           `json:"issueId,omitempty"`
	RunID             string           `json:"runId,omitempty"`
	Title             string           `json:"title"`
	RepoPath          string           `json:"repoPath,omitempty"`
	BaseCommit        string           `json:"baseCommit"`
	HeadCommit        string           `json:"headCommit,omitempty"`
	DiffSHA256        string           `json:"diffSha256"`
	DiffPreview       string           `json:"diffPreview,omitempty"`
	WriterKind        string           `json:"writerKind"` // human | agent
	Writer            string           `json:"writer"`
	Reviewer          string           `json:"reviewer"`
	Model             string           `json:"model,omitempty"`
	DifferentExecutor bool             `json:"differentExecutor"`
	Verdict           string           `json:"verdict"` // pass | fail | unknown
	VagueFail         bool             `json:"vagueFail"`
	Complete          bool             `json:"complete"`
	BinaryDetected    bool             `json:"binaryDetected"`
	TotalParts        int              `json:"totalParts"`
	Parts             []CodeReviewPart `json:"parts"`
	Uncovered         []string         `json:"uncovered"`
	Blocking          []string         `json:"blocking"`
	Advisory          []string         `json:"advisory"`
	AC                []CodeReviewAC   `json:"ac"`
	Scope             CodeReviewScope  `json:"scope"`
	Evidence          string           `json:"evidence,omitempty"`
	Cause             string           `json:"cause,omitempty"`
	Guide             string           `json:"guide,omitempty"`
	Binding           string           `json:"binding"`
	CreatedBy         string           `json:"createdBy,omitempty"`
	CreatedAt         time.Time        `json:"createdAt"`
}

// CodeReviewFinding is one addressable AI finding derived from a review snapshot.
type CodeReviewFinding struct {
	ID     string `json:"id"`   // blocking:0, advisory:1, ...
	Kind   string `json:"kind"` // blocking | advisory
	Detail string `json:"detail"`
}

// CodeReviewSnapshot is the immutable binding target for human review feedback.
type CodeReviewSnapshot struct {
	Binding    string              `json:"binding"`
	Base       string              `json:"base"`
	DiffSHA256 string              `json:"diffSha256"`
	CreatedAt  time.Time           `json:"createdAt"`
	Findings   []CodeReviewFinding `json:"findings"`
}

// CodeReviewFindingJudgment is a human's per-finding verdict.
type CodeReviewFindingJudgment struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"` // valid | false_positive | unassessed
	Reason  string `json:"reason"`
}

// CodeReviewFeedback records a human reviewer's observation of one exact AI review.
type CodeReviewFeedback struct {
	ID            string                      `json:"id"`
	ReviewID      string                      `json:"reviewId"`
	ProjectID     string                      `json:"projectId"`
	Actor         string                      `json:"actor"`
	RequestID     string                      `json:"requestId"`
	Binding       string                      `json:"binding"`
	PayloadDigest string                      `json:"payloadDigest"`
	Minutes       int                         `json:"minutes"`
	Findings      []CodeReviewFindingJudgment `json:"findings"`
	Missed        []string                    `json:"missed"`
	Note          string                      `json:"note"`
	Current       bool                        `json:"current"`
	CreatedAt     time.Time                   `json:"createdAt"`
}

// CodeReviewFeedbackSummary aggregates valid/false-positive/missed stats per project.
type CodeReviewFeedbackSummary struct {
	Valid         int                  `json:"valid"`
	FalsePositive int                  `json:"falsePositive"`
	Unassessed    int                  `json:"unassessed"`
	Observations  int                  `json:"observations"`
	Reviews       int                  `json:"reviews"`
	Minutes       *int                 `json:"minutes"`
	Missed        int                  `json:"missed"`
	ValidRate     *float64             `json:"validRate"`
	Records       []CodeReviewFeedback `json:"records"`
}
