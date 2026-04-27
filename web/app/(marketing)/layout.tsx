// Marketing route group — landing, docs, integrations, faq, legal.
// Parenthesized folders are Next.js route groups: they don't affect URLs,
// they just let these pages share a layout and be organized together.
//
// Existing pages (landing `/`, `/docs`, `/faq`, ...) still live
// at the app root for URL stability. New marketing pages go here, using this
// group's layout.
import type { ReactNode } from "react";

export default function MarketingLayout({ children }: { children: ReactNode }) {
  return <>{children}</>;
}
