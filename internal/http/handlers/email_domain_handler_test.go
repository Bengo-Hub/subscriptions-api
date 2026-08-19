package handlers

import (
	"net"
	"testing"
)

func TestMxHostMatches(t *testing.T) {
	cases := []struct {
		name     string
		records  []*net.MX
		expected string
		want     bool
	}{
		{"exact match with trailing dot", []*net.MX{{Host: "mx1.codevertexafrica.com."}}, "mx1.codevertexafrica.com", true},
		{"exact match without trailing dot on either side", []*net.MX{{Host: "mx1.codevertexafrica.com"}}, "mx1.codevertexafrica.com.", true},
		{"case-insensitive", []*net.MX{{Host: "MX1.CodeVertexAfrica.com."}}, "mx1.codevertexafrica.com", true},
		{"wrong host (Google Workspace, the real-world failure case)", []*net.MX{{Host: "smtp.google.com."}}, "mx1.codevertexafrica.com", false},
		{"one of several records matches", []*net.MX{{Host: "smtp.google.com."}, {Host: "mx1.codevertexafrica.com."}}, "mx1.codevertexafrica.com", true},
		{"no records at all", nil, "mx1.codevertexafrica.com", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mxHostMatches(c.records, c.expected); got != c.want {
				t.Errorf("mxHostMatches(%v, %q) = %v, want %v", c.records, c.expected, got, c.want)
			}
		})
	}
}

func TestAnyContains(t *testing.T) {
	cases := []struct {
		name    string
		records []string
		substr  string
		want    bool
	}{
		{"found in first record", []string{"v=spf1 ip4:77.237.232.66 ~all"}, "v=spf1", true},
		{"found in second of several records", []string{"google-site-verification=abc", "v=spf1 ip4:77.237.232.66 ~all"}, "77.237.232.66", true},
		{"not found", []string{"google-site-verification=abc"}, "v=spf1", false},
		{"empty records", nil, "v=spf1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := anyContains(c.records, c.substr); got != c.want {
				t.Errorf("anyContains(%v, %q) = %v, want %v", c.records, c.substr, got, c.want)
			}
		})
	}
}
