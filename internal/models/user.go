package models

import "time"

type User struct {
	ID             string
	Email          string
	PasswordHash   string
	Name           string
	Tier           string
	StripeCustomer string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
