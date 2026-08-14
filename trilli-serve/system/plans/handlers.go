package plans

import (
	"encoding/json"
	"fmt"
	"net/http"

	"trilli/system/logging"
)

// Handlers exposes the plan catalog endpoints.
type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}

// Register wires the public catalog route. The catalog is intentionally public
// (no auth) so it can also feed the signup plan-picker later.
func (h *Handlers) Register(m muxLike) {
	m.Handle("GET /api/plans", http.HandlerFunc(h.ListPublic))
}

// publicPlan is the shape the Settings → Plans grid renders. price_monthly is in
// dollars; the annual rate + "Save X%" are derived client-side from the discount.
type publicPlan struct {
	ID                int64    `json:"id"`
	Code              string   `json:"code"`
	Name              string   `json:"name"`
	Icon              string   `json:"icon"`
	Callout           string   `json:"callout"`
	Tagline           string   `json:"tagline"`
	PriceMonthly      float64  `json:"price_monthly"`
	AnnualDiscountPct int      `json:"annual_discount_pct"`
	Popular           bool     `json:"popular"`
	Features          []string `json:"features"`
	// Seat allowance + per-extra-seat price (monthly, dollars). included_seats
	// is null when the plan has unlimited seats; per_seat_monthly is null when
	// extra seats aren't sold.
	IncludedSeats  *int64   `json:"included_seats"`
	PerSeatMonthly *float64 `json:"per_seat_monthly"`
	// Structured allowances so external consumers (the marketing site's plan
	// cards + compare matrix) can render exact values without parsing the
	// feature prose. Null = unlimited.
	StorageBytes       *int64 `json:"storage_bytes"`
	TransferBytesMonth *int64 `json:"transfer_bytes_month"`
	MaxFileBytes       *int64 `json:"max_file_bytes"`
	APIAccess          bool   `json:"api_access"`
	Support            string `json:"support"`
}

// ListPublic handles GET /api/plans — available plans, ordered, ready to render.
func (h *Handlers) ListPublic(w http.ResponseWriter, r *http.Request) {
	ps, err := h.svc.ListPublic(r.Context())
	if err != nil {
		logging.Error(packageName, "ListPublic failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	out := make([]publicPlan, 0, len(ps))
	maxDiscount := 0
	for _, p := range ps {
		if p.AnnualDiscountPct > maxDiscount {
			maxDiscount = p.AnnualDiscountPct
		}
		out = append(out, publicPlan{
			ID:                 p.ID,
			Code:               p.Code,
			Name:               p.Name,
			Icon:               p.IconKey,
			Callout:            p.Callout,
			Tagline:            p.Tagline,
			PriceMonthly:       float64(p.PriceMonthlyCents) / 100,
			AnnualDiscountPct:  p.AnnualDiscountPct,
			Popular:            p.IsPopular,
			Features:           composeFeatures(p),
			IncludedSeats:      p.MaxUsers,
			PerSeatMonthly:     centsPtrToDollars(p.PerSeatCents),
			StorageBytes:       p.MaxStorageBytes,
			TransferBytesMonth: p.MaxTransferBytesMonth,
			MaxFileBytes:       p.MaxFileSizeBytes,
			APIAccess:          p.APIAccess,
			Support:            supportLine(p.SupportLevel),
		})
	}
	// The catalog changes rarely (admin edits) — let browsers and the
	// marketing-site proxy cache it briefly so pricing surfaces stay fast
	// without ever drifting more than a few minutes from the database.
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Access-Control-Allow-Origin", "*") // public catalog data
	writeJSON(w, http.StatusOK, map[string]any{
		"plans":               out,
		"annual_discount_pct": maxDiscount,
	})
}

// composeFeatures builds the bullet list for a plan card: per-plan allowances
// (list A) first, then the universal standard features (list B), then the
// per-plan API/support lines, then any free-text marketing lines.
func composeFeatures(p Plan) []string {
	f := []string{
		storageLine(p.MaxStorageBytes),
		transferLine(p.MaxTransferBytesMonth),
		fileSizeLine(p.MaxFileSizeBytes),
		workspacesLine(p.MaxWorkspaces),
		seatLine(p.MaxUsers, p.PerSeatCents),
		// Universal standard features — identical on every plan.
		"Included apps: TrilliSign + TrilliPDF",
		"2FA & passkey sign-in",
		"HIPAA compliant",
		"Access activity log",
		"Advanced workspace + folder access control",
		"Audit log exports",
		"Trusted-device & session controls",
	}
	if p.APIAccess {
		f = append(f, "API access")
	}
	f = append(f, supportLine(p.SupportLevel))
	f = append(f, p.MarketingLines...)
	return f
}

func storageLine(n *int64) string {
	if n == nil {
		return "Unlimited encrypted storage"
	}
	return human(*n) + " encrypted storage"
}
func transferLine(n *int64) string {
	if n == nil {
		return "Unlimited transfer (fair use)"
	}
	return human(*n) + " / mo transfer"
}
func fileSizeLine(n *int64) string {
	if n == nil {
		return "Unlimited file size"
	}
	return human(*n) + " max file size"
}
func workspacesLine(n *int64) string {
	if n == nil {
		return "Unlimited workspaces"
	}
	return fmt.Sprintf("%d workspaces", *n)
}

// seatLine describes the plan's seat allowance and the price of buying more.
// maxUsers == nil → unlimited seats; perSeatCents == nil → extra seats not sold.
func seatLine(maxUsers, perSeatCents *int64) string {
	if maxUsers == nil {
		return "Unlimited users"
	}
	base := fmt.Sprintf("%d users included", *maxUsers)
	if perSeatCents == nil {
		return base
	}
	return fmt.Sprintf("%s · extra seats %s/mo", base, dollarsLabel(*perSeatCents))
}

// dollarsLabel renders cents as "$8" or "$8.50" (drops trailing ".00").
func dollarsLabel(cents int64) string {
	if cents%100 == 0 {
		return fmt.Sprintf("$%d", cents/100)
	}
	return fmt.Sprintf("$%.2f", float64(cents)/100)
}

func centsPtrToDollars(cents *int64) *float64 {
	if cents == nil {
		return nil
	}
	v := float64(*cents) / 100
	return &v
}
func supportLine(level int) string {
	switch level {
	case 3:
		return "24/7 priority support"
	case 2:
		return "Priority email & chat support"
	default:
		return "Email support"
	}
}

// human renders a byte count as whole TB/GB where it divides cleanly (the seeded
// allowances are exact binary-TB multiples), falling back to one decimal GB.
func human(n int64) string {
	const tb = 1 << 40
	const gb = 1 << 30
	switch {
	case n%tb == 0:
		return fmt.Sprintf("%d TB", n/tb)
	case n%gb == 0:
		return fmt.Sprintf("%d GB", n/gb)
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gb))
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
