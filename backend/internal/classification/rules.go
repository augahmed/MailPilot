package classification

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"mailpilot.local/core/internal/domain"
	mailtext "mailpilot.local/core/internal/mail"
)

var (
	ErrInvalidConfig = errors.New("invalid classification rules")
	ErrInvalidInput  = errors.New("invalid classification input")
)

//go:embed starter_rules.json
var starterRules []byte

// Condition requires one complete phrase (subject/body), an exact address or
// domain, or an exact label name. Reasons describe the signal, never quote mail.
type Condition struct {
	Field  string   `json:"field"`
	AnyOf  []string `json:"any_of"`
	Reason string   `json:"reason,omitempty"`
}
type Rule struct {
	ID           string      `json:"id"`
	CategoryName string      `json:"category_name"`
	Confidence   float64     `json:"confidence"`
	All          []Condition `json:"all"`
	Unless       []Condition `json:"unless"`
}
type RuleConfig struct {
	Name                   string   `json:"name"`
	Version                string   `json:"version"`
	AmbiguityMargin        float64  `json:"ambiguity_margin"`
	ExcludeSubjectPrefixes []string `json:"exclude_subject_prefixes"`
	Rules                  []Rule   `json:"rules"`
}

// StarterConfig returns a fresh copy; callers may deliberately modify rules and
// supply a new version to NewRuleBasedClassifier. It is not an email-controlled
// configuration or a remote download.
func StarterConfig() RuleConfig {
	config, err := ParseConfig(starterRules)
	if err != nil {
		panic("invalid bundled classification rules")
	}
	return config
}
func ParseConfig(data []byte) (RuleConfig, error) {
	var config RuleConfig
	if len(data) > 64*1024 {
		return config, ErrInvalidConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return RuleConfig{}, ErrInvalidConfig
	}
	if !validConfig(config) {
		return RuleConfig{}, ErrInvalidConfig
	}
	return config, nil
}

type RuleBasedClassifier struct {
	config  RuleConfig
	version string
}

var _ ClassificationEngine = (*RuleBasedClassifier)(nil)

func NewRuleBasedClassifier(config RuleConfig) (*RuleBasedClassifier, error) {
	// Copy through JSON so later caller changes cannot race or silently change
	// the rules under the same persisted classifier version.
	data, err := json.Marshal(config)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	cloned, err := ParseConfig(data)
	if err != nil {
		return nil, err
	}
	return &RuleBasedClassifier{config: cloned, version: configVersion(struct {
		Algorithm string
		Rules     RuleConfig
	}{"rules-3-direct-gate-0.60", cloned})}, nil
}
func (c *RuleBasedClassifier) Name() string    { return c.config.Name }
func (c *RuleBasedClassifier) Version() string { return c.version }
func validConfig(c RuleConfig) bool {
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 100 || strings.TrimSpace(c.Version) == "" || len(c.Version) > 100 || math.IsNaN(c.AmbiguityMargin) || c.AmbiguityMargin < 0 || c.AmbiguityMargin > 1 || len(c.Rules) == 0 || len(c.Rules) > 64 || len(c.ExcludeSubjectPrefixes) > 16 {
		return false
	}
	for _, prefix := range c.ExcludeSubjectPrefixes {
		if strings.TrimSpace(prefix) == "" || len(prefix) > 100 {
			return false
		}
	}
	ids := map[string]bool{}
	for _, r := range c.Rules {
		if r.ID == "" || len(r.ID) > 100 || ids[r.ID] || strings.TrimSpace(r.CategoryName) == "" || len(r.CategoryName) > 100 || math.IsNaN(r.Confidence) || r.Confidence <= 0 || r.Confidence > 1 || len(r.All) == 0 || len(r.All) > 6 || len(r.Unless) > 16 {
			return false
		}
		ids[r.ID] = true
		for _, condition := range r.All {
			if !validCondition(condition, true) {
				return false
			}
		}
		for _, condition := range r.Unless {
			if !validCondition(condition, false) {
				return false
			}
		}
	}
	return true
}
func validCondition(c Condition, requireReason bool) bool {
	switch c.Field {
	case "sender", "sender_domain", "subject", "body", "label":
	default:
		return false
	}
	if len(c.AnyOf) == 0 || len(c.AnyOf) > 32 || len(c.Reason) > 160 || (requireReason && strings.TrimSpace(c.Reason) == "") {
		return false
	}
	for _, value := range c.AnyOf {
		if strings.TrimSpace(value) == "" || len(value) > 160 || ((c.Field == "subject" || c.Field == "body") && words(value) == "") {
			return false
		}
	}
	return true
}
func words(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}

type signals struct {
	sender, domain, subject, body string
	labels                        []string
}

// Positive lexical evidence must be asserted, not immediately negated. This
// bounded English check is intentionally not a general language parser. Rule
// exclusions still match literally, including their own negation phrases.
func phraseMatches(text, phrase string, positive bool) bool {
	text, phrase = " "+text+" ", " "+phrase+" "
	for offset := 0; offset < len(text); {
		at := strings.Index(text[offset:], phrase)
		if at < 0 {
			return false
		}
		at += offset
		negated := false
		if positive {
			before := text[:at]
			for _, prefix := range []string{" no", " not", " without", " unrelated to", " not about", " not for", " not a", " not an"} {
				if strings.HasSuffix(before, prefix) {
					negated = true
					break
				}
			}
		}
		if !negated {
			return true
		}
		offset = at + 1
	}
	return false
}
func (s signals) matches(c Condition, positive bool) bool {
	for _, value := range c.AnyOf {
		switch c.Field {
		case "subject":
			if phraseMatches(s.subject, words(value), positive) {
				return true
			}
		case "body":
			if s.body != "" && phraseMatches(s.body, words(value), positive) {
				return true
			}
		case "sender":
			if s.sender != "" && strings.EqualFold(s.sender, strings.TrimSpace(value)) {
				return true
			}
		case "sender_domain":
			if s.domain != "" && strings.EqualFold(s.domain, strings.TrimSpace(value)) {
				return true
			}
		case "label":
			for _, label := range s.labels {
				if strings.EqualFold(label, strings.TrimSpace(value)) {
					return true
				}
			}
		}
	}
	return false
}

