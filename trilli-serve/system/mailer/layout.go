package mailer

// This file defines the single shared visual system for every transactional
// email Trilli sends. Before it existed, each email hand-rolled its own
// <!DOCTYPE> shell with one of three different header/button styles and no
// <head>/viewport — which is why desktop webmail rendered them inconsistently.
//
// Everything now flows through emailShell(): one bulletproof, mobile-safe
// document with a branded indigo header (the CID logo), one button style, and
// one footer carrying Privacy · Terms · copyright · manage-preferences
// (unsubscribe) — consistent across the whole product.

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// Brand tokens — the single source of truth for email styling.
const (
	brandIndigo  = "#635BFF" // primary brand color (header + buttons + links)
	brandInk     = "#1A1F36" // headings + bold emphasis (Stripe-style near-black navy)
	brandBody    = "#525F7F" // body copy (soft blue-gray — clear, not harsh)
	brandMuted   = "#8792A2" // footer / secondary
	brandFaint   = "#CBD5E1" // copyright line
	brandRule    = "#E2E8F0" // dividers / card borders (visible but subtle)
	brandCanvas  = "#EEF1F6" // page background behind the card
	brandCardBg  = "#FFFFFF"
	brandLinkAlt = "#2563EB" // raw copyable links

	legalEntity = "Trilli Media LLC"
	fontStack   = "-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif"

	// Absolute targets used in the footer. The preferences link is login-gated:
	// clicking it sends the recipient through sign-in and lands them on the
	// notification settings where they can turn emails off ("unsubscribe").
	appBase    = "https://app.trilli.com"
	urlPrivacy = appBase + "/privacy"
	urlTerms   = appBase + "/terms"
	urlPrefs   = appBase + "/settings/notifications"
)

// esc is a short alias for html.EscapeString — every dynamic field is escaped.
func esc(s string) string { return html.EscapeString(s) }

// currentYear renders the copyright year at send time so it never goes stale.
func currentYear() string { return fmt.Sprintf("%d", time.Now().Year()) }

// shellParams configures one rendered email. BodyHTML is trusted markup built
// from the helpers below (pText, emailButton, …); plain strings inside it must
// already be escaped by the caller.
type shellParams struct {
	Preheader    string // hidden inbox-preview text (plain, will be escaped)
	Heading      string // the H1 (plain, will be escaped)
	HeadingColor string // optional override (e.g. red for the final lapse notice); defaults to ink
	BodyHTML     string // pre-built body markup
	FooterNote   string // optional contextual "why you got this" line (pre-built HTML)
}

