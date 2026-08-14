// Package mailer wraps SendGrid for transactional emails. Every message is
// composed through the shared design system in layout.go (emailShell + the
// pText/emailButton/infoCard helpers), so all mail — invites, password resets,
// receipts, notifications — shares one branded header, one button style, and
// one footer (Privacy · Terms · copyright · manage-preferences/unsubscribe).
//
// The SendGrid API key comes from the SENDGRID_API_KEY environment variable;
// the From identity can be overridden with TRILLI_MAIL_FROM and
// TRILLI_MAIL_FROM_NAME. Without a key, sends fail and are logged — the app
// itself keeps running.
package mailer

import (
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"

	"trilli/system/logging"
)

// The Trilli logo (white pinwheel + wordmark on transparent), embedded so it
// can be attached inline via CID. Inline/CID images render in clients that
// block remote images by default (notably Outlook), unlike a remote <img src>.
//
//go:embed logo-email.png
var logoEmailPNG []byte

// logoContentID is referenced from email HTML as <img src="cid:trilli-logo">.
const logoContentID = "trilli-logo"

// attachLogo adds the embedded logo as an inline attachment so cid:trilli-logo
// resolves. Every email's header uses it, so send() attaches it to all mail.
func attachLogo(msg *mail.SGMailV3) {
	a := mail.NewAttachment()
	a.SetContent(base64.StdEncoding.EncodeToString(logoEmailPNG))
	a.SetType("image/png")
	a.SetFilename("trilli.png")
	a.SetDisposition("inline")
	a.SetContentID(logoContentID)
	msg.AddAttachment(a)
}

const packageName = "mailer"

// Mail configuration — see package comment.
var (
	apiKey    = strings.TrimSpace(os.Getenv("SENDGRID_API_KEY"))
	fromEmail = envOr("TRILLI_MAIL_FROM", "notify@trilli.com")
	fromName  = envOr("TRILLI_MAIL_FROM_NAME", "Trilli")
)

// envOr returns the trimmed value of the environment variable key, or fallback
// when unset/empty.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// Mailer is the surface that callers hold.
type Mailer struct {
	client *sendgrid.Client
}

// New builds a Mailer with the configured SendGrid client.
func New() *Mailer {
	if apiKey == "" {
		logging.Error(packageName, "SENDGRID_API_KEY not set — outbound email will fail")
	}
	return &Mailer{client: sendgrid.NewSendClient(apiKey)}
}

// send is the single dispatch path for every email. It builds the multipart
// (text/plain + text/html) message, attaches the inline logo the shared header
// references, sends it, and normalizes the error/status handling. The plain
// part always gets the shared footer appended for parity with the HTML.
func (m *Mailer) send(label, subject, to, plain, htmlBody string) error {
	if strings.TrimSpace(to) == "" {
		return fmt.Errorf("mailer: empty recipient")
	}
	from := mail.NewEmail(fromName, fromEmail)
	msg := mail.NewSingleEmail(from, subject, mail.NewEmail("", to), plain+plainFooter(), htmlBody)
	attachLogo(msg)
	resp, err := m.client.Send(msg)
	if err != nil {
		return fmt.Errorf("mailer: send %s to %s: %w", label, to, err)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("mailer: send %s to %s: status %d body=%s", label, to, resp.StatusCode, resp.Body)
	}
	return nil
}

// --- Invite --------------------------------------------------------------

// InviteEmail is the payload for a single invite send.
type InviteEmail struct {
	To           string    // invitee's email
	TenantName   string    // workspace name shown in the subject + body
	InviterName  string    // optional — falls back to InviterEmail when blank
	InviterEmail string    // always present; shown parenthetically when a name is set
	Role         string    // "member" or "viewer"
	Link         string    // full absolute invite URL
	ExpiresAt    time.Time // shown as a calendar date in the body
}

// inviterDisplay renders the inviter's identity for the plaintext body.
func inviterDisplay(name, email string) string {
	return effectiveInviterName(name, email) + " (" + email + ")"
}

// effectiveInviterName returns the inviter's stored full_name if set,
// otherwise a titleized rendering of the email's local part.
func effectiveInviterName(name, email string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	return titleizeEmail(email)
}