// DirectRuleThreshold is an evidence-strength gate, not a probability.
const DirectRuleThreshold = .60

func configVersion(config any) string {
	data, _ := json.Marshal(config)
	return fmt.Sprintf("v1-%x", sha256.Sum256(data))
}
func (c *RuleBasedClassifier) Classify(ctx context.Context, email domain.Email, categories []domain.EmailCategory) (domain.ClassificationRun, error) {
	if err := ctx.Err(); err != nil {
		return domain.ClassificationRun{}, err
	}
	if email.ID == "" || email.Validate() != nil || len(email.Subject) > 16*1024 || len(email.Sender.Address) > 1024 || len(email.Sender.Name) > 1024 || len(email.BodyText) > 1024*1024 || len(email.Labels) > 1000 || len(categories) > 1000 {
		return domain.ClassificationRun{}, ErrInvalidInput
	}
	seen := map[domain.CategoryID]bool{}
	for _, category := range categories {
		if category.ID == "" || seen[category.ID] {
			return domain.ClassificationRun{}, ErrInvalidInput
		}
		seen[category.ID] = true
	}
	for _, label := range email.Labels {
		if label.AccountID != email.AccountID {
			return domain.ClassificationRun{}, ErrInvalidInput
		}
	}
	run := domain.ClassificationRun{UsedClassifier: "none", Reason: "No specific rule matched.", AccountID: email.AccountID, EmailID: email.ID, ClassifierName: c.Name(), ClassifierVersion: c.Version(), InputHash: InputHash(email, categories), Timestamp: time.Now().UTC().Truncate(time.Millisecond), Results: []domain.ClassificationResult{}}
	subject := strings.ToLower(strings.TrimSpace(email.Subject))
	for _, prefix := range c.config.ExcludeSubjectPrefixes {
		if strings.HasPrefix(subject, strings.ToLower(strings.TrimSpace(prefix))) {
			return run, nil
		}
	}
	s := signals{subject: words(email.Subject)}
	if email.BodyState == "complete" {
		body, err := mailtext.CleanBody(email)
		if err != nil {
			return domain.ClassificationRun{}, ErrInvalidInput
		}
		s.body = words(body)
	}
	if address, err := mail.ParseAddress(email.Sender.Address); err == nil {
		s.sender = strings.ToLower(address.Address)
		if at := strings.LastIndexByte(s.sender, '@'); at > 0 && at < len(s.sender)-1 {
			s.domain = s.sender[at+1:]
		}
	}
	for _, label := range email.Labels {
		s.labels = append(s.labels, strings.TrimSpace(label.Name))
	}
	candidates := map[domain.CategoryID]domain.ClassificationResult{}
	for _, rule := range c.config.Rules {
		if err := ctx.Err(); err != nil {
			return domain.ClassificationRun{}, err
		}
		var categoryID domain.CategoryID
		matches := 0
		for _, category := range categories {
			if category.Enabled && strings.EqualFold(strings.TrimSpace(category.Name), rule.CategoryName) {
				categoryID = category.ID
				matches++
			}
		}
		// Ambiguous category names or missing/disabled defaults never select an ID.
		if matches != 1 || categoryID == "" {
			continue
		}
		excluded := false
		for _, condition := range rule.Unless {
			if s.matches(condition, false) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		reasons := []string{}
		matched := true
		for _, condition := range rule.All {
			if !s.matches(condition, true) {
				matched = false
				break
			}
			reasons = append(reasons, condition.Reason)
		}
		if !matched {
			continue
		}
		result := domain.ClassificationResult{ScoreKind: "rule_confidence", AccountID: email.AccountID, EmailID: email.ID, CategoryID: categoryID, Confidence: rule.Confidence, ClassifierName: c.Name(), ClassifierVersion: c.Version(), InputHash: run.InputHash, Reasons: reasons, Timestamp: run.Timestamp}
		previous, ok := candidates[categoryID]
		if !ok || result.Confidence > previous.Confidence {
			candidates[categoryID] = result
		}
	}
	// Rank independent category matches; close or tied alternatives abstain.
	var best domain.ClassificationResult
	second := -1.0
	for _, result := range candidates {
		if result.Confidence > best.Confidence {
			second = best.Confidence
			best = result
		} else if result.Confidence > second {
			second = result.Confidence
		}
	}
	if best.CategoryID != "" && (len(candidates) == 1 || best.Confidence-second > c.config.AmbiguityMargin) {
		if best.Confidence < DirectRuleThreshold {
			run.Reason = "No specific rule met the 0.60 direct-rule gate."
			return run, nil
		}
		run.Results = append(run.Results, best)
		run.UsedClassifier = c.Name()
		run.Reason = "One category has a specific, unambiguous rule match."
	} else if len(candidates) > 1 {
		run.Reason = "Specific rules match competing categories; no category chosen."
	}
	if err := ctx.Err(); err != nil {
		return domain.ClassificationRun{}, err
	}
	return run, nil
}
