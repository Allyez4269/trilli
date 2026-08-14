import { FormEvent } from "react";
import { FileUp, FolderPlus, Grid3X3, List, Plus, Search } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

export interface PageHeaderProps {
  title: string;
  // Icon rendered to the left of the title (matches the Recent/Starred
  // pattern: <Clock className="h-6 w-6 ..." /> sitting next to the h1).
  icon?: React.ReactNode;
  subtitle?: React.ReactNode;
  searchValue?: string;
  onSearchChange?: (v: string) => void;
  onSearchSubmit?: (v: string) => void;
  viewMode?: "grid" | "list";
  onViewModeChange?: (m: "grid" | "list") => void;
  onCreateFolder?: () => void;
  onUpload?: () => void;
}

export function PageHeader({
  title,
  icon,
  subtitle,
  searchValue,
  onSearchChange,
  onSearchSubmit,
  viewMode,
  onViewModeChange,
  onCreateFolder,
  onUpload,
}: PageHeaderProps) {
  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    onSearchSubmit?.(searchValue ?? "");
  };

  const hasSearch = onSearchChange !== undefined;
  const hasActions = !!onCreateFolder || !!onUpload;
  const hasViewToggle = onViewModeChange !== undefined;

  return (
    <header className="flex flex-col gap-6">
      <div>
        {subtitle && (
          <div className="mb-1 flex items-center gap-1.5 text-sm text-muted-foreground">
            {subtitle}
          </div>
        )}
        <h1 className="flex items-center gap-2 text-2xl font-bold tracking-tight text-foreground">
          {icon}
          {title}
        </h1>
      </div>

      {(hasSearch || hasActions || hasViewToggle) && (
        <div className="flex flex-col items-stretch gap-4 sm:flex-row sm:items-center">
          {hasSearch && (
            <form onSubmit={handleSubmit} className="relative max-w-md flex-1">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                placeholder="Search files and folders…"
                value={searchValue ?? ""}
                onChange={(e) => onSearchChange?.(e.target.value)}
                className="border border-border bg-card pl-10 focus-visible:ring-1 focus-visible:ring-primary/40"
              />
            </form>
          )}

          <div className="flex items-center gap-2 sm:ml-auto">
            {hasViewToggle && (
              <div className="flex items-center rounded-lg bg-secondary p-1">
                <Button
                  variant="ghost"
                  size="icon"
                  className={cn("h-8 w-8 rounded-md", viewMode === "grid" && "bg-background shadow-sm")}
                  onClick={() => onViewModeChange?.("grid")}
                  title="Grid view"
                >
                  <Grid3X3 className="h-4 w-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  className={cn("h-8 w-8 rounded-md", viewMode === "list" && "bg-background shadow-sm")}
                  onClick={() => onViewModeChange?.("list")}
                  title="List view"
                >
                  <List className="h-4 w-4" />
                </Button>
              </div>
            )}

            {hasActions && (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button className="gap-2">
                    <Plus className="h-4 w-4" />
                    <span className="hidden sm:inline">New</span>
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-48">
                  {onCreateFolder && (
                    <DropdownMenuItem onSelect={onCreateFolder}>
                      <FolderPlus className="mr-2 h-4 w-4" />
                      New folder
                    </DropdownMenuItem>
                  )}
                  {onUpload && (
                    <DropdownMenuItem onSelect={onUpload}>
                      <FileUp className="mr-2 h-4 w-4" />
                      Upload files
                    </DropdownMenuItem>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
          </div>
        </div>
      )}
    </header>
  );
}
