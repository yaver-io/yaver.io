package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// ProjectTemplate describes a full-stack SaaS project template.
type ProjectTemplate struct {
	Name                string
	Description         string
	Stack               string
	Features            []string
	Includes            []string
	EstimatedSetupTime  string
}

// TemplateFile is a single generated file with its relative path and content.
type TemplateFile struct {
	Path    string
	Content string
}

// TemplateManager creates and applies project templates.
type TemplateManager struct {
	mu      sync.Mutex
	workDir string
}

// ─── Constructor ──────────────────────────────────────────────────────────────

// NewTemplateManager returns a TemplateManager rooted at workDir.
func NewTemplateManager(workDir string) *TemplateManager {
	return &TemplateManager{workDir: workDir}
}

// ─── Public API ───────────────────────────────────────────────────────────────

// List returns all available project templates with descriptions.
func (tm *TemplateManager) List() ([]ProjectTemplate, error) {
	return []ProjectTemplate{
		{
			Name:        "saas-complete",
			Description: "Production-ready SaaS starter with auth, billing, and a dashboard",
			Stack:       "Next.js 15, React 19, Better Auth, Stripe, Postgres, Drizzle ORM, Tailwind CSS v4, shadcn/ui",
			Features: []string{
				"Google + GitHub OAuth via Better Auth",
				"Stripe checkout, customer portal, and webhooks",
				"Postgres database with Drizzle ORM migrations",
				"Dashboard layout with collapsible sidebar",
				"Landing page: hero, features, pricing, CTA",
				"Dark-mode ready with Tailwind v4 + shadcn/ui",
			},
			Includes: []string{
				"package.json with all dependencies",
				"tsconfig.json, drizzle.config.ts, tailwind.config.ts",
				"app/layout.tsx, app/page.tsx (landing)",
				"app/dashboard/page.tsx (protected)",
				"app/api/auth/[...all]/route.ts",
				"app/api/stripe/webhook/route.ts",
				"lib/db.ts, lib/auth.ts, lib/stripe.ts",
				"components/ui/ (button, card, badge)",
				".yaver/services.yaml (postgres, redis, mailpit)",
				".env.local.example",
			},
			EstimatedSetupTime: "3-5 minutes",
		},
		{
			Name:        "indie-hacker",
			Description: "Lean single-app stack for solo founders: Next.js + PocketBase + Stripe + MDX blog",
			Stack:       "Next.js 15, PocketBase, Stripe, MDX + Contentlayer",
			Features: []string{
				"PocketBase embedded backend (zero config, single binary)",
				"Stripe payment integration",
				"MDX blog with Contentlayer (tags, categories, RSS)",
				"Unified marketing + app in one Next.js project",
				"Landing page optimised for conversions",
			},
			Includes: []string{
				"package.json with all dependencies",
				"next.config.ts with Contentlayer",
				"app/layout.tsx, app/page.tsx",
				"app/blog/ MDX listing + detail pages",
				"lib/pocketbase.ts, lib/stripe.ts",
				"content/posts/ with sample MDX post",
				".env.local.example",
			},
			EstimatedSetupTime: "2-3 minutes",
		},
		{
			Name:        "api-first",
			Description: "API-only backend: Hono.js, Postgres, Drizzle, OpenAPI, JWT, Docker",
			Stack:       "Hono.js, TypeScript, Postgres, Drizzle ORM, Zod, Docker",
			Features: []string{
				"Hono.js with type-safe request validation via Zod",
				"OpenAPI 3.1 spec auto-generated from route definitions",
				"JWT authentication middleware",
				"Postgres + Drizzle ORM with migrations",
				"Docker + docker-compose for local dev",
				"Health-check and metrics endpoints",
			},
			Includes: []string{
				"package.json, tsconfig.json",
				"src/index.ts (Hono app entry)",
				"src/routes/auth.ts, src/routes/users.ts",
				"src/db/schema.ts, src/db/index.ts",
				"src/middleware/auth.ts",
				"Dockerfile, docker-compose.yml",
				"openapi.yaml stub",
				".env.example",
			},
			EstimatedSetupTime: "2-3 minutes",
		},
		{
			Name:        "content-site",
			Description: "Blog / magazine: Astro static site with Keystatic CMS, SEO, and newsletter signup",
			Stack:       "Astro 4, Keystatic CMS, Tailwind CSS, TypeScript",
			Features: []string{
				"Keystatic CMS with live preview",
				"Blog with tags, categories, and paginated listing",
				"RSS feed auto-generated",
				"Newsletter signup form (adapts to any provider)",
				"Full SEO: sitemap.xml, robots.txt, JSON-LD structured data",
				"Lighthouse 100 performance baseline",
			},
			Includes: []string{
				"package.json, astro.config.mjs",
				"keystatic.config.ts",
				"src/pages/index.astro, src/pages/blog/",
				"src/content/blog/ with sample post",
				"src/components/ (Header, Footer, PostCard, NewsletterForm)",
				"src/layouts/BaseLayout.astro, BlogPostLayout.astro",
				"public/robots.txt",
			},
			EstimatedSetupTime: "2-3 minutes",
		},
	}, nil
}

// Use applies the named template, creating a new project at <workDir>/<projectName>.
// Returns a human-readable setup summary.
func (tm *TemplateManager) Use(name, projectName string) (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	files, err := tm.generateTemplate(name, projectName)
	if err != nil {
		return "", fmt.Errorf("template %q: %w", name, err)
	}

	projectDir := filepath.Join(tm.workDir, projectName)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return "", fmt.Errorf("create project dir: %w", err)
	}

	if err := writeTemplateFiles(projectDir, files); err != nil {
		return "", fmt.Errorf("write files: %w", err)
	}

	// Install dependencies
	if err := runInDir(projectDir, "npm", "install", "--silent"); err != nil {
		return "", fmt.Errorf("npm install: %w", err)
	}

	if err := initGitRepo(projectDir); err != nil {
		// Non-fatal — just mention it in the summary
		return buildSummary(name, projectName, projectDir, files, "git init skipped: "+err.Error()), nil
	}

	return buildSummary(name, projectName, projectDir, files, ""), nil
}

// ─── Internal Helpers ─────────────────────────────────────────────────────────

func (tm *TemplateManager) generateTemplate(name, projectName string) ([]TemplateFile, error) {
	switch name {
	case "saas-complete":
		return saasCompleteTemplate(projectName), nil
	case "indie-hacker":
		return indieHackerTemplate(projectName), nil
	case "api-first":
		return apiFirstTemplate(projectName), nil
	case "content-site":
		return contentSiteTemplate(projectName), nil
	default:
		return nil, fmt.Errorf("unknown template %q — run 'yaver templates list' to see available templates", name)
	}
}

func writeTemplateFiles(dir string, files []TemplateFile) error {
	for _, f := range files {
		dst := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("mkdir for %s: %w", f.Path, err)
		}
		if err := os.WriteFile(dst, []byte(f.Content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
	}
	return nil
}

func initGitRepo(dir string) error {
	if err := runInDir(dir, "git", "init"); err != nil {
		return err
	}
	if err := runInDir(dir, "git", "add", "-A"); err != nil {
		return err
	}
	return runInDir(dir, "git", "commit", "-m", "chore: initial project scaffold from yaver template")
}

func runInDir(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, string(out))
	}
	return nil
}

