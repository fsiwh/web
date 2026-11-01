package users

import "testing"

func TestEmailEligibility(t *testing.T) {
	for _, email := range []string{
		"name@students.example.invalid", "name@council.example.invalid", "Name@STUDENTS.EXAMPLE.INVALID",
		"first.last+tag@council.example.invalid", `"name@elsewhere"@council.example.invalid`,
	} {
		if err := validateEmail(email, []string{"students.example.invalid", "council.example.invalid"}); err != "" {
			t.Errorf("%q rejected: %s", email, err)
		}
	}
	for _, email := range []string{
		"", "name", "name@external.example.invalid", "name@university.example.invalid",
		"name@sub.council.example.invalid", "name@council.example.invalid.example.org", "name@notcouncil.example.invalid",
		"name@students.example.invalid.example.org", "name@council.example.invalid.",
		"Name <name@council.example.invalid>", "name@council.example.invalid (Name)",
	} {
		if validateEmail(email, []string{"students.example.invalid", "council.example.invalid"}) == "" {
			t.Errorf("%q must be rejected", email)
		}
	}
}
