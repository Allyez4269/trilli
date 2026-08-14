package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"trilli/system/accountdelete"
	"trilli/system/adminapi"
	"trilli/system/apikeys"
	"trilli/system/audit"
	"trilli/system/auth"
	"trilli/system/billing"
	"trilli/system/clouddrive"
	"trilli/system/cluster"
	"trilli/system/comp"
	"trilli/system/credentials"
	"trilli/system/crypto"
	"trilli/system/database/migrations"
	"trilli/system/database/postgres"
	"trilli/system/docpreview"
	"trilli/system/files"
	"trilli/system/folders"
	"trilli/system/googleauth"
	"trilli/system/invites"
	"trilli/system/keystore"
	"trilli/system/lifecycle"
	"trilli/system/logging"
	"trilli/system/mailer"
	"trilli/system/metering"
	"trilli/system/microsoftauth"
	"trilli/system/notifications"
	"trilli/system/officeapi"
	"trilli/system/officesessions"
	"trilli/system/passkeys"
	"trilli/system/passwordreset"
	"trilli/system/pdftools"
	"trilli/system/plans"
	"trilli/system/productivity"
	"trilli/system/qserve"
	"trilli/system/server"
	"trilli/system/session"
	"trilli/system/sharing"
	"trilli/system/sign"
	"trilli/system/signupintents"
	"trilli/system/stats"
	"trilli/system/storage"
	"trilli/system/storage/azureblob"
	"trilli/system/storage/encryptedstore"
	"trilli/system/storage/tiering"
	"trilli/system/supporttickets"
	"trilli/system/tenantsettings"
	"trilli/system/thumbs"
	"trilli/system/tls"
	"trilli/system/trash"
	"trilli/system/twofactor"
	"trilli/system/web"
	"trilli/system/workspaces"
)

const packageName = "main"

func main() {
	logging.EnableAll()

	// Source the master key from the database (wrapped by the binary's root
	// key), seeding it once from APP_ENCRYPTION_KEY if not yet present. Just
	// registers the provider; it runs lazily on first crypto use.
	keystore.Install()

	args := os.Args[1:]
	if len(args) == 0 {
		runServer()
		return
	}

	switch args[0] {
	case "migrate":
		runMigrate(args[1:])
	case "creds":
		runCreds(args[1:])
	case "stripe":
		runStripe(args[1:])
	case "lifecycle":
		runLifecycle(args[1:])
	case "storage":
		runStorage(args[1:])
	case "jobs":
		runJobs(args[1:])
	case "key":
		runKey(args[1:])
	case "mail":
		runMail(args[1:])
	case "help", "-h", "--help":
		printUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "trilli: unknown command %q\n\n", args[0])
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, `Usage:
  trilli                       Start the HTTP server daemon
  trilli migrate up            Apply all pending DB migrations
  trilli migrate down [N]      Roll back N migration steps (default 1)
  trilli migrate version       Print current migration version
  trilli stripe sync-plans     Sync the plan catalog to Stripe Products/Prices
  trilli lifecycle status      List lapsed accounts + days until purge
  trilli lifecycle sweep       Run the lapse-warning + purge sweep now
  trilli lifecycle purge <id>  Force-purge one lapsed tenant (testing)
  trilli storage report        Analyze per-tenant storage/egress + tiering savings
  trilli storage tier          Dry-run the adaptive tiering pass (no changes)
  trilli storage tier --apply  Run the tiering pass for real (re-tiers blobs)
  trilli storage encrypt-blobs Encrypt all legacy plaintext blobs at rest
  trilli jobs status           Show last run of each coordinated background job
  trilli jobs ping             Test cluster mutual-exclusion (advisory lock)
  trilli key status            Show whether the master key is stored in the DB
  trilli key seed              Seed the DB master key from APP_ENCRYPTION_KEY
  trilli mail preview <email>  Send one sample of every transactional email
  trilli help                  Print this help`)
}

