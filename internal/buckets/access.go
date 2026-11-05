// Package buckets holds what the rest of the backend needs to know about shared buckets: who may see a purchase.
package buckets

// Visible is the SQL condition that holds when the purchase column `col` (for example "p.bucket_id") belongs to a
// bucket that the user in query parameter `param` (for example "$1") is a member of. It is the one security boundary
// for purchases and their receipts: every query that looks a purchase up for a person uses it, and a purchase that does
// not match is reported as not found.
func Visible(col, param string) string {
	return col + " IN (SELECT bm.bucket_id FROM bucket_members bm WHERE bm.user_id = " + param + ")"
}
