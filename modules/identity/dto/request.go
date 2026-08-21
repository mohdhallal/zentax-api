package dto

type LoginBody struct {
	Email    string `json:"email"    validate:"required,email" example:"admin@acme.com"`
	Password string `json:"password" validate:"required,min=1,max=200"`
}

// MfaCodeBody is the 6-digit TOTP code for enable / verify.
type MfaCodeBody struct {
	Code string `json:"code" validate:"required,len=6,numeric" example:"123456"`
}
