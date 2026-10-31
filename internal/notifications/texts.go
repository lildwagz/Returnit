package notifications

import (
	"fmt"
	"time"
)

// Languages the reminders are written in; the same set as the app. Indonesian is the default.
var Languages = []string{"id", "en", "ar"}

// IsLanguage reports whether l is one of Languages.
func IsLanguage(l string) bool {
	for _, x := range Languages {
		if x == l {
			return true
		}
	}
	return false
}

// Texts returns the title and body of a reminder in the given language (unknown or empty: Indonesian). kind is
// KindWarranty or KindReturn, days the offset before end.
func Texts(lang, kind, product string, days int, end time.Time) (title, body string) {
	date := end.Format("2006-01-02")
	ret := kind == KindReturn
	switch lang {
	case "en":
		when := fmt.Sprintf("in %d days", days)
		if days == 1 {
			when = "tomorrow"
		}
		if ret {
			return "Return deadline ending soon", fmt.Sprintf("The return period for %s ends %s (%s).", product, when, date)
		}
		return "Warranty ending soon", fmt.Sprintf("The warranty for %s ends %s (%s).", product, when, date)
	case "ar":
		when := fmt.Sprintf("خلال %d أيام", days)
		switch days {
		case 1:
			when = "غدًا"
		case 2:
			when = "بعد يومين"
		}
		if ret {
			return "اقتراب انتهاء مهلة الإرجاع", fmt.Sprintf("تنتهي مهلة إرجاع %s %s (%s).", product, when, date)
		}
		return "اقتراب انتهاء الضمان", fmt.Sprintf("ينتهي ضمان %s %s (%s).", product, when, date)
	default:
		when := fmt.Sprintf("dalam %d hari", days)
		if days == 1 {
			when = "besok"
		}
		if ret {
			return "Batas pengembalian segera berakhir", fmt.Sprintf("Batas pengembalian %s berakhir %s (%s).", product, when, date)
		}
		return "Garansi segera berakhir", fmt.Sprintf("Garansi %s berakhir %s (%s).", product, when, date)
	}
}