// titleizeEmail splits the local part on . _ + - and Title-Cases each token.
func titleizeEmail(email string) string {
	local := email
	if i := strings.Index(email, "@"); i > 0 {
		local = email[:i]
	}
	parts := strings.FieldsFunc(local, func(r rune) bool {
		return r == '.' || r == '_' || r == '+' || r == '-'
	})
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// SendInvite sends the magic-link invite email. Best-effort: the invite stays
// valid in the DB even if this errors (the Owner can copy the link from the UI).
func (m *Mailer) SendInvite(ctx context.Context, in InviteEmail) error {
	_ = ctx
	if strings.TrimSpace(in.Link) == "" {
		return fmt.Errorf("mailer: empty invite link")
	}
	subject := fmt.Sprintf("%s invited you to %s on Trilli",
		effectiveInviterName(in.InviterName, in.InviterEmail), in.TenantName)
	expires := in.ExpiresAt.Format("January 2, 2006")

	var plain strings.Builder
	fmt.Fprintf(&plain, "You've been invited to join %s on Trilli.\n\n", in.TenantName)
	fmt.Fprintf(&plain, "%s invited you to join their workspace as a %s.\n\n",
		inviterDisplay(in.InviterName, in.InviterEmail), titleRole(in.Role))
	fmt.Fprintf(&plain, "Accept the invitation:\n%s\n\n", in.Link)
	fmt.Fprintf(&plain, "This invite expires on %s. If you weren't expecting this, you can safely ignore this email.\n", expires)

	inviter := strongInk(effectiveInviterName(in.InviterName, in.InviterEmail)) +
		` <span style="color:` + brandMuted + `;">(` + esc(in.InviterEmail) + `)</span>`
	body := pText(inviter+` invited you to join their workspace as a `+strongInk(titleRole(in.Role))+`.`) +
		emailButton("Accept invitation", in.Link) +
		copyableLink(in.Link)
	htmlBody := emailShell(shellParams{
		Preheader:  "Join " + in.TenantName + " on Trilli",
		Heading:    "You're invited to " + in.TenantName,
		BodyHTML:   body,
		FooterNote: "This invite expires on " + esc(expires) + ". If you weren't expecting this, you can safely ignore this email.",
	})

	if err := m.send("invite", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "invite sent to %s (tenant=%s)", in.To, in.TenantName)
	return nil
}

// --- Comp / ambassador invite --------------------------------------------

// CompInviteEmail is the payload for a complimentary-account invite (SPEC §6.10).
type CompInviteEmail struct {
	To           string
	PlanName     string    // granted plan, shown in the body
	FreeTermDays int       // length of the comp term
	Link         string    // full absolute acceptance URL
	ExpiresAt    time.Time // when the acceptance link expires
}

// SendCompInvite sends the comp/ambassador acceptance email. The recipient
// clicks through to set up a free account — no payment step. Best-effort.
func (m *Mailer) SendCompInvite(ctx context.Context, in CompInviteEmail) error {
	_ = ctx
	if strings.TrimSpace(in.Link) == "" {
		return fmt.Errorf("mailer: empty comp invite link")
	}
	subject := "Welcome to Trilli — your complimentary account is ready"
	expires := in.ExpiresAt.Format("January 2, 2006")

	// FreeTermDays == 0 is an indefinite (never-expiring) comp term.
	termPlain := fmt.Sprintf("yours free for %d days", in.FreeTermDays)
	if in.FreeTermDays <= 0 {
		termPlain = "yours free for an indefinite period — no expiry, and no payment, ever"
	}

	var plain strings.Builder
	fmt.Fprintf(&plain, "Welcome to Trilli!\n\n")
	fmt.Fprintf(&plain, "You've been given complimentary access to Trilli — the secure home for your "+
		"files and the easiest way to share them. Your %s account is %s.\n\n", in.PlanName, termPlain)
	fmt.Fprintf(&plain, "With Trilli you can store and organize files in workspaces and folders, share "+
		"them through links, email, or drop portals, and keep everything encrypted and private — all "+
		"from a fast, modern workspace that works on every device.\n\n")
	fmt.Fprintf(&plain, "Just set up your account and you're ready to go — it takes less than a minute:\n%s\n\n", in.Link)
	fmt.Fprintf(&plain, "This setup link expires on %s.\n", expires)

	body := pText("You've been given complimentary access to Trilli — the secure home for your files, "+
		"and the easiest way to keep them organized and shared. Your "+esc(in.PlanName)+" account is "+
		esc(termPlain)+".") +
		pText("With Trilli you can store and organize files in workspaces and folders, share them through "+
			"links, email, or drop portals, and rest easy knowing everything is encrypted and private — "+
			"all from a fast, modern workspace that works on every device.") +
		emailButtonScaled("Set up your account", in.Link, 0.8) +
		pMuted("Just set up your account and you're ready to go — it takes less than a minute.") +
		copyableLink(in.Link)
	htmlBody := emailShell(shellParams{
		Preheader:  "Your complimentary Trilli account is ready — set it up in under a minute.",
		Heading:    "Welcome to Trilli",
		BodyHTML:   body,
		FooterNote: "This setup link expires on " + esc(expires) + ". If you weren't expecting this, you can safely ignore this email.",
	})

	if err := m.send("comp-invite", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "comp invite sent to %s (plan=%s, %dd)", in.To, in.PlanName, in.FreeTermDays)
	return nil
}

// --- Email change --------------------------------------------------------

// EmailChangeEmail is the payload for a "confirm your new email" send.
type EmailChangeEmail struct {
	To           string
	Name         string
	CurrentEmail string
	Link         string
	ExpiresAt    time.Time
}

// greetingName returns "Hi Name," when a name is set, else a neutral "Hi,".
func greetingName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Hi,"
	}
	return "Hi " + name + ","
}

// SendEmailChange sends the confirmation email for an email-address change.
func (m *Mailer) SendEmailChange(ctx context.Context, in EmailChangeEmail) error {
	_ = ctx
	if strings.TrimSpace(in.Link) == "" {
		return fmt.Errorf("mailer: empty confirm link")
	}
	subject := "Confirm your new email address for Trilli"
	expires := in.ExpiresAt.Format("3:04 PM on January 2, 2006")

	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n\n", greetingName(in.Name))
	fmt.Fprintf(&plain, "We received a request to change the email on your Trilli account")
	if strings.TrimSpace(in.CurrentEmail) != "" {
		fmt.Fprintf(&plain, " from %s", in.CurrentEmail)
	}
	fmt.Fprintf(&plain, " to %s.\n\n", in.To)
	fmt.Fprintf(&plain, "Confirm the change:\n%s\n\n", in.Link)
	fmt.Fprintf(&plain, "This link expires at %s. If you didn't request this, you can safely ignore this email — nothing will change.\n", expires)

	currentLine := ""
	if strings.TrimSpace(in.CurrentEmail) != "" {
		currentLine = " from " + strongInk(in.CurrentEmail)
	}
	body := pText(esc(greetingName(in.Name))+" we received a request to change the email on your Trilli account"+currentLine+" to "+strongInk(in.To)+".") +
		emailButton("Confirm email change", in.Link) +
		copyableLink(in.Link)
	htmlBody := emailShell(shellParams{
		Preheader:  "Confirm your new email address",
		Heading:    "Confirm your new email",
		BodyHTML:   body,
		FooterNote: "This link expires at " + esc(expires) + ". If you didn't request this, you can safely ignore this email — your address won't change.",
	})

	if err := m.send("email-change", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "email-change confirm sent to %s", in.To)
	return nil
}

// --- Signup funnel -------------------------------------------------------

// SignupConfirmEmail confirms ownership of the address before payment.
type SignupConfirmEmail struct {
	To   string
	Link string
}

