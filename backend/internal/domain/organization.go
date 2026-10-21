package domain

// OrganizationPlan is the immutable, account-scoped preview stored under
// mailbox_actions. Steps contain the narrowly bounded remote write journal.
const PotentialPhishingLabel = "Potential Phishing"

// OrganizationRisk is immutable review evidence; it grants no write authority.
type OrganizationRisk struct {
	Score     int               `json:"score"`
	Threshold int               `json:"threshold"`
	Version   string            `json:"version"`
	Findings  []PhishingFinding `json:"findings"`
}
type OrganizationClassification struct {
	Version           string `json:"version"`
	AssessmentHash    string `json:"assessmentHash"`
	BaseHash          string `json:"baseHash"`
	SourceRevision    string `json:"sourceRevision"`
	ModelVersion      string `json:"modelVersion"`
	ClassifierVersion string `json:"classifierVersion"`
	ScoreKind         string `json:"scoreKind"`
}
type OrganizationRow struct {
	Classification      *OrganizationClassification `json:"classification,omitempty"`
	Phishing            *OrganizationRisk           `json:"phishing,omitempty"`
	EmailID             EmailID                     `json:"emailId"`
	ProviderID          ProviderMessageID           `json:"providerId"`
	CategoryID          CategoryID                  `json:"categoryId"`
	InputHash           string                      `json:"inputHash"`
	PredictionTimestamp int64                       `json:"predictionTimestamp"`
	Sender              string                      `json:"sender"`
	Subject             string                      `json:"subject"`
	CurrentLabels       []string                    `json:"currentLabels"`
	BeforeLabels        []ProviderLabelID           `json:"beforeLabels"`
	ProposedLabel       string                      `json:"proposedLabel"`
	LabelID             ProviderLabelID             `json:"labelId"`
	Confidence          float64                     `json:"confidence"`
	Change              string                      `json:"change"`
}
type OrganizationStep struct {
	Kind        string            `json:"kind"` // create_label, add, remove
	EmailID     EmailID           `json:"emailId"`
	Name        string            `json:"name"`
	LabelID     ProviderLabelID   `json:"labelId"`
	State       ActionItemState   `json:"state"`
	ResultID    ProviderLabelID   `json:"resultId"`
	Added       bool              `json:"added"` // only a successful, verified addition is undoable
	AfterLabels []ProviderLabelID `json:"afterLabels"`
	Code        string            `json:"code"`
}
type OrganizationPlan struct {
	ID        ActionID           `json:"id"`
	AccountID AccountID          `json:"accountId"`
	Kind      ActionKind         `json:"kind"`
	Reverses  ActionID           `json:"reverses"`
	Status    ActionStatus       `json:"status"`
	Digest    string             `json:"digest"`
	Revision  int64              `json:"revision"`
	CreatedAt int64              `json:"createdAt"`
	ExpiresAt int64              `json:"expiresAt"`
	Rows      []OrganizationRow  `json:"rows"`
	Steps     []OrganizationStep `json:"steps"`
}
