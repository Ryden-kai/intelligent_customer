// Package model defines wire-shape structs shared by repository, service and
// handler layers. Keep this package thin: no business logic, no SQL.
package model

import "time"

// Intent is the canonical set of user intents recognised by the system.
type Intent string

const (
	IntentRefund Intent = "refund"
	IntentOrder  Intent = "order"
	IntentTech   Intent = "tech"
	IntentOther  Intent = "other"
	IntentUnknown Intent = "unknown"
)

type ConvStatus string

const (
	ConvStatusOpen       ConvStatus = "open"
	ConvStatusHandedOver ConvStatus = "handed_over"
	ConvStatusClosed     ConvStatus = "closed"
)

type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
	RoleAgent     MessageRole = "agent" // human agent reply after handover
)

type Conversation struct {
	ID         string     `json:"id"`
	UserID     string     `json:"userId"`
	Title      string     `json:"title"`
	Status     ConvStatus `json:"status"`
	HandedOver bool       `json:"handedOver"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

type Message struct {
	ID              string      `json:"id"`
	ConversationID  string      `json:"conversationId"`
	Role            MessageRole `json:"role"`
	Content         string      `json:"content"`
	Intent          Intent      `json:"intent,omitempty"`
	IntentConfidence *float64   `json:"intentConfidence,omitempty"`
	Model           string      `json:"model,omitempty"`
	CreatedAt       time.Time   `json:"createdAt"`
}

type Feedback struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	Rating         int       `json:"rating"`
	Comment        string    `json:"comment"`
	CreatedAt      time.Time `json:"createdAt"`
}

type FAQ struct {
	ID        string    `json:"id"`
	Category  Intent    `json:"category"`
	Question  string    `json:"question"`
	Answer    string    `json:"answer"`
	Keywords  []string  `json:"keywords"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
}

type HandoverSignal struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	Source         string    `json:"source"`
	Detail         string    `json:"detail"`
	CreatedAt      time.Time `json:"createdAt"`
}

// AdminUser is an admin-backend account. PasswordHash stores a PHC-encoded
// Argon2id string; the plaintext is never persisted.
type AdminUser struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"` // never serialise
	Role         string     `json:"role"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	LastLoginAt  *time.Time `json:"lastLoginAt,omitempty"`
}