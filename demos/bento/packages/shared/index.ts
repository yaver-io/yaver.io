// Cross-surface types + utils for Bento.
//
// Anything that lives on more than one surface (web, mobile,
// backend) goes in this file or a sibling. Keep it lean — no
// React imports, no Node APIs, pure TypeScript only.

export const APP_NAME = "Bento";
export const APP_TAGLINE = "Meal prep that ships itself";

export interface User {
  id: string;
  email: string;
  name?: string;
  avatarUrl?: string;
}
