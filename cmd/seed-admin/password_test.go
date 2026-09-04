package main

import (
	"errors"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolvePassword(t *testing.T) {
	const good = "correct-horse-battery" // 21 chars
	tests := []struct {
		name    string
		flag    string
		env     map[string]string
		want    string
		wantErr string
	}{
		{name: "flag wins", flag: good, env: map[string]string{passwordEnv: "env-password-value"}, want: good},
		{name: "env when flag absent", env: map[string]string{passwordEnv: good}, want: good},
		{name: "neither set names both", wantErr: "--password"},
		{name: "empty env counts as unset", env: map[string]string{passwordEnv: ""}, wantErr: passwordEnv},
		{name: "flag too short", flag: "short", wantErr: "--password: password too short: 5 characters, minimum 12"},
		{name: "env too short", env: map[string]string{passwordEnv: "short"}, wantErr: passwordEnv + ": password too short"},
		{name: "exactly minimum", flag: strings.Repeat("a", minPasswordLen), want: strings.Repeat("a", minPasswordLen)},
		{name: "multibyte counted in runes", flag: strings.Repeat("é", minPasswordLen), want: strings.Repeat("é", minPasswordLen)},
		{name: "too long", flag: strings.Repeat("a", maxPasswordLen+1), wantErr: "password too long: 201 characters, maximum 200"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolvePassword(tc.flag, envOf(tc.env))
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (password %q)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				if got != "" {
					t.Fatalf("expected empty password on error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolvePasswordNeitherSetNamesBothSources(t *testing.T) {
	_, err := resolvePassword("", envOf(nil))
	if !errors.Is(err, errNoPassword) {
		t.Fatalf("expected errNoPassword, got %v", err)
	}
	for _, want := range []string{"--password", passwordEnv} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should name %s", err.Error(), want)
		}
	}
}

func TestResolvePasswordErrorsNeverEchoPassword(t *testing.T) {
	for _, secret := range []string{"hunter2", strings.Repeat("Z", maxPasswordLen+1)} {
		_, err := resolvePassword(secret, envOf(nil))
		if err == nil {
			t.Fatalf("expected error for %d-char password", len(secret))
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error %q echoes the password", err.Error())
		}
		_, err = resolvePassword("", envOf(map[string]string{passwordEnv: secret}))
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("env path: error %v should fail without echoing the password", err)
		}
	}
}
