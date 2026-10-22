import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cn } from "../../lib/utils";
const variants: Record<string,string> = {
  default: "bg-[var(--accent)] text-white hover:bg-[#9F2D16]",
  outline: "border border-[var(--line)] bg-[var(--surface)] text-[var(--ink)] hover:bg-[var(--soft)]",
  ghost: "bg-transparent text-[var(--muted)] hover:bg-[var(--soft)]",
  danger: "border border-[#e2b8b5] text-[#b3261e] hover:bg-[#fdeceb]",
};
const sizes: Record<string,string> = { default: "h-12 px-6 text-[15px]", sm: "h-11 px-3 text-[13px]", lg: "h-12 px-8 text-base" };
export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: keyof typeof variants; size?: keyof typeof sizes; asChild?: boolean;
}
export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant = "default", size = "default", asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button";
    return <Comp ref={ref} className={cn("inline-flex shrink-0 whitespace-nowrap items-center justify-center gap-2 font-semibold transition duration-200 cursor-pointer rounded-[10px] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--accent)] disabled:opacity-45 disabled:cursor-not-allowed active:scale-[.98] motion-reduce:transition-none", variants[variant], sizes[size], className)} {...props} />;
  });
Button.displayName = "Button";