// SendSignupConfirm sends the "confirm your email to continue" funnel email.
func (m *Mailer) SendSignupConfirm(ctx context.Context, in SignupConfirmEmail) error {
	_ = ctx
	if strings.TrimSpace(in.Link) == "" {
		return fmt.Errorf("mailer: empty link")
	}
	plain := fmt.Sprintf("Welcome to Trilli!\n\nConfirm your email to continue setting up your account:\n%s\n\nIf you didn't start a Trilli signup, you can ignore this email.\n", in.Link)
	htmlBody := buildSignupEmailHTML("Confirm your email", "Confirm your email address to continue setting up your Trilli account and choose your plan.", "Confirm & continue", in.Link)
	if err := m.send("signup-confirm", "Confirm your email to continue with Trilli", in.To, plain, htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "signup-confirm link sent to %s", in.To)
	return nil
}

// SignupResumeEmail is sent after a funnel payment succeeds.
type SignupResumeEmail struct {
	To   string
	Link string
}

// SendSignupResume nudges a paid-but-incomplete signup to finish account setup.
func (m *Mailer) SendSignupResume(ctx context.Context, in SignupResumeEmail) error {
	_ = ctx
	if strings.TrimSpace(in.Link) == "" {
		return fmt.Errorf("mailer: empty link")
	}
	plain := fmt.Sprintf("Your Trilli payment went through — thank you!\n\nFinish setting up your account (just a name and password):\n%s\n", in.Link)
	htmlBody := buildSignupEmailHTML("Finish setting up Trilli", "Your payment went through — thank you! Finish creating your account to start using Trilli.", "Finish setup", in.Link)
	if err := m.send("signup-resume", "Finish setting up your Trilli account", in.To, plain, htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "signup-resume link sent to %s", in.To)
	return nil
}

// buildSignupEmailHTML renders the shared funnel email (blurb + button + link).
func buildSignupEmailHTML(title, blurb, cta, link string) string {
	body := pText(esc(blurb)) + emailButton(esc(cta), link) + copyableLink(link)
	return emailShell(shellParams{Preheader: blurb, Heading: title, BodyHTML: body})
}

// --- Welcome -------------------------------------------------------------

// WelcomeEmail congratulates a newly provisioned account.
type WelcomeEmail struct {
	To        string
	FirstName string
	Link      string
}

// SendWelcome sends the branded welcome / thank-you email.
func (m *Mailer) SendWelcome(ctx context.Context, in WelcomeEmail) error {
	_ = ctx
	link := strings.TrimSpace(in.Link)
	if link == "" {
		link = appBase + "/home"
	}
	name := strings.TrimSpace(in.FirstName)
	heading := "Welcome to Trilli!"
	if name != "" {
		heading = "Welcome to Trilli, " + name + "!"
	}
	plain := fmt.Sprintf("%s\n\nThank you for trusting Trilli as your storage and collaboration home — your account is ready to go.\n\nWith Trilli you can:\n  • Keep every file in one secure place, encrypted at rest\n  • Organize work into separate, access-controlled workspaces\n  • Share with anyone via secure links, or collect files with drop portals\n  • Reach your files from anywhere, anytime\n\nGet started now: %s\n\n— The Trilli team\n", heading, link)

	benefit := func(title, desc string) string {
		return `<tr><td style="padding:0 0 16px 0;">
          <table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
            <td valign="top" width="32">
              <div style="width:22px;height:22px;border-radius:50%;background:#EEF0FF;color:` + brandIndigo + `;font-size:12px;font-weight:700;text-align:center;line-height:22px;font-family:` + fontStack + `;">&#10003;</div>
            </td>
            <td style="padding-left:10px;font-family:` + fontStack + `;">
              <div style="font-size:14px;font-weight:600;color:` + brandInk + `;line-height:1.35;">` + title + `</div>
              <div style="font-size:13px;color:#64748B;line-height:1.5;margin-top:2px;">` + desc + `</div>
            </td>
          </tr></table>
        </td></tr>`
	}
	benefits := `<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:4px 0 6px 0;">` +
		benefit("Everything in one secure place", "All your files, encrypted at rest and always at hand.") +
		benefit("Organized by workspace", "A separate, access-controlled home for every team, client, or project.") +
		benefit("Share with anyone", "Send files with secure links, or collect them with drop portals.") +
		benefit("Yours, anywhere", "Reach your files from any device, anytime — no lock-in, ever.") +
		`</table>`

	body := pText("Thank you for trusting Trilli as your storage and collaboration home. Your account is set up and ready — here's a little of what you can do:") +
		benefits +
		emailButton("Get started now", link)
	htmlBody := emailShell(shellParams{
		Preheader:  "Your Trilli account is ready",
		Heading:    heading,
		BodyHTML:   body,
		FooterNote: "We're glad you're here. Questions or feedback? Just reply to this email — a real person reads every one.",
	})

	if err := m.send("welcome", "Welcome to Trilli 🎉", in.To, plain, htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "welcome email sent to %s", in.To)
	return nil
}

// --- Support ticketing ---------------------------------------------------

// TicketCreatedEmail confirms a newly opened support ticket to the requester.
type TicketCreatedEmail struct {
	To       string
	Name     string
	Number   string
	Subject  string
	Severity string
	Link     string
}

