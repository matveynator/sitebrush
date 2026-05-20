package dirprotect

import "testing"

func TestFindBestRuleUsesPathBoundaries(t *testing.T) {
	rules := []Rule{
		{Domain: "localhost", Path: "/passport", PasswordHash: Hash("secret")},
		{Domain: "localhost", Path: "/passport/deep", PasswordHash: Hash("deeper")},
	}

	rule, found := FindBestRule("localhost", "/passport/deep/page", rules)
	if !found {
		t.Fatal("rule not found")
	}
	if rule.Path != "/passport/deep" {
		t.Fatalf("rule path = %q, want %q", rule.Path, "/passport/deep")
	}

	if _, found := FindBestRule("localhost", "/passports", rules); found {
		t.Fatal("sibling path matched protected prefix")
	}
}

func TestPrefixFileRoundTrip(t *testing.T) {
	rule := Rule{Domain: "localhost", Path: "/passport", PasswordHash: Hash("secret")}
	body := PrefixFileBody([]Rule{rule})
	parsedRule, found := FindBestRuleInPrefixData("localhost", "/passport/one", body)
	if !found {
		t.Fatal("prefix rule not found")
	}
	if parsedRule.Path != rule.Path || parsedRule.PasswordHash != rule.PasswordHash {
		t.Fatalf("parsed rule = %#v, want %#v", parsedRule, rule)
	}
	if !Matches(parsedRule.PasswordHash, "secret") {
		t.Fatal("password did not match parsed hash")
	}
}
