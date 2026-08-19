package events

import (
	"reflect"
	"testing"
)

func TestSubjectCovered(t *testing.T) {
	cases := []struct {
		name     string
		existing []string
		want     string
		covered  bool
	}{
		{"exact match", []string{"subscription.>"}, "subscription.>", true},
		{"wildcard covers narrower want", []string{"subscription.>"}, "subscription.created", true},
		{"wildcard does not cover unrelated subject", []string{"subscription.>"}, "email.>", false},
		{"empty existing covers nothing", nil, "email.>", false},
		{"non-wildcard existing does not cover a different literal", []string{"subscription.created"}, "subscription.updated", false},
		{"wildcard covers itself", []string{"email.>"}, "email.>", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := subjectCovered(c.existing, c.want)
			if got != c.covered {
				t.Errorf("subjectCovered(%v, %q) = %v, want %v", c.existing, c.want, got, c.covered)
			}
		})
	}
}

func TestMissingSubjects(t *testing.T) {
	cases := []struct {
		name     string
		existing []string
		want     []string
		missing  []string
	}{
		{
			name:     "nothing missing when already covered",
			existing: []string{"subscription.>", "email.>"},
			want:     []string{"subscription.>", "email.>"},
			missing:  nil,
		},
		{
			name:     "the real 2026-08-19 bug: existing stream predates email.>",
			existing: []string{"subscription.>"},
			want:     RequiredSubjects,
			missing:  []string{"email.>"},
		},
		{
			name:     "brand-new stream is missing everything",
			existing: nil,
			want:     []string{"subscription.>", "email.>"},
			missing:  []string{"subscription.>", "email.>"},
		},
		{
			name:     "partial overlap only reports the gap",
			existing: []string{"subscription.>", "other.>"},
			want:     []string{"subscription.>", "email.>"},
			missing:  []string{"email.>"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := missingSubjects(c.existing, c.want)
			if !reflect.DeepEqual(got, c.missing) {
				t.Errorf("missingSubjects(%v, %v) = %v, want %v", c.existing, c.want, got, c.missing)
			}
		})
	}
}
