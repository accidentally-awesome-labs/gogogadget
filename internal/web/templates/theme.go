package templates

// emailStyle holds the inline style strings transactional email needs. Mail
// clients strip <style> and cannot read CSS custom properties, so email is the
// one surface that inlines colour — from here, so a rebrand reaches it too.
//
// Values mirror the light-mode tokens in input.css: Brand/Link are
// --color-brand-600, Card is --color-surface on --color-fg, Page is
// --color-surface-raised, Body is --color-fg-muted, Divider is --color-border.
// Muted (#94a3b8) is the dark theme's --color-fg-muted, used here as the
// low-contrast grey for footers on a permanently light email surface.
//
// Only the attributes that carry a colour live here. The hex-free ones in
// emails.templ (margins, font sizes, line heights) stay literal: inline
// spacing in email cannot be tokenized and centralizing it buys no rebrand.
var emailStyle = struct {
	Page, Card, Brand, Footer, Link,
	DigestRow, DigestLink, DigestBody, DigestMeta, Manage, MutedLink string
}{
	Page: "background:#f8fafc;padding:24px 0;",
	Card: "background:#ffffff;border-radius:12px;padding:32px;" +
		"font-family:system-ui,-apple-system,sans-serif;color:#0f172a;",
	Brand:      "font-weight:700;font-size:18px;color:#4f46e5;margin-bottom:24px;",
	Footer:     "color:#94a3b8;font-size:12px;margin-top:32px;",
	Link:       "color:#4f46e5;",
	DigestRow:  "padding:0 0 16px;border-bottom:1px solid #e2e8f0;",
	DigestLink: "color:#4f46e5;text-decoration:none;",
	DigestBody: "color:#475569;font-size:14px;line-height:1.5;",
	DigestMeta: "color:#94a3b8;font-size:12px;margin-top:4px;",
	Manage:     "margin:16px 0 0;color:#94a3b8;font-size:12px;",
	MutedLink:  "color:#94a3b8;",
}
