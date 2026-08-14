package mailer

import (
	"os"
	"strings"
	"testing"
)

// TestDumpPreview writes a few representative rendered emails to /tmp so the
// shared shell (header, footer, button, mobile rules) can be eyeballed in a
// browser. Skipped unless MAIL_DUMP=1. The cid: logo is swapped for the remote
// URL so it renders outside an email client.
func TestDumpPreview(t *testing.T) {
	if os.Getenv("MAIL_DUMP") == "" {
		t.Skip("set MAIL_DUMP=1 to dump preview HTML")
	}
	web := func(s string) string {
		return strings.ReplaceAll(s, "cid:"+logoContentID, "https://app.trilli.com/logo-email.png")
	}

	// Receipt: heading + body + infoCard + button + muted note.
	receiptBody := pText(esc(greetingName("Alex"))+" your Plus plan is now active. Here's your receipt.") +
		infoCard(
			[2]string{"Order", `<span style="font-family:ui-monospace,Menlo,monospace;color:` + brandInk + `;">TRL-874446113058265</span>`},
			[2]string{"Plan", "Plus (Annual billing)"},
			[2]string{"Amount paid", `<span style="font-weight:700;">$237.48</span>`},
			[2]string{"Date", "June 13, 2026"},
		) +
		emailButton("View itemized receipt", "https://example.com") +
		pMuted(`Manage your plan anytime in <a href="#" style="color:`+brandIndigo+`;text-decoration:none;">your Trilli settings</a>.`)
	receipt := emailShell(shellParams{Preheader: "Your Trilli receipt", Heading: "Thanks for your purchase!", BodyHTML: receiptBody})

	// Lapse (standard read-only): ink heading, clean amber inline emphasis.
	lapseBody := pText("Your subscription has ended, so your account is now read-only. You can still sign in to view, download, and export everything — you just can't upload or make changes.") +
		pText(`We keep your data for 30 days. Unless you reactivate, everything <span style="color:#D97706;font-weight:700;">will be permanently deleted on July 13, 2026 (in 30 days)</span>. This can't be undone.`) +
		emailButton("Reactivate my account", "#")
	lapse := emailShell(shellParams{Preheader: "Read-only notice", Heading: "Your Trilli account is read-only", BodyHTML: lapseBody, FooterNote: "Need your files first? Sign in and download them — read access stays open the whole time."})

	// Drop alert: reworked copy + file chip + centered button.
	dropBody := pText("Hi Alex, someone just uploaded a new file to your "+strongInk("Client Intake")+" folder through your "+strongInk("“Send us your documents”")+" drop portal.") +
		fileChip("signed-contract.pdf") +
		emailButton("View in Trilli", "#")
	drop := emailShell(shellParams{Preheader: "New file in Client Intake", Heading: "New file dropped", BodyHTML: dropBody, FooterNote: "You're getting this because upload alerts are on for this drop portal. Turn them off in Shared → Links."})

	// Email change: heading + body + centered button + centered copyable link.
	ecBody := pText("Hi Alex, we received a request to change the email on your Trilli account from "+strongInk("old@example.com")+" to "+strongInk("mike@finmeta.com")+".") +
		emailButton("Confirm email change", "#") +
		copyableLink("https://app.trilli.com/confirm-email/sample-token-abc123")
	ec := emailShell(shellParams{Preheader: "Confirm your new email", Heading: "Confirm your new email", BodyHTML: ecBody, FooterNote: "This link expires at 6:07 PM on June 13, 2026. If you didn't request this, you can safely ignore this email — your address won't change."})

	for name, body := range map[string]string{
		"/tmp/email_receipt.html": web(receipt),
		"/tmp/email_lapse.html":   web(lapse),
		"/tmp/email_drop.html":    web(drop),
		"/tmp/email_ec.html":      web(ec),
	} {
		if err := os.WriteFile(name, []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		t.Logf("wrote %s", name)
	}
}
