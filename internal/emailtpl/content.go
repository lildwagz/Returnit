package emailtpl

import (
	"fmt"
	"strings"
	"time"
)

// Languages are the ones the app has. Anything else is written in Indonesian, the default.
func lang(l string) string {
	switch l {
	case "en", "ar":
		return l
	}
	return "id"
}

var months = map[string][12]string{
	"id": {"Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"},
	"en": {"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
	"ar": {"يناير", "فبراير", "مارس", "أبريل", "مايو", "يونيو", "يوليو", "أغسطس", "سبتمبر", "أكتوبر", "نوفمبر", "ديسمبر"},
}

var weekdays = map[string][7]string{ // Sunday first, like time.Weekday
	"id": {"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"},
	"en": {"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"},
	"ar": {"الأحد", "الاثنين", "الثلاثاء", "الأربعاء", "الخميس", "الجمعة", "السبت"},
}

// date is "Rabu, 30 November 2026" (with the weekday) or "30 November 2026".
func date(l string, t time.Time, weekday bool) string {
	s := fmt.Sprintf("%d %s %d", t.Day(), months[l][t.Month()-1], t.Year())
	if !weekday {
		return s
	}
	sep := ", "
	if l == "ar" {
		sep = "، "
	}
	return weekdays[l][t.Weekday()] + sep + s
}

// dateTime adds the clock and the zone abbreviation: "24 Desember 2026, 10:00 WIB".
func dateTime(l string, t time.Time) string {
	sep := ", "
	if l == "ar" {
		sep = "، "
	}
	return date(l, t, false) + sep + t.Format("15:04 MST")
}

// countdown is the pill on a reminder: "7 hari lagi", "besok".
func countdown(l string, days int) string {
	switch l {
	case "en":
		if days == 1 {
			return "tomorrow"
		}
		return fmt.Sprintf("in %d days", days)
	case "ar":
		switch days {
		case 1:
			return "غدًا"
		case 2:
			return "بعد يومين"
		}
		return fmt.Sprintf("بعد %d أيام", days)
	}
	if days == 1 {
		return "besok"
	}
	return fmt.Sprintf("%d hari lagi", days)
}

type reminderText struct {
	warrantyLabel, returnLabel, hintWarranty, hintReturn, footer string
}

var reminderTexts = map[string]reminderText{
	"id": {"Garansi berakhir", "Batas pengembalian", "Buka ReturnIt untuk melihat detail dan struk yang tersimpan.",
		"Masih ingin mengembalikannya? Lakukan sebelum tanggal itu.",
		"Email otomatis dari ReturnIt, mohon tidak dibalas. Atur pengingat di aplikasi: Pengaturan, Pengingat."},
	"en": {"Warranty ends", "Return deadline", "Open ReturnIt to see the details and the saved receipt.",
		"Still want to return it? Do it before that date.",
		"Automatic e-mail from ReturnIt, please do not reply. Change reminders in the app: Settings, Reminders."},
	"ar": {"ينتهي الضمان", "مهلة الإرجاع", "افتح ReturnIt لعرض التفاصيل والإيصال المحفوظ.",
		"هل ما زلت تريد إرجاعه؟ افعل ذلك قبل هذا التاريخ.",
		"رسالة تلقائية من ReturnIt، يرجى عدم الرد. يمكنك تغيير التذكيرات من التطبيق: الإعدادات، التذكيرات."},
}

// Reminder is the e-mail for one warranty or return-deadline reminder. title and body are the texts already stored
// for the reminder (in the person's language); end is the date it is about, days how many days are left.
func Reminder(language string, returnDeadline bool, title, body, product string, end time.Time, days int, logo, logoDark string) Mail {
	l := lang(language)
	tx := reminderTexts[l]
	label, hint := tx.warrantyLabel, tx.hintWarranty
	if returnDeadline {
		label, hint = tx.returnLabel, tx.hintReturn
	}
	m := mail{Lang: l, Title: title, Preheader: body, Footer: tx.footer, Logo: logo, LogoDark: logoDark,
		Blocks: []block{
			{Kind: kindFact, Label: label, Product: product, Date: date(l, end, true), Pill: countdown(l, days), Warn: days <= 7},
			{Kind: kindNote, Text: hint},
		}}
	return m.render(title)
}

type accountText struct {
	subject, title, intro, calloutLabel, body, hint, footer string
}

var accountTexts = map[string]map[string]accountText{
	"id": {
		"requested": {"Akun ReturnIt Anda akan dihapus", "Akun Anda akan dihapus",
			"Kami menerima permintaan untuk menghapus akun ReturnIt Anda.", "Dihapus permanen pada",
			"Akun dan seluruh isinya (pembelian, struk, pengingat, dan notifikasi) akan dihapus permanen. Setelah itu tidak ada yang bisa dipulihkan.",
			"Berubah pikiran? Buka aplikasi dan batalkan penghapusan sebelum waktu itu. Bukan Anda yang meminta? Batalkan penghapusan di aplikasi sekarang juga dan ganti kata sandi Anda.", ""},
		"reminder": {"Akun ReturnIt Anda akan dihapus besok", "Akun Anda akan dihapus besok",
			"Akun ReturnIt Anda dijadwalkan untuk dihapus permanen.", "Dihapus permanen pada",
			"Untuk mempertahankannya, buka aplikasi dan batalkan penghapusan sebelum waktu itu. Setelah itu tidak ada yang bisa dipulihkan.", "", ""},
		"erased": {"Akun ReturnIt Anda sudah dihapus", "Akun Anda sudah dihapus",
			"Sesuai permintaan Anda, akun ReturnIt dan seluruh datanya (pembelian, struk, pengingat, dan notifikasi) sudah dihapus permanen.", "",
			"Cadangan ditimpa dalam masa retensi yang tercantum di kebijakan retensi data kami. Tidak ada tindakan lain yang diperlukan dari Anda.", "", ""},
	},
	"en": {
		"requested": {"Your ReturnIt account will be deleted", "Your account will be deleted",
			"We received a request to delete your ReturnIt account.", "Permanently deleted on",
			"Your account and everything in it (purchases, receipts, reminders and notifications) will be permanently erased. After that nothing can be restored.",
			"Changed your mind? Open the app and cancel the deletion before then. Did not ask for this? Cancel the deletion in the app right away and change your password.", ""},
		"reminder": {"Your ReturnIt account will be erased tomorrow", "Your account will be erased tomorrow",
			"Your ReturnIt account is scheduled to be permanently erased.", "Permanently deleted on",
			"To keep it, open the app and cancel the deletion before then. After that nothing can be restored.", "", ""},
		"erased": {"Your ReturnIt account has been erased", "Your account has been erased",
			"As requested, your ReturnIt account and all its data (purchases, receipts, reminders and notifications) have been permanently erased.", "",
			"Backups are overwritten within the retention period described in our data retention policy. Nothing more is needed from you.", "", ""},
	},
	"ar": {
		"requested": {"سيتم حذف حسابك في ReturnIt", "سيتم حذف حسابك",
			"تلقينا طلبًا لحذف حسابك في ReturnIt.", "الحذف النهائي في",
			"سيتم حذف حسابك وكل ما فيه (المشتريات والإيصالات والتذكيرات والإشعارات) نهائيًا. وبعد ذلك لا يمكن استعادة أي شيء.",
			"غيّرت رأيك؟ افتح التطبيق وألغِ الحذف قبل ذلك الموعد. لم تطلب ذلك؟ ألغِ الحذف من التطبيق فورًا وغيّر كلمة المرور.", ""},
		"reminder": {"سيتم حذف حسابك في ReturnIt غدًا", "سيتم حذف حسابك غدًا",
			"حسابك في ReturnIt مجدول للحذف النهائي.", "الحذف النهائي في",
			"للاحتفاظ به، افتح التطبيق وألغِ الحذف قبل ذلك الموعد. وبعد ذلك لا يمكن استعادة أي شيء.", "", ""},
		"erased": {"تم حذف حسابك في ReturnIt", "تم حذف حسابك",
			"بناءً على طلبك، تم حذف حسابك في ReturnIt وجميع بياناته (المشتريات والإيصالات والتذكيرات والإشعارات) نهائيًا.", "",
			"تُستبدل النسخ الاحتياطية خلال مدة الاحتفاظ الموضحة في سياسة الاحتفاظ بالبيانات. لا يلزم منك أي إجراء آخر.", "", ""},
	},
}

var accountFooter = map[string]string{
	"id": "Email otomatis dari ReturnIt, mohon tidak dibalas.",
	"en": "Automatic e-mail from ReturnIt, please do not reply.",
	"ar": "رسالة تلقائية من ReturnIt، يرجى عدم الرد.",
}

// Account is a notice about account deletion; kind is "requested", "reminder" or "erased". at is the moment of
// erasure, already in the person's time zone.
func Account(language, kind string, at time.Time, logo, logoDark string) Mail {
	l := lang(language)
	tx, ok := accountTexts[l][kind]
	if !ok {
		tx = accountTexts[l]["erased"]
	}
	m := mail{Lang: l, Title: tx.title, Preheader: tx.intro, Footer: accountFooter[l], Logo: logo, LogoDark: logoDark}
	m.Blocks = append(m.Blocks, block{Kind: kindParagraph, Text: tx.intro})
	if tx.calloutLabel != "" {
		m.Blocks = append(m.Blocks, block{Kind: kindCallout, Label: tx.calloutLabel, Text: dateTime(l, at)})
	}
	m.Blocks = append(m.Blocks, block{Kind: kindParagraph, Text: tx.body})
	if tx.hint != "" {
		m.Blocks = append(m.Blocks, block{Kind: kindNote, Text: tx.hint})
	}
	return m.render(tx.subject)
}

// AssetURLs returns the logo addresses for a base such as https://dl.example.com/assets, or empty strings when no
// base is configured (the e-mail then shows the name as text).
func AssetURLs(base string) (logo, logoDark string) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return "", ""
	}
	return base + "/email-logo.png", base + "/email-logo-dark.png"
}
