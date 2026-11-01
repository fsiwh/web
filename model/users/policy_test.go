package users

import "testing"

func TestConfiguredDomainPoliciesRemainSeparate(t *testing.T) {
	s := NewService(nil, "https://example.org", DomainPolicy{Registration: []string{"students.example.org"}, Accounts: []string{"students.example.org", "council.example.org"}})
	for _, email := range []string{"person@STUDENTS.EXAMPLE.ORG", "member@council.example.org"} {
		if msg := s.EmailError(email); msg != "" {
			t.Fatal(msg)
		}
	}
	for _, email := range []string{"person@students.example.org.attacker.example.invalid", "person@sub.students.example.org", "person@students.example.invalid"} {
		if s.EmailError(email) == "" {
			t.Fatalf("unexpected domain accepted: %s", email)
		}
	}
	_, emailError, _ := s.RegistrationErrors("Member", "member@council.example.org", "password")
	if emailError == "" {
		t.Fatal("invitation domain allowed for self-registration")
	}
	s.domains.Registration = nil
	_, emailError, _ = s.RegistrationErrors("Member", "member@students.example.org", "password")
	if emailError == "" {
		t.Fatal("empty registration policy allowed registration")
	}
}
