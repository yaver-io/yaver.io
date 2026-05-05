import { cn } from "@/lib/cn";

type Tone = "success" | "warning" | "danger" | "info" | "muted";

const TONE: Record<Tone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
  info: "bg-info",
  muted: "bg-muted",
};

interface LiveDotProps {
  tone?: Tone;
  pulse?: boolean;
  size?: "xs" | "sm" | "md";
  className?: string;
}

const SIZE = {
  xs: "h-1.5 w-1.5",
  sm: "h-2 w-2",
  md: "h-2.5 w-2.5",
};

export function LiveDot({
  tone = "success",
  pulse = true,
  size = "sm",
  className,
}: LiveDotProps) {
  return (
    <span
      className={cn(
        "inline-block rounded-full shrink-0",
        SIZE[size],
        TONE[tone],
        pulse && "animate-live-pulse",
        className,
      )}
    />
  );
}
