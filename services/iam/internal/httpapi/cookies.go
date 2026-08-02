package httpapi

import (
	"net/http"
	"time"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
)

const (
	AccessTokenCookieName  = "iam_access_token"
	RefreshTokenCookieName = "iam_refresh_token"
	CSRFTokenCookieName    = "iam_csrf"
	CSRFTokenHeaderName    = "X-CSRF-Token"
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
	if credentials.CSRFToken != "" {
		setCSRFCookie(w, credentials.CSRFToken, credentials.RefreshTokenExpiresAt)
	}
}

func setCSRFCookie(w http.ResponseWriter, csrfToken string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFTokenCookieName,
		Value:    csrfToken,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: false,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearCredentialCookies(w http.ResponseWriter) {
	expired := time.Unix(0, 0).UTC()
	for _, name := range []string{
		AccessTokenCookieName,
		RefreshTokenCookieName,
		CSRFTokenCookieName,
	} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			Expires:  expired,
			MaxAge:   -1,
			HttpOnly: name != CSRFTokenCookieName,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		})
	}
}

func cookieValue(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil || cookie == nil {
		return ""
	}
	return cookie.Value
}
