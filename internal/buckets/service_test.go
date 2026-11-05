package buckets

import "testing"

func TestNormalizeTokenIgnoresCaseSpacesAndHyphens(t *testing.T) {
	want := hashToken("ABCDEFGH23456723")
	for _, in := range []string{"abcdefgh23456723", "ABCD-EFGH-2345-6723", " abcd efgh 2345 6723 ", "AbCd-EfGh\n23456723"} {
		if string(hashToken(in)) != string(want) {
			t.Errorf("%q hashes differently", in)
		}
	}
	if string(hashToken("ABCDEFGH23456724")) == string(want) {
		t.Error("different tokens must hash differently")
	}
}

func TestCleanName(t *testing.T) {
	for _, bad := range []string{"", "   ", "tab\there", string(make([]rune, 61))} {
		if _, err := cleanName(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	got, err := cleanName("  Rumah Kita  ")
	if err != nil || got != "Rumah Kita" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestOwnerOf(t *testing.T) {
	if err := ownerOf("owner", false); err != nil {
		t.Fatal(err)
	}
	if err := ownerOf("member", false); err != ErrNotOwner {
		t.Fatalf("member: %v", err)
	}
	if err := ownerOf("owner", true); err == nil {
		t.Fatal("a personal bucket has no owner-only actions")
	}
}
