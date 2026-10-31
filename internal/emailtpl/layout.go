// Package emailtpl builds the e-mails the server sends: one shared layout (logo, card, footer), written as real
// e-mail HTML (tables, inline styles; dark mode through prefers-color-scheme) plus a plain-text version of the same
// content. Every value that comes from a person (a product name) is escaped by html/template.
package emailtpl

import (
	"bytes"
	"html/template"
	"strings"
)

// Block kinds of a mail body, in the order they are drawn.
const (
	kindParagraph = "p"
	kindNote      = "note" // smaller, muted
	kindFact      = "fact"
	kindCallout   = "callout"
	kindButton    = "button"
	kindFallback  = "fallback" // "if the button does not work" plus the plain link
)

type block struct {
	Kind string
	Text string
	// fact card
	Label, Product, Date, Pill string
	Warn                       bool // the pill is amber instead of blue
	// button / fallback
	URL string
}

type mail struct {
	Lang, Title, Preheader, Footer string
	Blocks                         []block
	// Logo is the absolute https address of the small logo (light) and LogoDark (dark clients); empty means the
	// name is drawn as text.
	Logo, LogoDark string
}

// Mail is a finished message: subject, plain text and HTML.
type Mail struct {
	Subject, Text, HTML string
}

func (m mail) Dir() string {
	if m.Lang == "ar" {
		return "rtl"
	}
	return "ltr"
}

func (m mail) Align() string {
	if m.Lang == "ar" {
		return "right"
	}
	return "left"
}

func (m mail) text() string {
	var b strings.Builder
	b.WriteString(m.Title + "\n\n")
	for _, bl := range m.Blocks {
		switch bl.Kind {
		case kindFact:
			b.WriteString(bl.Label + ": " + bl.Product + "\n" + bl.Date + " (" + bl.Pill + ")\n\n")
		case kindCallout:
			b.WriteString(bl.Label + ": " + bl.Text + "\n\n")
		case kindButton:
			b.WriteString(bl.Label + ":\n" + bl.URL + "\n\n")
		case kindFallback:
			// the button line above already carries the link
		default:
			b.WriteString(bl.Text + "\n\n")
		}
	}
	b.WriteString("--\n" + m.Footer + "\n")
	return b.String()
}

func (m mail) render(subject string) Mail {
	var out bytes.Buffer
	if err := layout.Execute(&out, m); err != nil {
		// The template is fixed and the data is plain strings: this cannot happen, and if it ever does the text
		// version still goes out on its own.
		return Mail{Subject: subject, Text: m.text()}
	}
	return Mail{Subject: subject, Text: m.text(), HTML: out.String()}
}

const (
	fontStack = `-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,'Noto Sans Arabic',sans-serif`
)

