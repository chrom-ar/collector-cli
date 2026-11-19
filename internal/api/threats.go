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
	// Records are the log records behind it, newest first, as GET …/logs
	// gives them; empty when no longer kept.
	Records []json.RawMessage `json:"records"`
	URL     string            `json:"url"`
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
	Ref         string           `json:"ref"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Labels      map[string]Label `json:"labels"`
	Text        *string          `json:"text"`
	Default     bool             `json:"default"`
	Enabled     bool             `json:"enabled"`
	Paused      bool             `json:"paused"`
	Severity    string           `json:"severity"`
	Sentence    string           `json:"sentence"`
	Spec        json.RawMessage  `json:"spec"`
}

// Label is a rule's name and description in one language.
type Label struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// RuleCheck is POST …/threat-rules/check: what Collector understood of
// a rule's words ("ok"), or why it can't make a rule of them
// ("refused"). Text is the whole rule restated, as it stands after every
// message. Token goes on to another check, and, when ok, turns exactly
// that on, for an hour.
type RuleCheck struct {
	Status   string           `json:"status"`
	Reason   string           `json:"reason"`
	Text     string           `json:"text"`
	Sentence string           `json:"sentence"`
	Labels   map[string]Label `json:"labels"`
	// Name and Description are the suggested ones in the request's
	// language; nil when Collector suggests none.
	Name        *string      `json:"name"`
	Description *string      `json:"description"`
	Model       string       `json:"model"`
	Preview     *RulePreview `json:"preview"`
	Messages    int          `json:"messages"`
	Token       string       `json:"token"`
}

// RulePreview is what a checked rule would have caught over the last
// day, with up to 5 examples, how many records its conditions match and
// the newest 20 of them.
type RulePreview struct {
	Count    int `json:"count"`
	Examples []struct {
		Subject string  `json:"subject"`
		Measure float64 `json:"measure"`
	} `json:"examples"`
	Matched *int `json:"matched"`
	Records []struct {
		At     string            `json:"at"`
		Fields map[string]string `json:"fields"`
	} `json:"records"`
}
