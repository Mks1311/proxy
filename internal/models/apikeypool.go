package models

type APIKeyPool struct {
	ID            uint    `gorm:"primaryKey" json:"id"`
	APIKey        string  `gorm:"unique;not null" json:"api_key"`
	Service       string  `gorm:"not null;index" json:"service"`
	RateLimit     int     `gorm:"not null;default:100" json:"rate_limit"`
	RequestsToday int     `gorm:"not null;default:0" json:"requests_today"`
	OwnerUserId   *string `gorm:"not null;index" json:"owner_user_id"`
	IsActive      bool    `gorm:"default:true" json:"is_active"`
	CreatedAt     int64   `gorm:"autoCreateTime" json:"created_at"`
}