// runMail handles `trilli mail <subcommand>`.
func runMail(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "trilli mail: missing subcommand (try: preview <email>)")
		os.Exit(2)
	}
	switch args[0] {
	case "preview":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			fmt.Fprintln(os.Stderr, "trilli mail preview: missing recipient email")
			os.Exit(2)
		}
		runMailPreview(strings.TrimSpace(args[1]), args[2:])
	default:
		fmt.Fprintf(os.Stderr, "trilli mail: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

// runMailPreview sends one representative sample of every transactional email
// to `to`, with realistic placeholder data, so the rendered look-and-feel can
// be reviewed in a real inbox (desktop + mobile). Each subject is prefixed
// "[Preview]" so the batch is easy to spot. Sends are sequential and best
// effort — one failure doesn't stop the rest.
func runMailPreview(to string, only []string) {
	want := map[string]bool{}
	for _, n := range only {
		want[strings.TrimSpace(n)] = true
	}
	ctx := context.Background()
	m := mailer.New()
	now := time.Now()
	exp := now.Add(60 * time.Minute)
	shareExp := now.Add(7 * 24 * time.Hour)

	// Each entry sends one email and reports pass/fail. The "[Preview] " subject
	// prefix is applied by giving sample data; the mailer owns the real subject,
	// so we can't inject it directly — instead we note it's a preview in the
	// recipient-visible content via realistic sample fields.
	type step struct {
		name string
		send func() error
	}
	steps := []step{
		{"welcome", func() error {
			return m.SendWelcome(ctx, mailer.WelcomeEmail{To: to, FirstName: "Alex", Link: "https://app.trilli.com/home"})
		}},
		{"invite", func() error {
			return m.SendInvite(ctx, mailer.InviteEmail{To: to, TenantName: "Northwind Studio", InviterName: "Jordan Reyes", InviterEmail: "jordan@northwind.example", Role: "member", Link: "https://app.trilli.com/invite/sample-token-abc123", ExpiresAt: now.Add(7 * 24 * time.Hour)})
		}},
		{"password-reset", func() error {
			return m.SendPasswordReset(ctx, mailer.PasswordResetEmail{To: to, Name: "Alex", Link: "https://app.trilli.com/reset-password/sample-token-abc123", ExpiresAt: exp})
		}},
		{"email-change", func() error {
			return m.SendEmailChange(ctx, mailer.EmailChangeEmail{To: to, Name: "Alex", CurrentEmail: "old@example.com", Link: "https://app.trilli.com/confirm-email/sample-token-abc123", ExpiresAt: exp})
		}},
		{"signup-confirm", func() error {
			return m.SendSignupConfirm(ctx, mailer.SignupConfirmEmail{To: to, Link: "https://app.trilli.com/signup/verify/sample-token-abc123"})
		}},
		{"signup-resume", func() error {
			return m.SendSignupResume(ctx, mailer.SignupResumeEmail{To: to, Link: "https://app.trilli.com/signup/complete/sample-token-abc123"})
		}},
		{"receipt-purchase", func() error {
			return m.SendReceipt(ctx, mailer.ReceiptEmail{To: to, Name: "Alex", PlanName: "Plus", BillingPeriod: "annual", AmountCents: 23748, Currency: "usd", OrderNumber: "TRL-874446113058265", ReceiptURL: "https://pay.stripe.com/receipts/sample", PaidAt: now, Kind: "purchase"})
		}},
		{"receipt-seats", func() error {
			return m.SendReceipt(ctx, mailer.ReceiptEmail{To: to, Name: "Alex", PlanName: "Plus", BillingPeriod: "annual", AmountCents: 6000, CreditCents: 4513, Currency: "usd", OrderNumber: "TRL-487276697232616", ReceiptURL: "https://pay.stripe.com/receipts/sample", PaidAt: now, Kind: "seats", SeatCount: 1})
		}},
		{"payment-failed", func() error {
			return m.SendPaymentFailed(ctx, mailer.PaymentFailedEmail{To: to, Name: "Alex", PlanName: "Plus", AmountCents: 2999, Currency: "usd", InvoiceURL: "https://pay.stripe.com/invoice/sample"})
		}},
		{"lapse-warning", func() error {
			return m.SendLapseWarning(ctx, mailer.LapseWarningEmail{To: to, Name: "Alex", PurgeDate: now.Add(30 * 24 * time.Hour).Format("January 2, 2006"), DaysLeft: 30, Final: false})
		}},
		{"lapse-final", func() error {
			return m.SendLapseWarning(ctx, mailer.LapseWarningEmail{To: to, Name: "Alex", PurgeDate: now.Add(24 * time.Hour).Format("January 2, 2006"), DaysLeft: 1, Final: true})
		}},
		{"account-close-confirm", func() error {
			return m.SendAccountCloseConfirm(ctx, mailer.AccountCloseConfirmEmail{To: to, Name: "Alex", AccountName: "Northwind Studio", PurgeDate: now.Add(30 * 24 * time.Hour).Format("January 2, 2006"), DaysLeft: 30, RefundCents: 1999})
		}},
		{"account-close-warning", func() error {
			return m.SendAccountCloseWarning(ctx, mailer.AccountCloseWarningEmail{To: to, Name: "Alex", AccountName: "Northwind Studio", PurgeDate: now.Add(24 * time.Hour).Format("January 2, 2006"), DaysLeft: 1, Final: true})
		}},
		{"account-reactivated", func() error {
			return m.SendAccountReactivated(ctx, mailer.AccountReactivatedEmail{To: to, Name: "Alex", AccountName: "Northwind Studio", RechargedCents: 1999})
		}},
		{"ticket-created", func() error {
			return m.SendTicketCreated(ctx, mailer.TicketCreatedEmail{To: to, Name: "Alex Doe", Number: "sk-06132026149508857", Subject: "Can't preview a large PDF", Severity: "high", Link: "https://app.trilli.com/support/sk-06132026149508857"})
		}},
		{"ticket-update", func() error {
			return m.SendTicketUpdate(ctx, mailer.TicketUpdateEmail{To: to, Name: "Alex Doe", Number: "sk-06132026149508857", Subject: "Can't preview a large PDF", Status: "resolved", Link: "https://app.trilli.com/support/sk-06132026149508857"})
		}},
		{"share-link", func() error {
			return m.SendShareLink(ctx, mailer.ShareLinkEmail{To: to, SharerName: "Jordan Reyes", ResourceName: "Q3 Board Deck.pdf", IsFolder: false, URL: "https://app.trilli.com/s/sample-token-abc123", Capability: "download", HasPassword: true, ExpiresAt: &shareExp})
		}},
		{"drop-alert", func() error {
			return m.SendDropAlert(ctx, mailer.DropAlertEmail{To: to, OwnerName: "Alex", FileName: "signed-contract.pdf", FolderName: "Client Intake", PortalName: "Send us your documents"})
		}},
	}

	fmt.Printf("Sending sample emails to %s …\n", to)
	ok, fail := 0, 0
	for _, s := range steps {
		if len(want) > 0 && !want[s.name] {
			continue
		}
		if err := s.send(); err != nil {
			fmt.Printf("  ✗ %-18s %v\n", s.name, err)
			fail++
			continue
		}
		fmt.Printf("  ✓ %-18s sent\n", s.name)
		ok++
	}
	fmt.Printf("\nDone: %d sent, %d failed.\n", ok, fail)
	if fail > 0 {
		os.Exit(1)
	}
}

// fileBackend is the blob store the app wires everywhere: it covers random-key
// writes (PutAt, preview cache) and tiering (SetTier) on top of the basic
// Store. Both the raw Azure store and the encrypting decorator satisfy it, so
// the rest of the app is oblivious to whether encryption is on.
type fileBackend interface {
	storage.KeyedStore
	SetTier(ctx context.Context, blobPath, tier string) error
}

func runServer() {
	logging.Info(packageName, "Trilli starting")

	pg, err := postgres.NewClient(nil)
	if err != nil {
		logging.Error(packageName, "DB connect failed: %v", err)
		os.Exit(1)
	}
	defer pg.Close()

	sessions := session.NewStore(pg)
	authSvc := auth.NewService(pg, sessions)

	rawBlobStore, err := azureblob.New(azureblob.DefaultConfig())
	if err != nil {
		logging.Error(packageName, "Blob store init failed: %v", err)
		os.Exit(1)
	}
	// Trilli-controlled encryption at rest: wrap the blob store so file bytes
	// are encrypted with a per-tenant key before they reach Azure (the
	// hyperscaler only ever sees ciphertext). Activated ONLY when a real
	// APP_ENCRYPTION_KEY is configured — never the insecure dev fallback, since
	// encrypting under the wrong key would mean permanent data loss. Reads
	// transparently handle legacy plaintext blobs, so this is safe to toggle.
	var blobStore fileBackend = rawBlobStore
	var encStore *encryptedstore.Store
	if crypto.KeyConfigured() {
		encStore = encryptedstore.New(rawBlobStore, encryptedstore.NewKeyService(pg))
		blobStore = encStore
		logging.Info(packageName, "blob encryption ENABLED — per-tenant AES-256-GCM, hyperscaler sees ciphertext only")
	} else {
		logging.Error(packageName, "APP_ENCRYPTION_KEY not set — blob encryption DISABLED; files stored as plaintext")
	}
	mailerClient := mailer.New()
	authHandlers := auth.NewHandlers(authSvc, blobStore, mailerClient)
	auditSvc := audit.NewService(pg)
	auditHandlers := audit.NewHandlers(auditSvc)
	meteringSvc := metering.NewService(pg)
	filesSvc := files.NewService(pg, blobStore, meteringSvc)
	// Office→PDF preview, rendered in-process via headless LibreOffice and
	// cached in a hidden per-tenant blob prefix. Optional: if LibreOffice
	// isn't installed we log and run with previews disabled rather than
	// failing startup. Swap NewLibreOfficeConverter for a remote (Gotenberg)
	// converter here to isolate conversion into a sidecar later. The same
	// converter also backs Productivity's export-to-legacy-format (.doc/.xls/.ppt)
	// save-as + download, so it's hoisted out of the if-block and shared.
	var officeConv officeapi.Converter
	if conv, err := docpreview.NewLibreOfficeConverter(); err != nil {
		logging.Error(packageName, "Office preview disabled: %v", err)
	} else {
		filesSvc.SetPreview(docpreview.NewService(conv, blobStore))
		officeConv = conv
	}
	// List-row thumbnails (images in-process; PDFs/videos via pdftoppm/ffmpeg
	// when present), cached in a hidden per-tenant blob prefix like previews.
	filesSvc.SetThumbs(thumbs.NewService(blobStore))
	foldersSvc := folders.NewService(pg)
	// filesHandlers needs authSvc + foldersSvc for the cross-account copy
	// endpoint (authorize the actor against a non-session tenant, recreate
	// folder trees in it).
	filesHandlers := files.NewHandlers(filesSvc, auditSvc, authSvc, foldersSvc)
	officeSvc := officesessions.NewService(pg, blobStore)
	officeHandlers := officeapi.NewHandlers(officeSvc, filesSvc, officeConv)
	// Avatars follow the collaboration trust boundary: co-participants in a live
	// office session can see each other's avatar across tenants.
	authHandlers.SetAvatarTrust(officeSvc.SharesActiveSession)
	productivityHandlers := productivity.NewHandlers(productivity.NewService(pg))
	pdftoolsHandlers := pdftools.NewHandlers(filesSvc)
	clouddriveSvc := clouddrive.NewService(pg, credentials.NewService(pg), filesSvc, os.Getenv("GOOGLE_DRIVE_OAUTH_REDIRECT"))
	sharingHandlers := sharing.NewHandlers(sharing.NewService(pg, blobStore, meteringSvc, mailerClient, filesSvc))
	foldersHandlers := folders.NewHandlers(foldersSvc, auditSvc)
	trashSvc := trash.NewService(pg, blobStore)
	trashHandlers := trash.NewHandlers(trashSvc, auditSvc)
	tenantSettingsSvc := tenantsettings.NewService(pg)
	tenantSettingsHandlers := tenantsettings.NewHandlers(tenantSettingsSvc)
	workspacesSvc := workspaces.NewService(pg)
	workspacesHandlers := workspaces.NewHandlers(workspacesSvc)
	invitesSvc := invites.NewService(pg, sessions, tenantSettingsSvc)
	invitesHandlers := invites.NewHandlers(invitesSvc, mailerClient)
	twofactorSvc := twofactor.NewService(pg)
	twofactorHandlers := twofactor.NewHandlers(twofactorSvc, authSvc)
	passkeysSvc, err := passkeys.NewService(pg)
	if err != nil {
		logging.Error(packageName, "Passkeys init failed: %v", err)
		os.Exit(1)
	}
	passkeysHandlers := passkeys.NewHandlers(passkeysSvc, authSvc)
	passwordResetHandlers := passwordreset.NewHandlers(authSvc, twofactorSvc, passkeysSvc, mailerClient)
	notificationsHandlers := notifications.NewHandlers(notifications.NewService(pg))
	// Support ticketing: customer-raised incidents + threaded conversation.
	// Emails the requester on create / agent reply (best-effort). A future CMS
	// reads these tables and posts replies via supporttickets.Service.PostAgentMessage.
	supportSvc := supporttickets.NewService(pg, mailerClient, "")
	supportHandlers := supporttickets.NewHandlers(supportSvc)
	statsHandlers := stats.NewHandlers(stats.NewService(pg, meteringSvc))
	plansHandlers := plans.NewHandlers(plans.NewService(pg))
	// Programmatic API keys + the /api/v1 surface they authenticate.
	apiKeysHandlers := apikeys.NewHandlers(apikeys.NewService(pg), filesSvc, foldersSvc)
	// Billing: Stripe keys come from the encrypted credentials vault. Disabled
	// (endpoints 503) if no secret key is stored. The webhook assigns the plan
	// via authSvc.ChangePlan (assign + price lock) on payment success.
	billingSvc := billing.NewService(pg, credentials.NewService(pg))
	billingHandlers := billing.NewHandlers(billingSvc, authSvc.ChangePlan, mailerClient)
	// Owner-initiated account deletion (recoverable 30-day soft close) + reactivation.
	accountDeleteHandlers := accountdelete.NewHandlers(authSvc, billingSvc, twofactorSvc, passkeysSvc, auditSvc, mailerClient)
	// Pay-before-account signup funnel: holds pre-account state in signup_intents,
	// opens hosted Stripe Checkout, and provisions the account post-payment. The
	// billing webhook hands completed funnel checkouts to the signup layer.
	signupSvc := signupintents.NewService(pg)
	signupHandlers := signupintents.NewHandlers(signupSvc, billingSvc, authSvc, mailerClient)
	billingHandlers.SetSignupPaidHook(signupHandlers.OnCheckoutPaid)
	billingHandlers.SetSignupSubActivatedHook(signupHandlers.OnSignupSubActivated)
	// "Continue with Google" — OAuth login + sign-up. Client id/secret come from
	// the credentials vault; the redirect URI must match the Google console
	// (override via GOOGLE_OAUTH_REDIRECT, else the app.trilli.com default).
	googleAuthSvc := googleauth.NewService(authSvc, signupSvc, credentials.NewService(pg), os.Getenv("GOOGLE_OAUTH_REDIRECT"))
	// "Continue with Microsoft" — same OAuth flow against the Microsoft identity
	// platform. Client id/secret come from the vault; the redirect URI must
	// match the Azure app registration (override via MICROSOFT_OAUTH_REDIRECT,
	// else the app.trilli.com default).
	microsoftAuthSvc := microsoftauth.NewService(authSvc, signupSvc, credentials.NewService(pg), os.Getenv("MICROSOFT_OAUTH_REDIRECT"))
	// Lifecycle: lapsed-account warnings + the 30-day purge of ended accounts.
	// Driven by its own janitor below; never touches non-lapsed tenants.
	lifecycleSvc := lifecycle.NewService(pg, blobStore, mailerClient)
	// GeoIP (qserve): auto-fetches the MaxMind DB into data/geolocation and
	// serves IP→location lookups. Init is non-blocking + non-fatal.
	geoipSvc := qserve.NewService()
	if err := geoipSvc.Initialize(); err != nil {
		logging.Error(packageName, "GeoIP init failed: %v", err)
	}
	geoipHandler := qserve.NewHandler(geoipSvc)
	// Wire the GeoIP service into session creation so every new session records
	// resolved country/region/city/postal/lat/long alongside ip + user-agent.
	sessions.SetGeoResolver(geoResolver{geoipSvc})

	// PORT env override (defaults to 8081 when unset, so production is
	// unchanged). Lets a scratch instance run on a free port alongside the live
	// daemon — used to test the CMX /api/admin surface without touching prod.
	srvCfg := server.DefaultConfig()
	if p := strings.TrimSpace(os.Getenv("PORT")); p != "" {
		if n, perr := strconv.Atoi(p); perr == nil && n > 0 {
			srvCfg.Port = n
		}
	}

	// TLS: terminate HTTPS in-process using our wildcard certificate
	// (system/tls). Cert paths are baked in (~/certs) — no environment
	// variables — so the app serves HTTPS directly on its own IP.
	tlsMgr, err := tls.New(tls.DefaultConfig())
	if err != nil {
		logging.Fatal(packageName, "TLS init failed: %v", err)
	}
	srvCfg.TLSConfig = tlsMgr.TLSConfig()

	srv := server.NewServer(srvCfg)
	authHandlers.Register(srv)
	filesHandlers.Register(srv, authHandlers.RequireAuth)
	officeHandlers.Register(srv, authHandlers.RequireAuth)
	productivityHandlers.Register(srv, authHandlers.RequireAuth, authHandlers.RequireAdminOrOwner)
	pdftoolsHandlers.Register(srv, authHandlers.RequireAuth)
	clouddriveSvc.Register(srv, authHandlers.RequireAuth)

	// Trilli Sign (PRIVATE BETA — server-gated to the allowlist in system/sign).
	signSvc := sign.NewService(pg, blobStore, mailerClient, credentials.NewService(pg))
	signSvc.SetFileDepositor(filesSvc) // completed agreements deposit into Files
	if lo, ok := officeConv.(sign.DocConverter); ok && officeConv != nil {
		signSvc.SetConverter(lo) // Word→PDF envelope sources via LibreOffice
	}
	signSvc.SetGeoLookup(geoipSvc) // in-house GeoIP for the signing disclosure
	signSvc.Register(srv, authHandlers.RequireAuth)
	foldersHandlers.Register(srv, authHandlers.RequireAuth)
	// Sharing: /api/shares management (session-gated) + public /api/s/{token}
	// download/browse (token + optional password is the access control).
	sharingHandlers.Register(srv, authHandlers.RequireAuth)
	// Invite endpoints are admin/owner only — Members/Viewers shouldn't
	// be able to invite or revoke. The unauthenticated lookup + accept
	// flows are registered separately inside invitesHandlers and don't
	// pass through this middleware.
	invitesHandlers.Register(srv, authHandlers.RequireAdminOrOwner)
	// Activity / audit log — any signed-in member; admins/owners see the whole
	// workspace, members are scoped to their own events (in the handler).
	auditHandlers.Register(srv, authHandlers.RequireAuth)
	// Trash list/restore/permanent-delete — tenant-scoped, folder-access gated.
	trashHandlers.Register(srv, authHandlers.RequireAuth)
	// Workspace invite-policy settings — owner/admin only (owner-only fields
	// gated inside the handler).
	tenantSettingsHandlers.Register(srv, authHandlers.RequireAdminOrOwner)
	// Workspaces — listing for any member; create/assign owner/admin only.
	workspacesHandlers.Register(srv, authHandlers.RequireAuth, authHandlers.RequireAdminOrOwner)
	// Per-user 2FA (authenticator app) — managed against the caller's own user.
	twofactorHandlers.Register(srv, authHandlers.RequireAuth)
	// Per-user passkeys (WebAuthn). Management routes require a session; the
	// passwordless login routes are public (registered inside Register).
	passkeysHandlers.Register(srv, authHandlers.RequireAuth)
	// Verified password reset (step-up 2FA/passkey → emailed set-password link).
	// The /api/me/* routes require a session; the token routes are public.
	passwordResetHandlers.Register(srv, authHandlers.RequireAuth)
	accountDeleteHandlers.Register(srv, authHandlers.RequireAuth)
	// Per-user email notification preferences + consent/audit ledger.
	notificationsHandlers.Register(srv, authHandlers.RequireAuth)
	// Support tickets — any signed-in member (members see their own tickets,
	// owners/admins see the whole tenant's, gated in the handler).
	supportHandlers.Register(srv, authHandlers.RequireAuth)
	statsHandlers.Register(srv, authHandlers.RequireAuth)
	// Public plan catalog (available plans only) — feeds Settings → Plans and,
	// later, the signup picker. Public by design (no session required).
	plansHandlers.Register(srv)
	// Programmatic API: /api/keys management (admin/owner + plan-gated) and the
	// Bearer-key-authed /api/v1/* surface (both wired inside Register).
	apiKeysHandlers.Register(srv, authHandlers.RequireAdminOrOwner)
	// Stripe billing: config + create-intent (owner/admin) + public signed webhook.
	billingHandlers.Register(srv, authHandlers.RequireAuth, authHandlers.RequireAdminOrOwner)
	// Pay-before-account signup funnel — public endpoints (run before an account
	// exists): start (email + confirm), verify (→ hosted Checkout), intent (state),
	// complete (Step 6 → provision + log in).
	signupHandlers.Register(srv, authHandlers.RequireAuth)
	// Public OAuth routes: /api/auth/google/start + /callback.
	googleAuthSvc.Register(srv)
	// Public OAuth routes: /api/auth/microsoft/start + /callback.
	microsoftAuthSvc.Register(srv)
	// GeoIP lookup endpoint — gated by the qserve static API key (?k=), so it's
	// registered outside the session middleware. GET /api/geoip?k=...&ip=...
	srv.Handle("/api/geoip", geoipHandler)
	srv.Handle("GET /api/browse", authHandlers.RequireAuth(browseHandler(authSvc, foldersSvc, filesSvc, workspacesSvc)))
	srv.Handle("GET /api/copy-destinations", authHandlers.RequireAuth(copyDestinationsHandler(authSvc, foldersSvc, workspacesSvc)))

	// CMX operator surface: /api/admin/* tenant mutations, authenticated as the
	// CMX service principal (shared token in the credentials vault), NOT an app
	// user session. See system/adminapi + Trilli CMX (cmx.trilli.com).
	compSvc := comp.NewService(pg, authSvc)
	adminH := adminapi.NewHandlers(adminapi.NewService(pg), credentials.NewService(pg), plans.NewService(pg), compSvc, signupSvc, mailerClient, billingSvc, supportSvc)
	adminH.Register(srv)
	// Infrastructure write ops (jobs run-now + on-demand tiering) are attached
	// below, once the coordinator + job registry + tiering store exist.
	// NOTE: comp/ambassador invites now flow through the unified signup-intent
	// registration page (/signup/complete); the old standalone /api/comp/* and
	// /comp/:token acceptance path has been retired.

	// SPA must register last so its catch-all "/" doesn't shadow API routes.
	spa, err := web.Handler()
	if err != nil {
		logging.Error(packageName, "SPA handler init failed: %v", err)
		os.Exit(1)
	}
	srv.Handle("/", spa)

	// Internal loopback WOPI listener (plain HTTP, 127.0.0.1 only). The
	// Collabora engine reaches trilli-serve's WOPI endpoints server-side over
	// this listener (its aliasgroup points at http://127.0.0.1:8090). It shares
	// the public server's mux so the /api/office/wopi/* routes registered above
	// are reachable here; the browser NEVER talks to this listener (it's bound
	// to loopback only). The engine's only credential is the unguessable session
	// key (the WOPI access_token).
	wopiAddr := "127.0.0.1:8090"
	if v := strings.TrimSpace(os.Getenv("TRILLI_WOPI_LISTEN")); v != "" {
		wopiAddr = v
	}
	wopiSrv := &http.Server{Addr: wopiAddr, Handler: srv.Mux(), ReadTimeout: 60 * time.Second, WriteTimeout: 300 * time.Second}
	go func() {
		logging.Info(packageName, "Internal WOPI listener on http://%s", wopiAddr)
		if err := wopiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Error(packageName, "WOPI listener failed: %v", err)
		}
	}()

	// Background jobs. Every replica schedules these, but each run is wrapped in
	// a cluster-wide advisory lock (system/cluster) so only ONE node actually
	// does the work per tick — no duplicate emails / Azure ops across the
	// cluster. Initial delays are staggered so the jobs don't all fire at once.
	janitorCtx, stopJanitor := context.WithCancel(context.Background())
	coord := cluster.NewCoordinator(pg)
	logging.Info(packageName, "background jobs coordinated as node %s", coord.NodeID())

	// Eager preview/thumbnail warming for fresh uploads. Deliberately NOT a
	// coordinated cluster job: each node warms only the uploads it received,
	// which is collision-free by construction, and results land in the shared
	// blob cache where every node serves them.
	filesSvc.StartWarmers(janitorCtx)

	// Background job closures are defined once and registered in runnableJobs so
	// the scheduled loop and the CMX run-now admin endpoint share identical fns.
	runnableJobs := map[string]func(context.Context) error{}

	// Trash purge — delete blobs + rows for batches past their 7-day retention.
	trashJob := func(ctx context.Context) error { _, err := trashSvc.PurgeExpired(ctx); return err }
	runnableJobs["trash-purge"] = trashJob
	go runScheduled(janitorCtx, coord, "trash-purge", 30*time.Second, time.Hour, trashJob)

	// Blob encryption migration — re-encrypt legacy plaintext blobs in the
	// background until none remain (no-op once done). Only when encryption is on.
	if encStore != nil {
		mig := encryptedstore.NewMigrator(pg, encStore)
		encJob := func(ctx context.Context) error {
			n, err := mig.MigrateBatch(ctx, 200)
			if err == nil && n > 0 {
				logging.Info(packageName, "blob-encrypt: encrypted %d legacy blob(s)", n)
			}
			return err
		}
		runnableJobs["blob-encrypt-migrate"] = encJob
		go runScheduled(janitorCtx, coord, "blob-encrypt-migrate", 2*time.Minute, 30*time.Minute, encJob)
	}

	// Lifecycle — lapse warnings + purge of accounts past their 30-day window.
	lifecycleJob := func(ctx context.Context) error { return lifecycleSvc.RunSweep(ctx) }
	runnableJobs["lifecycle-sweep"] = lifecycleJob
	go runScheduled(janitorCtx, coord, "lifecycle-sweep", 90*time.Second, 6*time.Hour, lifecycleJob)

	// Signup funnel — expire abandoned pre-paid intents + send deferred
	// "finish setting up" reminders to buyers still 'paid' past a grace window.
	signupJob := func(ctx context.Context) error {
		if _, err := signupSvc.ExpireStale(ctx); err != nil {
			return err
		}
		_, err := signupHandlers.SweepResumeReminders(ctx, 20*time.Minute)
		return err
	}
	runnableJobs["signup-sweep"] = signupJob
	go runScheduled(janitorCtx, coord, "signup-sweep", time.Hour, time.Hour, signupJob)

	// Productivity session sanitation — reap edit sessions past their idle TTL
	// (4h after last engine activity) and delete their per-session working blobs
	// from the tenant scratch area (tenants/{id}/.office/work/...). Catches
	// browser closes / crashes that a purely client-driven cleanup can't, so the
	// hidden scratch area never accumulates orphaned working files. Coordinated
	// so only one node reaps per tick.
	officeSweep := func(ctx context.Context) error {
		n, err := officeSvc.Sweep(ctx, 200)
		if err == nil && n > 0 {
			logging.Info(packageName, "office-session-sweep: reaped %d session(s)", n)
		}
		return err
	}
	runnableJobs["office-session-sweep"] = officeSweep
	go runScheduled(janitorCtx, coord, "office-session-sweep", 2*time.Minute, 15*time.Minute, officeSweep)

	// Reap abandoned resumable-upload sessions past their expiry, discarding any
	// staged-but-uncommitted blocks (Azure's own uncommitted-block GC is the
	// backstop). Coordinated so only one node sweeps per tick.
	uploadSweep := func(ctx context.Context) error {
		n, err := filesSvc.SweepUploadSessions(ctx)
		if err == nil && n > 0 {
			logging.Info(packageName, "upload-session-sweep: reaped %d session(s)", n)
		}
		return err
	}
	runnableJobs["upload-session-sweep"] = uploadSweep
	go runScheduled(janitorCtx, coord, "upload-session-sweep", 10*time.Minute, 30*time.Minute, uploadSweep)

	// Adaptive storage-tiering. The SCHEDULED pass is OFF unless
	// TRILLI_TIERING_MODE is "dryrun" (logs decisions, moves nothing) or "live"
	// (re-tiers blobs). Independent of the schedule, operators can run an
	// on-demand dry-run/apply via the CMX admin endpoint (tierRun below).
	scheduledTierMode := tiering.ParseMode(os.Getenv("TRILLI_TIERING_MODE"))
	if scheduledTierMode != tiering.ModeOff {
		tierEng := tiering.NewEngine(pg, blobStore, scheduledTierMode)
		go runScheduled(janitorCtx, coord, "tiering", 3*time.Minute, 6*time.Hour,
			func(ctx context.Context) error {
				sum, err := tierEng.Run(ctx, time.Now().UTC())
				if err == nil && sum.Planned > 0 {
					logging.Info(packageName, "tiering (%s): planned=%d applied=%d failed=%d promotions=%d demotions=%d",
						sum.Mode, sum.Planned, sum.Applied, sum.Failed, sum.Promotions, sum.Demotions)
				}
				return err
			})
	}

	// tierRun executes one on-demand tiering pass under the cluster lock: dryrun
	// (plan only) when apply is false, live (re-tier blobs) when true.
	tierRun := func(ctx context.Context, apply bool) (tiering.Summary, bool, error) {
		mode := tiering.ModeDryRun
		if apply {
			mode = tiering.ModeLive
		}
		eng := tiering.NewEngine(pg, blobStore, mode)
		var sum tiering.Summary
		ran, err := coord.RunExclusive(ctx, "tiering", func(ctx context.Context) error {
			var e error
			sum, e = eng.Run(ctx, time.Now().UTC())
			return e
		})
		return sum, ran, err
	}

	// Attach the infra write ops to the CMX admin surface now that the
	// coordinator, job registry, and tiering runner exist.
	adminH.WithInfra(coord, runnableJobs, tierRun)

	startErr := make(chan error, 1)
	go func() {
		startErr <- srv.Start()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logging.Info(packageName, "Received signal: %v, shutting down", sig)
	case err := <-startErr:
		if err != nil {
			logging.Error(packageName, "Server failed to start: %v", err)
			os.Exit(1)
		}
	}

	stopJanitor()
	wopiStopCtx, wopiCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := wopiSrv.Shutdown(wopiStopCtx); err != nil {
		logging.Error(packageName, "WOPI listener shutdown failed: %v", err)
	}
	wopiCancel()
	if err := geoipSvc.Stop(); err != nil {
		logging.Error(packageName, "GeoIP service stop failed: %v", err)
	}
	if err := srv.Stop(); err != nil {
		logging.Error(packageName, "Graceful shutdown failed: %v", err)
		os.Exit(1)
	}

	logging.Info(packageName, "Trilli stopped cleanly")
}

