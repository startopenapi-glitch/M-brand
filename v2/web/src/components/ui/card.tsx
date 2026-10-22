import * as React from "react";
import { cn } from "../../lib/utils";
export const Card = ({ className, ...p }: React.HTMLAttributes<HTMLDivElement>) =>
  <div className={cn("surface p-5", className)} {...p} />;
export const CardTitle = ({ className, ...p }: React.HTMLAttributes<HTMLHeadingElement>) =>
  <h3 className={cn("text-lg font-semibold leading-snug", className)} {...p} />;
export const CardDesc = ({ className, ...p }: React.HTMLAttributes<HTMLParagraphElement>) =>
  <p className={cn("text-sm text-[var(--muted)]", className)} {...p} />;
