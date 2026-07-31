package identity

import (
	"errors"
	"strings"
)

type EmailAddress struct {
	canonical string
}

func CanonicalEmailAddress(value string) (EmailAddress, error) {
	canonical := strings.ToLower(strings.TrimSpace(value))
	if canonical == "" {
		return EmailAddress{}, errors.New("email address is required")
	}

	at := strings.LastIndex(canonical, "@")
	if at <= 0 || at == len(canonical)-1 {
		return EmailAddress{}, errors.New("email address is invalid")
	}
	if strings.Contains(canonical[:at], " ") || strings.Contains(canonical[at+1:], " ") {
		return EmailAddress{}, errors.New("email address is invalid")
	}
	if !strings.Contains(canonical[at+1:], ".") {
		return EmailAddress{}, errors.New("email address is invalid")
	}

	return EmailAddress{canonical: canonical}, nil
}

func (e EmailAddress) String() string {
	return e.canonical
}