// runScheduled is the shared janitor loop: on its interval (after an initial
// offset), it asks the cluster coordinator to run fn under a job-named advisory
// lock. Only the node that wins the lock executes fn; the rest skip that tick.
// Every replica runs this loop, but the work happens exactly once per cycle
// cluster-wide. Loops until the context is cancelled at shutdown.
func runScheduled(ctx context.Context, coord *cluster.Coordinator, job string, initial, interval time.Duration, fn func(context.Context) error) {
	timer := time.NewTimer(initial)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			ran, err := coord.RunExclusive(ctx, job, fn)
			switch {
			case err != nil:
				logging.Error(packageName, "job %q failed: %v", job, err)
			case !ran:
				logging.Debug(packageName, "job %q skipped — another node holds the lock", job)
			}
			timer.Reset(interval)
		}
	}
}

// browseHandler is the unified location endpoint for the file browser UI.
// Returns: { folder, breadcrumb, folders, files } for a given folder_id (or
// tenant root if folder_id is omitted/0).
func browseHandler(authSvc *auth.Service, foldersSvc *folders.Service, filesSvc *files.Service, workspacesSvc *workspaces.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := auth.IdentityFromContext(r.Context())
		if !ok || identity.Tenant == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		var folderID *int64
		if raw := strings.TrimSpace(r.URL.Query().Get("folder_id")); raw != "" && raw != "0" && raw != "null" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid folder_id"})
				return
			}
			folderID = &id
		}

		// Optional workspace selector (the My Files picker). When set, browse
		// scopes the workspace-root listing to it. Access is whole-workspace:
		// owner/admin reach any workspace in the tenant; members/viewers must be
		// assigned (workspace_members). CanAccess enforces both.
		var workspaceID *int64
		if raw := strings.TrimSpace(r.URL.Query().Get("workspace_id")); raw != "" && raw != "0" && raw != "null" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid workspace_id"})
				return
			}
			workspaceID = &id
		}
		if workspaceID != nil {
			can, err := workspacesSvc.CanAccess(r.Context(), identity, *workspaceID)
			if err != nil {
				logging.Error(packageName, "Browse workspace access check failed: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
				return
			}
			if !can {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "You don't have access to this workspace"})
				return
			}
		}

		// Folder-access gating for scoped members (member/viewer). Their
		// accessible set is every folder in their assigned workspaces. At the
		// tenant root with no workspace selected, surface the top-level folders
		// of their assigned workspaces as entry points; once they pick a
		// workspace or descend into a folder, the normal listing applies.
		if identity.Access.Restricted() {
			if folderID == nil && workspaceID == nil {
				rootIDs, err := authSvc.MemberWorkspaceRootFolderIDs(r.Context(), identity.Tenant.ID, identity.User.ID)
				if err != nil {
					logging.Error(packageName, "Browse workspace roots failed: %v", err)
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
					return
				}
				entryFolders, err := foldersSvc.ListByIDs(r.Context(), identity.Tenant.ID, rootIDs)
				if err != nil {
					logging.Error(packageName, "Browse entry folders failed: %v", err)
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"folder":     nil,
					"breadcrumb": nil,
					"folders":    entryFolders,
					"files":      []*files.File{},
				})
				return
			}
			if folderID != nil && !identity.Access.CanRead(folderID) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "You don't have access to this folder"})
				return
			}
		}

		var currentFolder *folders.Folder
		var breadcrumb []folders.BreadcrumbItem
		if folderID != nil {
			f, err := foldersSvc.Get(r.Context(), identity.Tenant.ID, *folderID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			currentFolder = f
			bc, err := foldersSvc.Breadcrumb(r.Context(), identity.Tenant.ID, *folderID)
			if err != nil {
				logging.Error(packageName, "Browse breadcrumb failed: %v", err)
			}
			breadcrumb = bc
		}

		var childFolders []*folders.Folder
		var childFiles []*files.File
		var err error
		if folderID == nil && workspaceID != nil {
			// Workspace root: list only the selected workspace's root items.
			childFolders, err = foldersSvc.ListChildrenScoped(r.Context(), identity.Tenant.ID, nil, workspaceID)
			if err == nil {
				if identity.Access.Restricted() {
					// Folder-scoped members see only their granted root folders.
					accessible := make([]*folders.Folder, 0, len(childFolders))
					for _, f := range childFolders {
						fid := f.ID
						if identity.Access.CanRead(&fid) {
							accessible = append(accessible, f)
						}
					}
					childFolders = accessible
					// Workspace-root files only for members assigned the whole
					// workspace; folder-scoped members don't reach the root.
					whole, werr := workspacesSvc.IsMemberWhole(r.Context(), *workspaceID, identity.User.ID)
					if werr != nil {
						err = werr
					} else if whole {
						childFiles, err = filesSvc.ListWorkspaceRootFiles(r.Context(), identity.Tenant.ID, *workspaceID)
					}
				} else {
					childFiles, err = filesSvc.ListWorkspaceRootFiles(r.Context(), identity.Tenant.ID, *workspaceID)
				}
			}
		} else {
			childFolders, err = foldersSvc.ListChildren(r.Context(), identity.Tenant.ID, folderID)
			if err == nil {
				childFiles, err = filesSvc.ListInFolder(r.Context(), identity.Tenant.ID, folderID)
			}
		}
		if err != nil {
			logging.Error(packageName, "Browse list failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"folder":     currentFolder,
			"breadcrumb": breadcrumb,
			"folders":    childFolders,
			"files":      childFiles,
		})
	})
}