func buildSummary(templateName, projectName, projectDir string, files []TemplateFile, warn string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Project %q created from template %q\n", projectName, templateName))
	sb.WriteString(fmt.Sprintf("Location: %s\n", projectDir))
	sb.WriteString(fmt.Sprintf("Files generated: %d\n", len(files)))
	if warn != "" {
		sb.WriteString(fmt.Sprintf("Warning: %s\n", warn))
	}
	sb.WriteString("\nNext steps:\n")
	switch templateName {
	case "saas-complete":
		sb.WriteString("  1. Copy .env.local.example to .env.local and fill in values\n")
		sb.WriteString("  2. Start local services: yaver services start\n")
		sb.WriteString("  3. Run migrations: npm run db:push\n")
		sb.WriteString("  4. Start dev server: npm run dev\n")
	case "indie-hacker":
		sb.WriteString("  1. Copy .env.local.example to .env.local and fill in Stripe keys\n")
		sb.WriteString("  2. Download PocketBase binary: https://pocketbase.io/docs/\n")
		sb.WriteString("  3. Start dev server: npm run dev\n")
	case "api-first":
		sb.WriteString("  1. Copy .env.example to .env and fill in values\n")
		sb.WriteString("  2. Start Postgres: docker compose up -d postgres\n")
		sb.WriteString("  3. Run migrations: npm run db:push\n")
		sb.WriteString("  4. Start dev server: npm run dev\n")
	case "content-site":
		sb.WriteString("  1. Start dev server: npm run dev\n")
		sb.WriteString("  2. Open Keystatic admin: http://localhost:4321/keystatic\n")
		sb.WriteString("  3. Build for production: npm run build\n")
	}
	return sb.String()
}

// ─── saas-complete ────────────────────────────────────────────────────────────

