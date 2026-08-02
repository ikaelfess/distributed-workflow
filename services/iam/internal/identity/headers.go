package identity

// Trusted identity headers propagated after successful online validation.
// Callers must never be trusted to supply these; IAM Check overwrites them
// (Envoy append=false) only after ValidateAccessToken succeeds.
const (
	HeaderUserID                  = "x-identity-user-id"
	HeaderSubjectKind             = "x-identity-subject-kind"
	HeaderAccessLevel             = "x-identity-access-level"
	HeaderAuthenticationSessionID = "x-identity-authentication-session-id"
	HeaderAccessTokenExpiresAt    = "x-identity-access-token-expires-at"
)

// TrustedIdentityHeaders lists every header Check overwrites on success.
func TrustedIdentityHeaders() []string {
	return []string{
		HeaderUserID,
		HeaderSubjectKind,
		HeaderAccessLevel,
		HeaderAuthenticationSessionID,
		HeaderAccessTokenExpiresAt,
	}
}