var layout = template.Must(template.New("layout").Funcs(template.FuncMap{"font": func() template.CSS { return fontStack }}).Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark">
<meta name="supported-color-schemes" content="light dark">
<title>{{.Title}}</title>
<style>@media (prefers-color-scheme: dark){.c-bg{background-color:#0B1220 !important;}.c-card{background-color:#131E33 !important;}.c-head{color:#F1F5F9 !important;}.c-body{color:#CBD5E1 !important;}.c-muted{color:#94A3B8 !important;}.c-line{border-color:#263552 !important;}.c-link{color:#60A5FA !important;}.c-btn{background-color:#60A5FA !important;}.c-btntext{color:#0B1220 !important;}.c-soft{background-color:#1B2A47 !important;}.c-warnbg{background-color:#3A2A0B !important;}.c-warntext{color:#FCD34D !important;}.c-okbg{background-color:#1E3A8A !important;}.c-oktext{color:#BFDBFE !important;}.c-dangerbg{background-color:#3B1517 !important;}.c-dangertext{color:#FCA5A5 !important;}.logo-light{display:none !important;}.logo-dark{display:block !important;}}</style>
</head>
<body class="c-bg" style="margin:0;padding:0;background:#F1F5F9;font-family:{{font}};">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">{{.Preheader}}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" class="c-bg" style="background:#F1F5F9;">
<tr><td align="center" style="padding:24px 12px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;">
<tr><td style="padding:0 4px 16px;text-align:{{.Align}};">{{if .Logo}}<img class="logo-light" src="{{.Logo}}" width="150" alt="ReturnIt" style="display:block;border:0;height:auto;">{{if .LogoDark}}<img class="logo-dark" src="{{.LogoDark}}" width="150" alt="ReturnIt" style="display:none;border:0;height:auto;">{{end}}{{else}}<span class="c-head" style="font-size:24px;font-weight:800;color:#17324D;">ReturnIt</span>{{end}}</td></tr>
<tr><td class="c-card c-line" style="background:#FFFFFF;border:1px solid #E2E8F0;border-radius:20px;padding:32px 28px;text-align:{{.Align}};">
<h1 class="c-head" style="margin:0 0 16px;font-size:24px;line-height:1.3;font-weight:800;color:#17324D;">{{.Title}}</h1>
{{range .Blocks}}{{if eq .Kind "p"}}<p class="c-body" style="margin:0 0 16px;font-size:16px;line-height:1.6;color:#334155;">{{.Text}}</p>
{{else if eq .Kind "note"}}<p class="c-muted" style="margin:0;font-size:14px;line-height:1.6;color:#5B6B80;">{{.Text}}</p>
{{else if eq .Kind "fact"}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:0 0 20px;"><tr><td class="c-soft c-line" style="background:#EFF4FA;border:1px solid #E2E8F0;border-radius:14px;padding:18px 20px;">
<p class="c-muted" style="margin:0 0 4px;font-size:13px;line-height:1.4;color:#5B6B80;">{{.Label}}</p>
<p class="c-head" style="margin:0 0 6px;font-size:19px;line-height:1.35;font-weight:700;color:#17324D;">{{.Product}}</p>
<p class="c-body" style="margin:0 0 12px;font-size:15px;line-height:1.5;color:#334155;">{{.Date}}</p>
{{if .Warn}}<span class="c-warnbg c-warntext" style="display:inline-block;padding:4px 12px;border-radius:999px;background:#FEF3C7;color:#92400E;font-size:14px;font-weight:700;line-height:20px;">{{.Pill}}</span>{{else}}<span class="c-okbg c-oktext" style="display:inline-block;padding:4px 12px;border-radius:999px;background:#DBEAFE;color:#1D4ED8;font-size:14px;font-weight:700;line-height:20px;">{{.Pill}}</span>{{end}}
</td></tr></table>
{{else if eq .Kind "callout"}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:0 0 20px;"><tr><td class="c-dangerbg c-dangertext" style="background:#FEE2E2;color:#991B1B;border-radius:14px;padding:16px 20px;">
<p style="margin:0 0 4px;font-size:15px;line-height:1.4;font-weight:700;">{{.Label}}</p>
<p style="margin:0;font-size:15px;line-height:1.5;">{{.Text}}</p>
</td></tr></table>
{{else if eq .Kind "button"}}<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:8px 0 20px;"><tr><td class="c-btn" align="center" bgcolor="#2563EB" style="border-radius:12px;background:#2563EB;"><a class="c-btntext" href="{{.URL}}" style="display:inline-block;min-height:48px;line-height:48px;padding:0 28px;font-size:16px;font-weight:700;color:#FFFFFF;text-decoration:none;border-radius:12px;">{{.Label}}</a></td></tr></table>
{{else if eq .Kind "fallback"}}<p class="c-muted" style="margin:0 0 16px;font-size:13px;line-height:1.5;color:#5B6B80;">{{.Text}}<br><a class="c-link" href="{{.URL}}" style="color:#2563EB;word-break:break-all;">{{.URL}}</a></p>
{{end}}{{end}}</td></tr>
<tr><td style="padding:20px 8px 0;text-align:{{.Align}};"><p class="c-muted" style="margin:0;font-size:12px;line-height:1.6;color:#5B6B80;">{{.Footer}}</p></td></tr>
</table>
</td></tr></table>
</body></html>`))
