import { Sparkles } from "lucide-react";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

export function ComingSoon({
  title,
  description,
  phase,
  details,
  note,
}: {
  title: string;
  description: string;
  phase: string;
  details?: string;
  // Overrides the default dev-flavored paragraph in the card body. Useful for
  // user-facing pages (legal, status) where the default note reads oddly.
  note?: string;
}) {
  return (
    <div className="mx-auto max-w-3xl px-6 py-10">
      <h1 className="text-3xl font-semibold">{title}</h1>
      <p className="mt-2 text-muted-foreground">{description}</p>

      <Card className="mt-8 border-dashed">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base font-medium">
            <Sparkles className="h-5 w-5 text-brand-purple" />
            Coming in {phase}
          </CardTitle>
          {details && <CardDescription className="pt-1">{details}</CardDescription>}
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">
            {note ??
              "The route is reserved and the database schema can already represent this feature. The handlers and UI haven't been built yet."}
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
