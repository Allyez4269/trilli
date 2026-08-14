package mailer

import (
	"context"
	"fmt"
	"strings"

	"trilli/system/logging"
)

// SignRequestEmail is the payload for a Trilli Sign signature request — the
// email a recipient gets when an envelope is sent to them.
type SignRequestEmail struct {
	To            string
	RecipientName string
	SenderName    string // display name of the requester
	SenderEmail   string
	Title         string // envelope title (document name)
	Subject       string // optional custom email subject
	Message       string // optional note from the sender
	Link          string // full absolute ceremony URL
}

// SendSignRequest sends the "please sign" email. Best-effort — the envelope
// stays sent even if a recipient email bounces (the sender can re-share the
// link from the envelope view).
func (m *Mailer) SendSignRequest(ctx context.Context, in SignRequestEmail) error {
	_ = ctx
	if strings.TrimSpace(in.Link) == "" {
		return fmt.Errorf("mailer: empty sign link")
	}
	subject := strings.TrimSpace(in.Subject)
	if subject == "" {
		// default subject inherits the agreement name
		subject = fmt.Sprintf("Please sign: %s", in.Title)
	}

	var plain strings.Builder
	fmt.Fprintf(&plain, "Your signature is requested on %q.\n\n", in.Title)
	if strings.TrimSpace(in.Message) != "" {
		fmt.Fprintf(&plain, "%s\n\n", in.Message)
	}
	fmt.Fprintf(&plain, "Review and sign:\n%s\n\n", in.Link)
	plain.WriteString("This document is hosted on Trilli — encrypted, private, and auditable.\n")

	// the heading already says who this is: "Your signature is requested" —
	// the body shows the agreement and the sender's message, nothing else
	body := pText(strongInk(esc(in.Title)))
	if strings.TrimSpace(in.Message) != "" {
		body += pText(`<em>&ldquo;` + esc(in.Message) + `&rdquo;</em>`)
	}
	body += emailButton("Review & sign", in.Link) + copyableLink(in.Link)

	htmlBody := emailShell(shellParams{
		Preheader:  "Signature requested: " + in.Title,
		Heading:    "Your signature is requested",
		BodyHTML:   body,
		FooterNote: "This document is hosted on Trilli — encrypted, private, and auditable. If you weren't expecting this, you can safely ignore this email.",
	})

	if err := m.send("sign-request", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "sign request sent to %s (title=%q)", in.To, in.Title)
	return nil
}

// SignCompletedEmail notifies the sender that every recipient has signed.
type SignCompletedEmail struct {
	To      string
	Name    string // sender display name (may be empty)
	Title   string
	Signers string // comma-joined signer names
}

// SendSignCompleted tells the envelope's sender the document is fully signed.
func (m *Mailer) SendSignCompleted(ctx context.Context, in SignCompletedEmail) error {
	_ = ctx
	subject := fmt.Sprintf("Completed: %q has been signed by everyone", in.Title)

	var plain strings.Builder
	fmt.Fprintf(&plain, "Good news — %q is fully signed.\n\n", in.Title)
	if in.Signers != "" {
		fmt.Fprintf(&plain, "Signed by: %s\n\n", in.Signers)
	}
	plain.WriteString("Open Trilli Sign to review the envelope and its audit trail.\n")

	body := pText(`Good news — ` + strongInk(esc(in.Title)) + ` has been signed by every recipient.`)
	if in.Signers != "" {
		body += pText(`<span style="color:` + brandMuted + `;">Signed by:</span> ` + esc(in.Signers))
	}
	body += emailButton("Open Trilli Sign", "https://app.trilli.com/apps/sign")

	htmlBody := emailShell(shellParams{
		Preheader:  "Fully signed: " + in.Title,
		Heading:    "Envelope completed",
		BodyHTML:   body,
		FooterNote: "The complete, tamper-evident audit trail is available in Trilli Sign.",
	})

	if err := m.send("sign-completed", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "sign completed notice sent to %s (title=%q)", in.To, in.Title)
	return nil
}

// SignDeclinedEmail notifies the sender that a recipient declined to sign.
type SignDeclinedEmail struct {
	To     string
	Name   string // sender display name (may be empty)
	Title  string
	Signer string
	Reason string // optional signer-provided reason
}