// SendTicketCreated emails the requester their new ticket number.
func (m *Mailer) SendTicketCreated(ctx context.Context, in TicketCreatedEmail) error {
	_ = ctx
	link := strings.TrimSpace(in.Link)
	if link == "" {
		link = appBase + "/support"
	}
	name := strings.TrimSpace(in.Name)
	greeting := "Thanks for reaching out!"
	if name != "" {
		greeting = "Thanks for reaching out, " + firstWord(name) + "!"
	}
	plain := fmt.Sprintf("%s\n\nWe've received your support request and opened ticket %s.\n\nSubject: %s\nPriority: %s\n\nOur team will respond right inside your ticket, where the whole conversation lives. We'll email you whenever there's an update.\n\nView your ticket: %s\n\n— The Trilli Support team\n",
		greeting, in.Number, in.Subject, prettySeverity(in.Severity), link)

	body := pText(esc(greeting)) +
		pText("We've received your request and opened a support ticket. Our team will respond right inside your ticket — that's where the whole conversation lives. We'll email you whenever there's an update.") +
		infoCard(
			[2]string{"Ticket", `<span style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-weight:700;color:` + brandInk + `;">` + esc(in.Number) + `</span>`},
			[2]string{"Subject", esc(in.Subject)},
			[2]string{"Priority", severityBadge(in.Severity)},
		) +
		emailButton("View your ticket", link)
	htmlBody := emailShell(shellParams{
		Preheader:  "We've opened ticket " + in.Number,
		Heading:    greeting,
		BodyHTML:   body,
		FooterNote: "You're receiving this because you opened a support request with Trilli. Replies and updates will arrive at this address.",
	})

	if err := m.send("ticket-created", "We've received your ticket — "+in.Number, in.To, plain, htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "ticket-created email sent to %s (%s)", in.To, in.Number)
	return nil
}

// TicketUpdateEmail notifies the requester that there's a new update.
type TicketUpdateEmail struct {
	To      string
	Name    string
	Number  string
	Subject string
	Status  string
	Link    string
}

// SendTicketUpdate emails the requester that there's an update on their ticket.
func (m *Mailer) SendTicketUpdate(ctx context.Context, in TicketUpdateEmail) error {
	_ = ctx
	link := strings.TrimSpace(in.Link)
	if link == "" {
		link = appBase + "/support"
	}
	plain := fmt.Sprintf("There's an update on your support ticket %s.\n\nSubject: %s\nStatus: %s\n\nWe've posted a response in your ticket. Open it to read the update and reply.\n\nView your ticket: %s\n\n— The Trilli Support team\n",
		in.Number, in.Subject, prettyStatus(in.Status), link)

	body := pText("There's a new update on your support ticket. We've posted a response — open the ticket to read it and reply. Here's where things stand:") +
		infoCard(
			[2]string{"Ticket", `<span style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-weight:700;color:` + brandInk + `;">` + esc(in.Number) + `</span>`},
			[2]string{"Subject", esc(in.Subject)},
			[2]string{"Status", statusBadge(in.Status)},
		) +
		emailButton("View the response", link)
	htmlBody := emailShell(shellParams{
		Preheader:  "There's an update on ticket " + in.Number,
		Heading:    "There's an update on your ticket",
		BodyHTML:   body,
		FooterNote: "You're receiving this because you opened a support request with Trilli. The full conversation lives in your ticket.",
	})

	if err := m.send("ticket-update", "Update on your ticket — "+in.Number, in.To, plain, htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "ticket-update email sent to %s (%s)", in.To, in.Number)
	return nil
}

func severityBadge(sev string) string {
	bg, fg := "#EEF0FF", "#4F46E5"
	switch strings.ToLower(sev) {
	case "low":
		bg, fg = "#F1F5F9", "#475569"
	case "high":
		bg, fg = "#FFF4E5", "#B45309"
	case "urgent":
		bg, fg = "#FEE2E2", "#B91C1C"
	}
	return `<span style="display:inline-block;background:` + bg + `;color:` + fg + `;font-size:12px;font-weight:600;padding:3px 10px;border-radius:999px;font-family:` + fontStack + `;">` + esc(prettySeverity(sev)) + `</span>`
}

func statusBadge(status string) string {
	bg, fg := "#EEF0FF", "#4F46E5"
	switch status {
	case "resolved":
		bg, fg = "#DCFCE7", "#15803D"
	case "closed":
		bg, fg = "#F1F5F9", "#475569"
	case "awaiting_customer":
		bg, fg = "#FFF4E5", "#B45309"
	}
	return `<span style="display:inline-block;background:` + bg + `;color:` + fg + `;font-size:12px;font-weight:600;padding:3px 10px;border-radius:999px;font-family:` + fontStack + `;">` + esc(prettyStatus(status)) + `</span>`
}

func prettySeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return "Low"
	case "high":
		return "High"
	case "urgent":
		return "Urgent"
	default:
		return "Normal"
	}
}

func prettyStatus(s string) string {
	switch s {
	case "open":
		return "Open"
	case "pending":
		return "In progress"
	case "awaiting_customer":
		return "Awaiting your reply"
	case "resolved":
		return "Resolved"
	case "closed":
		return "Closed"
	default:
		return strings.ReplaceAll(s, "_", " ")
	}
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}

// --- Password reset ------------------------------------------------------

// PasswordResetEmail is the payload for a "set a new password" send.
type PasswordResetEmail struct {
	To        string
	Name      string
	Link      string
	ExpiresAt time.Time
}

// SendPasswordReset sends the "set a new password" email.
func (m *Mailer) SendPasswordReset(ctx context.Context, in PasswordResetEmail) error {
	_ = ctx
	if strings.TrimSpace(in.Link) == "" {
		return fmt.Errorf("mailer: empty reset link")
	}
	expires := in.ExpiresAt.Format("3:04 PM on January 2, 2006")

	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n\n", greetingName(in.Name))
	fmt.Fprintf(&plain, "We received a request to reset the password on your Trilli account.\n\n")
	fmt.Fprintf(&plain, "Set a new password:\n%s\n\n", in.Link)
	fmt.Fprintf(&plain, "This link expires at %s. If you didn't request this, you can safely ignore this email — your password won't change.\n", expires)

	body := pText(esc(greetingName(in.Name))+" we received a request to reset the password on your Trilli account. Click below to set a new one.") +
		emailButton("Set a new password", in.Link) +
		copyableLink(in.Link)
	htmlBody := emailShell(shellParams{
		Preheader:  "Reset your Trilli password",
		Heading:    "Reset your password",
		BodyHTML:   body,
		FooterNote: "This link expires at " + esc(expires) + ". If you didn't request this, you can safely ignore this email — your password won't change.",
	})

	if err := m.send("password-reset", "Reset your Trilli password", in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "password-reset link sent to %s", in.To)
	return nil
}

