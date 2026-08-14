// Brand marks + metadata for the Cloud Import providers. Only Google Drive is
// live today; OneDrive and Dropbox are scaffolded and rendered disabled.
import type { ComponentType } from "react";

export function GoogleDriveLogo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 87.3 78" className={className} xmlns="http://www.w3.org/2000/svg" aria-hidden>
      <path d="m6.6 66.85 3.85 6.65c.8 1.4 1.95 2.5 3.3 3.3l13.75-23.8h-27.5c0 1.55.4 3.1 1.2 4.5z" fill="#0066da" />
      <path d="m43.65 25-13.75-23.8c-1.35.8-2.5 1.9-3.3 3.3l-25.4 44c-.8 1.4-1.2 2.95-1.2 4.5h27.5z" fill="#00ac47" />
      <path d="m73.55 76.8c1.35-.8 2.5-1.9 3.3-3.3l1.6-2.75 7.65-13.25c.8-1.4 1.2-2.95 1.2-4.5h-27.502l5.852 11.5z" fill="#ea4335" />
      <path d="m43.65 25 13.75-23.8c-1.35-.8-2.9-1.2-4.5-1.2h-18.5c-1.6 0-3.15.45-4.5 1.2z" fill="#00832d" />
      <path d="m59.8 53h-32.3l-13.75 23.8c1.35.8 2.9 1.2 4.5 1.2h50.8c1.6 0 3.15-.45 4.5-1.2z" fill="#2684fc" />
      <path d="m73.4 26.5-12.7-22c-.8-1.4-1.95-2.5-3.3-3.3l-13.75 23.8 16.15 28h27.45c0-1.55-.4-3.1-1.2-4.5z" fill="#ffba00" />
    </svg>
  );
}

export function OneDriveLogo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} xmlns="http://www.w3.org/2000/svg" aria-hidden>
      <path
        d="M13.3 8.2a5.2 5.2 0 0 0-9.5 1.6A4.3 4.3 0 0 0 4.6 18h12.9a3.7 3.7 0 0 0 .5-7.36 4.6 4.6 0 0 0-4.7-2.44z"
        fill="#0078d4"
      />
    </svg>
  );
}

export function DropboxLogo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 43 40" className={className} xmlns="http://www.w3.org/2000/svg" fill="#0061ff" aria-hidden>
      <path d="M12.6 0 0 8.03l8.7 6.97L21.3 7 12.6 0Zm17.8 0L17.8 7l12.6 8 8.7-6.97L30.4 0ZM0 21.97 12.6 30l8.7-7L8.7 15 0 21.97Zm34.3-6.97-8.7 7 8.7 7L43 21.97 34.3 15ZM21.3 24.6l-8.7 7.03L8.9 29.2v2.6L21.3 39.2l12.4-7.4v-2.6l-3.7 2.43-8.7-7.03Z" />
    </svg>
  );
}

export type CloudProvider = {
  key: "google" | "onedrive" | "dropbox";
  name: string;
  Logo: ComponentType<{ className?: string }>;
  enabled: boolean;
  blurb: string;
};

export const CLOUD_PROVIDERS: CloudProvider[] = [
  { key: "google", name: "Google Drive", Logo: GoogleDriveLogo, enabled: true, blurb: "Import documents, sheets, and files" },
  { key: "onedrive", name: "OneDrive", Logo: OneDriveLogo, enabled: false, blurb: "Coming soon" },
  { key: "dropbox", name: "Dropbox", Logo: DropboxLogo, enabled: false, blurb: "Coming soon" },
];
