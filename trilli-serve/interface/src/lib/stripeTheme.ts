import type { Appearance } from "@stripe/stripe-js";

// Saved name/address (from the customer's saved card or stored account billing
// address) used to prefill the Address Element on checkout.
export type Prefill = {
  name?: string;
  address?: {
    line1?: string;
    line2?: string;
    city?: string;
    state?: string;
    postal_code?: string;
    country?: string;
  };
};

// Stripe Elements appearance — themed to the site's warm-brown palette so the
// embedded Payment/Address Elements match the rest of the app, not Stripe blue.
// Shared by the dedicated checkout page and the quick card-on-file modal.
export const STRIPE_APPEARANCE: Appearance = {
  theme: "stripe",
  variables: {
    colorPrimary: "#604a3f",
    colorText: "#2b2722",
    colorTextSecondary: "#6b6259",
    colorBackground: "#ffffff",
    colorDanger: "#b3261e",
    fontFamily: 'ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif',
    fontSizeBase: "13px",
    borderRadius: "6px",
    spacingUnit: "3px",
  },
  rules: {
    ".Input": { border: "1px solid rgba(57,49,42,0.25)", boxShadow: "none" },
    ".Input:focus": { border: "1px solid #604a3f", boxShadow: "0 0 0 1px rgba(96,74,63,0.35)" },
    ".Label": { fontWeight: "500" },
  },
};
