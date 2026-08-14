import { useEffect, lazy, Suspense } from "react";
import { BrowserRouter, Routes, Route, Navigate, useLocation, useParams } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { AuthProvider } from "@/contexts/AuthContext";
import { ProtectedRoute } from "@/components/ProtectedRoute";
// Home and Files are the most-used authed pages — kept eager (in the initial
// bundle) so they render with no Suspense flash. Everything else is lazy.
import Home from "@/pages/Home";
import Files from "@/pages/Files";
// Pages are lazy-loaded so each becomes its own on-demand chunk, keeping the
// initial bundle small. The <Suspense> boundary around <Routes> covers the
// brief load. Named-export page modules are adapted to a default for lazy().
const Login = lazy(() => import("@/pages/Login"));
const SignEditor = lazy(() => import("@/pages/sign/SignEditor"));
const SignCeremony = lazy(() => import("@/pages/sign/SignCeremony"));
const SelectAccount = lazy(() => import("@/pages/SelectAccount"));
const GetStarted = lazy(() => import("@/pages/GetStarted"));
const NewAccountFinish = lazy(() => import("@/pages/NewAccountFinish"));
const Signup = lazy(() => import("@/pages/Signup"));
const SignupVerify = lazy(() => import("@/pages/SignupVerify"));
const SignupCheckout = lazy(() => import("@/pages/SignupCheckout"));
const SignupComplete = lazy(() => import("@/pages/SignupComplete"));
const Recent = lazy(() => import("@/pages/Recent"));
const Starred = lazy(() => import("@/pages/Starred"));
const Trash = lazy(() => import("@/pages/Trash"));
const Usage = lazy(() => import("@/pages/Usage"));
const Checkout = lazy(() => import("@/pages/Checkout"));
const Welcome = lazy(() => import("@/pages/Welcome"));
const Members = lazy(() => import("@/pages/Members"));
const InviteAccept = lazy(() => import("@/pages/InviteAccept"));
const ConfirmEmail = lazy(() => import("@/pages/ConfirmEmail"));
const ResetPassword = lazy(() => import("@/pages/ResetPassword"));
const Activity = lazy(() => import("@/pages/Activity"));
const Settings = lazy(() => import("@/pages/Settings"));
const NotFound = lazy(() => import("@/pages/NotFound"));
const Shared = lazy(() => import("@/pages/Shared"));
const ShareLanding = lazy(() => import("@/pages/ShareLanding"));
const PortalLanding = lazy(() => import("@/pages/PortalLanding"));
const Support = lazy(() => import("@/pages/Support"));
const SupportTicket = lazy(() => import("@/pages/SupportTicket"));
const StatusPage = lazy(() => import("@/pages/Stubs").then((m) => ({ default: m.StatusPage })));
const PdfLauncher = lazy(() => import("@/pages/pdf/PdfLauncher"));
const PdfTool = lazy(() => import("@/pages/pdf/PdfTool"));
const TrilliSign = lazy(() => import("@/pages/Stubs").then((m) => ({ default: m.TrilliSign })));
const Privacy = lazy(() => import("@/pages/Legal").then((m) => ({ default: m.Privacy })));
const Terms = lazy(() => import("@/pages/Legal").then((m) => ({ default: m.Terms })));
const ApiAccess = lazy(() => import("@/pages/ApiAccess"));
const ProductivityLauncher = lazy(() => import("@/pages/productivity/Launcher"));
const ProductivityEditor = lazy(() => import("@/pages/productivity/Editor"));

// Key the editor by the :app param so switching apps (docs ↔ sheets ↔ slides)
// REMOUNTS it. Without this, react-router reuses one instance across the param
// change and the editor keeps the previous app's session/state (e.g. switching
// back to Docs still showed Sheets).
function ProductivityEditorRoute() {
  const { app } = useParams();
  return <ProductivityEditor key={app ?? "productivity"} />;
}

// Key the PDF tool shell by :tool so switching tools remounts it with fresh
// state (selected files, params, result), the same reason as the editor above.
function PdfToolRoute() {
  const { tool } = useParams();
  return <PdfTool key={tool ?? "pdf"} />;
}

const queryClient = new QueryClient();

// TitleManager keeps the browser-tab title in sync with the section being
// viewed. Longest-prefix match against the path, "<Section> · Trilli";
// unknown routes fall back to the product default from index.html.
const TITLES: [prefix: string, title: string][] = [
  ["/home", "Home"],
  ["/files", "Files"],
  ["/recent", "Recent"],
  ["/starred", "Starred"],
  ["/shared", "Shared"],
  ["/trash", "Trash"],
  ["/members", "Users"],
  ["/activity", "Activity"],
  ["/usage", "Plan & usage"],
  ["/api-access", "API access"],
  ["/support", "Support"],
  ["/settings", "Settings"],
  ["/checkout", "Checkout"],
  ["/welcome", "Welcome"],
  ["/apps/pdf", "Trilli PDF"],
  ["/apps/sign", "Trilli Sign"],
  ["/sign/", "Sign document"],
  ["/status", "Status"],
  ["/privacy", "Privacy Policy"],
  ["/terms", "Terms of Service"],
  ["/login", "Sign in"],
  ["/signup", "Sign up"],
  ["/invite", "Invitation"],
  ["/confirm-email", "Confirm email"],
  ["/reset-password", "Reset password"],
  ["/s/", "Shared file"],
  ["/p/", "Drop portal"],
];
const DEFAULT_TITLE = "Trilli — File Storage";