func titleRole(r string) string {
	r = strings.TrimSpace(r)
	if r == "" {
		return "member"
	}
	return strings.ToUpper(r[:1]) + r[1:]
}

// --- Receipt -------------------------------------------------------------

// ReceiptEmail is the payload for a purchase receipt.
type ReceiptEmail struct {
	To            string
	Name          string
	PlanName      string
	BillingPeriod string
	AmountCents   int64 // the gross charge (the prorated line total, before any credit)
	CreditCents   int64 // account credit applied to this invoice (0 = none)
	Currency      string
	OrderNumber   string
	ReceiptURL    string
	PaidAt        time.Time
	Kind          string // "purchase" | "renewal" | "upgrade" | "seats"
	SeatCount     int
}

type receiptCopy struct {
	subject     string
	heading     string
	intro       string
	amountLabel string
}

func receiptCopyFor(kind, plan string) receiptCopy {
	switch kind {
	case "upgrade":
		return receiptCopy{
			subject:     "Your Trilli upgrade receipt",
			heading:     "Your plan has been upgraded",
			intro:       "Your account has been upgraded to " + plan + ". Here's the receipt for the prorated adjustment covering the rest of your current billing period.",
			amountLabel: "Prorated adjustment",
		}
	case "renewal":
		return receiptCopy{
			subject:     "Your Trilli renewal receipt",
			heading:     "Your plan has renewed",
			intro:       "Your " + plan + " plan has renewed for another term. Here's your receipt.",
			amountLabel: "Amount paid",
		}
	case "seats":
		return receiptCopy{
			subject:     "Your Trilli seats receipt",
			heading:     "Seats added to your account",
			intro:       "Here's the receipt for the additional seats added to your account — a prorated charge covering the rest of your current billing period. Your seats renew with your plan.",
			amountLabel: "Prorated charge",
		}
	default: // "purchase"
		return receiptCopy{
			subject:     "Your Trilli receipt",
			heading:     "Thanks for your purchase!",
			intro:       "Your " + plan + " plan is now active. Here's your receipt.",
			amountLabel: "Amount paid",
		}
	}
}

// fmtMoney renders minor units as a currency amount, e.g. (1999,"usd") -> "$19.99".
func fmtMoney(cents int64, currency string) string {
	amt := float64(cents) / 100
	if strings.EqualFold(currency, "usd") || currency == "" {
		return fmt.Sprintf("$%.2f", amt)
	}
	return fmt.Sprintf("%.2f %s", amt, strings.ToUpper(currency))
}

func seatsLabel(n int) string {
	if n == 1 {
		return "1 additional seat"
	}
	return fmt.Sprintf("%d additional seats", n)
}

func billingLabel(period string) string {
	if strings.EqualFold(period, "annual") {
		return "Annual"
	}
	return "Monthly"
}

// pluralDays renders a day count with correct singular/plural, e.g. "1 day" /
// "30 days".
func pluralDays(n int) string {
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}

// SendReceipt sends our own branded purchase receipt. Best-effort.
func (m *Mailer) SendReceipt(ctx context.Context, in ReceiptEmail) error {
	_ = ctx
	cp := receiptCopyFor(in.Kind, in.PlanName)
	subject := fmt.Sprintf("%s — %s", cp.subject, in.OrderNumber)

	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n\n", greetingName(in.Name))
	fmt.Fprintf(&plain, "%s\n\n", cp.intro)
	fmt.Fprintf(&plain, "Order number: %s\n", in.OrderNumber)
	if in.Kind == "seats" {
		fmt.Fprintf(&plain, "Seats: %s\n", seatsLabel(in.SeatCount))
	} else {
		fmt.Fprintf(&plain, "Plan: %s (%s billing)\n", in.PlanName, billingLabel(in.BillingPeriod))
	}
	hasCredit := in.CreditCents > 0
	netCents := in.AmountCents - in.CreditCents
	fmt.Fprintf(&plain, "%s: %s\n", cp.amountLabel, fmtMoney(in.AmountCents, in.Currency))
	if hasCredit {
		fmt.Fprintf(&plain, "Account credit applied: -%s\n", fmtMoney(in.CreditCents, in.Currency))
		fmt.Fprintf(&plain, "Charged today: %s\n", fmtMoney(netCents, in.Currency))
	}
	fmt.Fprintf(&plain, "Date: %s\n\n", in.PaidAt.Format("January 2, 2006"))
	if strings.TrimSpace(in.ReceiptURL) != "" {
		fmt.Fprintf(&plain, "View your itemized receipt:\n%s\n\n", in.ReceiptURL)
	}
	fmt.Fprintf(&plain, "Manage your plan anytime at %s/settings/plan\n\nThank you for choosing Trilli.\n", appBase)

	planRow := [2]string{"Plan", esc(in.PlanName) + " (" + esc(billingLabel(in.BillingPeriod)) + " billing)"}
	if in.Kind == "seats" {
		planRow = [2]string{"Seats", esc(seatsLabel(in.SeatCount))}
	}
	// When account credit is applied, show the gross charge, the credit, and the
	// net charged — so a smaller-than-expected card charge is self-explanatory.
	bold := func(s string) string { return `<span style="font-weight:700;">` + esc(s) + `</span>` }
	rows := [][2]string{
		{"Order", `<span style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace;color:` + brandInk + `;">` + esc(in.OrderNumber) + `</span>`},
		planRow,
	}
	if hasCredit {
		rows = append(rows,
			[2]string{esc(cp.amountLabel), esc(fmtMoney(in.AmountCents, in.Currency))},
			[2]string{"Account credit applied", `<span style="color:#16a34a;">&minus;` + esc(fmtMoney(in.CreditCents, in.Currency)) + `</span>`},
			[2]string{"Charged today", bold(fmtMoney(netCents, in.Currency))},
		)
	} else {
		rows = append(rows, [2]string{esc(cp.amountLabel), bold(fmtMoney(in.AmountCents, in.Currency))})
	}
	rows = append(rows, [2]string{"Date", esc(in.PaidAt.Format("January 2, 2006"))})
	body := pText(esc(greetingName(in.Name))+" "+esc(cp.intro)) + infoCard(rows...)
	if strings.TrimSpace(in.ReceiptURL) != "" {
		body += emailButton("View itemized receipt", in.ReceiptURL)
	}
	body += pMuted(`Manage your plan anytime in <a href="` + appBase + `/settings/plan" style="color:` + brandIndigo + `;text-decoration:none;">your Trilli settings</a>. Thank you for choosing Trilli.`)
	htmlBody := emailShell(shellParams{
		Preheader: cp.subject + " — " + in.OrderNumber,
		Heading:   cp.heading,
		BodyHTML:  body,
	})

	if err := m.send("receipt", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "receipt sent to %s (order=%s)", in.To, in.OrderNumber)
	return nil
}