func saasCompleteTemplate(projectName string) []TemplateFile {
	return []TemplateFile{
		{
			Path: "package.json",
			Content: fmt.Sprintf(`{
  "name": "%s",
  "version": "0.1.0",
  "private": true,
  "scripts": {
    "dev": "next dev",
    "build": "next build",
    "start": "next start",
    "lint": "next lint",
    "db:generate": "drizzle-kit generate",
    "db:push": "drizzle-kit push",
    "db:studio": "drizzle-kit studio"
  },
  "dependencies": {
    "better-auth": "^1.2.7",
    "drizzle-orm": "^0.43.1",
    "next": "^15.3.1",
    "react": "^19.1.0",
    "react-dom": "^19.1.0",
    "stripe": "^17.7.0",
    "@neondatabase/serverless": "^0.10.4",
    "lucide-react": "^0.511.0",
    "class-variance-authority": "^0.7.1",
    "clsx": "^2.1.1",
    "tailwind-merge": "^3.3.0"
  },
  "devDependencies": {
    "@types/node": "^22.15.3",
    "@types/react": "^19.1.3",
    "@types/react-dom": "^19.1.3",
    "drizzle-kit": "^0.31.1",
    "typescript": "^5.8.3",
    "tailwindcss": "^4.1.5",
    "@tailwindcss/postcss": "^4.1.5",
    "postcss": "^8.5.3",
    "eslint": "^9.26.0",
    "eslint-config-next": "^15.3.1"
  }
}
`, projectName),
		},
		{
			Path: "tsconfig.json",
			Content: `{
  "compilerOptions": {
    "target": "ES2017",
    "lib": ["dom", "dom.iterable", "esnext"],
    "allowJs": true,
    "skipLibCheck": true,
    "strict": true,
    "noEmit": true,
    "esModuleInterop": true,
    "module": "esnext",
    "moduleResolution": "bundler",
    "resolveJsonModule": true,
    "isolatedModules": true,
    "jsx": "preserve",
    "incremental": true,
    "plugins": [{ "name": "next" }],
    "paths": { "@/*": ["./src/*"] }
  },
  "include": ["next-env.d.ts", "**/*.ts", "**/*.tsx", ".next/types/**/*.ts"],
  "exclude": ["node_modules"]
}
`,
		},
		{
			Path: "next.config.ts",
			Content: `import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  experimental: { typedRoutes: true },
};

export default nextConfig;
`,
		},
		{
			Path: "drizzle.config.ts",
			Content: `import { defineConfig } from "drizzle-kit";

export default defineConfig({
  schema: "./src/lib/db/schema.ts",
  out: "./drizzle",
  dialect: "postgresql",
  dbCredentials: {
    url: process.env.DATABASE_URL!,
  },
});
`,
		},
		{
			Path: "tailwind.config.ts",
			Content: `import type { Config } from "tailwindcss";

const config: Config = {
  darkMode: "class",
  content: ["./src/**/*.{ts,tsx}"],
};

export default config;
`,
		},
		{
			Path: ".env.local.example",
			Content: `# Database (Postgres)
DATABASE_URL=postgresql://postgres:postgres@localhost:5432/saas

# Better Auth
BETTER_AUTH_SECRET=change-me-to-a-long-random-string
BETTER_AUTH_URL=http://localhost:3000

# OAuth — Google
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=

# OAuth — GitHub
GITHUB_CLIENT_ID=
GITHUB_CLIENT_SECRET=

# Stripe
STRIPE_SECRET_KEY=sk_test_...
STRIPE_PUBLISHABLE_KEY=pk_test_...
STRIPE_WEBHOOK_SECRET=whsec_...
STRIPE_PRICE_ID_PRO=price_...

# App
NEXT_PUBLIC_APP_URL=http://localhost:3000
`,
		},
		{
			Path: ".yaver/services.yaml",
			Content: `services:
  postgres:
    image: postgres:16-alpine
    ports: ["5432:5432"]
    environment:
      POSTGRES_PASSWORD: postgres
      POSTGRES_DB: saas
    volumes: [postgres_data:/var/lib/postgresql/data]

  redis:
    image: redis:7-alpine
    ports: ["6379:6379"]

  mailpit:
    image: axllent/mailpit:latest
    ports:
      - "1025:1025"   # SMTP
      - "8025:8025"   # Web UI

volumes:
  postgres_data:
`,
		},
		{
			Path: "src/lib/db/schema.ts",
			Content: `import { pgTable, text, timestamp, boolean, integer } from "drizzle-orm/pg-core";

export const users = pgTable("users", {
  id: text("id").primaryKey(),
  name: text("name").notNull(),
  email: text("email").notNull().unique(),
  emailVerified: boolean("email_verified").notNull().default(false),
  image: text("image"),
  createdAt: timestamp("created_at").notNull().defaultNow(),
  updatedAt: timestamp("updated_at").notNull().defaultNow(),
  stripeCustomerId: text("stripe_customer_id"),
  stripePriceId: text("stripe_price_id"),
  stripeSubscriptionId: text("stripe_subscription_id"),
  subscriptionStatus: text("subscription_status"),
});

export const sessions = pgTable("sessions", {
  id: text("id").primaryKey(),
  expiresAt: timestamp("expires_at").notNull(),
  token: text("token").notNull().unique(),
  createdAt: timestamp("created_at").notNull().defaultNow(),
  updatedAt: timestamp("updated_at").notNull().defaultNow(),
  ipAddress: text("ip_address"),
  userAgent: text("user_agent"),
  userId: text("user_id")
    .notNull()
    .references(() => users.id, { onDelete: "cascade" }),
});

export const accounts = pgTable("accounts", {
  id: text("id").primaryKey(),
  accountId: text("account_id").notNull(),
  providerId: text("provider_id").notNull(),
  userId: text("user_id")
    .notNull()
    .references(() => users.id, { onDelete: "cascade" }),
  accessToken: text("access_token"),
  refreshToken: text("refresh_token"),
  idToken: text("id_token"),
  accessTokenExpiresAt: timestamp("access_token_expires_at"),
  refreshTokenExpiresAt: timestamp("refresh_token_expires_at"),
  scope: text("scope"),
  password: text("password"),
  createdAt: timestamp("created_at").notNull().defaultNow(),
  updatedAt: timestamp("updated_at").notNull().defaultNow(),
});

export const verifications = pgTable("verifications", {
  id: text("id").primaryKey(),
  identifier: text("identifier").notNull(),
  value: text("value").notNull(),
  expiresAt: timestamp("expires_at").notNull(),
  createdAt: timestamp("created_at").defaultNow(),
  updatedAt: timestamp("updated_at").defaultNow(),
});
`,
		},
		{
			Path: "src/lib/db/index.ts",
			Content: `import { drizzle } from "drizzle-orm/neon-serverless";
import { neon } from "@neondatabase/serverless";
import * as schema from "./schema";

const sql = neon(process.env.DATABASE_URL!);
export const db = drizzle(sql, { schema });
`,
		},
		{
			Path: "src/lib/auth.ts",
			Content: `import { betterAuth } from "better-auth";
import { drizzleAdapter } from "better-auth/adapters/drizzle";
import { db } from "@/lib/db";
import * as schema from "@/lib/db/schema";

export const auth = betterAuth({
  database: drizzleAdapter(db, {
    provider: "pg",
    schema: {
      user: schema.users,
      session: schema.sessions,
      account: schema.accounts,
      verification: schema.verifications,
    },
  }),
  socialProviders: {
    google: {
      clientId: process.env.GOOGLE_CLIENT_ID!,
      clientSecret: process.env.GOOGLE_CLIENT_SECRET!,
    },
    github: {
      clientId: process.env.GITHUB_CLIENT_ID!,
      clientSecret: process.env.GITHUB_CLIENT_SECRET!,
    },
  },
  session: {
    expiresIn: 60 * 60 * 24 * 30, // 30 days
    updateAge: 60 * 60 * 24,       // refresh if older than 1 day
  },
});

` + "export type Session = typeof auth.$Infer.Session;\n" +
				"export type User = typeof auth.$Infer.Session.user;\n",
		},
		{
			Path: "src/lib/stripe.ts",
			Content: `import Stripe from "stripe";

export const stripe = new Stripe(process.env.STRIPE_SECRET_KEY!, {
  apiVersion: "2025-04-30.basil",
  typescript: true,
});

export async function createCheckoutSession(userId: string, email: string) {
  return stripe.checkout.sessions.create({
    mode: "subscription",
    line_items: [{ price: process.env.STRIPE_PRICE_ID_PRO!, quantity: 1 }],
    success_url: ` + "`${process.env.NEXT_PUBLIC_APP_URL}/dashboard?upgraded=1`" + `,
    cancel_url: ` + "`${process.env.NEXT_PUBLIC_APP_URL}/pricing`" + `,
    customer_email: email,
    metadata: { userId },
    subscription_data: { metadata: { userId } },
  });
}

export async function createCustomerPortalSession(customerId: string) {
  return stripe.billingPortal.sessions.create({
    customer: customerId,
    return_url: ` + "`${process.env.NEXT_PUBLIC_APP_URL}/dashboard`" + `,
  });
}
`,
		},
		{
			Path: "src/app/layout.tsx",
			Content: `import type { Metadata } from "next";
import { Geist } from "next/font/google";
import "./globals.css";

const geist = Geist({ subsets: ["latin"] });

export const metadata: Metadata = {
  title: { default: "SaaS App", template: "%s | SaaS App" },
  description: "Your product description here.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body className={geist.className}>{children}</body>
    </html>
  );
}
`,
		},
		{
			Path: "src/app/globals.css",
			Content: `@import "tailwindcss";
`,
		},
		{
			Path: "src/app/page.tsx",
			Content: `import Link from "next/link";
import { ArrowRight, CheckCircle, Zap, Shield, BarChart3 } from "lucide-react";

const features = [
  { icon: Zap, title: "Blazing Fast", desc: "Built on Next.js 15 with React 19 and Turbopack." },
  { icon: Shield, title: "Secure by Default", desc: "Better Auth handles OAuth, sessions, and CSRF for you." },
  { icon: BarChart3, title: "Analytics Built-in", desc: "Track everything that matters from day one." },
];

const plans = [
  { name: "Free", price: "$0", features: ["5 projects", "1 user", "Community support"], cta: "Get started", href: "/sign-up", highlight: false },
  { name: "Pro", price: "$19/mo", features: ["Unlimited projects", "5 users", "Priority support", "Advanced analytics"], cta: "Start free trial", href: "/sign-up?plan=pro", highlight: true },
];

export default function HomePage() {
  return (
    <main className="min-h-screen bg-white">
      {/* Nav */}
      <nav className="mx-auto flex max-w-6xl items-center justify-between px-6 py-4">
        <span className="text-xl font-bold">YourSaaS</span>
        <div className="flex items-center gap-4">
          <Link href="/sign-in" className="text-sm text-gray-600 hover:text-gray-900">Sign in</Link>
          <Link href="/sign-up" className="rounded-lg bg-black px-4 py-2 text-sm font-medium text-white hover:bg-gray-800">
            Get started
          </Link>
        </div>
      </nav>

      {/* Hero */}
      <section className="mx-auto max-w-4xl px-6 py-24 text-center">
        <h1 className="text-5xl font-extrabold tracking-tight text-gray-900 sm:text-6xl">
          Ship your SaaS<br />in days, not months
        </h1>
        <p className="mt-6 text-xl text-gray-500">
          Auth, billing, database, and a beautiful UI — all wired up and ready to customize.
        </p>
        <div className="mt-10 flex justify-center gap-4">
          <Link href="/sign-up" className="flex items-center gap-2 rounded-lg bg-black px-6 py-3 font-semibold text-white hover:bg-gray-800">
            Start building <ArrowRight className="h-4 w-4" />
          </Link>
          <Link href="https://github.com" className="rounded-lg border border-gray-300 px-6 py-3 font-semibold text-gray-700 hover:border-gray-400">
            View on GitHub
          </Link>
        </div>
      </section>

      {/* Features */}
      <section className="mx-auto max-w-6xl px-6 py-16">
        <h2 className="mb-12 text-center text-3xl font-bold">Everything you need</h2>
        <div className="grid gap-8 md:grid-cols-3">
          {features.map((f) => (
            <div key={f.title} className="rounded-2xl border border-gray-100 p-8 shadow-sm">
              <f.icon className="mb-4 h-8 w-8 text-black" />
              <h3 className="mb-2 text-lg font-semibold">{f.title}</h3>
              <p className="text-gray-500">{f.desc}</p>
            </div>
          ))}
        </div>
      </section>

      {/* Pricing */}
      <section className="mx-auto max-w-4xl px-6 py-16">
        <h2 className="mb-12 text-center text-3xl font-bold">Simple pricing</h2>
        <div className="grid gap-8 md:grid-cols-2">
          {plans.map((p) => (
            <div key={p.name} className={
              "rounded-2xl p-8 " + (p.highlight ? "bg-black text-white" : "border border-gray-200")
            }>
              <p className="text-lg font-semibold">{p.name}</p>
              <p className={" text-4xl font-extrabold mt-2 " + (p.highlight ? "" : "text-gray-900")}>{p.price}</p>
              <ul className="mt-6 space-y-3">
                {p.features.map((f) => (
                  <li key={f} className="flex items-center gap-2 text-sm">
                    <CheckCircle className={"h-4 w-4 " + (p.highlight ? "text-green-400" : "text-green-600")} />
                    {f}
                  </li>
                ))}
              </ul>
              <Link href={p.href} className={
                "mt-8 block rounded-lg px-4 py-3 text-center font-semibold transition " +
                (p.highlight ? "bg-white text-black hover:bg-gray-100" : "bg-black text-white hover:bg-gray-800")
              }>
                {p.cta}
              </Link>
            </div>
          ))}
        </div>
      </section>

      <footer className="border-t border-gray-100 py-8 text-center text-sm text-gray-400">
        © {new Date().getFullYear()} YourSaaS. All rights reserved.
      </footer>
    </main>
  );
}
`,
		},
		{
			Path: "src/app/dashboard/page.tsx",
			Content: `import { auth } from "@/lib/auth";
import { headers } from "next/headers";
import { redirect } from "next/navigation";
import Link from "next/link";
import { LayoutDashboard, Settings, CreditCard, LogOut } from "lucide-react";

export default async function DashboardPage() {
  const session = await auth.api.getSession({ headers: await headers() });
  if (!session) redirect("/sign-in");

  const navItems = [
    { href: "/dashboard", icon: LayoutDashboard, label: "Overview" },
    { href: "/dashboard/billing", icon: CreditCard, label: "Billing" },
    { href: "/dashboard/settings", icon: Settings, label: "Settings" },
  ];

  return (
    <div className="flex min-h-screen bg-gray-50">
      {/* Sidebar */}
      <aside className="w-64 border-r border-gray-200 bg-white px-4 py-6">
        <p className="mb-8 px-2 text-xl font-bold">YourSaaS</p>
        <nav className="space-y-1">
          {navItems.map((item) => (
            <Link
              key={item.href}
              href={item.href}
              className="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-100"
            >
              <item.icon className="h-4 w-4" />
              {item.label}
            </Link>
          ))}
        </nav>
        <div className="absolute bottom-6 left-0 w-64 px-4">
          <form action="/api/auth/sign-out" method="POST">
            <button className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-gray-500 hover:bg-gray-100">
              <LogOut className="h-4 w-4" />
              Sign out
            </button>
          </form>
        </div>
      </aside>

      {/* Main content */}
      <main className="flex-1 p-8">
        <div className="mb-8">
          <h1 className="text-2xl font-bold text-gray-900">Welcome back, {session.user.name}</h1>
          <p className="text-gray-500">{session.user.email}</p>
        </div>
        <div className="grid gap-6 md:grid-cols-3">
          {[
            { label: "Total revenue", value: "$0.00" },
            { label: "Active users", value: "0" },
            { label: "Subscription", value: "Free" },
          ].map((stat) => (
            <div key={stat.label} className="rounded-2xl border border-gray-200 bg-white p-6">
              <p className="text-sm text-gray-500">{stat.label}</p>
              <p className="mt-1 text-2xl font-bold text-gray-900">{stat.value}</p>
            </div>
          ))}
        </div>
      </main>
    </div>
  );
}
`,
		},
		{
			Path: "src/app/api/auth/[...all]/route.ts",
			Content: `import { auth } from "@/lib/auth";
import { toNextJsHandler } from "better-auth/next-js";

export const { POST, GET } = toNextJsHandler(auth);
`,
		},
		{
			Path: "src/app/api/stripe/webhook/route.ts",
			Content: `import { NextRequest, NextResponse } from "next/server";
import { stripe } from "@/lib/stripe";
import { db } from "@/lib/db";
import { users } from "@/lib/db/schema";
import { eq } from "drizzle-orm";
import type Stripe from "stripe";

export async function POST(req: NextRequest) {
  const body = await req.text();
  const sig = req.headers.get("stripe-signature");
  if (!sig) return NextResponse.json({ error: "Missing signature" }, { status: 400 });

  let event: Stripe.Event;
  try {
    event = stripe.webhooks.constructEvent(body, sig, process.env.STRIPE_WEBHOOK_SECRET!);
  } catch (err) {
    return NextResponse.json({ error: "Webhook signature verification failed" }, { status: 400 });
  }

  switch (event.type) {
    case "customer.subscription.created":
    case "customer.subscription.updated": {
      const sub = event.data.object as Stripe.Subscription;
      const userId = sub.metadata.userId;
      if (userId) {
        await db.update(users).set({
          stripeSubscriptionId: sub.id,
          stripePriceId: sub.items.data[0]?.price.id ?? null,
          subscriptionStatus: sub.status,
        }).where(eq(users.id, userId));
      }
      break;
    }
    case "customer.subscription.deleted": {
      const sub = event.data.object as Stripe.Subscription;
      const userId = sub.metadata.userId;
      if (userId) {
        await db.update(users).set({
          stripeSubscriptionId: null,
          stripePriceId: null,
          subscriptionStatus: "canceled",
        }).where(eq(users.id, userId));
      }
      break;
    }
  }

  return NextResponse.json({ received: true });
}
`,
		},
		{
			Path: "src/components/ui/button.tsx",
			Content: `import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const buttonVariants = cva(
  "inline-flex items-center justify-center rounded-lg text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 disabled:pointer-events-none disabled:opacity-50",
  {
    variants: {
      variant: {
        default: "bg-black text-white hover:bg-gray-800",
        outline: "border border-gray-300 bg-white text-gray-700 hover:bg-gray-50",
        ghost: "hover:bg-gray-100 text-gray-700",
        destructive: "bg-red-600 text-white hover:bg-red-700",
      },
      size: {
        default: "h-10 px-4 py-2",
        sm: "h-8 px-3",
        lg: "h-12 px-6",
      },
    },
    defaultVariants: { variant: "default", size: "default" },
  }
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, ...props }, ref) => (
    <button ref={ref} className={cn(buttonVariants({ variant, size }), className)} {...props} />
  )
);
Button.displayName = "Button";
`,
		},
		{
			Path: "src/components/ui/card.tsx",
			Content: `import * as React from "react";
import { cn } from "@/lib/utils";

export const Card = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn("rounded-2xl border border-gray-200 bg-white shadow-sm", className)} {...props} />
  )
);
Card.displayName = "Card";

export const CardHeader = ({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) => (
  <div className={cn("p-6 pb-0", className)} {...props} />
);
export const CardTitle = ({ className, ...props }: React.HTMLAttributes<HTMLHeadingElement>) => (
  <h3 className={cn("text-lg font-semibold", className)} {...props} />
);
export const CardContent = ({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) => (
  <div className={cn("p-6", className)} {...props} />
);
`,
		},
		{
			Path: "src/lib/utils.ts",
			Content: `import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
`,
		},
	}
}