// emailShell renders the full, client-bulletproof HTML document: a real <head>
// with viewport + resets + a mobile media query, a 600px centered card with the
// branded header, the body, and the unified footer.
func emailShell(p shellParams) string {
	headingColor := p.HeadingColor
	if strings.TrimSpace(headingColor) == "" {
		headingColor = brandInk
	}

	preheader := ""
	if s := strings.TrimSpace(p.Preheader); s != "" {
		// Hidden preview text, padded so the client doesn't pull body copy in.
		preheader = `<div style="display:none;font-size:1px;color:` + brandCanvas + `;line-height:1px;max-height:0;max-width:0;opacity:0;overflow:hidden;mso-hide:all;">` +
			esc(s) + `&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;&nbsp;&zwnj;</div>`
	}

	footerNote := ""
	if strings.TrimSpace(p.FooterNote) != "" {
		footerNote = `<p style="margin:0 0 16px 0;font-size:12.5px;line-height:1.6;color:` + brandMuted + `;font-family:` + fontStack + `;">` + p.FooterNote + `</p>`
	}

	return `<!DOCTYPE html>
<html lang="en" xmlns="http://www.w3.org/1999/xhtml">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="X-UA-Compatible" content="IE=edge">
<meta name="x-apple-disable-message-reformatting">
<title>Trilli</title>
<!--[if mso]><style>*{font-family:Arial,Helvetica,sans-serif !important;}</style><![endif]-->
<style>
  body,table,td,a{-webkit-text-size-adjust:100%;-ms-text-size-adjust:100%;}
  table,td{mso-table-lspace:0pt;mso-table-rspace:0pt;}
  img{-ms-interpolation-mode:bicubic;border:0;height:auto;line-height:100%;outline:none;text-decoration:none;}
  body{margin:0 !important;padding:0 !important;width:100% !important;background:` + brandCanvas + `;}
  a{text-decoration:none;}
  @media only screen and (max-width:620px){
    .t-card{width:100% !important;border-radius:0 !important;}
    .t-px{padding-left:24px !important;padding-right:24px !important;}
    .t-h{font-size:21px !important;}
  }
</style>
</head>
<body style="margin:0;padding:0;background:` + brandCanvas + `;">
` + preheader + `
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:` + brandCanvas + `;">
  <tr><td align="center" style="padding:32px 16px;">
    <table role="presentation" class="t-card" width="600" cellpadding="0" cellspacing="0" border="0" style="width:600px;max-width:600px;background:` + brandCardBg + `;border-radius:16px;overflow:hidden;">
      <tr><td bgcolor="` + brandIndigo + `" class="t-px" style="background:` + brandIndigo + `;padding:22px 36px;">
        <img src="cid:` + logoContentID + `" alt="Trilli" height="28" style="display:block;height:28px;width:auto;border:0;">
      </td></tr>
      <tr><td class="t-px" style="padding:34px 36px 0 36px;">
        <h1 class="t-h" style="margin:0;font-size:23px;line-height:1.3;font-weight:700;color:` + headingColor + `;font-family:` + fontStack + `;">` + esc(p.Heading) + `</h1>
      </td></tr>
      <tr><td class="t-px" style="padding:14px 36px 10px 36px;">` + p.BodyHTML + `</td></tr>
      <tr><td class="t-px" style="padding:28px 36px 30px 36px;border-top:1px solid ` + brandRule + `;">
        ` + footerNote + `
        <p style="margin:0;font-size:12.5px;line-height:1.6;color:` + brandMuted + `;font-family:` + fontStack + `;">
          <a href="` + urlPrivacy + `" style="color:` + brandIndigo + `;text-decoration:none;">Privacy</a>
          &nbsp;&middot;&nbsp;
          <a href="` + urlTerms + `" style="color:` + brandIndigo + `;text-decoration:none;">Terms</a>
          &nbsp;&middot;&nbsp;
          <a href="` + urlPrefs + `" style="color:` + brandIndigo + `;text-decoration:none;">Email preferences</a>
        </p>
        <p style="margin:10px 0 0 0;font-size:12px;line-height:1.6;color:` + brandMuted + `;font-family:` + fontStack + `;">
          Don't want emails like this? <a href="` + urlPrefs + `" style="color:` + brandIndigo + `;text-decoration:none;">Sign in to Trilli</a> and update your notification settings to unsubscribe.
        </p>
        <p style="margin:14px 0 0 0;font-size:12px;line-height:1.6;color:` + brandMuted + `;font-family:` + fontStack + `;">
          &copy; ` + currentYear() + ` ` + legalEntity + ` &middot; Secure file storage &amp; collaboration for modern teams.
        </p>
      </td></tr>
    </table>
  </td></tr>
</table>
</body></html>`
}

// pText renders a standard body paragraph — Stripe-style: 16px, generous
// line-height, soft blue-gray, with clear paragraph spacing.
func pText(innerHTML string) string {
	return `<p style="margin:0 0 16px 0;font-size:16px;line-height:1.6;color:` + brandBody + `;font-family:` + fontStack + `;">` + innerHTML + `</p>`
}

// pMuted renders a smaller, secondary paragraph (fine print inside the body).
func pMuted(innerHTML string) string {
	return `<p style="margin:0 0 12px 0;font-size:13px;line-height:1.6;color:` + brandMuted + `;font-family:` + fontStack + `;">` + innerHTML + `</p>`
}

// strongInk wraps a value in emphasized ink-colored text.
func strongInk(s string) string {
	return `<span style="color:` + brandInk + `;font-weight:600;">` + esc(s) + `</span>`
}

// emailButton renders the one primary call-to-action button. CSS padding can't
// produce a clean button in Outlook's Word engine (it ignores it and renders a
// tall, misaligned box), so this uses the canonical "bulletproof button":
//
//   - Outlook (mso): a VML <v:roundrect> — a native vector shape drawn at an
//     exact height and width, so it's a perfectly proportioned rounded button.
//   - Every other client: an <a> whose HEIGHT comes from a fixed line-height
//     (no vertical padding to misbehave) and whose width comes from horizontal
//     padding — so it's snug and vertically centered everywhere.
//
// The button is centered (in a full-width align=center cell) with generous top
// margin so it never crowds the body. The VML width is estimated from the label
// length so the Outlook button fits its text.
func emailButton(label, url string) string { return emailButtonScaled(label, url, 1.0) }