// --- Payment failed ------------------------------------------------------

// PaymentFailedEmail is the dunning nudge sent when a renewal charge fails.
type PaymentFailedEmail struct {
	To          string
	Name        string
	PlanName    string
	AmountCents int64
	Currency    string
	InvoiceURL  string
}

// SendPaymentFailed nudges the customer to fix a failed renewal payment.
func (m *Mailer) SendPaymentFailed(ctx context.Context, in PaymentFailedEmail) error {
	_ = ctx
	subject := "Action needed: your Trilli payment didn't go through"

	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n\n", greetingName(in.Name))
	fmt.Fprintf(&plain, "We couldn't process the payment for your %s plan (%s).\n\n", in.PlanName, fmtMoney(in.AmountCents, in.Currency))
	fmt.Fprintf(&plain, "We'll retry automatically, but you can fix it now by updating your card or paying the open invoice:\n%s\n\n", in.InvoiceURL)
	fmt.Fprintf(&plain, "Or manage your payment methods at %s/settings/billing\n\n", appBase)
	fmt.Fprintf(&plain, "If we can't collect payment, your plan will eventually pause. We don't want that — fixing it takes a minute.\n")

	body := pText(esc(greetingName(in.Name))+" we couldn't charge for your "+strongInk(in.PlanName)+" plan ("+esc(fmtMoney(in.AmountCents, in.Currency))+"). We'll retry automatically — or you can fix it now.") +
		emailButton("Update card & pay", in.InvoiceURL) +
		pMuted(`Manage payment methods in <a href="`+appBase+`/settings/billing" style="color:`+brandIndigo+`;text-decoration:none;">your Trilli billing settings</a>.`)
	htmlBody := emailShell(shellParams{
		Preheader:  "Your Trilli payment didn't go through",
		Heading:    "Your payment didn't go through",
		BodyHTML:   body,
		FooterNote: "If we can't collect payment, your plan will eventually pause. Updating your card takes a minute.",
	})

	if err := m.send("payment-failed", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "payment-failed nudge sent to %s", in.To)
	return nil
}

// --- Lapse warning -------------------------------------------------------

// LapseWarningEmail is one of the escalating read-only / purge-countdown notices.
type LapseWarningEmail struct {
	To        string
	Name      string
	PurgeDate string
	DaysLeft  int
	Final     bool
}

// SendLapseWarning tells a lapsed customer their account is read-only and their
// data will be deleted on PurgeDate unless they reactivate.
func (m *Mailer) SendLapseWarning(ctx context.Context, in LapseWarningEmail) error {
	_ = ctx
	subject := fmt.Sprintf("Your Trilli account has been suspended — your data will be deleted in %s", pluralDays(in.DaysLeft))
	heading := "Your Trilli account has been suspended"
	if in.Final {
		subject = "Final notice: your Trilli account and data will be deleted tomorrow"
		heading = "Final notice — your account will be deleted tomorrow"
	}
	// Keep the heading ink on the standard notice — a colored heading there read
	// as muddy brown. Color lives on the inline "deleted on…" emphasis (clean
	// amber). The final notice is a true alarm, so its heading goes red too.
	accent := "#D97706" // clean amber for the standard notice emphasis
	headingColor := ""  // ink (default)
	if in.Final {
		accent = "#DC2626" // clean red
		headingColor = "#DC2626"
	}

	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n\n", greetingName(in.Name))
	fmt.Fprintf(&plain, "Your Trilli subscription has ended, so your account has been suspended and is now read-only. You can still sign in to view, download, and export everything, but you can't upload or make changes.\n\n")
	fmt.Fprintf(&plain, "We'll keep your data for 30 days. Unless you reactivate, your account and all of your files will be permanently deleted on %s (in %s). This cannot be undone.\n\n", in.PurgeDate, pluralDays(in.DaysLeft))
	fmt.Fprintf(&plain, "Reactivate anytime to restore full access: %s/settings/plan\n\n", appBase)
	fmt.Fprintf(&plain, "Need to get your files out first? Sign in and download them — read access stays open the whole time.\n")

	body := pText("Your subscription has ended, so your account has been suspended and is now read-only. You can still sign in to view, download, and export everything — you just can't upload or make changes.") +
		pText(`We'll keep your data for 30 days. Unless you reactivate, your account and all of your files <span style="color:`+accent+`;font-weight:700;">will be permanently deleted on `+esc(in.PurgeDate)+` (in `+pluralDays(in.DaysLeft)+`)</span>. This can't be undone.`) +
		emailButton("Reactivate my account", appBase+"/settings/plan")
	htmlBody := emailShell(shellParams{
		Preheader:    fmt.Sprintf("Your account and files will be deleted on %s unless you reactivate", in.PurgeDate),
		Heading:      heading,
		HeadingColor: headingColor,
		BodyHTML:     body,
		FooterNote:   "Need your files first? Sign in and download them — read access stays open the whole time.",
	})

	if err := m.send("lapse-warning", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "lapse-warning (daysLeft=%d final=%t) sent to %s", in.DaysLeft, in.Final, in.To)
	return nil
}

