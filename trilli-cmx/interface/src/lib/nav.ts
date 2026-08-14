import {
  LayoutDashboard,
  Users,
  Building2,
  Package,
  CreditCard,
  Server,
  BarChart3,
  Shield,
  type LucideIcon,
} from "lucide-react";
import type { Role } from "@/lib/api";

// The CMX left-nav IA (SPEC §5.4.1). `minRole` gates visibility: "cmx" items
// show for everyone; "global" items are Global-admin only. `comingSoon` renders
// the entry inactive with a muted badge (SPEC §5.5) — discoverable, not usable,
// until its backing feature ships.
export interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  minRole: Role; // "cmx" => all; "global" => global only
  section: string; // sidebar group heading (design system §8.2)
  comingSoon?: boolean;
}

export const NAV: NavItem[] = [
  { to: "/", label: "Main", icon: LayoutDashboard, minRole: "cmx", section: "Overview" },
  { to: "/accounts", label: "Accounts", icon: Building2, minRole: "cmx", section: "Management" },
  { to: "/customers", label: "Customers", icon: Users, minRole: "cmx", section: "Management" },
  { to: "/catalog", label: "Catalog", icon: Package, minRole: "cmx", section: "Management" },
  { to: "/revenue", label: "Revenue", icon: CreditCard, minRole: "global", section: "Platform" },
  { to: "/infrastructure", label: "Infrastructure", icon: Server, minRole: "global", section: "Platform" },
  { to: "/reports", label: "Reports & Marketing", icon: BarChart3, minRole: "global", section: "Platform" },
  { to: "/administration", label: "Administration", icon: Shield, minRole: "global", section: "Platform" },
];

// Section order for the sidebar (design system §8.2). Groups with no
// role-visible items are skipped at render time.
export const NAV_SECTIONS = ["Overview", "Management", "Platform"] as const;

// canSee reports whether an operator role may see a nav item.
export function canSee(item: NavItem, role: Role): boolean {
  return item.minRole === "cmx" || role === "global";
}
