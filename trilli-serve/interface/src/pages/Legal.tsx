import { Link } from "react-router-dom";
import { Shield, ScrollText } from "lucide-react";

// Trilli's legal pages. These are real, customer-facing policies modeled on
// modern cloud-storage providers (Dropbox / Box). They should still be reviewed
// by counsel before relying on them in a dispute.
const ENTITY = "Trilli Media LLC";
const UPDATED = "June 6, 2026";
const PRIVACY_EMAIL = "privacy@trilli.com";
const LEGAL_EMAIL = "legal@trilli.com";
const SUPPORT_EMAIL = "support@trilli.com";

interface Section {
  id: string;
  heading: string;
  body: React.ReactNode;
}

function P({ children }: { children: React.ReactNode }) {
  return <p className="text-sm leading-relaxed text-muted-foreground">{children}</p>;
}

function UL({ children }: { children: React.ReactNode }) {
  return (
    <ul className="list-disc space-y-1.5 pl-5 text-sm leading-relaxed text-muted-foreground marker:text-muted-foreground/50">
      {children}
    </ul>
  );
}

function LegalPage({
  icon: Icon,
  eyebrow,
  title,
  intro,
  sections,
  counterpart,
}: {
  icon: typeof Shield;
  eyebrow: string;
  title: string;
  intro: React.ReactNode;
  sections: Section[];
  counterpart: { to: string; label: string };
}) {
  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-3xl px-6 py-10 lg:py-12">
        <header className="border-b border-border pb-6">
          <div className="mb-3 inline-flex items-center gap-2 rounded-full bg-primary/10 px-3 py-1 text-[11px] font-semibold uppercase tracking-wide text-primary">
            <Icon className="h-3.5 w-3.5" />
            {eyebrow}
          </div>
          <h1 className="text-3xl font-bold tracking-tight text-foreground">{title}</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            Last updated {UPDATED} · {ENTITY}
          </p>
          <div className="mt-4 max-w-2xl space-y-3">{intro}</div>
        </header>

        {/* On this page */}
        <nav className="mt-6 rounded-xl border border-border bg-card p-4">
          <p className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
            On this page
          </p>
          <ol className="grid gap-x-6 gap-y-1 sm:grid-cols-2">
            {sections.map((s, i) => (
              <li key={s.id}>
                <a
                  href={`#${s.id}`}
                  className="text-[13px] text-muted-foreground transition-colors hover:text-primary"
                >
                  {i + 1}. {s.heading}
                </a>
              </li>
            ))}
          </ol>
        </nav>

        <div className="mt-8 space-y-8">
          {sections.map((s, i) => (
            <section key={s.id} id={s.id} className="scroll-mt-6 space-y-2.5">
              <h2 className="text-lg font-semibold text-foreground">
                {i + 1}. {s.heading}
              </h2>
              {s.body}
            </section>
          ))}
        </div>

        <footer className="mt-10 flex flex-col gap-2 border-t border-border pt-6 text-sm text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
          <span>
            Questions? Email{" "}
            <a className="text-primary hover:underline" href={`mailto:${LEGAL_EMAIL}`}>
              {LEGAL_EMAIL}
            </a>
            .
          </span>
          <Link to={counterpart.to} className="text-primary hover:underline">
            {counterpart.label} →
          </Link>
        </footer>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Privacy Policy
// ---------------------------------------------------------------------------
export function Privacy() {
  const sections: Section[] = [
    {
      id: "overview",
      heading: "Our privacy commitment",
      body: (
        <>
          <P>
            Trilli is built for people and teams who care about keeping their files private.
            We treat the documents you store with us as yours — not as a data source to mine,
            profile, or sell. This policy explains what we collect, why, how we protect it, and
            the choices and rights you have.
          </P>
          <P>
            We never sell your personal information or the contents of your files, and we do not
            use the contents of your files to train advertising or marketing systems.
          </P>
        </>
      ),
    },
    {
      id: "what-we-collect",
      heading: "Information we collect",
      body: (
        <UL>
          <li>
            <strong className="text-foreground">Account information</strong> — your name, email
            address, and a securely hashed password (we never store your password in plain text).
          </li>
          <li>
            <strong className="text-foreground">Your content</strong> — the files and folders you
            upload, their names, and metadata such as size, type, and timestamps.
          </li>
          <li>
            <strong className="text-foreground">Usage &amp; device data</strong> — log records
            such as IP address, approximate location derived from IP, browser/device details, and
            actions taken in the product (uploads, shares, sign-ins) for security and auditing.
          </li>
          <li>
            <strong className="text-foreground">Payment information</strong> — handled by our
            payment processor (Stripe). We receive billing status and the last four digits of a
            card, but we do not store full card numbers on our systems.
          </li>
        </UL>
      ),
    },
    {
      id: "how-we-use",
      heading: "How we use information",
      body: (
        <UL>
          <li>To provide, maintain, and secure the service and your account.</li>
          <li>To process payments, manage subscriptions, and send billing notices.</li>
          <li>To detect, investigate, and prevent fraud, abuse, and security incidents.</li>
          <li>To provide support and respond to your requests.</li>
          <li>
            To send service and account communications. Product/marketing emails are sent only
            with your consent and you can opt out at any time.
          </li>
        </UL>
      ),
    },
    {
      id: "storage-security",
      heading: "How we store and protect your data",
      body: (
        <>
          <P>
            Files are stored encrypted at rest on Microsoft Azure infrastructure and transmitted
            over encrypted connections (TLS). Each account&apos;s data is logically isolated, and
            access is scoped per workspace and per member.
          </P>
          <P>
            We offer two-factor authentication, passkey (WebAuthn) sign-in, trusted-device and
            session controls, and an immutable activity log so you can see and govern access to
            your account.
          </P>
        </>
      ),
    },
    {
      id: "sharing-disclosure",
      heading: "Sharing and disclosure",
      body: (
        <>
          <P>
            We do not sell your personal information or file contents. We share data only in these
            limited circumstances:
          </P>
          <UL>
            <li>
              <strong className="text-foreground">Service providers (subprocessors)</strong> — such
              as Microsoft Azure (storage/hosting), Stripe (payments), and SendGrid (transactional
              email), each bound by contract to protect your data and use it only to provide their
              service to us.
            </li>
            <li>
              <strong className="text-foreground">At your direction</strong> — when you share a
              file, folder, or link with someone.
            </li>
            <li>
              <strong className="text-foreground">Legal process</strong> — see the section below.
            </li>
          </UL>
        </>
      ),
    },
    {
      id: "law-enforcement",
      heading: "Government and law-enforcement requests",
      body: (
        <>
          <P>
            We require valid legal process before disclosing customer data.{" "}
            <strong className="text-foreground">
              We do not provide your personal information or the contents of your files to law
              enforcement or other third parties without a valid warrant, subpoena, or court order
            </strong>{" "}
            issued under applicable law — except in a genuine emergency where we believe in good
            faith that disclosure is necessary to prevent imminent death or serious physical harm.
          </P>
          <P>
            Where we are legally permitted to do so, we will notify the affected account before
            disclosing data so you have an opportunity to object. We publish the categories of
            requests we receive and narrowly limit any disclosure to what the legal process
            actually requires.
          </P>
        </>
      ),
    },
    {
      id: "retention",
      heading: "Data retention and deletion",
      body: (
        <>
          <P>
            We keep your content for as long as your account is active. Deleted items move to Trash
            and can be restored; once purged, they are removed from active systems and deleted from
            backups within a reasonable period. Activity-log retention depends on your plan.
          </P>
          <P>
            You can delete files at any time, export your data, or close your account. When an
            account is closed or a subscription permanently lapses, we delete the associated data
            on the schedule described in those flows.
          </P>
        </>
      ),
    },
    {
      id: "your-rights",
      heading: "Your rights and choices",
      body: (
        <>
          <P>
            Depending on where you live, you may have the right to access, correct, export, or
            delete your personal information, and to object to or restrict certain processing. You
            can exercise most of these directly in the product, or by contacting us.
          </P>
          <P>
            To make a privacy request, email{" "}
            <a className="text-primary hover:underline" href={`mailto:${PRIVACY_EMAIL}`}>
              {PRIVACY_EMAIL}
            </a>
            . We will respond within the timeframe required by applicable law.
          </P>
        </>
      ),
    },
    {
      id: "cookies",
      heading: "Cookies and sessions",
      body: (
        <P>
          We use a small number of strictly-necessary cookies to keep you signed in and to remember
          trusted devices. We do not use third-party advertising or cross-site tracking cookies.
        </P>
      ),
    },
    {
      id: "children",
      heading: "Children",
      body: (
        <P>
          Trilli is not directed to children and is not intended for use by anyone under 16. We do
          not knowingly collect personal information from children; if you believe a child has
          provided us information, contact us and we will delete it.
        </P>
      ),
    },
    {
      id: "changes",
      heading: "Changes to this policy",
      body: (
        <P>
          We may update this policy as the product and the law evolve. If we make material changes,
          we&apos;ll update the date above and, where appropriate, notify you in the product or by
          email.
        </P>
      ),
    },
    {
      id: "contact",
      heading: "Contact us",
      body: (
        <P>
          Questions about privacy? Email{" "}
          <a className="text-primary hover:underline" href={`mailto:${PRIVACY_EMAIL}`}>
            {PRIVACY_EMAIL}
          </a>{" "}
          or reach our team at{" "}
          <a className="text-primary hover:underline" href={`mailto:${SUPPORT_EMAIL}`}>
            {SUPPORT_EMAIL}
          </a>
          .
        </P>
      ),
    },
  ];

  return (
    <LegalPage
      icon={Shield}
      eyebrow="Privacy"
      title="Privacy Policy"
      intro={
        <P>
          This Privacy Policy describes how {ENTITY} (&ldquo;Trilli,&rdquo; &ldquo;we,&rdquo;
          &ldquo;us&rdquo;) collects, uses, stores, and protects information when you use our file
          storage and collaboration service.
        </P>
      }
      sections={sections}
      counterpart={{ to: "/terms", label: "Read our Terms of Service" }}
    />
  );
}

// ---------------------------------------------------------------------------
// Terms of Service
// ---------------------------------------------------------------------------
export function Terms() {
  const sections: Section[] = [
    {
      id: "agreement",
      heading: "Agreement to these Terms",
      body: (
        <P>
          These Terms of Service (the &ldquo;Terms&rdquo;) are a binding agreement between you and{" "}
          {ENTITY} governing your access to and use of Trilli. By creating an account or using the
          service, you agree to these Terms. If you are using Trilli on behalf of an organization,
          you represent that you are authorized to bind that organization.
        </P>
      ),
    },
    {
      id: "service",
      heading: "The service",
      body: (
        <P>
          Trilli provides secure file storage, sharing, and collaboration tools, including
          workspaces, sharing links, drop portals, and related apps. We may add, change, or remove
          features over time. We work hard to keep the service available but do not guarantee it
          will be uninterrupted or error-free.
        </P>
      ),
    },
    {
      id: "accounts",
      heading: "Your account",
      body: (
        <UL>
          <li>You must provide accurate information and keep it up to date.</li>
          <li>
            You are responsible for safeguarding your credentials and for all activity under your
            account. Enable two-factor authentication or a passkey to help keep it secure.
          </li>
          <li>Notify us promptly of any unauthorized use or security breach.</li>
        </UL>
      ),
    },
    {
      id: "acceptable-use",
      heading: "Acceptable use and fair use",
      body: (
        <>
          <P>
            Trilli is intended for storing, sharing, and collaborating on your own files and your
            team&apos;s files. It is <strong className="text-foreground">not</strong> a platform for
            operating a public content-distribution business. In particular, you agree not to:
          </P>
          <UL>
            <li>
              Use Trilli as a content delivery network (CDN), public file host, media-streaming
              server, or to distribute files to the general public at scale.
            </li>
            <li>Resell, sublicense, or redistribute storage, bandwidth, or the service itself.</li>
            <li>
              Generate excessive automated traffic, or use the service in a way that degrades
              performance or stability for others.
            </li>
            <li>
              Circumvent storage, file-size, transfer, or other plan limits, or share a single
              account to evade per-account limits.
            </li>
          </UL>
          <P>
            Plans described as &ldquo;unlimited&rdquo; are offered for normal individual and team
            use, subject to fair use. We may contact you, apply reasonable limits, or require a
            different plan if your usage materially exceeds typical patterns or is consistent with
            the prohibited uses above.
          </P>
        </>
      ),
    },
    {
      id: "prohibited-content",
      heading: "Prohibited content and conduct",
      body: (
        <>
          <P>You may not store, upload, share, or transmit content that:</P>
          <UL>
            <li>
              Is illegal, or facilitates illegal activity — including child sexual abuse material
              (CSAM), which we report as required by law; threats; or content that promotes
              terrorism or serious violence.
            </li>
            <li>Infringes the intellectual-property or privacy rights of others.</li>
            <li>Contains malware, or is used for phishing, fraud, or to harm others.</li>
            <li>Harasses, defames, or violates the rights or safety of any person.</li>
          </UL>
        </>
      ),
    },
    {
      id: "enforcement",
      heading: "Enforcement, suspension, and law enforcement",
      body: (
        <>
          <P>
            If we learn — whether through a report, directly, or indirectly — that an account may
            contain or be used for content or conduct that is illegal, abusive, or harmful, we may
            investigate and, at our discretion, remove the content, suspend or terminate the
            account, and preserve relevant records.
          </P>
          <P>
            Where we reasonably believe content or activity is unlawful, we may, at our discretion,
            refer the matter and the relevant account information to the applicable law-enforcement
            authorities. Our handling of any associated personal data is governed by our{" "}
            <Link to="/privacy" className="text-primary hover:underline">
              Privacy Policy
            </Link>
            , including our requirement for valid legal process.
          </P>
        </>
      ),
    },
    {
      id: "your-content",
      heading: "Your content and ownership",
      body: (
        <>
          <P>
            You retain all ownership of the content you store in Trilli. We claim no ownership over
            it.
          </P>
          <P>
            You grant us a limited license to host, store, copy, transmit, and display your content
            solely as needed to operate and provide the service to you and the people you share with
            (for example, to back it up, generate previews, and deliver shared links). This license
            ends when you delete the content or close your account, subject to reasonable backup
            retention.
          </P>
        </>
      ),
    },
    {
      id: "billing",
      heading: "Plans, billing, and cancellation",
      body: (
        <>
          <P>
            Paid plans are billed in advance on a recurring basis. Prices and plan features are
            shown at checkout. Unless required by law, payments are non-refundable, including for
            partial billing periods.
          </P>
          <P>
            You may cancel at any time; changes that reduce your plan take effect at the end of the
            current term. If a subscription lapses and your account exceeds the limits of the free
            tier, your account may become read-only during a grace period before data is removed,
            as described in the product. We will never delete your data without that notice period.
          </P>
        </>
      ),
    },
    {
      id: "ip",
      heading: "Our intellectual property",
      body: (
        <P>
          Trilli, the Trilli logo, and the software and design of the service are owned by {ENTITY}{" "}
          and protected by intellectual-property laws. These Terms do not grant you any right to use
          our trademarks without our prior written permission.
        </P>
      ),
    },
    {
      id: "third-party",
      heading: "Third-party services",
      body: (
        <P>
          The service integrates third-party providers (such as Stripe for payments). Your use of
          those features may be subject to the third party&apos;s terms, and we are not responsible
          for third-party services.
        </P>
      ),
    },
    {
      id: "disclaimer",
      heading: "Disclaimers",
      body: (
        <P>
          The service is provided &ldquo;as is&rdquo; and &ldquo;as available,&rdquo; without
          warranties of any kind, whether express or implied, including merchantability, fitness for
          a particular purpose, and non-infringement. You are responsible for maintaining your own
          copies of critical data.
        </P>
      ),
    },
    {
      id: "liability",
      heading: "Limitation of liability",
      body: (
        <P>
          To the maximum extent permitted by law, {ENTITY} will not be liable for any indirect,
          incidental, special, consequential, or punitive damages, or for lost profits or data. Our
          total liability for any claim relating to the service is limited to the amount you paid us
          for the service in the twelve months before the event giving rise to the claim.
        </P>
      ),
    },
    {
      id: "termination",
      heading: "Termination",
      body: (
        <P>
          You may stop using and close your account at any time. We may suspend or terminate your
          access if you violate these Terms or if necessary to protect the service or other users.
          Sections that by their nature should survive termination will survive.
        </P>
      ),
    },
    {
      id: "changes",
      heading: "Changes to these Terms",
      body: (
        <P>
          We may update these Terms from time to time. If we make material changes, we&apos;ll update
          the date above and, where appropriate, notify you. Continued use of the service after
          changes take effect means you accept the revised Terms.
        </P>
      ),
    },
    {
      id: "governing-law",
      heading: "Governing law",
      body: (
        <P>
          These Terms are governed by the laws of the State of Wyoming, United States, without
          regard to its conflict-of-laws rules, and the parties submit to the exclusive jurisdiction
          of the courts located there, except where applicable law provides otherwise.
        </P>
      ),
    },
    {
      id: "contact",
      heading: "Contact us",
      body: (
        <P>
          Questions about these Terms? Email{" "}
          <a className="text-primary hover:underline" href={`mailto:${LEGAL_EMAIL}`}>
            {LEGAL_EMAIL}
          </a>
          .
        </P>
      ),
    },
  ];

  return (
    <LegalPage
      icon={ScrollText}
      eyebrow="Terms"
      title="Terms of Service"
      intro={
        <P>
          These Terms govern your use of Trilli, a file storage and collaboration service provided
          by {ENTITY}. Please read them carefully — by using Trilli, you agree to them.
        </P>
      }
      sections={sections}
      counterpart={{ to: "/privacy", label: "Read our Privacy Policy" }}
    />
  );
}
