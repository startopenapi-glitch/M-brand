import * as React from "react";
import { cn } from "../../lib/utils";
export const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className, ...p }, ref) => <input ref={ref} className={cn("ui-input", className)} {...p} />);
Input.displayName = "Input";
export const Textarea = React.forwardRef<HTMLTextAreaElement, React.TextareaHTMLAttributes<HTMLTextAreaElement>>(
  ({ className, ...p }, ref) => <textarea ref={ref} className={cn("ui-input ui-textarea", className)} {...p} />);
Textarea.displayName = "Textarea";
export const Label = ({ children, required, className, ...props }: React.LabelHTMLAttributes<HTMLLabelElement> & { required?: boolean }) =>
  <label className={cn("ui-label", className)} {...props}>{children}{required && <span aria-hidden="true" className="required-mark"> *</span>}</label>;
export const Badge = ({ children }: { children: React.ReactNode }) =>
  <span className="eyebrow">{children}</span>;
