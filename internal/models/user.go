package models

type User struct {
	ID           string  `gorm:"primaryKey" json:"id"`
	Email        string  `gorm:"unique;not null" json:"email"`
	PasswordHash string  `gorm:"not null" json:"-"`
	APIKey       *string `gorm:"unique" json:"api_key"`
	DailyLimit   int     `gorm:"default:100" json:"daily_limit"`
	CreatedAt    int64   `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    int64   `gorm:"autoUpdateTime" json:"updated_at"`
}