// SendSignDeclined tells the envelope's sender a recipient declined.
func (m *Mailer) SendSignDeclined(ctx context.Context, in SignDeclinedEmail) error {
	_ = ctx
	subject := fmt.Sprintf("Declined: %s declined to sign %q", in.Signer, in.Title)

	var plain strings.Builder
	fmt.Fprintf(&plain, "%s declined to sign %q.\n\n", in.Signer, in.Title)
	if in.Reason != "" {
		fmt.Fprintf(&plain, "Reason: %s\n\n", in.Reason)
	}
	plain.WriteString("The envelope is closed. Open Trilli Sign to review it or send a new one.\n")

	body := pText(strongInk(esc(in.Signer)) + ` declined to sign ` + strongInk(esc(in.Title)) + `.`)
	if in.Reason != "" {
		body += pText(`<span style="color:` + brandMuted + `;">Reason:</span> ` + esc(in.Reason))
	}
	body += emailButton("Open Trilli Sign", "https://app.trilli.com/apps/sign")

	htmlBody := emailShell(shellParams{
		Preheader:  "Declined: " + in.Title,
		Heading:    "Envelope declined",
		BodyHTML:   body,
		FooterNote: "The envelope is closed; its audit trail records the decline.",
	})

	if err := m.send("sign-declined", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "sign declined notice sent to %s (title=%q)", in.To, in.Title)
	return nil
}

// SignVoidedEmail tells a recipient the envelope was voided by its sender.
type SignVoidedEmail struct {
	To     string
	Name   string // recipient display name
	Title  string
	Sender string // who voided it (email)
}

// SendSignVoided notifies a recipient that the agreement is void: the signing
// link no longer works and the document should be considered withdrawn.
func (m *Mailer) SendSignVoided(ctx context.Context, in SignVoidedEmail) error {
	_ = ctx
	subject := fmt.Sprintf("Voided: %q is no longer available for signing", in.Title)

	var plain strings.Builder
	if in.Name != "" {
		fmt.Fprintf(&plain, "Hi %s,\n\n", in.Name)
	}
	fmt.Fprintf(&plain, "%q has been voided by its sender and is no longer available for signature.\n\n", in.Title)
	plain.WriteString("The agreement should be considered void; your signing link has been deactivated. No action is needed.\n")

	body := pText(strongInk(esc(in.Title)) + ` has been voided by its sender and is no longer available for signature.`)
	body += pText(`The agreement should be considered void; your signing link has been deactivated. No action is needed.`)

	htmlBody := emailShell(shellParams{
		Preheader:  "Voided: " + in.Title,
		Heading:    "Envelope voided",
		BodyHTML:   body,
		FooterNote: "This notice was sent because the envelope's sender withdrew the document.",
	})

	if err := m.send("sign-voided", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "sign voided notice sent to %s (title=%q)", in.To, in.Title)
	return nil
}

// SignFinalCopyEmail tells a signer the document is fully executed.
type SignFinalCopyEmail struct {
	To    string
	Name  string
	Title string
	Link  string // the signer's ceremony page (offers the sealed download)
}

// SendSignFinalCopy notifies a signer that every party has signed and their
// sealed copy is ready to download.
func (m *Mailer) SendSignFinalCopy(ctx context.Context, in SignFinalCopyEmail) error {
	_ = ctx
	subject := fmt.Sprintf("Fully signed: %q is complete", in.Title)

	var plain strings.Builder
	if in.Name != "" {
		fmt.Fprintf(&plain, "Hi %s,\n\n", in.Name)
	}
	fmt.Fprintf(&plain, "%q has been signed by all parties.\n\nDownload your sealed copy: %s\n", in.Title, in.Link)

	body := pText(`Every party has signed ` + strongInk(esc(in.Title)) + `. Your copy is sealed with a cryptographic signature and ready to download.`)
	body += emailButton("Download your signed copy", in.Link)
	body += copyableLink(in.Link)

	htmlBody := emailShell(shellParams{
		Preheader:  "Fully signed: " + in.Title,
		Heading:    "Document complete",
		BodyHTML:   body,
		FooterNote: "This link is unique to you — anyone with it can view the signed document.",
	})

	if err := m.send("sign-final-copy", subject, in.To, plain.String(), htmlBody); err != nil {
		return err
	}
	logging.Info(packageName, "final-copy notice sent to %s (title=%q)", in.To, in.Title)
	return nil
}
