package browser

import (
	"time"

	"github.com/browserwing/browserwing/models"
	"github.com/google/uuid"
)

// diagnosticOps are read-only operations that should be filtered from generated scripts
var diagnosticOps = map[string]bool{
	"screenshot":             true,
	"get_page_info":          true,
	"accessibility_snapshot": true,
	"get_page_content":       true,
	"get_page_text":          true,
	"console_messages":       true,
	"network_requests":       true,
	"extract":                true,
	"get_text":               true,
	"get_value":              true,
	"hover":                  true,
	"wait":                   true,
}

// OptimizeOperationsToScript converts raw AI operation records into an optimized Script
func OptimizeOperationsToScript(rawOps []models.OpRecord, taskDesc, startURL string) *models.Script {
	// Step 1: filter out diagnostic and failed operations
	actionable := filterActionableOps(rawOps)

	// Step 2: deduplicate consecutive navigations to the same URL
	actionable = deduplicateNavigations(actionable)

	// Step 3: merge consecutive input operations on the same element
	actionable = mergeConsecutiveInputs(actionable)

	// Step 4: convert to ScriptAction list
	actions := convertToScriptActions(actionable)

	// Step 5: insert wait steps after navigations
	actions = insertWaitsAfterNavigation(actions)

	script := &models.Script{
		ID:          uuid.New().String(),
		Name:        truncateString(taskDesc, 50),
		Description: taskDesc,
		URL:         startURL,
		Actions:     actions,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	return script
}

// filterActionableOps removes diagnostic / failed operations
func filterActionableOps(ops []models.OpRecord) []models.OpRecord {
	result := make([]models.OpRecord, 0, len(ops))
	for _, op := range ops {
		if !op.Success {
			continue
		}
		if diagnosticOps[op.Type] {
			continue
		}
		result = append(result, op)
	}
	return result
}

// deduplicateNavigations keeps only the last navigation when consecutive navigations go to the same URL
func deduplicateNavigations(ops []models.OpRecord) []models.OpRecord {
	if len(ops) == 0 {
		return ops
	}
	result := make([]models.OpRecord, 0, len(ops))
	for i := 0; i < len(ops); i++ {
		if ops[i].Type == "navigate" && i+1 < len(ops) && ops[i+1].Type == "navigate" && ops[i].URL == ops[i+1].URL {
			continue
		}
		result = append(result, ops[i])
	}
	return result
}

// mergeConsecutiveInputs merges consecutive type operations on the same element, keeping only the last value
func mergeConsecutiveInputs(ops []models.OpRecord) []models.OpRecord {
	if len(ops) == 0 {
		return ops
	}
	result := make([]models.OpRecord, 0, len(ops))
	for i := 0; i < len(ops); i++ {
		if ops[i].Type == "input" && i+1 < len(ops) && ops[i+1].Type == "input" && ops[i].ResolvedXPath != "" && ops[i].ResolvedXPath == ops[i+1].ResolvedXPath {
			continue
		}
		result = append(result, ops[i])
	}
	return result
}

// convertToScriptActions converts OpRecord list to ScriptAction list
func convertToScriptActions(ops []models.OpRecord) []models.ScriptAction {
	actions := make([]models.ScriptAction, 0, len(ops))

	for _, op := range ops {
		action := models.ScriptAction{
			Timestamp: op.Timestamp.UnixMilli(),
		}

		switch op.Type {
		case "navigate":
			action.Type = "navigate"
			action.URL = op.URL
		case "click":
			action.Type = "click"
			action.XPath = op.ResolvedXPath
			if action.XPath == "" {
				action.Selector = op.Identifier
			}
			fillElementInfo(&action, op.ElementInfo)
		case "input":
			action.Type = "input"
			action.Value = op.Value
			action.XPath = op.ResolvedXPath
			if action.XPath == "" {
				action.Selector = op.Identifier
			}
			fillElementInfo(&action, op.ElementInfo)
		case "select":
			action.Type = "select"
			action.Value = op.Value
			action.XPath = op.ResolvedXPath
			if action.XPath == "" {
				action.Selector = op.Identifier
			}
			fillElementInfo(&action, op.ElementInfo)
		case "keyboard":
			action.Type = "keyboard"
			action.Key = op.Key
		case "evaluate":
			action.Type = "execute_js"
			action.JSCode = op.JSCode
		case "scroll":
			action.Type = "scroll"
		case "go_back":
			action.Type = "navigate"
			action.URL = "javascript:history.back()"
		case "go_forward":
			action.Type = "navigate"
			action.URL = "javascript:history.forward()"
		case "reload":
			action.Type = "navigate"
			action.URL = "javascript:location.reload()"
		default:
			continue
		}

		actions = append(actions, action)
	}

	return actions
}

// insertWaitsAfterNavigation adds a short sleep action after navigation steps
func insertWaitsAfterNavigation(actions []models.ScriptAction) []models.ScriptAction {
	if len(actions) == 0 {
		return actions
	}
	result := make([]models.ScriptAction, 0, len(actions)+4)
	for i, action := range actions {
		result = append(result, action)
		if action.Type == "navigate" && i < len(actions)-1 {
			result = append(result, models.ScriptAction{
				Type:      "sleep",
				Duration:  2000,
				Timestamp: action.Timestamp + 1,
			})
		}
	}
	return result
}

func fillElementInfo(action *models.ScriptAction, info map[string]string) {
	if info == nil {
		return
	}
	if tag, ok := info["tag"]; ok {
		action.TagName = tag
	}
	if text, ok := info["text"]; ok {
		action.Text = text
	}
}

func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
