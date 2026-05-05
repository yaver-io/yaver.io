import { cn } from "@/lib/cn";

export type BadgeTone =
  | "brand"
  | "success"
  | "warning"
  | "danger"
  | "info"
  | "muted";

export type BadgeVariant = "soft" | "solid" | "outline";

const TONE_SOFT: Record<BadgeTone, string> = {
  brand: "bg-brand-soft text-brand-softFg",
  success: "bg-success-soft text-success-softFg",
  warning: "bg-warning-soft text-warning-softFg",
  danger: "bg-danger-soft text-danger-softFg",
  info: "bg-info-soft text-info-softFg",
  muted: "bg-muted-soft text-muted-softFg",
};

const TONE_SOLID: Record<BadgeTone, string> = {
  brand: "bg-brand text-brand-fg",
  success: "bg-success text-success-fg",
  warning: "bg-warning text-warning-fg",
  danger: "bg-danger text-danger-fg",
  info: "bg-info text-info-fg",
  muted: "bg-muted text-muted-fg",
};

const TONE_OUTLINE: Record<BadgeTone, string> = {
  brand: "border border-brand/40 text-brand",
  success: "border border-success/40 text-success",
  warning: "border border-warning/40 text-warning",
  danger: "border border-danger/40 text-danger",
  info: "border border-info/40 text-info",
  muted: "border border-muted/30 text-muted-softFg",
};

interface BadgeProps {
  children: React.ReactNode;
  tone?: BadgeTone;
  variant?: BadgeVariant;
  className?: string;
  uppercase?: boolean;
  icon?: React.ReactNode;
}

export function Badge({
  children,
  tone = "muted",
  variant = "soft",
  className,
  uppercase = true,
  icon,
}: BadgeProps) {
  const toneStyles =
    variant === "solid"
      ? TONE_SOLID[tone]
      : variant === "outline"
        ? TONE_OUTLINE[tone]
        : TONE_SOFT[tone];

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10.5px] font-semibold leading-none whitespace-nowrap",
        uppercase && "tracking-wider uppercase",
        toneStyles,
        className,
      )}
    >
      {icon ? <span className="-ml-0.5 flex shrink-0">{icon}</span> : null}
      {children}
    </span>
  );
}
