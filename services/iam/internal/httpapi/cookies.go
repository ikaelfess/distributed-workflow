package httpapi

import (
	"net/http"
	"time"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
)

const (
	AccessTokenCookieName  = "iam_access_token"
	RefreshTokenCookieName = "iam_refresh_token"
)

func setCredentialCookies(w http.ResponseWriter, credentials identity.IssuedCredentials) {
	http.SetCookie(w, &http.Cookie{
		Name:     AccessTokenCookieName,
		Value:    credentials.AccessToken,
		Path:     "/",
		Expires:  credentials.AccessTokenExpiresAt,
		MaxAge:   int(time.Until(credentials.AccessTokenExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshTokenCookieName,
		Value:    credentials.RefreshToken,
		Path:     "/",
		Expires:  credentials.RefreshTokenExpiresAt,
		MaxAge:   int(time.Until(credentials.RefreshTokenExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}
