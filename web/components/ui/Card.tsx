import { cn } from "@/lib/cn";

export type CardTone = "default" | "success" | "warning" | "danger" | "info";

const TONE: Record<CardTone, string> = {
  default: "border-surface-700/60 dark:border-surface-700/70",
  success:
    "border-success/40 ring-1 ring-success/15 dark:border-success/30 dark:ring-success/10",
  warning:
    "border-warning/40 ring-1 ring-warning/15 dark:border-warning/30 dark:ring-warning/10",
  danger:
    "border-danger/40 ring-1 ring-danger/15 dark:border-danger/30 dark:ring-danger/10",
  info: "border-info/40 ring-1 ring-info/15 dark:border-info/30 dark:ring-info/10",
};

interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
  tone?: CardTone;
  interactive?: boolean;
  padding?: "sm" | "md" | "lg";
}

const PADDING = {
  sm: "px-3 py-2.5",
  md: "px-4 py-3.5",
  lg: "px-5 py-4",
};

export function UICard({
  tone = "default",
  interactive = false,
  padding = "md",
  className,
  children,
  ...rest
}: CardProps) {
  return (
    <div
      className={cn(
        "rounded-xl border bg-surface-900/40 dark:bg-surface-850/60",
        "shadow-[0_1px_2px_rgba(0,0,0,0.04)] dark:shadow-[0_2px_8px_rgba(0,0,0,0.4)]",
        "transition-colors duration-200",
        PADDING[padding],
        TONE[tone],
        interactive &&
          "cursor-pointer hover:border-surface-600 dark:hover:border-surface-600 active:scale-[0.997] transition-transform",
        className,
      )}
      {...rest}
    >
      {children}
    </div>
  );
}
