// Adapted from shadcn-admin at e16c87f213a5ba5e45964e9b67c792105ec74d26.
// MIT, Copyright (c) 2024 Sat Naing. See THIRD_PARTY_NOTICES.md.
import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { clsx } from "clsx";
import { twMerge } from "tailwind-merge";
const buttonVariants = cva("button", {
  variants: {
    variant: {
      default: "button-primary",
      outline: "button-outline",
      ghost: "button-ghost",
    },
    size: { default: "button-normal", sm: "button-small" },
  },
  defaultVariants: { variant: "default", size: "default" },
});
export function Button({
  className,
  variant,
  size,
  asChild = false,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot : "button";
  return (
    <Comp
      className={twMerge(clsx(buttonVariants({ variant, size }), className))}
      {...props}
    />
  );
}
