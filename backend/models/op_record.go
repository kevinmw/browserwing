package models

import "time"

// OpRecord represents a single browser operation recorded during AI exploration.
// Stored in the models package to avoid import cycles between executor and browser packages.
type OpRecord struct {
	Type          string            `json:"type"`           // navigate, click, input, select, keyboard, scroll, go_back, go_forward, reload, evaluate
	Identifier    string            `json:"identifier"`     // original identifier used by AI
	ResolvedXPath string            `json:"resolved_xpath"` // stable full XPath resolved from the actual element
	Value         string            `json:"value"`          // input text / select value
	URL           string            `json:"url"`            // navigation URL
	Key           string            `json:"key"`            // keyboard key
	JSCode        string            `json:"js_code"`        // JavaScript code (for evaluate)
	Success       bool              `json:"success"`
	Timestamp     time.Time         `json:"timestamp"`
	ElementInfo   map[string]string `json:"element_info"`
}
