// Package model: skill-related types shared by registry, repo, agent and
// handler. The wire shape here is stable across hard-coded and dynamic
// skills so the LLM only sees one tool-list format.
package model

import "time"

// Skill is the on-disk + wire shape. Built-in skills are projected into
// this struct by the registry; dynamic skills live in the DB.
type Skill struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Category        string    `json:"category"`
	ParametersJSON  string    `json:"parametersJson"` // JSON Schema for arguments
	HandlerKind     string    `json:"handlerKind"`    // builtin | http
	HandlerConfig   string    `json:"handlerConfig"`  // JSON
	Enabled         bool      `json:"enabled"`
	RequiresHuman   bool      `json:"requiresHuman"`  // must be human-confirmed
	ReadOnly        bool      `json:"readOnly"`       // cannot mutate
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// SkillInvocation is the audit row for one tool call. Independent of
// Skill state so it survives even if the skill is later disabled.
type SkillInvocation struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	SkillName      string    `json:"skillName"`
	ArgsJSON       string    `json:"argsJson"`
	ResultJSON     string    `json:"resultJson,omitempty"`
	Status         string    `json:"status"` // ok | pending_human | error | timeout
	PendingTicket  string    `json:"pendingTicket,omitempty"`
	TraceID        string    `json:"traceId"`
	StepIndex      int       `json:"stepIndex"`
	DurationMS     int64     `json:"durationMs"`
	CreatedAt      time.Time `json:"createdAt"`
}

// SkillPendingTicket represents a pending human confirmation. The user
// must call /api/skills/confirm with this id before the skill actually
// runs its mutation.
type SkillPendingTicket struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	UserID         string    `json:"userId"`
	SkillName      string    `json:"skillName"`
	PayloadJSON    string    `json:"payloadJson"`
	Summary        string    `json:"summary"`
	Status         string    `json:"status"`
	ExpiresAt      time.Time `json:"expiresAt"`
	CreatedAt      time.Time `json:"createdAt"`
	ConfirmedAt    *time.Time `json:"confirmedAt,omitempty"`
}