package main

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// passwordEnv is the environment variable seed-admin reads the admin password
// from when --password is not given. In deployed environments ECS injects it
// from Secrets Manager, so the password never appears in a task definition,
// a CloudTrail RunTask record, or a shell history.
const passwordEnv = "SEED_ADMIN_PASSWORD"

// The same length-based policy POST /auth/accept-invite applies
// (dto.AcceptInviteBody: NIST 800-63B, min 12 / max 200 characters).
const (
	minPasswordLen = 12
	maxPasswordLen = 200
)

var errNoPassword = errors.New("admin password not set: pass --password or set " + passwordEnv +
	" (the environment variable is preferred in deployed environments)")

// resolvePassword picks the admin password: the --password flag when given,
// otherwise SEED_ADMIN_PASSWORD from the environment. It fails when neither
// is set and enforces the length policy. Errors never echo the password.
func resolvePassword(flagValue string, getenv func(string) string) (string, error) {
	source := "--password"
	password := flagValue
	if password == "" {
		source = passwordEnv
		password = getenv(passwordEnv)
	}
	if password == "" {
		return "", errNoPassword
	}
	if n := utf8.RuneCountInString(password); n < minPasswordLen {
		return "", fmt.Errorf("%s: password too short: %d characters, minimum %d", source, n, minPasswordLen)
	} else if n > maxPasswordLen {
		return "", fmt.Errorf("%s: password too long: %d characters, maximum %d", source, n, maxPasswordLen)
	}
	return password, nil
}