// ─── indie-hacker ─────────────────────────────────────────────────────────────

func indieHackerTemplate(projectName string) []TemplateFile {
	return []TemplateFile{
		{
			Path: "package.json",
			Content: fmt.Sprintf(`{
  "name": "%s",
  "version": "0.1.0",
  "private": true,
  "scripts": {
    "dev": "next dev",
    "build": "next build",
    "start": "next start",
    "lint": "next lint",
    "pb:serve": "pocketbase serve --http=127.0.0.1:8090"
  },
  "dependencies": {
    "next": "^15.3.1",
    "react": "^19.1.0",
    "react-dom": "^19.1.0",
    "stripe": "^17.7.0",
    "pocketbase": "^0.25.2",
    "contentlayer2": "^0.5.3",
    "next-contentlayer2": "^0.5.3",
    "rehype-highlight": "^7.0.2",
    "remark-gfm": "^4.0.1",
    "date-fns": "^4.1.0",
    "lucide-react": "^0.511.0"
  },
  "devDependencies": {
    "@types/node": "^22.15.3",
    "@types/react": "^19.1.3",
    "@types/react-dom": "^19.1.3",
    "typescript": "^5.8.3",
    "tailwindcss": "^4.1.5",
    "@tailwindcss/postcss": "^4.1.5",
    "postcss": "^8.5.3",
    "eslint": "^9.26.0",
    "eslint-config-next": "^15.3.1"
  }
}
`, projectName),
		},
		{
			Path: "next.config.ts",
			Content: `import type { NextConfig } from "next";
import { withContentlayer } from "next-contentlayer2";

const nextConfig: NextConfig = {
  pageExtensions: ["ts", "tsx", "mdx"],
};

export default withContentlayer(nextConfig);
`,
		},
		{
			Path: "contentlayer.config.ts",
			Content: `import { defineDocumentType, makeSource } from "contentlayer2/source-files";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";

export const Post = defineDocumentType(() => ({
  name: "Post",
  filePathPattern: "posts/**/*.mdx",
  contentType: "mdx",
  fields: {
    title: { type: "string", required: true },
    description: { type: "string", required: true },
    publishedAt: { type: "date", required: true },
    tags: { type: "list", of: { type: "string" }, default: [] },
    category: { type: "string", default: "general" },
    featured: { type: "boolean", default: false },
  },
  computedFields: {
    slug: {
      type: "string",
      resolve: (post) => post._raw.flattenedPath.replace("posts/", ""),
    },
    url: {
      type: "string",
      resolve: (post) => "/blog/" + post._raw.flattenedPath.replace("posts/", ""),
    },
  },
}));

export default makeSource({
  contentDirPath: "content",
  documentTypes: [Post],
  mdx: { remarkPlugins: [remarkGfm], rehypePlugins: [rehypeHighlight] },
});
`,
		},
		{
			Path: ".env.local.example",
			Content: `# PocketBase (download binary from https://pocketbase.io)
NEXT_PUBLIC_POCKETBASE_URL=http://127.0.0.1:8090

# Stripe
STRIPE_SECRET_KEY=sk_test_...
NEXT_PUBLIC_STRIPE_PUBLISHABLE_KEY=pk_test_...
STRIPE_WEBHOOK_SECRET=whsec_...
STRIPE_PRICE_ID=price_...

NEXT_PUBLIC_APP_URL=http://localhost:3000
`,
		},
		{
			Path: "lib/pocketbase.ts",
			Content: `import PocketBase from "pocketbase";

// Server-side: a fresh client per request (no shared state)
export function createServerPB() {
  return new PocketBase(process.env.NEXT_PUBLIC_POCKETBASE_URL);
}

// Client-side singleton
let _pb: PocketBase | null = null;
export function getPB() {
  if (!_pb) _pb = new PocketBase(process.env.NEXT_PUBLIC_POCKETBASE_URL);
  return _pb;
}
`,
		},
		{
			Path: "lib/stripe.ts",
			Content: `import Stripe from "stripe";

export const stripe = new Stripe(process.env.STRIPE_SECRET_KEY!, {
  apiVersion: "2025-04-30.basil",
  typescript: true,
});

export async function createCheckoutSession(email: string, userId: string) {
  return stripe.checkout.sessions.create({
    mode: "subscription",
    line_items: [{ price: process.env.STRIPE_PRICE_ID!, quantity: 1 }],
    success_url: ` + "`${process.env.NEXT_PUBLIC_APP_URL}/dashboard?upgraded=1`" + `,
    cancel_url: ` + "`${process.env.NEXT_PUBLIC_APP_URL}/pricing`" + `,
    customer_email: email,
    metadata: { userId },
  });
}
`,
		},
		{
			Path: "app/page.tsx",
			Content: `import Link from "next/link";
import { allPosts } from "contentlayer/generated";
import { compareDesc, format } from "date-fns";

export default function HomePage() {
  const featured = allPosts
    .filter((p) => p.featured)
    .sort((a, b) => compareDesc(new Date(a.publishedAt), new Date(b.publishedAt)))
    .slice(0, 3);

  return (
    <main className="mx-auto max-w-5xl px-6 py-16">
      {/* Hero */}
      <section className="py-24 text-center">
        <h1 className="text-5xl font-extrabold tracking-tight">Your Product Tagline</h1>
        <p className="mt-4 text-xl text-gray-500">One-liner that explains what you do and who it&apos;s for.</p>
        <Link href="/sign-up" className="mt-8 inline-block rounded-lg bg-black px-8 py-4 font-semibold text-white hover:bg-gray-800">
          Get started — it&apos;s free
        </Link>
      </section>

      {/* Blog preview */}
      {featured.length > 0 && (
        <section className="mt-16">
          <h2 className="mb-8 text-2xl font-bold">From the blog</h2>
          <div className="grid gap-8 md:grid-cols-3">
            {featured.map((post) => (
              <Link key={post.slug} href={post.url} className="group rounded-2xl border border-gray-100 p-6 hover:shadow-md transition">
                <p className="text-xs text-gray-400">{format(new Date(post.publishedAt), "MMM d, yyyy")}</p>
                <h3 className="mt-2 font-semibold group-hover:text-blue-600">{post.title}</h3>
                <p className="mt-1 text-sm text-gray-500 line-clamp-2">{post.description}</p>
              </Link>
            ))}
          </div>
          <div className="mt-8 text-center">
            <Link href="/blog" className="text-sm font-medium text-gray-500 hover:text-gray-900">View all posts →</Link>
          </div>
        </section>
      )}
    </main>
  );
}
`,
		},
		{
			Path: "app/blog/page.tsx",
			Content: `import { allPosts } from "contentlayer/generated";
import { compareDesc, format } from "date-fns";
import Link from "next/link";

export const metadata = { title: "Blog" };

export default function BlogPage() {
  const posts = allPosts.sort((a, b) => compareDesc(new Date(a.publishedAt), new Date(b.publishedAt)));
  return (
    <main className="mx-auto max-w-3xl px-6 py-16">
      <h1 className="mb-10 text-4xl font-extrabold">Blog</h1>
      <div className="space-y-8">
        {posts.map((post) => (
          <article key={post.slug}>
            <Link href={post.url} className="group">
              <h2 className="text-xl font-semibold group-hover:text-blue-600">{post.title}</h2>
            </Link>
            <p className="mt-1 text-sm text-gray-400">{format(new Date(post.publishedAt), "MMMM d, yyyy")}</p>
            <p className="mt-2 text-gray-600">{post.description}</p>
          </article>
        ))}
      </div>
    </main>
  );
}
`,
		},
		{
			Path: "content/posts/hello-world.mdx",
			Content: `---
title: "Hello, World"
description: "The very first post on our new blog. Welcome!"
publishedAt: "2025-01-01"
tags: ["announcement"]
category: "general"
featured: true
---

# Hello, World!

Welcome to the blog. This is a sample post written in **MDX** with [Contentlayer](https://contentlayer.dev/).

## What you can do

- Write posts in MDX (Markdown + JSX)
- Add tags and categories
- Embed React components inside your prose

` + "```ts\nconsole.log(\"Hello, World!\");\n```" + `

Stay tuned for more.
`,
		},
	}
}

