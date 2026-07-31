package identity

import "time"

const (
	StatusUnverified = "unverified"
	StatusActive     = "active"
	StatusSuspended  = "suspended"

	AccessLevelStandard      = "standard"
	AccessLevelAdministrator = "administrator"

	ChallengeTypeVerification = "verification"

	AuditRegistrationAccepted  = "registration.accepted"
	AuditVerificationSucceeded = "verification.succeeded"
	AuditVerificationFailed    = "verification.failed"

	RetentionRoutine   = "routine"
	RetentionSensitive = "sensitive"

	EmailTemplateVerify = "verify-email"
)

type User struct {
	ID              string
	Email           string
	PasswordHash    string
	Status          string
	AccessLevel     string
	EmailVerifiedAt *time.Time
}