// copyDestinationsHandler powers the cross-account copy modal's folder picker.
// It browses the folder tree of a DESTINATION tenant the caller belongs to
// (NOT their session tenant), so they can choose a subfolder to copy into
// rather than always landing in the root. Authorization is independent of the
// session: AccessForTenant resolves the caller's access in the target tenant,
// and write access is required (it's a copy target). Returns the target tenant's
// workspaces plus the folder level requested.
func copyDestinationsHandler(authSvc *auth.Service, foldersSvc *folders.Service, workspacesSvc *workspaces.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := auth.IdentityFromContext(r.Context())
		if !ok || identity.Tenant == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		destTenantID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("tenant_id")), 10, 64)
		if err != nil || destTenantID == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id required"})
			return
		}

		destAccess, destTenant, destMembership, err := authSvc.AccessForTenant(r.Context(), identity.User.ID, destTenantID)
		if errors.Is(err, auth.ErrUnauthorized) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "You don't have access to that account"})
			return
		}
		if err != nil {
			logging.Error(packageName, "copy-destinations: access for tenant %d: %v", destTenantID, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if !destAccess.Write {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Your role in that account is read-only"})
			return
		}
		// Synthetic identity scoped to the destination tenant — reuses the
		// access-aware workspace listing without touching the live session.
		destIdentity := &auth.Identity{User: identity.User, Tenant: destTenant, Membership: destMembership, Access: destAccess}

		wss, err := workspacesSvc.ListForIdentity(r.Context(), destIdentity)
		if err != nil {
			logging.Error(packageName, "copy-destinations: list workspaces: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		var workspaceID, folderID *int64
		if raw := strings.TrimSpace(r.URL.Query().Get("workspace_id")); raw != "" && raw != "0" && raw != "null" {
			if id, perr := strconv.ParseInt(raw, 10, 64); perr == nil {
				workspaceID = &id
			}
		}
		if raw := strings.TrimSpace(r.URL.Query().Get("folder_id")); raw != "" && raw != "0" && raw != "null" {
			if id, perr := strconv.ParseInt(raw, 10, 64); perr == nil {
				folderID = &id
			}
		}

		var currentFolder *folders.Folder
		var breadcrumb []folders.BreadcrumbItem
		var childFolders []*folders.Folder
		if folderID != nil {
			if destAccess.Restricted() && !destAccess.CanRead(folderID) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "You don't have access to that folder"})
				return
			}
			currentFolder, err = foldersSvc.Get(r.Context(), destTenantID, *folderID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "folder not found"})
				return
			}
			breadcrumb, _ = foldersSvc.Breadcrumb(r.Context(), destTenantID, *folderID)
			childFolders, err = foldersSvc.ListChildren(r.Context(), destTenantID, folderID)
		} else if workspaceID != nil {
			childFolders, err = foldersSvc.ListChildrenScoped(r.Context(), destTenantID, nil, workspaceID)
		}
		if err != nil {
			logging.Error(packageName, "copy-destinations: list folders: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		// Folder-scoped members only see the folders they can reach.
		if destAccess.Restricted() {
			filtered := make([]*folders.Folder, 0, len(childFolders))
			for _, f := range childFolders {
				fid := f.ID
				if destAccess.CanRead(&fid) {
					filtered = append(filtered, f)
				}
			}
			childFolders = filtered
		}
		if childFolders == nil {
			childFolders = []*folders.Folder{}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"workspaces": wss,
			"folder":     currentFolder,
			"breadcrumb": breadcrumb,
			"folders":    childFolders,
		})
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

// geoResolver adapts the qserve GeoIP service to session.GeoResolver, mapping a
// lookup into the neutral GeoInfo the session store persists. This composition-
// root adapter keeps the session/auth packages decoupled from qserve. A miss or
// a not-ready DB simply yields an empty GeoInfo (no geo recorded).
type geoResolver struct{ svc *qserve.Service }

func (g geoResolver) Locate(ip string) session.GeoInfo {
	resp, err := g.svc.LookupIP(ip)
	if err != nil || resp == nil {
		return session.GeoInfo{}
	}
	region := ""
	if len(resp.Subdivisions) > 0 {
		region = resp.Subdivisions[0].Name
	}
	return session.GeoInfo{
		CountryCode: resp.CountryCode,
		Country:     resp.CountryName,
		Region:      region,
		City:        resp.City,
		PostalCode:  resp.PostalCode,
		Latitude:    resp.Latitude,
		Longitude:   resp.Longitude,
	}
}

// runCreds manages the encrypted service-credentials vault from the CLI. It
// needs APP_ENCRYPTION_KEY in the environment (same key the daemon uses) so the
// daemon can later decrypt what's stored here.
//
//	trilli creds set <provider> <key_name> <env> <value>
//	trilli creds list
func runCreds(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: trilli creds {set <provider> <key_name> <env> <value> | list}")
		os.Exit(2)
	}
	client, err := postgres.NewClient(nil)
	if err != nil {
		logging.Error(packageName, "Failed to connect to DB: %v", err)
		os.Exit(1)
	}
	defer client.Close()
	svc := credentials.NewService(client)
	ctx := context.Background()

	switch args[0] {
	case "set":
		if len(args) < 5 {
			fmt.Fprintln(os.Stderr, "usage: trilli creds set <provider> <key_name> <env> <value>")
			os.Exit(2)
		}
		if err := svc.Set(ctx, args[1], args[2], args[3], args[4]); err != nil {
			logging.Error(packageName, "creds set failed: %v", err)
			os.Exit(1)
		}
		fmt.Printf("stored %s/%s (%s)\n", args[1], args[2], args[3])
	case "list":
		creds, err := svc.List(ctx)
		if err != nil {
			logging.Error(packageName, "creds list failed: %v", err)
			os.Exit(1)
		}
		for _, c := range creds {
			active := ""
			if c.IsActive {
				active = " [active]"
			}
			fmt.Printf("%-8s %-16s %-5s ••••%s%s\n", c.Provider, c.KeyName, c.Environment, c.Last4, active)
		}
	default:
		fmt.Fprintf(os.Stderr, "trilli creds: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

// runStripe handles `trilli stripe <subcommand>`.
//
//	trilli stripe sync-plans   Create/update Stripe Products + Prices from the catalog
func runStripe(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: trilli stripe {sync-plans | backfill-first-invoice <tenantID>}")
		os.Exit(2)
	}
	client, err := postgres.NewClient(nil)
	if err != nil {
		logging.Error(packageName, "Failed to connect to DB: %v", err)
		os.Exit(1)
	}
	defer client.Close()

	svc := billing.NewService(client, credentials.NewService(client))
	ctx := context.Background()

	switch args[0] {
	case "sync-plans":
		report, err := svc.SyncPlans(ctx)
		if err != nil {
			logging.Error(packageName, "stripe sync-plans failed: %v", err)
			os.Exit(1)
		}
		fmt.Printf("Synced %d plan(s) to Stripe:\n", len(report))
		for _, line := range report {
			fmt.Printf("  %s\n", line)
		}

	case "backfill-first-invoice":
		// One-time backfill for the pay-before-account funnel: record a tenant's
		// first (subscription_create) invoice that onInvoicePaid skipped because
		// the charge landed before the tenant was provisioned. Idempotent.
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: trilli stripe backfill-first-invoice <tenantID>")
			os.Exit(2)
		}
		tenantID, perr := strconv.ParseInt(args[1], 10, 64)
		if perr != nil || tenantID <= 0 {
			fmt.Fprintln(os.Stderr, "invalid tenantID")
			os.Exit(2)
		}
		var subID string
		if err := client.QueryRowContext(ctx,
			`SELECT COALESCE(stripe_subscription_id, '') FROM tenants WHERE id = $1`,
			tenantID).Scan(&subID); err != nil {
			logging.Error(packageName, "lookup tenant %d subscription: %v", tenantID, err)
			os.Exit(1)
		}
		if subID == "" {
			fmt.Fprintf(os.Stderr, "tenant %d has no stripe_subscription_id\n", tenantID)
			os.Exit(1)
		}
		fmt.Printf("Backfilling first invoice: tenant=%d subscription=%s (idempotent)...\n", tenantID, subID)
		svc.RecordFunnelFirstInvoice(ctx, subID, tenantID)
		fmt.Println("Done. See the [billing] first-invoice log line and verify billing_transactions.")

	default:
		fmt.Fprintln(os.Stderr, "usage: trilli stripe {sync-plans | backfill-first-invoice <tenantID>}")
		os.Exit(2)
	}
}

func runLifecycle(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: trilli lifecycle {status|sweep|purge <tenantID>}")
		os.Exit(2)
	}
	client, err := postgres.NewClient(nil)
	if err != nil {
		logging.Error(packageName, "Failed to connect to DB: %v", err)
		os.Exit(1)
	}
	defer client.Close()

	// status only needs the DB; sweep/purge need the blob store (to delete
	// bytes) and mailer (to send warnings). Init the heavy deps lazily.
	ctx := context.Background()
	switch args[0] {
	case "status":
		svc := lifecycle.NewService(client, nil, nil)
		rows, err := svc.Status(ctx)
		if err != nil {
			logging.Error(packageName, "lifecycle status failed: %v", err)
			os.Exit(1)
		}
		if len(rows) == 0 {
			fmt.Println("No lapsed accounts.")
			return
		}
		fmt.Printf("%d lapsed account(s):\n", len(rows))
		for _, r := range rows {
			flag := ""
			if r.Expired {
				flag = "  ⚠ PURGE-ELIGIBLE"
			}
			fmt.Printf("  tenant=%d %-24s status=%s warn=%d/%d daysLeft=%d purge=%s%s\n",
				r.TenantID, r.Name, r.Status, r.WarnStage, 4, r.DaysLeft, r.PurgeDate, flag)
		}
	case "sweep":
		blobStore, err := azureblob.New(azureblob.DefaultConfig())
		if err != nil {
			logging.Error(packageName, "Blob store init failed: %v", err)
			os.Exit(1)
		}
		svc := lifecycle.NewService(client, blobStore, mailer.New())
		if err := svc.RunSweep(ctx); err != nil {
			logging.Error(packageName, "lifecycle sweep failed: %v", err)
			os.Exit(1)
		}
		fmt.Println("Sweep complete.")
	case "purge":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: trilli lifecycle purge <tenantID>")
			os.Exit(2)
		}
		tenantID, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "lifecycle purge: invalid tenant id %q\n", args[1])
			os.Exit(2)
		}
		blobStore, err := azureblob.New(azureblob.DefaultConfig())
		if err != nil {
			logging.Error(packageName, "Blob store init failed: %v", err)
			os.Exit(1)
		}
		svc := lifecycle.NewService(client, blobStore, nil)
		if err := svc.PurgeTenant(ctx, tenantID); err != nil {
			logging.Error(packageName, "lifecycle purge tenant=%d failed: %v", tenantID, err)
			os.Exit(1)
		}
		fmt.Printf("Purged tenant=%d (if it was still lapsed).\n", tenantID)
	default:
		fmt.Fprintf(os.Stderr, "trilli lifecycle: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

// runKey handles `trilli key {status|seed}` — the DB-stored master key.
func runKey(args []string) {
	if len(args) == 0 || (args[0] != "status" && args[0] != "seed") {
		fmt.Fprintln(os.Stderr, "usage: trilli key status | trilli key seed")
		os.Exit(2)
	}
	client, err := postgres.NewClient(nil)
	if err != nil {
		logging.Error(packageName, "Failed to connect to DB: %v", err)
		os.Exit(1)
	}
	defer client.Close()

	if args[0] == "seed" {
		// Force the lazy provider to run, which seeds the row from
		// APP_ENCRYPTION_KEY if it isn't there yet.
		if _, err := crypto.Encrypt([]byte("seed-trigger")); err != nil {
			logging.Error(packageName, "seed failed: %v", err)
			os.Exit(1)
		}
	}

	inDB, createdAt, err := keystore.Status(context.Background(), client)
	if err != nil {
		logging.Error(packageName, "key status failed: %v", err)
		os.Exit(1)
	}
	if inDB {
		when := "unknown"
		if createdAt.Valid {
			when = createdAt.Time.Format("2006-01-02 15:04 MST")
		}
		fmt.Printf("master key: stored in database (wrapped), since %s\n", when)
		fmt.Println("APP_ENCRYPTION_KEY is no longer required at runtime and can be removed from the environment.")
	} else {
		fmt.Println("master key: NOT in the database yet — set APP_ENCRYPTION_KEY and run `trilli key seed`.")
	}
}

// runJobs handles `trilli jobs {status|ping}` — visibility into and a self-test
// of the cluster-coordinated background jobs.
func runJobs(args []string) {
	if len(args) == 0 || (args[0] != "status" && args[0] != "ping") {
		fmt.Fprintln(os.Stderr, "usage: trilli jobs status | trilli jobs ping")
		os.Exit(2)
	}
	client, err := postgres.NewClient(nil)
	if err != nil {
		logging.Error(packageName, "Failed to connect to DB: %v", err)
		os.Exit(1)
	}
	defer client.Close()

	if args[0] == "ping" {
		coord := cluster.NewCoordinator(client)
		ran, err := coord.RunExclusive(context.Background(), "diagnostic-ping",
			func(ctx context.Context) error { time.Sleep(200 * time.Millisecond); return nil })
		if err != nil {
			logging.Error(packageName, "ping failed: %v", err)
			os.Exit(1)
		}
		fmt.Printf("node=%s  lock-key=%d  ran=%v%s\n", coord.NodeID(), cluster.Key("diagnostic-ping"), ran,
			map[bool]string{true: "  (acquired the lock and ran)", false: "  (another holder has the lock — skipped)"}[ran])
		return
	}

	rows, err := client.Query(`SELECT job, last_node, last_started_at, last_finished_at, last_ok, run_count, last_note FROM job_runs ORDER BY job`)
	if err != nil {
		logging.Error(packageName, "jobs status failed: %v", err)
		os.Exit(1)
	}
	defer rows.Close()
	fmt.Printf("\n%-16s %-22s %-22s %6s %4s  %s\n", "JOB", "LAST NODE", "LAST FINISHED", "RUNS", "OK", "NOTE")
	fmt.Println(strings.Repeat("-", 100))
	any := false
	for rows.Next() {
		any = true
		var job, node, note string
		var started, finished sql.NullTime
		var ok bool
		var runCount int64
		if err := rows.Scan(&job, &node, &started, &finished, &ok, &runCount, &note); err != nil {
			logging.Error(packageName, "scan: %v", err)
			os.Exit(1)
		}
		fin := "—"
		if finished.Valid {
			fin = finished.Time.Format("2006-01-02 15:04:05")
		}
		okMark := "✓"
		if !ok {
			okMark = "✗"
		}
		fmt.Printf("%-16s %-22s %-22s %6d %4s  %s\n", job, node, fin, runCount, okMark, note)
	}
	if !any {
		fmt.Println("(no jobs have run yet)")
	}
	fmt.Println()
}

// runStorage handles `trilli storage {report|tier [--apply]}` — the tiering
// cost analysis and the on-demand tiering pass.
func runStorage(args []string) {
	if len(args) == 0 || (args[0] != "report" && args[0] != "tier" && args[0] != "encrypt-blobs") {
		fmt.Fprintln(os.Stderr, "usage: trilli storage report | trilli storage tier [--apply] | trilli storage encrypt-blobs")
		os.Exit(2)
	}
	client, err := postgres.NewClient(nil)
	if err != nil {
		logging.Error(packageName, "Failed to connect to DB: %v", err)
		os.Exit(1)
	}
	defer client.Close()

	if args[0] == "tier" {
		runStorageTier(client, args[1:])
		return
	}
	if args[0] == "encrypt-blobs" {
		runStorageEncryptBlobs(client)
		return
	}

	rep, err := tiering.NewService(client).Analyze(context.Background(), time.Now().UTC())
	if err != nil {
		logging.Error(packageName, "storage report failed: %v", err)
		os.Exit(1)
	}

	fmt.Printf("\nTrilli storage tiering analysis — %s\n", rep.GeneratedAt.Format("2006-01-02 15:04 MST"))
	fmt.Println("(Cool = not read 30–90d, Cold = not read >90d. Egress is trailing-30d internet download.)")
	fmt.Printf("\n%-22s %-12s %12s %12s %10s %10s %10s  %s\n",
		"TENANT", "PLAN", "STORED", "EGRESS/30d", "RATIO", "COOL", "COLD", "CLASS")
	fmt.Println(strings.Repeat("-", 120))
	for _, t := range rep.Tenants {
		name := t.Name
		if len(name) > 21 {
			name = name[:20] + "…"
		}
		fmt.Printf("%-22s %-12s %12s %12s %10.2f %10s %10s  %s\n",
			name, t.Plan, tiering.GB(t.StoredBytes), tiering.GB(t.Egress30dBytes),
			t.Ratio, tiering.GB(t.CoolBytes), tiering.GB(t.ColdBytes), t.Class)
	}
	fmt.Println(strings.Repeat("-", 120))
	fmt.Printf("%-22s %-12s %12s %12s %10s %10s %10s\n",
		"TOTAL", "", tiering.GB(rep.TotalStored), tiering.GB(rep.TotalEgress30d),
		"", tiering.GB(rep.TotalCool), tiering.GB(rep.TotalCold))
	fmt.Printf("\nProjected storage savings if the cold tail is tiered down: $%.2f/month (gross).\n",
		rep.TotalSavings)
	fmt.Println("Note: tiering reduces STORAGE cost only — egress is tier-independent (CDN/SAS is that lever).")
	fmt.Println()
}

// runStorageEncryptBlobs migrates all legacy plaintext blobs to encrypted at
// rest, in batches, until none remain. Refuses to run without a real
// APP_ENCRYPTION_KEY (encrypting under the dev key would lose the data).
func runStorageEncryptBlobs(client *postgres.Client) {
	if !crypto.KeyConfigured() {
		fmt.Fprintln(os.Stderr, "refusing: APP_ENCRYPTION_KEY is not set (would encrypt under an insecure dev key).")
		os.Exit(1)
	}
	raw, err := azureblob.New(azureblob.DefaultConfig())
	if err != nil {
		logging.Error(packageName, "Blob store init failed: %v", err)
		os.Exit(1)
	}
	mig := encryptedstore.NewMigrator(client, encryptedstore.New(raw, encryptedstore.NewKeyService(client)))
	total := 0
	for {
		n, err := mig.MigrateBatch(context.Background(), 200)
		if err != nil {
			logging.Error(packageName, "encrypt-blobs failed after %d: %v", total, err)
			os.Exit(1)
		}
		total += n
		if n == 0 {
			break
		}
	}
	fmt.Printf("blob encryption migration complete — %d file(s) processed.\n", total)
}

// runStorageTier runs one tiering pass. Default is a dry run (logs the moves it
// would make); `--apply` actually re-tiers blobs.
func runStorageTier(client *postgres.Client, args []string) {
	mode := tiering.ModeDryRun
	for _, a := range args {
		if a == "--apply" {
			mode = tiering.ModeLive
		}
	}
	blobStore, err := azureblob.New(azureblob.DefaultConfig())
	if err != nil {
		logging.Error(packageName, "Blob store init failed: %v", err)
		os.Exit(1)
	}
	eng := tiering.NewEngine(client, blobStore, mode)
	sum, err := eng.Run(context.Background(), time.Now().UTC())
	if err != nil {
		logging.Error(packageName, "tiering run failed: %v", err)
		os.Exit(1)
	}
	fmt.Printf("\nTiering pass (%s): scanned=%d planned=%d applied=%d failed=%d (promotions=%d demotions=%d)%s\n\n",
		sum.Mode, sum.Scanned, sum.Planned, sum.Applied, sum.Failed, sum.Promotions, sum.Demotions,
		cappedNote(sum.Capped))
	if mode == tiering.ModeDryRun {
		fmt.Println("This was a dry run — no blobs were moved. Re-run with --apply to act on it.")
	}
}

func cappedNote(capped bool) string {
	if capped {
		return " [capped — more pending next run]"
	}
	return ""
}

func runMigrate(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "trilli migrate: missing subcommand")
		fmt.Fprintln(os.Stderr, "usage: trilli migrate {up|down [N]|version}")
		os.Exit(2)
	}

	client, err := postgres.NewClient(nil)
	if err != nil {
		logging.Error(packageName, "Failed to connect to DB: %v", err)
		os.Exit(1)
	}
	defer client.Close()

	switch args[0] {
	case "up":
		if err := client.MigrateUp(migrations.FS, "."); err != nil {
			logging.Error(packageName, "Migration up failed: %v", err)
			os.Exit(1)
		}
	case "down":
		steps := 1
		if len(args) > 1 {
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 1 {
				fmt.Fprintf(os.Stderr, "trilli migrate down: invalid step count %q\n", args[1])
				os.Exit(2)
			}
			steps = n
		}
		if err := client.MigrateDown(migrations.FS, ".", steps); err != nil {
			logging.Error(packageName, "Migration down failed: %v", err)
			os.Exit(1)
		}
	case "version":
		v, dirty, err := client.MigrateVersion(migrations.FS, ".")
		if err != nil {
			logging.Error(packageName, "Failed to read migration version: %v", err)
			os.Exit(1)
		}
		if v == 0 && !dirty {
			fmt.Println("no migrations applied")
		} else {
			fmt.Printf("version=%d dirty=%v\n", v, dirty)
		}
	default:
		fmt.Fprintf(os.Stderr, "trilli migrate: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}
