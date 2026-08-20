package utils

// Invariant asserts a condition is truthy, panics if not.
func Invariant(condition bool, message string) {
	if !condition {
		panic("Invariant violation: " + message)
	}
}

// AssertDefined asserts a value is not nil.
func AssertDefined[T any](value *T, message string) T {
	if value == nil {
		panic("Assertion failed: " + message)
	}
	return *value
}