// emailButtonScaled renders the bulletproof CTA at a size multiplier (1.0 =
// default). scale < 1 produces a smaller button (height, padding, font, and the
// Outlook VML width all scale together) for secondary/lighter calls to action.
func emailButtonScaled(label, url string, scale float64) string {
	h := int(44 * scale)
	pad := int(28 * scale)
	radius := int(8 * scale)
	if radius < 4 {
		radius = 4
	}
	msoFont := int(14 * scale)
	htmlFont := 14.5 * scale
	w := int(float64(len([]rune(label))*8+56) * scale)
	if lo := int(168 * scale); w < lo {
		w = lo
	} else if hi := int(340 * scale); w > hi {
		w = hi
	}
	return `<table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0" style="margin:24px 0 24px 0;"><tr><td align="center">
        <!--[if mso]>
        <v:roundrect xmlns:v="urn:schemas-microsoft-com:vml" xmlns:w="urn:schemas-microsoft-com:office:word" href="` + esc(url) + `" style="height:` + fmt.Sprintf("%d", h) + `px;v-text-anchor:middle;width:` + fmt.Sprintf("%d", w) + `px;" arcsize="20%" stroke="f" fillcolor="` + brandIndigo + `">
          <w:anchorlock/>
          <center style="color:#ffffff;font-family:Arial,sans-serif;font-size:` + fmt.Sprintf("%d", msoFont) + `px;font-weight:bold;">` + label + `</center>
        </v:roundrect>
        <![endif]-->
        <!--[if !mso]><!-->
        <a href="` + esc(url) + `" target="_blank" style="display:inline-block;height:` + fmt.Sprintf("%d", h) + `px;line-height:` + fmt.Sprintf("%d", h) + `px;padding:0 ` + fmt.Sprintf("%d", pad) + `px;background:` + brandIndigo + `;border-radius:` + fmt.Sprintf("%d", radius) + `px;font-family:` + fontStack + `;font-size:` + fmt.Sprintf("%.1f", htmlFont) + `px;font-weight:600;color:#ffffff;text-decoration:none;text-align:center;white-space:nowrap;">` + label + `</a>
        <!--<![endif]-->
      </td></tr></table>`
}

// fileChip renders a filename in a small bordered card with a document glyph —
// a tidier presentation than bare bold text.
func fileChip(name string) string {
	return `<table role="presentation" cellspacing="0" cellpadding="0" border="0" style="margin:8px 0 4px 0;"><tr>
        <td style="background:#F8FAFC;border:1px solid ` + brandRule + `;border-radius:10px;padding:12px 16px;font-family:` + fontStack + `;font-size:14.5px;font-weight:600;color:` + brandInk + `;">
          <span style="font-size:16px;">&#128196;</span>&nbsp;&nbsp;` + esc(name) + `
        </td>
      </tr></table>`
}

// copyableLink renders the "or paste this link" fallback used by magic-link
// emails, so the CTA still works when the button can't be clicked.
func copyableLink(url string) string {
	e := esc(url)
	return `<p style="margin:0 0 4px 0;font-size:12.5px;line-height:1.6;color:` + brandMuted + `;font-family:` + fontStack + `;text-align:center;">Or copy this link into your browser:</p>
      <p style="margin:0 0 6px 0;font-size:12.5px;line-height:1.6;word-break:break-all;font-family:` + fontStack + `;text-align:center;"><a href="` + e + `" style="color:` + brandLinkAlt + `;text-decoration:none;">` + e + `</a></p>`
}

// infoCard renders a light summary box of label/value rows (receipts, tickets).
// Values are trusted HTML (callers escape or pass badge markup).
func infoCard(rows ...[2]string) string {
	var b strings.Builder
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#F8FAFC;border:1px solid ` + brandRule + `;border-radius:12px;margin:6px 0 14px 0;">`)
	for _, r := range rows {
		b.WriteString(`<tr>
      <td valign="top" style="padding:10px 16px;font-size:12px;color:` + brandMuted + `;text-transform:uppercase;letter-spacing:0.04em;font-weight:600;font-family:` + fontStack + `;white-space:nowrap;">` + r[0] + `</td>
      <td valign="top" align="right" style="padding:10px 16px;font-size:14px;line-height:1.4;color:` + brandInk + `;font-family:` + fontStack + `;">` + r[1] + `</td>
    </tr>`)
	}
	b.WriteString(`</table>`)
	return b.String()
}

// plainFooter is appended to every text/plain body so the plaintext part stays
// consistent with the HTML footer (preferences, legal, copyright).
func plainFooter() string {
	return "\n\n— — —\n" +
		"Manage which emails you receive (or unsubscribe): sign in at " + urlPrefs + "\n" +
		"Privacy: " + urlPrivacy + "   Terms: " + urlTerms + "\n" +
		"© " + currentYear() + " " + legalEntity + " · Secure file storage & collaboration for modern teams.\n"
}