// ─── api-first ────────────────────────────────────────────────────────────────

func apiFirstTemplate(projectName string) []TemplateFile {
	return []TemplateFile{
		{
			Path: "package.json",
			Content: fmt.Sprintf(`{
  "name": "%s",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "tsx watch src/index.ts",
    "build": "tsc",
    "start": "node dist/index.js",
    "db:generate": "drizzle-kit generate",
    "db:push": "drizzle-kit push",
    "db:studio": "drizzle-kit studio"
  },
  "dependencies": {
    "hono": "^4.7.10",
    "@hono/node-server": "^1.14.3",
    "@hono/zod-openapi": "^0.18.4",
    "drizzle-orm": "^0.43.1",
    "postgres": "^3.4.5",
    "zod": "^3.24.4",
    "jsonwebtoken": "^9.0.2",
    "@scalar/hono-api-reference": "^0.5.168"
  },
  "devDependencies": {
    "@types/node": "^22.15.3",
    "@types/jsonwebtoken": "^9.0.9",
    "drizzle-kit": "^0.31.1",
    "tsx": "^4.19.4",
    "typescript": "^5.8.3"
  }
}
`, projectName),
		},
		{
			Path: "tsconfig.json",
			Content: `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "outDir": "dist",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "resolveJsonModule": true
  },
  "include": ["src"],
  "exclude": ["node_modules", "dist"]
}
`,
		},
		{
			Path: ".env.example",
			Content: `DATABASE_URL=postgresql://postgres:postgres@localhost:5432/api
JWT_SECRET=change-me-to-a-long-random-string
PORT=3000
`,
		},
		{
			Path: "Dockerfile",
			Content: `FROM node:22-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM node:22-alpine AS runner
WORKDIR /app
COPY package*.json ./
RUN npm ci --omit=dev
COPY --from=builder /app/dist ./dist
EXPOSE 3000
CMD ["node", "dist/index.js"]
`,
		},
		{
			Path: "docker-compose.yml",
			Content: fmt.Sprintf(`version: "3.9"
services:
  api:
    build: .
    ports: ["3000:3000"]
    environment:
      DATABASE_URL: postgresql://postgres:postgres@postgres:5432/api
      JWT_SECRET: dev-secret-change-in-production
    depends_on:
      postgres:
        condition: service_healthy

  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_PASSWORD: postgres
      POSTGRES_DB: api
    ports: ["5432:5432"]
    volumes: [%s_pg_data:/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 5

volumes:
  %s_pg_data:
`, projectName, projectName),
		},
		{
			Path: "src/db/schema.ts",
			Content: `import { pgTable, text, timestamp, uuid } from "drizzle-orm/pg-core";

export const users = pgTable("users", {
  id: uuid("id").primaryKey().defaultRandom(),
  email: text("email").notNull().unique(),
  passwordHash: text("password_hash").notNull(),
  name: text("name").notNull(),
  createdAt: timestamp("created_at").notNull().defaultNow(),
  updatedAt: timestamp("updated_at").notNull().defaultNow(),
});

` + "export type User = typeof users.$inferSelect;\n" +
				"export type NewUser = typeof users.$inferInsert;\n",
		},
		{
			Path: "src/db/index.ts",
			Content: `import { drizzle } from "drizzle-orm/postgres-js";
import postgres from "postgres";
import * as schema from "./schema.js";

const client = postgres(process.env.DATABASE_URL!);
export const db = drizzle(client, { schema });
`,
		},
		{
			Path: "src/middleware/auth.ts",
			Content: `import { createMiddleware } from "hono/factory";
import jwt from "jsonwebtoken";

export interface JWTPayload {
  sub: string;
  email: string;
  iat: number;
  exp: number;
}

export const jwtAuth = createMiddleware<{
  Variables: { user: JWTPayload };
}>(async (c, next) => {
  const auth = c.req.header("Authorization");
  if (!auth?.startsWith("Bearer ")) {
    return c.json({ error: "Unauthorized" }, 401);
  }
  const token = auth.slice(7);
  try {
    const payload = jwt.verify(token, process.env.JWT_SECRET!) as JWTPayload;
    c.set("user", payload);
    await next();
  } catch {
    return c.json({ error: "Invalid or expired token" }, 401);
  }
});

export function signToken(payload: Omit<JWTPayload, "iat" | "exp">) {
  return jwt.sign(payload, process.env.JWT_SECRET!, { expiresIn: "7d" });
}
`,
		},
		{
			Path: "src/routes/auth.ts",
			Content: `import { Hono } from "hono";
import { zValidator } from "@hono/zod-validator";
import { z } from "zod";
import { db } from "../db/index.js";
import { users } from "../db/schema.js";
import { eq } from "drizzle-orm";
import { signToken } from "../middleware/auth.js";
import { createHash } from "node:crypto";

const router = new Hono();

function hashPassword(password: string) {
  return createHash("sha256").update(password + process.env.JWT_SECRET).digest("hex");
}

const signUpSchema = z.object({
  email: z.string().email(),
  password: z.string().min(8),
  name: z.string().min(1),
});

const signInSchema = z.object({
  email: z.string().email(),
  password: z.string(),
});

router.post("/sign-up", zValidator("json", signUpSchema), async (c) => {
  const { email, password, name } = c.req.valid("json");
  const existing = await db.query.users.findFirst({ where: eq(users.email, email) });
  if (existing) return c.json({ error: "Email already registered" }, 409);

  const [user] = await db.insert(users).values({
    email,
    name,
    passwordHash: hashPassword(password),
  }).returning({ id: users.id, email: users.email });

  const token = signToken({ sub: user.id, email: user.email });
  return c.json({ token, user: { id: user.id, email: user.email, name } }, 201);
});

router.post("/sign-in", zValidator("json", signInSchema), async (c) => {
  const { email, password } = c.req.valid("json");
  const user = await db.query.users.findFirst({ where: eq(users.email, email) });
  if (!user || user.passwordHash !== hashPassword(password)) {
    return c.json({ error: "Invalid credentials" }, 401);
  }
  const token = signToken({ sub: user.id, email: user.email });
  return c.json({ token, user: { id: user.id, email: user.email, name: user.name } });
});

export default router;
`,
		},
		{
			Path: "src/routes/users.ts",
			Content: `import { Hono } from "hono";
import { jwtAuth } from "../middleware/auth.js";
import { db } from "../db/index.js";
import { users } from "../db/schema.js";
import { eq } from "drizzle-orm";

const router = new Hono();

router.get("/me", jwtAuth, async (c) => {
  const { sub } = c.get("user");
  const user = await db.query.users.findFirst({
    where: eq(users.id, sub),
    columns: { id: true, email: true, name: true, createdAt: true },
  });
  if (!user) return c.json({ error: "User not found" }, 404);
  return c.json({ user });
});

export default router;
`,
		},
		{
			Path: "src/index.ts",
			Content: fmt.Sprintf(`import { Hono } from "hono";
import { serve } from "@hono/node-server";
import { logger } from "hono/logger";
import { cors } from "hono/cors";
import { prettyJSON } from "hono/pretty-json";
import { apiReference } from "@scalar/hono-api-reference";
import authRoutes from "./routes/auth.js";
import userRoutes from "./routes/users.js";

const app = new Hono();

app.use("*", logger());
app.use("*", cors());
app.use("*", prettyJSON());

// Health check
app.get("/health", (c) => c.json({ status: "ok", service: "%s" }));

// API routes
app.route("/auth", authRoutes);
app.route("/users", userRoutes);

// OpenAPI docs (Scalar)
app.get("/openapi.yaml", async (c) => {
  const { readFile } = await import("node:fs/promises");
  const spec = await readFile(new URL("../openapi.yaml", import.meta.url), "utf-8").catch(() => "{}");
  return c.text(spec, 200, { "Content-Type": "application/yaml" });
});
app.get("/docs", apiReference({ spec: { url: "/openapi.yaml" } }));

const port = Number(process.env.PORT ?? 3000);
serve({ fetch: app.fetch, port }, () =>
  console.log("Server running on http://localhost:" + port + "  — docs at /docs")
);

export default app;
`, projectName),
		},
		{
			Path: "openapi.yaml",
			Content: fmt.Sprintf(`openapi: "3.1.0"
info:
  title: %s API
  version: "1.0.0"
  description: Auto-generated OpenAPI specification

servers:
  - url: http://localhost:3000
    description: Local dev server

components:
  securitySchemes:
    BearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT

paths:
  /health:
    get:
      summary: Health check
      responses:
        "200":
          description: Service is healthy

  /auth/sign-up:
    post:
      summary: Register a new user
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [email, password, name]
              properties:
                email: { type: string, format: email }
                password: { type: string, minLength: 8 }
                name: { type: string }
      responses:
        "201":
          description: User created, returns JWT token

  /auth/sign-in:
    post:
      summary: Sign in with email and password
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [email, password]
              properties:
                email: { type: string, format: email }
                password: { type: string }
      responses:
        "200":
          description: Authenticated, returns JWT token
        "401":
          description: Invalid credentials

  /users/me:
    get:
      summary: Get current authenticated user
      security:
        - BearerAuth: []
      responses:
        "200":
          description: Current user profile
        "401":
          description: Unauthorized
`, projectName),
		},
		{
			Path: "drizzle.config.ts",
			Content: `import { defineConfig } from "drizzle-kit";

export default defineConfig({
  schema: "./src/db/schema.ts",
  out: "./drizzle",
  dialect: "postgresql",
  dbCredentials: { url: process.env.DATABASE_URL! },
});
`,
		},
	}
}

