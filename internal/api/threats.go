package api

import "encoding/json"

// Detection is what a threat rule caught: one subject (an IP, an
// account) firing one rule.
type Detection struct {
	ID          string         `json:"id"`
	RuleKey     string         `json:"rule_key"`
	RuleName    string         `json:"rule_name"`
	Subject     map[string]any `json:"subject"`
	Status      string         `json:"status"`   // open or resolved.
	Severity    string         `json:"severity"` // high, medium or low.
	FirstSeenAt string         `json:"first_seen_at"`
	LastSeenAt  string         `json:"last_seen_at"`
	FiredCount  int            `json:"fired_count"`
	LastMeasure *float64       `json:"last_measure"`
	Evidence    []Evidence     `json:"evidence"`
	URL         string         `json:"url"`
}

// Evidence is a record behind a detection.
type Evidence struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	At   string `json:"at"`
}

// ThreatRule is one of a project's threat rules: a default ("d-…") or
// one of its own ("r-…").
type ThreatRule struct {
	Ref      string          `json:"ref"`
	Name     string          `json:"name"`
	Text     *string         `json:"text"`
	Default  bool            `json:"default"`
	Enabled  bool            `json:"enabled"`
	Paused   bool            `json:"paused"`
	Severity string          `json:"severity"`
	Sentence string          `json:"sentence"`
	Spec     json.RawMessage `json:"spec"`
}

// RuleCheck is POST …/threat-rules/check: what Collector understood of
// a rule's words ("ok"), or why it can't make a rule of them
// ("refused"). Token turns exactly that on, for an hour.
type RuleCheck struct {
	Status   string       `json:"status"`
	Reason   string       `json:"reason"`
	Sentence string       `json:"sentence"`
	Model    string       `json:"model"`
	Preview  *RulePreview `json:"preview"`
	Token    string       `json:"token"`
}

// RulePreview is what a checked rule would have caught over the last
// day, with up to 5 examples.
type RulePreview struct {
	Count    int `json:"count"`
	Examples []struct {
		Subject string  `json:"subject"`
		Measure float64 `json:"measure"`
	} `json:"examples"`
}
