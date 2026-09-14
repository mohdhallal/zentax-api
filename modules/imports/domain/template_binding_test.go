package domain

import "testing"

// The generated template writes Field.Name as its header row, so a field whose
// own name is not an accepted spelling produces a sheet the product hands out
// and then refuses to read. That happened once, to paymentFixedDates, and it is
// the kind of defect that can only appear when someone adds a field — by which
// time nobody remembers the rule. So the rule is a test.
func TestEveryFieldNameBindsAsItsOwnHeader(t *testing.T) {
	for _, target := range []Target{TargetEntities, TargetObligations} {
		for _, f := range Fields(target) {
			got, ok := FieldForHeader(target, f.Name)
			if !ok {
				t.Errorf("%s: the template's own header %q binds to no field — add it to that field's aliases", target, f.Name)
				continue
			}
			if got != f.Name {
				t.Errorf("%s: the header %q binds to %q rather than to itself", target, f.Name, got)
			}
		}
	}
}
