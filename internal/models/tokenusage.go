package models

type TokenUsage struct {
	ID               uint   `gorm:"primaryKey" json:"id"`
	UserID           string `gorm:"not null;index" json:"user_id"`
	Service          string `gorm:"not null" json:"service"`
	Model            string `gorm:"not null" json:"model"`
	PromptTokens     int    `gorm:"not null;default:0" json:"prompt_tokens"`
	CompletionTokens int    `gorm:"not null;default:0" json:"completion_tokens"`
	TotalTokens      int    `gorm:"not null;default:0" json:"total_tokens"`
	CreatedAt        int64  `gorm:"autoCreateTime" json:"created_at"`
}