// ─── content-site ─────────────────────────────────────────────────────────────

func contentSiteTemplate(projectName string) []TemplateFile {
	return []TemplateFile{
		{
			Path: "package.json",
			Content: fmt.Sprintf(`{
  "name": "%s",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "astro dev",
    "build": "astro build",
    "preview": "astro preview",
    "sync": "astro sync"
  },
  "dependencies": {
    "astro": "^5.7.13",
    "@astrojs/tailwind": "^5.1.4",
    "@astrojs/mdx": "^4.2.6",
    "@astrojs/sitemap": "^3.3.0",
    "@astrojs/rss": "^4.0.11",
    "@keystatic/core": "^0.5.41",
    "@keystatic/astro": "^5.0.2",
    "tailwindcss": "^3.4.17",
    "sharp": "^0.34.1"
  },
  "devDependencies": {
    "typescript": "^5.8.3"
  }
}
`, projectName),
		},
		{
			Path: "astro.config.mjs",
			Content: fmt.Sprintf(`import { defineConfig } from "astro/config";
import tailwind from "@astrojs/tailwind";
import mdx from "@astrojs/mdx";
import sitemap from "@astrojs/sitemap";
import keystatic from "@keystatic/astro";

export default defineConfig({
  site: "https://%s.com",
  integrations: [tailwind(), mdx(), sitemap(), keystatic()],
  output: "hybrid",
});
`, projectName),
		},
		{
			Path: "keystatic.config.ts",
			Content: `import { config, fields, collection } from "@keystatic/core";

export default config({
  storage: { kind: "local" },

  collections: {
    posts: collection({
      label: "Blog Posts",
      slugField: "title",
      path: "src/content/blog/*",
      format: { contentField: "content" },
      schema: {
        title: fields.slug({ name: { label: "Title" } }),
        description: fields.text({ label: "Description", multiline: true }),
        publishedAt: fields.date({ label: "Published At" }),
        tags: fields.array(fields.text({ label: "Tag" }), {
          label: "Tags",
          itemLabel: (props) => props.fields.value.value ?? "Tag",
        }),
        category: fields.select({
          label: "Category",
          options: [
            { label: "General", value: "general" },
            { label: "Tutorial", value: "tutorial" },
            { label: "News", value: "news" },
          ],
          defaultValue: "general",
        }),
        featured: fields.checkbox({ label: "Featured", defaultValue: false }),
        content: fields.markdoc({ label: "Content" }),
      },
    }),
  },
});
`,
		},
		{
			Path: "src/content/config.ts",
			Content: `import { defineCollection, z } from "astro:content";

const blog = defineCollection({
  type: "content",
  schema: z.object({
    title: z.string(),
    description: z.string(),
    publishedAt: z.coerce.date(),
    tags: z.array(z.string()).default([]),
    category: z.string().default("general"),
    featured: z.boolean().default(false),
  }),
});

export const collections = { blog };
`,
		},
		{
			Path: "src/content/blog/hello-world.md",
			Content: `---
title: "Hello, World"
description: "Welcome to the blog. This is the very first post."
publishedAt: 2025-01-01
tags: ["announcement"]
category: "general"
featured: true
---

# Hello, World!

Welcome to the blog. Start editing this file in **Keystatic** or directly in your favourite editor.

## Features

- Astro for blazing-fast static output
- Keystatic for a Git-native CMS
- Tailwind CSS for styling
- MDX support for rich embeds
` + "- RSS feed at `/rss.xml`\n- Sitemap at `/sitemap-index.xml`\n",
		},
		{
			Path: "src/layouts/BaseLayout.astro",
			Content: `---
import "../styles/global.css";

export interface Props {
  title: string;
  description?: string;
  image?: string;
}

const { title, description = "A great content site built with Astro.", image } = Astro.props;
const canonicalURL = new URL(Astro.url.pathname, Astro.site);
---

<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <link rel="canonical" href={canonicalURL} />
    <title>{title}</title>
    <meta name="description" content={description} />
    {image && <meta property="og:image" content={image} />}
    <meta property="og:title" content={title} />
    <meta property="og:description" content={description} />
    <meta property="og:url" content={canonicalURL} />
    <meta name="twitter:card" content="summary_large_image" />
    <link rel="sitemap" href="/sitemap-index.xml" />
    <link rel="alternate" type="application/rss+xml" href="/rss.xml" title="RSS Feed" />
  </head>
  <body class="bg-white text-gray-900 antialiased">
    <header class="border-b border-gray-100 px-6 py-4">
      <nav class="mx-auto flex max-w-4xl items-center justify-between">
        <a href="/" class="text-xl font-bold">MySite</a>
        <div class="flex gap-6 text-sm">
          <a href="/blog" class="text-gray-600 hover:text-gray-900">Blog</a>
          <a href="/newsletter" class="text-gray-600 hover:text-gray-900">Newsletter</a>
        </div>
      </nav>
    </header>
    <slot />
    <footer class="mt-24 border-t border-gray-100 px-6 py-8 text-center text-sm text-gray-400">
      © {new Date().getFullYear()} MySite. Built with Astro.
    </footer>
  </body>
</html>
`,
		},
		{
			Path: "src/layouts/BlogPostLayout.astro",
			Content: `---
import BaseLayout from "./BaseLayout.astro";
import type { CollectionEntry } from "astro:content";

export interface Props {
  post: CollectionEntry<"blog">;
}
const { post } = Astro.props;
const { title, description, publishedAt, tags } = post.data;
const formattedDate = publishedAt.toLocaleDateString("en-US", { year: "numeric", month: "long", day: "numeric" });
---

<BaseLayout title={title} description={description}>
  <article class="mx-auto max-w-3xl px-6 py-16">
    <header class="mb-10">
      <div class="mb-3 flex flex-wrap gap-2">
        {tags.map((tag) => (
          <a href={"/blog/tags/" + tag} class="rounded-full bg-gray-100 px-3 py-1 text-xs font-medium text-gray-600 hover:bg-gray-200">
            {tag}
          </a>
        ))}
      </div>
      <h1 class="text-4xl font-extrabold tracking-tight">{title}</h1>
      <p class="mt-3 text-gray-500">{formattedDate}</p>
    </header>
    <div class="prose prose-gray max-w-none">
      <slot />
    </div>
  </article>

  <script type="application/ld+json" set:html={JSON.stringify({
    "@context": "https://schema.org",
    "@type": "BlogPosting",
    "headline": title,
    "description": description,
    "datePublished": publishedAt.toISOString(),
  })} />
</BaseLayout>
`,
		},
		{
			Path: "src/pages/index.astro",
			Content: `---
import BaseLayout from "../layouts/BaseLayout.astro";
import { getCollection } from "astro:content";

const featured = (await getCollection("blog", ({ data }) => data.featured))
  .sort((a, b) => b.data.publishedAt.valueOf() - a.data.publishedAt.valueOf())
  .slice(0, 3);
---

<BaseLayout title="Home">
  <!-- Hero -->
  <section class="mx-auto max-w-4xl px-6 py-28 text-center">
    <h1 class="text-5xl font-extrabold tracking-tight">Ideas worth reading</h1>
    <p class="mt-4 text-xl text-gray-500">Tutorials, essays, and deep dives from our team.</p>
    <a href="/blog" class="mt-8 inline-block rounded-lg bg-black px-8 py-3 font-semibold text-white hover:bg-gray-800">
      Browse the blog
    </a>
  </section>

  <!-- Featured posts -->
  {featured.length > 0 && (
    <section class="mx-auto max-w-5xl px-6 pb-24">
      <h2 class="mb-8 text-2xl font-bold">Featured</h2>
      <div class="grid gap-8 md:grid-cols-3">
        {featured.map((post) => (
          <a href={"/blog/" + post.slug} class="group rounded-2xl border border-gray-100 p-6 transition hover:shadow-md">
            <p class="text-xs text-gray-400">
              {post.data.publishedAt.toLocaleDateString("en-US", { year: "numeric", month: "short", day: "numeric" })}
            </p>
            <h3 class="mt-2 font-semibold group-hover:text-blue-600">{post.data.title}</h3>
            <p class="mt-1 line-clamp-2 text-sm text-gray-500">{post.data.description}</p>
          </a>
        ))}
      </div>
    </section>
  )}

  <!-- Newsletter -->
  <section class="bg-gray-50 py-20">
    <div class="mx-auto max-w-xl px-6 text-center">
      <h2 class="text-3xl font-bold">Stay in the loop</h2>
      <p class="mt-3 text-gray-500">Get new posts delivered to your inbox. No spam, ever.</p>
      <form class="mt-6 flex gap-3" action="/api/newsletter" method="POST">
        <input type="email" name="email" required placeholder="you@example.com"
          class="flex-1 rounded-lg border border-gray-300 px-4 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-black" />
        <button type="submit" class="rounded-lg bg-black px-5 py-2 text-sm font-medium text-white hover:bg-gray-800">
          Subscribe
        </button>
      </form>
    </div>
  </section>
</BaseLayout>
`,
		},
		{
			Path: "src/pages/blog/index.astro",
			Content: `---
import BaseLayout from "../../layouts/BaseLayout.astro";
import { getCollection } from "astro:content";

const posts = (await getCollection("blog"))
  .sort((a, b) => b.data.publishedAt.valueOf() - a.data.publishedAt.valueOf());

// Collect all tags
const allTags = [...new Set(posts.flatMap((p) => p.data.tags))].sort();
---

<BaseLayout title="Blog" description="All blog posts">
  <main class="mx-auto max-w-4xl px-6 py-16">
    <h1 class="mb-4 text-4xl font-extrabold">Blog</h1>

    <!-- Tags filter -->
    <div class="mb-10 flex flex-wrap gap-2">
      {allTags.map((tag) => (
        <a href={"/blog/tags/" + tag}
           class="rounded-full bg-gray-100 px-3 py-1 text-sm text-gray-600 hover:bg-gray-200">
          #{tag}
        </a>
      ))}
    </div>

    <div class="space-y-10">
      {posts.map((post) => (
        <article class="border-b border-gray-100 pb-10">
          <a href={"/blog/" + post.slug} class="group">
            <h2 class="text-2xl font-bold group-hover:text-blue-600">{post.data.title}</h2>
          </a>
          <p class="mt-1 text-sm text-gray-400">
            {post.data.publishedAt.toLocaleDateString("en-US", { year: "numeric", month: "long", day: "numeric" })}
            {post.data.category && <span class="ml-2 capitalize text-gray-400">· {post.data.category}</span>}
          </p>
          <p class="mt-3 text-gray-600">{post.data.description}</p>
          <a href={"/blog/" + post.slug} class="mt-3 inline-block text-sm font-medium text-blue-600 hover:underline">
            Read more →
          </a>
        </article>
      ))}
    </div>
  </main>
</BaseLayout>
`,
		},
		{
			Path: "src/pages/blog/[slug].astro",
			Content: `---
import { getCollection } from "astro:content";
import BlogPostLayout from "../../layouts/BlogPostLayout.astro";

export async function getStaticPaths() {
  const posts = await getCollection("blog");
  return posts.map((post) => ({ params: { slug: post.slug }, props: { post } }));
}

const { post } = Astro.props;
const { Content } = await post.render();
---

<BlogPostLayout post={post}>
  <Content />
</BlogPostLayout>
`,
		},
		{
			Path: "src/pages/rss.xml.ts",
			Content: `import rss from "@astrojs/rss";
import { getCollection } from "astro:content";
import type { APIContext } from "astro";

export async function GET(context: APIContext) {
  const posts = (await getCollection("blog")).sort(
    (a, b) => b.data.publishedAt.valueOf() - a.data.publishedAt.valueOf()
  );
  return rss({
    title: "My Site Blog",
    description: "Latest posts from My Site",
    site: context.site!,
    items: posts.map((post) => ({
      title: post.data.title,
      description: post.data.description,
      pubDate: post.data.publishedAt,
      link: "/blog/" + post.slug,
    })),
  });
}
`,
		},
		{
			Path: "public/robots.txt",
			Content: `User-agent: *
Allow: /

Sitemap: /sitemap-index.xml
`,
		},
		{
			Path: "src/styles/global.css",
			Content: `@tailwind base;
@tailwind components;
@tailwind utilities;

/* Prose styles for blog posts */
.prose h2 { @apply text-2xl font-bold mt-10 mb-4; }
.prose h3 { @apply text-xl font-semibold mt-8 mb-3; }
.prose p  { @apply leading-relaxed text-gray-700 mb-4; }
.prose a  { @apply text-blue-600 hover:underline; }
.prose pre { @apply bg-gray-900 text-gray-100 rounded-xl p-6 overflow-x-auto my-6; }
.prose code { @apply bg-gray-100 text-gray-800 rounded px-1 py-0.5 text-sm; }
.prose pre code { @apply bg-transparent text-inherit p-0; }
.prose ul { @apply list-disc pl-6 mb-4 space-y-1 text-gray-700; }
.prose ol { @apply list-decimal pl-6 mb-4 space-y-1 text-gray-700; }
.prose blockquote { @apply border-l-4 border-gray-300 pl-4 italic text-gray-500 my-6; }
`,
		},
	}
}
