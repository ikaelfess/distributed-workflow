package identity

import "time"

const (
	StatusUnverified = "unverified"
	StatusActive     = "active"
	StatusSuspended  = "suspended"

	AccessLevelStandard      = "standard"
	AccessLevelAdministrator = "administrator"

	ChallengeTypeVerification = "verification"

	AuditRegistrationAccepted    = "registration.accepted"
	AuditVerificationSucceeded   = "verification.succeeded"
	AuditVerificationFailed      = "verification.failed"
	AuditAuthenticationSucceeded = "authentication.succeeded"
	AuditAuthenticationFailed    = "authentication.failed"
	AuditValidationSucceeded     = "validation.succeeded"
	AuditValidationFailed        = "validation.failed"
	AuditRefreshSucceeded        = "authentication.refresh.succeeded"
	AuditRefreshRejected         = "authentication.refresh.rejected"
	AuditRefreshReuseDetected    = "authentication.refresh.reuse_detected"
	AuditLogoutSucceeded         = "authentication.logout.succeeded"
	AuditSessionRevoked          = "authentication.session.revoked"
	AuditSessionsRevokedAll      = "authentication.sessions.revoked_all"

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
