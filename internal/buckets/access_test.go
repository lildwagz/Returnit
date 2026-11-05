package buckets

import "testing"

func TestVisible(t *testing.T) {
	got := Visible("p.bucket_id", "$2")
	want := "p.bucket_id IN (SELECT bm.bucket_id FROM bucket_members bm WHERE bm.user_id = $2)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
