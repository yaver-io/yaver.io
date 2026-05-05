import { cn } from "@/lib/cn";

interface EmptyStateProps {
  icon?: React.ReactNode;
  title: string;
  description?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
  compact?: boolean;
}

export function EmptyState({
  icon,
  title,
  description,
  action,
  className,
  compact = false,
}: EmptyStateProps) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center text-center",
        compact ? "py-6 gap-2" : "py-12 gap-3",
        className,
      )}
    >
      {icon ? (
        <div
          className={cn(
            "flex items-center justify-center rounded-full bg-surface-800/50 text-surface-400 dark:bg-surface-700/40 dark:text-surface-400",
            compact ? "h-9 w-9 [&>svg]:h-4 [&>svg]:w-4" : "h-12 w-12 [&>svg]:h-5 [&>svg]:w-5",
          )}
        >
          {icon}
        </div>
      ) : null}
      <div className={compact ? "text-sm font-semibold text-surface-100" : "text-base font-semibold text-surface-100"}>
        {title}
      </div>
      {description ? (
        <div className="max-w-sm text-xs text-surface-400 leading-relaxed">
          {description}
        </div>
      ) : null}
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  );
}
