package repositories

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEscapeLike(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"", ""},
		{"acme", "acme"},
		{"100%", `100\%`},
		{"Under_score", `Under\_score`},
		{`O'Brien \ Partners`, `O'Brien \\ Partners`},
		// The backslash is escaped FIRST, so an already-escaped-looking input
		// still matches itself literally rather than turning into an escape.
		{`\%`, `\\\%`},
		{`%_\`, `\%\_\\`},
		{"Müller & Söhne", "Müller & Söhne"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, EscapeLike(tc.in), "input %q", tc.in)
	}
}
