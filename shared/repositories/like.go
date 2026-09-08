package repositories

import "strings"

// EscapeLike escapes the LIKE metacharacters (% and _) and the escape
// character itself so a search term matches literally under
// `ILIKE $n ESCAPE '\'`. Every ILIKE predicate in the code base pairs with it:
// a term like "100%" or "Under_score" must find those names, not everything.
func EscapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
