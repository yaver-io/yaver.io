import { forwardRef } from "react";
import { cn } from "@/lib/cn";

export type ButtonVariant =
  | "primary"
  | "secondary"
  | "ghost"
  | "danger"
  | "danger-ghost";

export type ButtonSize = "sm" | "md" | "lg";

const VARIANT: Record<ButtonVariant, string> = {
  primary:
    "bg-brand text-brand-fg hover:bg-brand/90 active:bg-brand/85 disabled:opacity-50",
  secondary:
    "border border-surface-700 text-surface-100 bg-surface-900 dark:bg-transparent hover:bg-surface-850 hover:border-surface-600 dark:hover:bg-surface-700/30 disabled:opacity-50",
  ghost:
    "text-surface-200 hover:text-surface-50 hover:bg-surface-800/40 dark:hover:bg-surface-700/30 disabled:opacity-50",
  danger:
    "bg-danger text-danger-fg hover:bg-danger/90 active:bg-danger/85 disabled:opacity-50",
  "danger-ghost":
    "text-danger hover:bg-danger/10 hover:text-danger disabled:opacity-50",
};

const SIZE: Record<ButtonSize, string> = {
  sm: "h-7 px-2.5 text-xs gap-1.5 rounded-md",
  md: "h-8 px-3 text-[13px] gap-1.5 rounded-md",
  lg: "h-10 px-4 text-sm gap-2 rounded-lg",
};

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  fullWidth?: boolean;
  iconLeft?: React.ReactNode;
  iconRight?: React.ReactNode;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  {
    variant = "primary",
    size = "md",
    fullWidth,
    iconLeft,
    iconRight,
    className,
    children,
    type = "button",
    ...rest
  },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={cn(
        "inline-flex items-center justify-center font-medium transition-all duration-150 select-none",
        "focus:outline-none focus-visible:ring-2 focus-visible:ring-brand focus-visible:ring-offset-2 focus-visible:ring-offset-surface-950",
        "disabled:cursor-not-allowed",
        "active:scale-[0.97]",
        SIZE[size],
        VARIANT[variant],
        fullWidth && "w-full",
        className,
      )}
      {...rest}
    >
      {iconLeft ? <span className="shrink-0 [&>svg]:h-3.5 [&>svg]:w-3.5">{iconLeft}</span> : null}
      {children}
      {iconRight ? <span className="shrink-0 [&>svg]:h-3.5 [&>svg]:w-3.5">{iconRight}</span> : null}
    </button>
  );
});