// AccountCloseConfirmEmail confirms an owner-initiated deletion (sent once, at
// the moment of deletion). RefundCents > 0 when a full money-back refund was
// issued (account < 30 days old).
type AccountCloseConfirmEmail struct {
	To          string
	Name        string
	AccountName string
	PurgeDate   string
	DaysLeft    int
	RefundCents int64
}

// SendAccountCloseConfirm tells the owner their account is now scheduled for
// deletion, that it's read-only and recoverable until PurgeDate, and (when
// applicable) that a full refund was issued.
func (m *Mailer) SendAccountCloseConfirm(ctx context.Context, in AccountCloseConfirmEmail) error {
	_ = ctx
	subject := fmt.Sprintf("Your Trilli account %q is scheduled for deletion", in.AccountName)

	refundHTML, refundPlain := "", ""
	if in.RefundCents > 0 {
		amt := fmt.Sprintf("$%.2f", float64(in.RefundCents)/100)
		refundHTML = pText("Because your account was less than 30 days old, we've issued a full refund of <strong>" + amt + "</strong> to your original payment method.")
		refundPlain = fmt.Sprintf("Because your account was less than 30 days old, we've issued a full refund of %s to your original payment method.\n\n", amt)
	}

	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n\n", greetingName(in.Name))
	fmt.Fprintf(&plain, "Your Trilli account %q is now scheduled for deletion. It's read-only from now on — you can still sign in to view, download, and export everything.\n\n", in.AccountName)
	plain.WriteString(refundPlain)
	fmt.Fprintf(&plain, "All data will be permanently deleted on %s (in %s). Changed your mind? Reactivate any time before then: %s/settings/account\n", in.PurgeDate, pluralDays(in.DaysLeft), appBase)

	body := pText("Your account <strong>"+esc(in.AccountName)+"</strong> is now scheduled for deletion. It's read-only from now on — you can still sign in to view, download, and export everything.") +
		refundHTML +
		pText(`All data will be <span style="color:#D97706;font-weight:700;">permanently deleted on `+esc(in.PurgeDate)+` (in `+pluralDays(in.DaysLeft)+`)</span>. Changed your mind?`) +
		emailButton("Reactivate my account", appBase+"/settings/account")
	htmlBody := emailShell(shellParams{
		Preheader:  fmt.Sprintf("Scheduled for deletion on %s — reactivate any time before then", in.PurgeDate),
		Heading:    "Your account is scheduled for deletion",
		BodyHTML:   body,
		FooterNote: "If you didn't request this, reactivate your account and change your password right away.",
	})
	if err := m.send("account-close-confirm", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "account-close-confirm sent to %s", in.To)
	return nil
}

// AccountCloseWarningEmail is an escalating reminder during the deletion grace
// window (mirrors the lapse cadence, with delete-specific wording).
type AccountCloseWarningEmail struct {
	To          string
	Name        string
	AccountName string
	PurgeDate   string
	DaysLeft    int
	Final       bool
}

// SendAccountCloseWarning reminds the owner that their scheduled-for-deletion
// account will be erased on PurgeDate unless they reactivate.
func (m *Mailer) SendAccountCloseWarning(ctx context.Context, in AccountCloseWarningEmail) error {
	_ = ctx
	subject := fmt.Sprintf("Reminder: %q will be deleted in %s", in.AccountName, pluralDays(in.DaysLeft))
	heading := "Your account is scheduled for deletion"
	accent, headingColor := "#D97706", ""
	if in.Final {
		subject = fmt.Sprintf("Final notice: %q will be permanently deleted tomorrow", in.AccountName)
		heading = "Final notice — your account is deleted tomorrow"
		accent, headingColor = "#DC2626", "#DC2626"
	}

	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n\n", greetingName(in.Name))
	fmt.Fprintf(&plain, "You scheduled your Trilli account %q for deletion, so it's read-only until then.\n\n", in.AccountName)
	fmt.Fprintf(&plain, "Your account and all of its files will be permanently deleted on %s (in %s). This cannot be undone.\n\n", in.PurgeDate, pluralDays(in.DaysLeft))
	fmt.Fprintf(&plain, "Changed your mind? Reactivate anytime: %s/settings/account\n", appBase)

	body := pText("You scheduled your account <strong>"+esc(in.AccountName)+"</strong> for deletion, so it's read-only until then.") +
		pText(`Your account and all of its files <span style="color:`+accent+`;font-weight:700;">will be permanently deleted on `+esc(in.PurgeDate)+` (in `+pluralDays(in.DaysLeft)+`)</span>. This can't be undone.`) +
		emailButton("Reactivate my account", appBase+"/settings/account")
	htmlBody := emailShell(shellParams{
		Preheader:    fmt.Sprintf("Deleted on %s unless you reactivate", in.PurgeDate),
		Heading:      heading,
		HeadingColor: headingColor,
		BodyHTML:     body,
		FooterNote:   "Need your files first? Sign in and download them — read access stays open the whole time.",
	})
	if err := m.send("account-close-warning", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "account-close-warning (daysLeft=%d final=%t) sent to %s", in.DaysLeft, in.Final, in.To)
	return nil
}

// AccountReactivatedEmail confirms a deletion was cancelled. RechargedCents > 0
// when a previously refunded account was re-charged to restore its plan.
type AccountReactivatedEmail struct {
	To             string
	Name           string
	AccountName    string
	RechargedCents int64
}