function TitleManager() {
  const { pathname } = useLocation();
  useEffect(() => {
    const hit = TITLES.find(([prefix]) => pathname.startsWith(prefix));
    document.title = hit ? `${hit[1]} · Trilli` : DEFAULT_TITLE;
  }, [pathname]);
  return null;
}

function Protected({ children }: { children: React.ReactNode }) {
  return <ProtectedRoute>{children}</ProtectedRoute>;
}

function AdminOnly({ children }: { children: React.ReactNode }) {
  return <ProtectedRoute adminOnly>{children}</ProtectedRoute>;
}

function ProtectedBare({ children }: { children: React.ReactNode }) {
  return <ProtectedRoute bare>{children}</ProtectedRoute>;
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <TitleManager />
        <AuthProvider>
          <Suspense
            fallback={
              <div className="flex min-h-screen items-center justify-center text-sm text-muted-foreground">
                Loading…
              </div>
            }
          >
          <Routes>
            <Route path="/" element={<Navigate to="/home" replace />} />
            <Route path="/dashboard" element={<Navigate to="/home" replace />} />

            <Route path="/login" element={<Login />} />
            {/* Clean slash route for the "I already have an account but want my
                own" hand-off (vs a ?intent= query string). */}
            <Route path="/login/new-account" element={<Login />} />
            {/* Post-auth account picker (self-gates on identity; no AppShell). */}
            <Route path="/select-account" element={<SelectAccount />} />
            {/* Buy-your-own-account (authed, self-gating, full-screen). */}
            <Route path="/get-started" element={<GetStarted />} />
            <Route path="/account/new/finish/:token" element={<NewAccountFinish />} />
            <Route path="/signup" element={<Signup />} />
            <Route path="/signup/verify/:token" element={<SignupVerify />} />
            <Route path="/signup/checkout/:token" element={<SignupCheckout />} />
            <Route path="/signup/complete/:token" element={<SignupComplete />} />
            <Route path="/invite/:token" element={<InviteAccept />} />
            <Route path="/confirm-email/:token" element={<ConfirmEmail />} />
            <Route path="/reset-password/:token" element={<ResetPassword />} />
            <Route path="/s/:token" element={<ShareLanding />} />
            <Route path="/p/:token" element={<PortalLanding />} />
            <Route path="/sign/:token" element={<SignCeremony />} />
            {/* Legal pages are public: email footers link here, and recipients
                may not be logged in (or may not have an account at all). */}
            <Route path="/privacy" element={<Privacy />} />
            <Route path="/terms" element={<Terms />} />

            <Route path="/home" element={<Protected><Home /></Protected>} />
            {/* Opaque path-based file browser. Workspace and folder ids are
                scrambled into URL tokens (see lib/ids) so the address bar never
                shows raw sequential ids. The bare /files and folder-only
                /files/f/:folderToken forms self-resolve their workspace and
                upgrade the URL to the canonical /w/.../f/... shape. */}
            <Route path="/files" element={<Protected><Files /></Protected>} />
            <Route path="/files/w/:wsToken" element={<Protected><Files /></Protected>} />
            <Route path="/files/w/:wsToken/f/:folderToken" element={<Protected><Files /></Protected>} />
            <Route path="/files/f/:folderToken" element={<Protected><Files /></Protected>} />
            <Route path="/recent" element={<Protected><Recent /></Protected>} />
            <Route path="/starred" element={<Protected><Starred /></Protected>} />
            <Route path="/shared" element={<Protected><Shared /></Protected>} />
            <Route path="/trash" element={<Protected><Trash /></Protected>} />

            <Route path="/members" element={<AdminOnly><Members /></AdminOnly>} />
            <Route path="/activity" element={<Protected><Activity /></Protected>} />
            <Route path="/usage" element={<AdminOnly><Usage /></AdminOnly>} />
            <Route path="/api-access" element={<AdminOnly><ApiAccess /></AdminOnly>} />
            <Route path="/support" element={<Protected><Support /></Protected>} />
            <Route path="/support/:ref" element={<Protected><SupportTicket /></Protected>} />
            <Route path="/apps/pdf" element={<Protected><PdfLauncher /></Protected>} />
            <Route path="/apps/pdf/:tool" element={<Protected><PdfToolRoute /></Protected>} />
            <Route path="/apps/sign" element={<Protected><TrilliSign /></Protected>} />
            <Route path="/apps/sign/e/:id" element={<Protected><SignEditor /></Protected>} />
            <Route path="/apps/sign/e/:id/preview" element={<ProtectedBare><SignCeremony /></ProtectedBare>} />
            <Route path="/status" element={<Protected><StatusPage /></Protected>} />
            <Route path="/apps/productivity" element={<Protected><ProductivityLauncher /></Protected>} />
            <Route path="/apps/productivity/:app" element={<Protected><ProductivityEditorRoute /></Protected>} />
            <Route path="/checkout" element={<AdminOnly><Checkout /></AdminOnly>} />
            <Route path="/welcome" element={<Protected><Welcome /></Protected>} />
            {/* Settings sub-areas are clean path segments (/settings/account,
                /settings/notifications, …). Bare /settings canonicalizes to
                /settings/account inside the page. */}
            <Route path="/settings" element={<Protected><Settings /></Protected>} />
            <Route path="/settings/:tab" element={<Protected><Settings /></Protected>} />

            {/* Legacy page — personal info lives in Settings → Account now. */}
            <Route path="/profile" element={<Navigate to="/settings/account" replace />} />

            <Route path="*" element={<NotFound />} />
          </Routes>
          </Suspense>
        </AuthProvider>
      </BrowserRouter>
    </QueryClientProvider>
  );
}
