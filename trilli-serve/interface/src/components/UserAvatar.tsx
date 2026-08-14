import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { userInitials } from "@/lib/user-name";
import { cn } from "@/lib/utils";
import type { User } from "@/lib/api";

// UserAvatar renders a user's uploaded avatar image when present, falling back
// to their initials on a tinted circle. One place so the nav, settings, and
// anywhere else stay consistent. `className` sizes the circle; `fallbackClassName`
// tunes the initials' type size for small vs. large renders.
export function UserAvatar({
  user,
  className,
  fallbackClassName,
}: {
  user?: Partial<User> | null;
  className?: string;
  fallbackClassName?: string;
}) {
  const src = user?.avatar_url ?? undefined;
  return (
    <Avatar className={cn("h-8 w-8", className)}>
      {src && <AvatarImage src={src} alt="" className="object-cover" />}
      <AvatarFallback
        className={cn("bg-primary/10 text-[11px] font-medium text-primary", fallbackClassName)}
      >
        {userInitials(user ?? undefined)}
      </AvatarFallback>
    </Avatar>
  );
}