// SendAccountReactivated confirms the scheduled deletion was cancelled and the
// account is fully active again.
func (m *Mailer) SendAccountReactivated(ctx context.Context, in AccountReactivatedEmail) error {
	_ = ctx
	subject := fmt.Sprintf("Your Trilli account %q is active again", in.AccountName)

	rechargeHTML, rechargePlain := "", ""
	if in.RechargedCents > 0 {
		amt := fmt.Sprintf("$%.2f", float64(in.RechargedCents)/100)
		rechargeHTML = pText("We re-charged <strong>" + amt + "</strong> (the amount refunded when you deleted the account) to restore your plan.")
		rechargePlain = fmt.Sprintf("We re-charged %s (the amount refunded when you deleted the account) to restore your plan.\n\n", amt)
	}

	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n\n", greetingName(in.Name))
	fmt.Fprintf(&plain, "Good news — the scheduled deletion of %q has been cancelled and your account is fully active again.\n\n", in.AccountName)
	plain.WriteString(rechargePlain)
	fmt.Fprintf(&plain, "Pick up right where you left off: %s/home\n", appBase)

	body := pText("Good news — the scheduled deletion of <strong>"+esc(in.AccountName)+"</strong> has been cancelled and your account is fully active again.") +
		rechargeHTML +
		emailButton("Open Trilli", appBase+"/home")
	htmlBody := emailShell(shellParams{
		Preheader: "Your account is active again — the scheduled deletion was cancelled.",
		Heading:   "Your account is active again",
		BodyHTML:  body,
	})
	if err := m.send("account-reactivated", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "account-reactivated sent to %s", in.To)
	return nil
}

// --- Share link ----------------------------------------------------------

// ShareLinkEmail is the magic-link email sent when a file/folder is shared.
type ShareLinkEmail struct {
	To           string
	SharerName   string
	ResourceName string
	IsFolder     bool
	URL          string
	Capability   string
	HasPassword  bool
	ExpiresAt    *time.Time
}

// SendShareLink emails a recipient the magic link to a shared file/folder.
func (m *Mailer) SendShareLink(ctx context.Context, in ShareLinkEmail) error {
	_ = ctx
	kind := "file"
	if in.IsFolder {
		kind = "folder"
	}
	who := strings.TrimSpace(in.SharerName)
	subject := fmt.Sprintf("A %s was shared with you on Trilli", kind)
	lead := fmt.Sprintf("A %s was shared with you on Trilli.", kind)
	if who != "" {
		subject = fmt.Sprintf("%s shared a %s with you on Trilli", who, kind)
		lead = fmt.Sprintf("%s shared a %s with you on Trilli.", who, kind)
	}
	can := "view"
	if in.Capability == "download" {
		can = "view & download"
	}
	var notes []string
	notes = append(notes, "You can "+can+" it.")
	if in.HasPassword {
		notes = append(notes, "It's password protected — the sender will share the password with you separately.")
	}
	if in.ExpiresAt != nil {
		notes = append(notes, "Access expires on "+in.ExpiresAt.Format("January 2, 2006")+".")
	}

	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n\n%s\n\n", lead, in.ResourceName)
	for _, n := range notes {
		fmt.Fprintf(&plain, "%s\n", n)
	}
	fmt.Fprintf(&plain, "\nOpen it here:\n%s\n", in.URL)

	noteHTML := ""
	for _, n := range notes {
		noteHTML += `<li style="margin:2px 0;">` + esc(n) + `</li>`
	}
	body := `<p style="margin:0 0 6px 0;font-size:16px;font-weight:600;color:` + brandInk + `;font-family:` + fontStack + `;">` + esc(in.ResourceName) + `</p>` +
		`<ul style="margin:0 0 14px 0;padding-left:18px;font-size:14px;line-height:1.6;color:` + brandBody + `;font-family:` + fontStack + `;">` + noteHTML + `</ul>` +
		emailButton("Open "+kind, in.URL)
	htmlBody := emailShell(shellParams{
		Preheader:  lead,
		Heading:    lead,
		BodyHTML:   body,
		FooterNote: "If you weren't expecting this, you can ignore this email — the link won't work without it.",
	})

	if err := m.send("share-link", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "share-link sent to %s", in.To)
	return nil
}

// --- Drop-portal alert ---------------------------------------------------

// DropAlertEmail notifies a drop-portal owner that a file was uploaded.
type DropAlertEmail struct {
	To         string
	OwnerName  string
	FileName   string
	FolderName string
	PortalName string
}

// SendDropAlert tells a portal owner someone dropped a file. Best-effort.
func (m *Mailer) SendDropAlert(ctx context.Context, in DropAlertEmail) error {
	_ = ctx
	subject := "New file dropped in " + in.FolderName + " on Trilli"

	portalClause := ""
	if strings.TrimSpace(in.PortalName) != "" {
		portalClause = fmt.Sprintf(" through your “%s” drop portal", in.PortalName)
	}

	var plain strings.Builder
	fmt.Fprintf(&plain, "%s\n\n", greetingName(in.OwnerName))
	fmt.Fprintf(&plain, "Someone just uploaded a new file to your %s folder%s.\n\n", in.FolderName, portalClause)
	fmt.Fprintf(&plain, "File: %s\n\n", in.FileName)
	fmt.Fprintf(&plain, "View it at %s/files\n", appBase)

	portalClauseHTML := ""
	if strings.TrimSpace(in.PortalName) != "" {
		portalClauseHTML = " through your " + strongInk("“"+in.PortalName+"”") + " drop portal"
	}
	body := pText(esc(greetingName(in.OwnerName))+" someone just uploaded a new file to your "+strongInk(in.FolderName)+" folder"+portalClauseHTML+".") +
		fileChip(in.FileName) +
		emailButton("View in Trilli", appBase+"/files")
	htmlBody := emailShell(shellParams{
		Preheader:  "New file in " + in.FolderName,
		Heading:    "New file dropped",
		BodyHTML:   body,
		FooterNote: "You're getting this because upload alerts are on for this drop portal. Turn them off in Shared → Links.",
	})

	if err := m.send("drop-alert", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "drop-alert sent to %s", in.To)
	return nil
}
