// Package support — prompt.go
//
// System + user prompt templates for the Claude classifier (XOL-55 SUP-4).
// No external dependencies; callers pass the rendered strings to the LLM.
package support

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// System prompt
// ---------------------------------------------------------------------------

// classifierSystemPrompt is the system message sent to Claude.
// It explains xolto, the taxonomy enums, and the exact JSON schema to emit.
const classifierSystemPrompt = `You are the support classifier for xolto, a used-electronics buying copilot focused on the BG/OLX market.

Your job: read a customer support thread and output a single JSON object with EXACTLY these keys:

{
  "category":    <one of the category enum values>,
  "market":      <one of the market enum values>,
  "product_cat": <one of the product_cat enum values>,
  "severity":    <one of the severity enum values>,
  "action_needed": <one of the action_needed enum values>
}

ENUM VALUES — you MUST use exactly these strings:

category:
  pricing, listing_wrong, verdict, marketplace, login, billing, bug, feature, general

market:
  olx_bg, marktplaats, vinted_nl, vinted_dk, unknown

product_cat:
  camera, laptop, phone, audio, gaming, tablet, appliance, other

severity:
  low, medium, high, incident
  Use "incident" only for: service outages, authentication failures (can't log in), security issues, double-billing, legal threats.

action_needed:
  reply_only, backend_fix, dash_fix, scorer_fix, scraper_fix, billing_auth_fix, product_clarification, roadmap_candidate

ROUTING NOTES:
- pricing/listing_wrong + olx_bg → action_needed = scraper_fix or backend_fix
- login/billing issues → action_needed = billing_auth_fix
- feature requests → action_needed = roadmap_candidate
- general questions → action_needed = reply_only

OUTPUT: Emit ONLY the JSON object — no prose, no markdown, no code fences.`

// ---------------------------------------------------------------------------
// User prompt builder
// ---------------------------------------------------------------------------

// BuildClassifierUserPrompt constructs the user message for the Claude
// classifier from the thread body, subject, and customer email.
func BuildClassifierUserPrompt(body, subject, customerEmail string) string {
	var sb strings.Builder
	sb.WriteString("SUPPORT THREAD\n")
	sb.WriteString("==============\n")
	if subject != "" {
		sb.WriteString(fmt.Sprintf("Subject: %s\n", subject))
	}
	if customerEmail != "" {
		sb.WriteString(fmt.Sprintf("Customer: %s\n", customerEmail))
	}
	sb.WriteString("\n")
	sb.WriteString(body)
	sb.WriteString("\n\nClassify this thread using the schema above.")
	return sb.String()
}

// ClassifierSystemPrompt returns the system prompt string. Exported so tests
// can verify its contents without duplicating the constant.
func ClassifierSystemPrompt() string {
	return classifierSystemPrompt
}

// ---------------------------------------------------------------------------
// Draft-note prompt builder
// ---------------------------------------------------------------------------

// BuildDraftNotePrompt constructs the prompt used to generate a draft reply
// in the thread's language. The language hint should be "bg", "nl", or "en".
func BuildDraftNotePrompt(body, subject, customerEmail, langHint string, category, action string) string {
	lang := "English"
	switch strings.ToLower(langHint) {
	case "bg":
		lang = "Bulgarian"
	case "nl":
		lang = "Dutch"
	}

	return fmt.Sprintf(`You are a helpful support agent for xolto, a used-electronics buying copilot.

Write a SHORT (2-4 sentence) draft reply for this support thread.
Language: %s
Category: %s
Suggested action: %s

Do NOT send the reply — a human will review and send it.
Do NOT include a subject line or greeting salutation.
Be empathetic, concise, and specific to the issue.

THREAD
======
Subject: %s
Customer: %s

%s

Draft reply:`, lang, category, action, subject, customerEmail, body)
}
