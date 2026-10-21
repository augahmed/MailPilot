package domain

import "time"

// Account stores identity and an optional OS credential-store reference, never a token.
type Account struct {
	ID                AccountID
	Provider          string
	ProviderAccountID string
	Address           EmailAddress
	CredentialRef     string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type ClassificationCandidate struct {
	CategoryID CategoryID
	Similarity float64
}

type ClassificationResult struct {
	ScoreKind         string // rule_confidence or cosine_similarity; neither is calibrated probability.
	AccountID         AccountID
	EmailID           EmailID
	CategoryID        CategoryID
	Confidence        float64
	ClassifierName    string
	ClassifierVersion string
	InputHash         string
	Reasons           []string
	Timestamp         time.Time
}

// ClassificationRun persists abstention as well as predictions. Results are
// suggestions, except explicitly typed user assignments reconstructed from manual
// preferences. Neither authorizes provider writes.
type ClassificationRun struct {
	Semantic          *SemanticEvidence `json:",omitempty"`
	UsedClassifier    string            // Actual selected engine, or none on abstention.
	Reason            string
	Candidates        []ClassificationCandidate
	AccountID         AccountID
	EmailID           EmailID
	ClassifierName    string
	ClassifierVersion string
	InputHash         string
	Timestamp         time.Time
	Results           []ClassificationResult
}

type PhishingFinding struct {
	Points      int
	Code        string
	Explanation string
	Evidence    string
}
type PhishingResult struct {
	AccountID       AccountID
	EmailID         EmailID
	Score           int
	AnalyzerVersion string
	InputHash       string
	Findings        []PhishingFinding
	AssessedAt      time.Time
}

type ActionID string
type ActionStatus string

const (
	ActionPreview   ActionStatus = "preview"
	ActionApplying  ActionStatus = "applying"
	ActionApplied   ActionStatus = "applied"
	ActionPartial   ActionStatus = "partial"
	ActionFailed    ActionStatus = "failed"
	ActionCancelled ActionStatus = "cancelled"
	ActionUncertain ActionStatus = "uncertain"
	ActionExpired   ActionStatus = "expired"
)

type ActionItemState string

const (
	ItemPending   ActionItemState = "pending"
	ItemInFlight  ActionItemState = "in_flight"
	ItemApplied   ActionItemState = "applied"
	ItemFailed    ActionItemState = "failed"
	ItemUncertain ActionItemState = "uncertain"
	ItemConflict  ActionItemState = "conflict"
	ItemSkipped   ActionItemState = "skipped"
)

type ActionKind string

const (
	ActionLabelDelta ActionKind = "label_delta"
	ActionUndo       ActionKind = "undo"
)

type LabelDelta struct {
	Add    []ProviderLabelID
	Remove []ProviderLabelID
}
type MailboxActionItem struct {
	EmailID           EmailID
	ProviderMessageID ProviderMessageID
	RequestedDelta    LabelDelta
	BeforeLabels      []ProviderLabelID
	// Nil means unverified; a non-nil empty result means a verified no-op.
	ActualDelta   *LabelDelta
	AfterLabels   *[]ProviderLabelID
	State         ActionItemState
	AttemptCount  int
	LastErrorCode string
	UpdatedAt     time.Time
}
type MailboxAction struct {
	ID               ActionID
	AccountID        AccountID
	Kind             ActionKind
	ReversesActionID *ActionID
	Status           ActionStatus
	PlanDigest       string
	Revision         int64
	CreatedAt        time.Time
	ExpiresAt        time.Time
	ConfirmedAt      *time.Time
	CompletedAt      *time.Time
	Items            []MailboxActionItem
}
