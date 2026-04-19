import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";
import type {
  WizardGenerateResult,
  WizardQuestion,
  WizardSession,
} from "../../src/lib/quic";

// --- stage metadata --------------------------------------------------------
//
// The wizard shows a horizontal "stage strip" so the user sees the arc of
// the build before they start answering — Discovery, Identity, Design,
// Stack, Mobile, Permissions, Policies, Release, Repo, Ready. Each question
// maps to exactly one stage; the strip highlights the current stage and
// marks earlier ones done. This is what makes the wizard feel like
// product work instead of a form.

type StageId =
  | "Discovery"
  | "Identity"
  | "Design"
  | "Surfaces"
  | "Stack"
  | "Mobile"
  | "Auth"
  | "Permissions"
  | "Policies"
  | "Release"
  | "Repo"
  | "Ready";

const STAGE_ORDER: StageId[] = [
  "Discovery",
  "Identity",
  "Design",
  "Surfaces",
  "Stack",
  "Mobile",
  "Auth",
  "Permissions",
  "Policies",
  "Release",
  "Repo",
  "Ready",
];

const STAGE_META: Record<StageId, { emoji: string; title: string; blurb: string }> = {
  Discovery: { emoji: "🎯", title: "Discovery", blurb: "What is this app for, and who is it for?" },
  Identity: { emoji: "🪪", title: "Identity", blurb: "Name, slug, one-paragraph description." },
  Design: { emoji: "🎨", title: "Design", blurb: "Palette, tone, design references." },
  Surfaces: { emoji: "🧩", title: "Surfaces", blurb: "Web, mobile, backend, landing — pick what you ship." },
  Stack: { emoji: "🧱", title: "Stack", blurb: "Frameworks and backend that fit the shape of the app." },
  Mobile: { emoji: "📱", title: "Mobile", blurb: "Navigation shape, bundle IDs, build targets." },
  Auth: { emoji: "🔑", title: "Auth", blurb: "OAuth providers, email fallback, who can sign in." },
  Permissions: { emoji: "🔐", title: "Permissions", blurb: "Only what the first release actually uses." },
  Policies: { emoji: "📜", title: "Policies", blurb: "Privacy, terms, store posture." },
  Release: { emoji: "🚀", title: "Release", blurb: "TestFlight, Play Store, Cloudflare." },
  Repo: { emoji: "🗂", title: "Repo", blurb: "Where the fresh monorepo gets pushed." },
  Ready: { emoji: "✨", title: "Ready", blurb: "Review, confirm, generate." },
};

const QUESTION_TO_STAGE: Record<string, StageId> = {
  // Discovery — product strategy
  app_template: "Discovery",
  audience_type: "Discovery",
  problem_statement: "Discovery",
  unique_angle: "Discovery",
  competitor_inspiration: "Discovery",
  monetization: "Discovery",
  launch_timeline: "Discovery",
  success_metric: "Discovery",
  distribution_channel: "Discovery",

  // Identity
  app_name: "Identity",
  slug: "Identity",
  description: "Identity",
  tagline: "Identity",
  supported_languages: "Identity",

  // Design
  domain: "Identity",
  primary_color: "Design",
  secondary_color: "Design",
  accent_color: "Design",
  surface_color: "Design",
  tone: "Design",
  design_source: "Design",
  design_reference_url: "Design",
  design_notes: "Design",

  // Surfaces
  include_web: "Surfaces",
  include_mobile: "Surfaces",
  include_backend: "Surfaces",
  include_landing: "Surfaces",

  // Stack
  web_framework: "Stack",
  web_host: "Stack",
  backend: "Stack",
  mobile_stack: "Stack",

  // Mobile
  mobile_nav_style: "Mobile",
  mobile_nav_count: "Mobile",
  mobile_nav_labels: "Mobile",
  ios_bundle_id: "Mobile",
  android_package: "Mobile",

  // Auth
  oauth_apple: "Auth",
  oauth_google: "Auth",
  oauth_microsoft: "Auth",
  oauth_email: "Auth",

  // Permissions
  mobile_permission_camera: "Permissions",
  mobile_permission_camera_usage: "Permissions",
  mobile_permission_photos: "Permissions",
  mobile_permission_photos_usage: "Permissions",
  mobile_permission_photos_save: "Permissions",
  mobile_permission_photos_save_usage: "Permissions",
  mobile_permission_microphone: "Permissions",
  mobile_permission_microphone_usage: "Permissions",
  mobile_permission_location: "Permissions",
  mobile_permission_location_usage: "Permissions",
  mobile_permission_location_always: "Permissions",
  mobile_permission_location_always_usage: "Permissions",
  mobile_permission_bluetooth: "Permissions",
  mobile_permission_bluetooth_usage: "Permissions",
  mobile_permission_notifications: "Permissions",
  mobile_permission_notifications_usage: "Permissions",
  mobile_permission_tracking: "Permissions",
  mobile_permission_tracking_usage: "Permissions",

  // Policies
  mobile_account_deletion: "Policies",
  mobile_data_collection: "Policies",
  audience_children: "Policies",
  legal_entity_name: "Policies",
  legal_support_email: "Policies",
  legal_jurisdiction: "Policies",
  legal_privacy_notes: "Policies",

  // Release
  payments: "Release",
  apple_team_id: "Release",
  play_service_account: "Release",
  cloudflare_zone: "Release",

  // Repo
  git_provider: "Repo",
  git_visibility: "Repo",
  git_org: "Repo",
  git_repo_name: "Repo",

  // Ready
  confirm: "Ready",
};

function stageFor(id: string): StageId {
  return QUESTION_TO_STAGE[id] ?? "Ready";
}

// --- rich choice metadata --------------------------------------------------
//
// For opinionated enum questions, show an emoji + subtitle so the answer
// feels like a product decision, not a dropdown item. Missing entries
// fall back to the bare label.

type ChoiceMeta = { emoji: string; label: string; hint: string };
type ChoiceMetaMap = Record<string, ChoiceMeta>;

const CHOICE_META: Record<string, ChoiceMetaMap> = {
  app_template: {
    "saas-dashboard": { emoji: "📊", label: "SaaS dashboard", hint: "B2B product with accounts, teams, billing." },
    "creator-marketplace": { emoji: "🛍", label: "Creator marketplace", hint: "Buyers + sellers, reviews, payouts." },
    "internal-tool": { emoji: "🛠", label: "Internal tool", hint: "One team, admin-heavy, not on any store." },
    "consumer-social": { emoji: "💬", label: "Consumer social", hint: "Feeds, profiles, notifications, DMs." },
    commerce: { emoji: "🛒", label: "Commerce", hint: "Catalog, cart, checkout, orders." },
    booking: { emoji: "📆", label: "Booking", hint: "Availability, calendars, confirmations." },
    "ai-companion": { emoji: "🤖", label: "AI companion", hint: "Chat-first, voice, memory, routines." },
    "habit-wellness": { emoji: "🧘", label: "Habit & wellness", hint: "Streaks, check-ins, nudges." },
    "journal-notes": { emoji: "📓", label: "Journal & notes", hint: "Personal writing, private by default." },
    "fitness-tracker": { emoji: "🏃", label: "Fitness tracker", hint: "Workouts, progress, health kit." },
    "meal-recipe": { emoji: "🥗", label: "Meal & recipe", hint: "Planning, grocery lists, nutrition." },
    "education-course": { emoji: "🎓", label: "Education / course", hint: "Lessons, quizzes, certificates." },
    "finance-budget": { emoji: "💰", label: "Finance / budget", hint: "Accounts, spend, goals, reports." },
    "dating-social": { emoji: "💘", label: "Dating / social", hint: "Profiles, matching, messaging." },
    "local-discovery": { emoji: "🗺", label: "Local discovery", hint: "Maps, nearby, reviews, hours." },
    "creator-tool": { emoji: "🎬", label: "Creator tool", hint: "Editing, publishing, analytics." },
    "newsletter-blog": { emoji: "📰", label: "Newsletter / blog", hint: "Write, publish, grow a list." },
    "dev-tool": { emoji: "🧑‍💻", label: "Dev tool", hint: "Built for developers — CLI-like UX." },
    "analytics-dashboard": { emoji: "📈", label: "Analytics dashboard", hint: "Charts, filters, exports." },
    "photo-video": { emoji: "📷", label: "Photo / video", hint: "Capture, edit, share media." },
  },
  audience_type: {
    consumers: { emoji: "🧑", label: "Consumers", hint: "Individual users, no contract." },
    "small-businesses": { emoji: "🏪", label: "Small businesses", hint: "Owner-operator, self-serve." },
    "enterprise-teams": { emoji: "🏢", label: "Enterprise teams", hint: "Procurement, SSO, compliance." },
    "agency-clients": { emoji: "🧑‍💼", label: "Agency clients", hint: "You build, they own it." },
    "family-and-friends": { emoji: "🏡", label: "Family & friends", hint: "Tiny audience, no store." },
    "internal-team": { emoji: "🧪", label: "Internal team", hint: "Your coworkers only." },
    developers: { emoji: "⌨️", label: "Developers", hint: "Technical users, CLI-friendly." },
    creators: { emoji: "🎨", label: "Creators", hint: "Makers, artists, newsletter hosts." },
  },
  monetization: {
    free: { emoji: "🆓", label: "Free", hint: "No paywalls, no ads." },
    freemium: { emoji: "🎁", label: "Freemium", hint: "Free tier + upgrade for power." },
    subscription: { emoji: "🔁", label: "Subscription", hint: "Monthly / yearly recurring." },
    "one-time-purchase": { emoji: "💳", label: "One-time purchase", hint: "Paid once, unlock forever." },
    "marketplace-commission": { emoji: "🪙", label: "Marketplace commission", hint: "Cut of seller transactions." },
    ads: { emoji: "📣", label: "Ads", hint: "Ad inventory or sponsorships." },
    "b2b-contract": { emoji: "📝", label: "B2B contract", hint: "Annual deals, invoicing." },
  },
  launch_timeline: {
    weekend: { emoji: "⚡", label: "Weekend", hint: "Ship something rough by Monday." },
    "1-2-weeks": { emoji: "🟢", label: "1–2 weeks", hint: "Tight MVP scope." },
    "1-month": { emoji: "🗓", label: "1 month", hint: "Room to polish a few flows." },
    "3-months": { emoji: "🏗", label: "3 months", hint: "Real product, real analytics." },
    whenever: { emoji: "🌙", label: "No rush", hint: "Side project, evolves as it goes." },
  },
  success_metric: {
    "daily-active-users": { emoji: "📈", label: "Daily active users", hint: "Habit — people come back." },
    "monthly-recurring-revenue": { emoji: "💵", label: "MRR", hint: "Subscription revenue." },
    "week-4-retention": { emoji: "🔁", label: "Week-4 retention", hint: "Real value, not just curiosity." },
    "community-size": { emoji: "🫂", label: "Community size", hint: "People who care, not just users." },
    "enterprise-contracts": { emoji: "🏢", label: "Enterprise deals", hint: "Signed contracts." },
    "personal-usage": { emoji: "✍️", label: "Personal usage", hint: "You use it every day — that's enough." },
  },
  distribution_channel: {
    "app-store-seo": { emoji: "🔎", label: "App Store SEO", hint: "Keywords, screenshots, reviews." },
    "social-tiktok-instagram": { emoji: "📱", label: "Social (TikTok / IG)", hint: "Shortform, founder voice." },
    "email-newsletter": { emoji: "📧", label: "Email / newsletter", hint: "Owned audience." },
    "word-of-mouth": { emoji: "🗣", label: "Word of mouth", hint: "Referrals, invites, viral loops." },
    "paid-ads": { emoji: "💸", label: "Paid ads", hint: "Meta, Google, Reddit buys." },
    "dev-community": { emoji: "🧑‍🚀", label: "Dev community", hint: "HN, Reddit, Discord, GitHub." },
    "niche-forum": { emoji: "🌱", label: "Niche forum", hint: "Tight community with deep fit." },
    "b2b-outreach": { emoji: "📞", label: "B2B outreach", hint: "Sales calls, LinkedIn, partners." },
  },
  tone: {
    light: { emoji: "☀️", label: "Light", hint: "Airy, bright, high-contrast." },
    dark: { emoji: "🌙", label: "Dark", hint: "Moody, OLED-friendly." },
    system: { emoji: "🌓", label: "System", hint: "Follows the OS setting." },
  },
  mobile_nav_style: {
    "bottom-tabs": { emoji: "📱", label: "Bottom tabs", hint: "Thumb-first, default for consumer." },
    "top-tabs": { emoji: "🔝", label: "Top tabs", hint: "Data-heavy, scannable." },
    drawer: { emoji: "📂", label: "Drawer", hint: "Many destinations, not all primary." },
    "stack-only": { emoji: "🧾", label: "Stack only", hint: "Single flow, no nav chrome." },
  },
  backend: {
    sqlite: { emoji: "🟦", label: "Yaver SQLite", hint: "Portable — phone, laptop, or cloud." },
    postgres: { emoji: "🐘", label: "Postgres", hint: "Relational, your own host." },
    supabase: { emoji: "🟩", label: "Supabase", hint: "Postgres + auth + storage managed." },
    convex: { emoji: "🌀", label: "Convex", hint: "Reactive, TypeScript-first." },
    pocketbase: { emoji: "📦", label: "PocketBase", hint: "Single binary, SQLite-backed." },
    appwrite: { emoji: "🟪", label: "Appwrite", hint: "Self-hostable backend suite." },
    none: { emoji: "🚫", label: "No backend", hint: "Client-only, no server." },
  },
  web_framework: {
    nextjs: { emoji: "▲", label: "Next.js", hint: "App router, server components." },
    remix: { emoji: "💿", label: "Remix", hint: "Web standards, nested routes." },
    astro: { emoji: "🚀", label: "Astro", hint: "Content-first, island hydration." },
  },
  web_host: {
    cloudflare: { emoji: "🟧", label: "Cloudflare", hint: "Workers + Pages." },
    vercel: { emoji: "▲", label: "Vercel", hint: "Next.js native home." },
    netlify: { emoji: "🟢", label: "Netlify", hint: "Simple static + serverless." },
    "self-host": { emoji: "🖥", label: "Self-host", hint: "Your own VPS." },
  },
  payments: {
    stripe: { emoji: "💳", label: "Stripe", hint: "Global, most integrations." },
    "lemon-squeezy": { emoji: "🍋", label: "Lemon Squeezy", hint: "Merchant of record, VAT handled." },
    paddle: { emoji: "🏓", label: "Paddle", hint: "MoR, subscription-native." },
    none: { emoji: "🚫", label: "None", hint: "Skip payments for now." },
  },
  mobile_data_collection: {
    none: { emoji: "🕊", label: "Local-only", hint: "Nothing leaves the device." },
    minimal: { emoji: "🧂", label: "Minimal", hint: "Auth + crash reports only." },
    standard: { emoji: "📊", label: "Standard", hint: "Auth + crash + product analytics." },
    tracking: { emoji: "📡", label: "Tracking", hint: "Adds cross-app ad / attribution data." },
  },
  git_provider: {
    gitlab: { emoji: "🦊", label: "GitLab", hint: "Default for this workspace." },
    github: { emoji: "🐙", label: "GitHub", hint: "Most common — best CI marketplace." },
    none: { emoji: "🚫", label: "Skip", hint: "Keep it local for now." },
  },
  git_visibility: {
    private: { emoji: "🔒", label: "Private", hint: "Default — you'll invite collaborators." },
    public: { emoji: "🌍", label: "Public", hint: "Open from day one." },
  },
  design_source: {
    "prompt-only": { emoji: "✍️", label: "Prompt only", hint: "No external reference yet." },
    figma: { emoji: "🎛", label: "Figma", hint: "Paste a Figma share link." },
    canva: { emoji: "🖌", label: "Canva", hint: "Paste a Canva board link." },
    screenshots: { emoji: "🖼", label: "Screenshots", hint: "You'll drop them in later." },
    "other-url": { emoji: "🔗", label: "Other URL", hint: "Any web reference you like." },
  },
};

function metaFor(questionId: string, choice: string): ChoiceMeta | null {
  return CHOICE_META[questionId]?.[choice] ?? null;
}

// --- quick presets --------------------------------------------------------
//
// One-tap packs so the user can stamp out a typical starter without walking
// every question. They only fill the Discovery + Identity + Surfaces +
// Auth + Permissions posture bits — the user still tunes colors, domain,
// nav labels, and deployment credentials.

type Preset = {
  id: string;
  emoji: string;
  title: string;
  blurb: string;
  answers: Record<string, string>;
};

const PRESETS: Preset[] = [
  {
    id: "indie-maker",
    emoji: "🧑‍🎨",
    title: "Indie maker",
    blurb: "Solo-built consumer mobile app, freemium, ship fast.",
    answers: {
      app_template: "ai-companion",
      audience_type: "consumers",
      monetization: "freemium",
      launch_timeline: "1-2-weeks",
      success_metric: "week-4-retention",
      distribution_channel: "social-tiktok-instagram",
      include_web: "false",
      include_mobile: "true",
      include_backend: "true",
      include_landing: "true",
      mobile_data_collection: "minimal",
      audience_children: "false",
      mobile_account_deletion: "true",
    },
  },
  {
    id: "b2b-saas",
    emoji: "🏢",
    title: "B2B SaaS",
    blurb: "Dashboard + API, sold via outreach and paid ads.",
    answers: {
      app_template: "saas-dashboard",
      audience_type: "small-businesses",
      monetization: "subscription",
      launch_timeline: "1-month",
      success_metric: "monthly-recurring-revenue",
      distribution_channel: "b2b-outreach",
      include_web: "true",
      include_mobile: "false",
      include_backend: "true",
      include_landing: "true",
      oauth_google: "true",
      oauth_microsoft: "true",
      oauth_email: "true",
      mobile_data_collection: "standard",
      audience_children: "false",
    },
  },
  {
    id: "consumer-social",
    emoji: "💬",
    title: "Consumer social",
    blurb: "Mobile-first feed, viral loops, App Store SEO.",
    answers: {
      app_template: "consumer-social",
      audience_type: "consumers",
      monetization: "ads",
      launch_timeline: "3-months",
      success_metric: "daily-active-users",
      distribution_channel: "app-store-seo",
      include_web: "false",
      include_mobile: "true",
      include_backend: "true",
      include_landing: "true",
      mobile_permission_camera: "true",
      mobile_permission_photos: "true",
      mobile_permission_notifications: "true",
      mobile_data_collection: "standard",
      audience_children: "false",
      mobile_account_deletion: "true",
    },
  },
  {
    id: "internal-tool",
    emoji: "🛠",
    title: "Internal tool",
    blurb: "Your team only. No store, no landing page.",
    answers: {
      app_template: "internal-tool",
      audience_type: "internal-team",
      monetization: "free",
      launch_timeline: "weekend",
      success_metric: "personal-usage",
      distribution_channel: "word-of-mouth",
      include_web: "true",
      include_mobile: "false",
      include_backend: "true",
      include_landing: "false",
      oauth_google: "true",
      oauth_email: "true",
      mobile_data_collection: "none",
      audience_children: "false",
    },
  },
];

const QUICK_COLOR_SWATCHS = ["#4F46E5", "#0EA5E9", "#14B8A6", "#F97316", "#F59E0B", "#111827"];

// --- visibility mirror -----------------------------------------------------
//
// Mirrors nextQuestion() on the agent so the stage strip only counts the
// questions that will actually appear. Keeps the progress bar honest when
// whole branches (no-mobile, no-web, etc.) get skipped.

function isQuestionVisible(question: WizardQuestion, answers: Record<string, string>) {
  const mobileOn = answers.include_mobile === "true";
  const webOn = answers.include_web === "true" || answers.include_landing === "true";
  const backendOn = answers.include_backend === "true";
  const anyAuth = mobileOn || webOn || backendOn;

  if ((question.id === "web_framework" || question.id === "web_host") && !webOn) return false;
  if (
    [
      "mobile_stack",
      "mobile_nav_style",
      "mobile_nav_count",
      "mobile_nav_labels",
      "ios_bundle_id",
      "android_package",
      "apple_team_id",
      "play_service_account",
    ].includes(question.id) &&
    !mobileOn
  ) {
    return false;
  }
  if (question.id.startsWith("mobile_permission_") && !mobileOn) return false;
  if (question.id.endsWith("_usage")) {
    const baseId = question.id.replace(/_usage$/, "");
    if (baseId.startsWith("mobile_permission_") && answers[baseId] !== "true") return false;
  }
  if (
    (question.id === "mobile_permission_photos_save" || question.id === "mobile_permission_photos_save_usage") &&
    answers.mobile_permission_photos !== "true"
  ) {
    return false;
  }
  if (
    (question.id === "mobile_permission_location_always" || question.id === "mobile_permission_location_always_usage") &&
    answers.mobile_permission_location !== "true"
  ) {
    return false;
  }
  if (["mobile_account_deletion", "mobile_data_collection", "audience_children"].includes(question.id) && !mobileOn) {
    return false;
  }
  if (question.id === "backend" && !backendOn) return false;
  if (["oauth_apple", "oauth_google", "oauth_microsoft", "oauth_email"].includes(question.id) && !anyAuth) {
    return false;
  }
  if (question.id.startsWith("legal_") && !mobileOn && !webOn) return false;
  if (question.id === "cloudflare_zone" && answers.web_host !== "cloudflare") return false;
  if (question.id === "payments" && !webOn && !mobileOn) return false;
  if (["git_visibility", "git_org", "git_repo_name"].includes(question.id) && answers.git_provider === "none") {
    return false;
  }
  if (question.id === "design_reference_url" && (!answers.design_source || answers.design_source === "prompt-only")) {
    return false;
  }
  return question.kind !== "done";
}

function formatChoice(choice: string) {
  return choice
    .split(/[-_]/g)
    .filter(Boolean)
    .map((part) => part.slice(0, 1).toUpperCase() + part.slice(1))
    .join(" ");
}

function buildInitialInput(question: WizardQuestion | null) {
  return question?.default ?? "";
}

// --- component -------------------------------------------------------------

export default function NewProjectScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { connectionStatus, devices, selectDevice } = useDevice();
  const connected = connectionStatus === "connected";
  const connecting = connectionStatus === "connecting";

  const [session, setSession] = useState<WizardSession | null>(null);
  const [question, setQuestion] = useState<WizardQuestion | null>(null);
  const [questions, setQuestions] = useState<WizardQuestion[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [skipping, setSkipping] = useState(false);
  const [applyingPreset, setApplyingPreset] = useState<string | null>(null);
  const [showPresets, setShowPresets] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<WizardGenerateResult | null>(null);

  const answers = session?.answers ?? {};

  const start = useCallback(async () => {
    setLoading(true);
    setError(null);
    setResult(null);
    setShowPresets(true);
    const [startRes, allQuestions] = await Promise.all([
      quicClient.wizardStart(),
      quicClient.wizardQuestions(),
    ]);
    setLoading(false);
    if (!startRes) {
      setError("Could not start the wizard. The agent may be offline.");
      return;
    }
    setQuestions(allQuestions ?? []);
    setSession(startRes.session);
    setQuestion(startRes.question);
    setInput(buildInitialInput(startRes.question));
  }, []);

  useEffect(() => {
    if (connected && !session && !result) {
      void start();
    }
  }, [connected, result, session, start]);

  const visibleQuestions = useMemo(
    () => questions.filter((item) => isQuestionVisible(item, answers)),
    [answers, questions]
  );

  const currentIndex = useMemo(() => {
    if (!question) return 0;
    const idx = visibleQuestions.findIndex((item) => item.id === question.id);
    return idx >= 0 ? idx : visibleQuestions.length;
  }, [question, visibleQuestions]);

  const progressTotal = Math.max(visibleQuestions.length, 1);
  const progressValue = Math.min(currentIndex / progressTotal, 1);

  const currentStage = question ? stageFor(question.id) : "Discovery";

  // Which stages actually appear in the visible question set — drives the
  // top strip so we don't show "Release" as a stage when the project has
  // no release surfaces.
  const stageList = useMemo(() => {
    const visibleStages = new Set<StageId>();
    for (const vq of visibleQuestions) visibleStages.add(stageFor(vq.id));
    visibleStages.add("Ready");
    return STAGE_ORDER.filter((s) => visibleStages.has(s));
  }, [visibleQuestions]);

  const currentStageIndex = stageList.indexOf(currentStage);

  const submitAnswer = useCallback(
    async (answer: string) => {
      if (!session || !question) return null;
      const res = await quicClient.wizardAnswer(session.id, question.id, answer);
      if (!res) return null;
      setSession(res.session);
      setQuestion(res.question);
      setInput(buildInitialInput(res.question));
      return res;
    },
    [question, session]
  );

  const handleNext = useCallback(async () => {
    if (!question) return;
    setLoading(true);
    setError(null);
    setShowPresets(false);

    let ok = true;
    if (question.kind === "confirm") {
      ok = !!(await submitAnswer(input.trim() || question.default || "true"));
      if (ok && session) {
        const gen = await quicClient.wizardGenerate(session.id);
        if (!gen || !gen.ok) ok = false;
        else setResult(gen);
      }
    } else if (question.kind === "done") {
      if (!session) ok = false;
      else {
        const gen = await quicClient.wizardGenerate(session.id);
        if (!gen || !gen.ok) ok = false;
        else setResult(gen);
      }
    } else {
      ok = !!(await submitAnswer(input.trim() || question.default || ""));
    }

    setLoading(false);
    if (!ok) {
      setError(question.kind === "confirm" || question.kind === "done" ? "Generation failed. Check agent logs and retry." : "Could not save that answer.");
    }
  }, [input, question, session, submitAnswer]);

  const skipThis = useCallback(async () => {
    if (!question) return;
    setLoading(true);
    setError(null);
    setShowPresets(false);
    const res = await submitAnswer(question.default ?? "");
    setLoading(false);
    if (!res) setError("Could not skip this step.");
  }, [question, submitAnswer]);

  const useDefaultsForRest = useCallback(async () => {
    if (!session || !question) return;
    setSkipping(true);
    setError(null);
    setShowPresets(false);

    let currentQuestion: WizardQuestion | null = question;
    let currentSession: WizardSession | null = session;

    while (currentQuestion && currentQuestion.kind !== "done") {
      const answer = currentQuestion.id === "confirm" ? "true" : currentQuestion.default ?? "";
      const res = await quicClient.wizardAnswer(currentSession.id, currentQuestion.id, answer);
      if (!res) {
        setSkipping(false);
        setError("Could not fast-forward the wizard.");
        return;
      }
      currentSession = res.session;
      currentQuestion = res.question;
    }

    setSession(currentSession);
    setQuestion(currentQuestion);
    setInput(buildInitialInput(currentQuestion));
    setSkipping(false);
  }, [question, session]);

  // Stamp a preset — posts every answer in the preset to the agent so the
  // wizard skips straight past the bits the preset covers. The user keeps
  // tuning colors, domain, and deploy creds.
  const applyPreset = useCallback(
    async (preset: Preset) => {
      if (!session) return;
      setApplyingPreset(preset.id);
      setError(null);

      for (const [qid, ans] of Object.entries(preset.answers)) {
        const res = await quicClient.wizardAnswer(session.id, qid, ans);
        if (!res) {
          setApplyingPreset(null);
          setError(`Preset ${preset.title} failed at ${qid}.`);
          return;
        }
        setSession(res.session);
        setQuestion(res.question);
        setInput(buildInitialInput(res.question));
      }
      setApplyingPreset(null);
      setShowPresets(false);
    },
    [session]
  );

  const summary = useMemo(() => {
    const items: string[] = [];
    if (answers.app_template) items.push(formatChoice(answers.app_template));
    if (answers.audience_type) items.push(formatChoice(answers.audience_type));
    if (answers.monetization) items.push(formatChoice(answers.monetization));
    if (answers.launch_timeline) items.push(formatChoice(answers.launch_timeline));
    if (answers.supported_languages) items.push(answers.supported_languages);
    return items.slice(0, 5);
  }, [answers]);

  const renderField = () => {
    if (!question) return null;

    if (question.kind === "choice" || question.kind === "bool") {
      const choices = question.kind === "bool" ? ["true", "false"] : question.choices ?? [];
      return (
        <View style={styles.choiceGrid}>
          {choices.map((choice) => {
            const selected = input === choice;
            const meta = metaFor(question.id, choice);
            const label =
              meta?.label ?? (question.kind === "bool" ? (choice === "true" ? "Yes" : "No") : formatChoice(choice));
            const hint = meta?.hint ?? (question.kind === "bool" ? "" : choice);
            const emoji = meta?.emoji ?? (question.kind === "bool" ? (choice === "true" ? "✅" : "⬜") : "•");
            return (
              <Pressable
                key={choice}
                onPress={() => setInput(choice)}
                style={[
                  styles.choiceCard,
                  {
                    backgroundColor: selected ? c.accent + "1F" : c.bgCard,
                    borderColor: selected ? c.accent : c.border,
                  },
                ]}
              >
                <View style={styles.choiceCardRow}>
                  <Text style={styles.choiceEmoji}>{emoji}</Text>
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.choiceLabel, { color: c.textPrimary }]}>{label}</Text>
                    {hint ? (
                      <Text style={[styles.choiceHint, { color: selected ? c.accent : c.textMuted }]}>{hint}</Text>
                    ) : null}
                  </View>
                  {selected ? (
                    <Text style={{ color: c.accent, fontSize: 18, fontWeight: "800" }}>✓</Text>
                  ) : null}
                </View>
              </Pressable>
            );
          })}
        </View>
      );
    }

    const multiline =
      question.id === "description" ||
      question.id === "design_notes" ||
      question.id === "mobile_nav_labels" ||
      question.id === "supported_languages" ||
      question.id === "problem_statement" ||
      question.id === "unique_angle" ||
      question.id === "legal_privacy_notes";

    return (
      <View style={{ gap: 12 }}>
        <TextInput
          value={input}
          onChangeText={setInput}
          placeholder={question.default ?? ""}
          placeholderTextColor={c.textMuted}
          autoCapitalize={question.id.includes("color") ? "characters" : "none"}
          autoCorrect={false}
          multiline={multiline}
          style={[
            styles.input,
            {
              minHeight: multiline ? 120 : 56,
              color: c.textPrimary,
              backgroundColor: c.bgInput,
              borderColor: c.border,
              textAlignVertical: multiline ? "top" : "center",
            },
          ]}
        />
        {question.kind === "color" ? (
          <View style={styles.swatchRow}>
            {QUICK_COLOR_SWATCHS.map((swatch) => (
              <Pressable
                key={swatch}
                onPress={() => setInput(swatch)}
                style={[
                  styles.swatch,
                  {
                    backgroundColor: swatch,
                    borderColor: input === swatch ? c.textPrimary : "rgba(255,255,255,0.14)",
                  },
                ]}
              />
            ))}
          </View>
        ) : null}
      </View>
    );
  };

  const renderPreviewCard = () => {
    const appName = answers.app_name?.trim() || "Your app";
    const tagline =
      answers.tagline?.trim() ||
      answers.problem_statement?.trim() ||
      answers.description?.trim() ||
      "Answer a few questions and a stylized preview will start filling in here.";
    const palette = [
      answers.primary_color,
      answers.secondary_color,
      answers.accent_color,
      answers.surface_color,
    ].filter(Boolean) as string[];
    const navLabels = (answers.mobile_nav_labels || "")
      .split(",")
      .map((x) => x.trim())
      .filter(Boolean);
    const template = answers.app_template;
    const templateMeta = template ? metaFor("app_template", template) : null;

    return (
      <View
        style={[
          styles.previewCard,
          {
            backgroundColor: answers.surface_color || c.bgCard,
            borderColor: answers.accent_color || c.border,
          },
        ]}
      >
        <View style={styles.previewTopRow}>
          <View style={[styles.previewIcon, { backgroundColor: answers.primary_color || c.accent }]}>
            <Text style={{ fontSize: 22 }}>{templateMeta?.emoji ?? "📦"}</Text>
          </View>
          <View style={{ flex: 1 }}>
            <Text numberOfLines={1} style={styles.previewName}>
              {appName}
            </Text>
            <Text numberOfLines={2} style={styles.previewTagline}>
              {tagline}
            </Text>
          </View>
        </View>
        {palette.length ? (
          <View style={styles.previewPaletteRow}>
            {palette.map((color, idx) => (
              <View
                key={`${color}-${idx}`}
                style={[styles.previewSwatch, { backgroundColor: color }]}
              />
            ))}
          </View>
        ) : null}
        {navLabels.length ? (
          <View style={styles.previewNavRow}>
            {navLabels.slice(0, 5).map((label) => (
              <View
                key={label}
                style={[
                  styles.previewNavChip,
                  { borderColor: answers.accent_color || "rgba(255,255,255,0.18)" },
                ]}
              >
                <Text style={styles.previewNavChipText}>{label}</Text>
              </View>
            ))}
          </View>
        ) : null}
        {answers.audience_type || answers.monetization || answers.launch_timeline ? (
          <View style={styles.previewFacts}>
            {answers.audience_type ? (
              <Text style={styles.previewFact}>
                For {metaFor("audience_type", answers.audience_type)?.label ?? formatChoice(answers.audience_type)}
              </Text>
            ) : null}
            {answers.monetization ? (
              <Text style={styles.previewFact}>
                {metaFor("monetization", answers.monetization)?.emoji}{" "}
                {metaFor("monetization", answers.monetization)?.label ?? formatChoice(answers.monetization)}
              </Text>
            ) : null}
            {answers.launch_timeline ? (
              <Text style={styles.previewFact}>
                Ship in{" "}
                {metaFor("launch_timeline", answers.launch_timeline)?.label ?? formatChoice(answers.launch_timeline)}
              </Text>
            ) : null}
          </View>
        ) : null}
      </View>
    );
  };

  const renderStageStrip = () => (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      contentContainerStyle={{ paddingHorizontal: 16, gap: 8 }}
      style={{ marginTop: 4 }}
    >
      {stageList.map((stageId, idx) => {
        const meta = STAGE_META[stageId];
        const isCurrent = idx === currentStageIndex;
        const isDone = idx < currentStageIndex;
        return (
          <View
            key={stageId}
            style={[
              styles.stageChip,
              {
                backgroundColor: isCurrent ? c.accent + "22" : c.bgCard,
                borderColor: isCurrent ? c.accent : c.border,
                opacity: isDone ? 0.92 : 1,
              },
            ]}
          >
            <Text style={styles.stageEmoji}>{isDone ? "✓" : meta.emoji}</Text>
            <Text
              style={[
                styles.stageLabel,
                {
                  color: isCurrent ? c.accent : isDone ? c.textMuted : c.textPrimary,
                },
              ]}
            >
              {meta.title}
            </Text>
          </View>
        );
      })}
    </ScrollView>
  );

  const renderPresetRow = () => {
    if (!showPresets || result) return null;
    const discoveryDone = !!(answers.app_template && answers.audience_type);
    if (discoveryDone) return null;
    return (
      <View style={styles.presetPanel}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginBottom: 10 }}>
          <Text style={{ fontSize: 15, fontWeight: "800", color: c.textPrimary }}>One-tap presets</Text>
          <Text style={{ fontSize: 12, color: c.textMuted, flex: 1 }}>Stamp typical defaults, then tune.</Text>
          <Pressable onPress={() => setShowPresets(false)}>
            <Text style={{ color: c.textMuted, fontSize: 12, fontWeight: "700" }}>Hide</Text>
          </Pressable>
        </View>
        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 10 }}>
          {PRESETS.map((preset) => {
            const busy = applyingPreset === preset.id;
            return (
              <Pressable
                key={preset.id}
                onPress={() => void applyPreset(preset)}
                disabled={applyingPreset !== null}
                style={[
                  styles.presetCard,
                  {
                    backgroundColor: c.bgCard,
                    borderColor: busy ? c.accent : c.border,
                    opacity: applyingPreset && !busy ? 0.5 : 1,
                  },
                ]}
              >
                <Text style={{ fontSize: 22 }}>{preset.emoji}</Text>
                <Text style={[styles.presetTitle, { color: c.textPrimary }]}>{preset.title}</Text>
                <Text style={[styles.presetBlurb, { color: c.textMuted }]}>{preset.blurb}</Text>
                <Text style={{ color: busy ? c.accent : c.accent, fontWeight: "800", fontSize: 12, marginTop: 6 }}>
                  {busy ? "Stamping…" : "Use this →"}
                </Text>
              </Pressable>
            );
          })}
        </View>
      </View>
    );
  };

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.navigate("/(tabs)/more" as any)} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>Mobile App Builder</Text>
        <View style={{ width: 50 }} />
      </View>

      {!connected ? (
        <ScrollView contentContainerStyle={{ padding: 20 }}>
          <Text style={{ color: c.textPrimary, fontSize: 18, fontWeight: "800", marginBottom: 8 }}>
            Connect a dev machine
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 14, lineHeight: 21, marginBottom: 18 }}>
            The wizard runs on your paired agent, then generates the monorepo scaffold there. Build from the phone, generate on the machine.
          </Text>
          {connecting ? <ActivityIndicator style={{ marginBottom: 16 }} /> : null}
          {devices.length === 0 ? (
            <Text style={{ color: c.textMuted, fontSize: 13 }}>
              No devices are registered yet. Run `brew install yaver && yaver auth && yaver serve` on your Mac.
            </Text>
          ) : (
            <View style={{ gap: 10 }}>
              {devices.map((device) => (
                <Pressable
                  key={device.id}
                  onPress={() => selectDevice(device)}
                  style={[
                    styles.deviceCard,
                    { backgroundColor: c.bgCard, borderColor: c.border },
                  ]}
                >
                  <View
                    style={{
                      width: 10,
                      height: 10,
                      borderRadius: 5,
                      backgroundColor: device.online ? "#22c55e" : c.textMuted,
                    }}
                  />
                  <View style={{ flex: 1 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700" }}>{device.name}</Text>
                    <Text style={{ color: c.textMuted, fontSize: 12 }}>{device.os}</Text>
                  </View>
                  <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>Connect</Text>
                </Pressable>
              ))}
            </View>
          )}
        </ScrollView>
      ) : result ? (
        <ScrollView contentContainerStyle={{ padding: 20 }}>
          <View
            style={[
              styles.completionHero,
              {
                backgroundColor: answers.surface_color || c.bgCard,
                borderColor: answers.accent_color || c.border,
              },
            ]}
          >
            <Text style={styles.completionBadge}>✨ Ready to vibe</Text>
            <Text style={styles.completionName}>
              {answers.app_name?.trim() || "Your app"}
            </Text>
            <Text style={styles.completionTagline}>
              {answers.tagline?.trim() ||
                answers.problem_statement?.trim() ||
                "The scaffold is on your dev machine."}
            </Text>
            <View style={styles.previewPaletteRow}>
              {[
                answers.primary_color,
                answers.secondary_color,
                answers.accent_color,
                answers.surface_color,
              ]
                .filter(Boolean)
                .map((color, idx) => (
                  <View
                    key={`${color}-${idx}`}
                    style={[styles.previewSwatch, { backgroundColor: color as string }]}
                  />
                ))}
            </View>
            <Text style={styles.completionPath}>{result.directory}</Text>
          </View>

          <View style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.panelTitle, { color: c.textPrimary }]}>Next moves</Text>
            {result.nextSteps.map((step, index) => (
              <View key={index} style={{ flexDirection: "row", marginTop: 8, gap: 8 }}>
                <Text style={{ color: c.accent, fontSize: 14, fontWeight: "800" }}>{index + 1}.</Text>
                <Text style={[styles.stepText, { color: c.textPrimary, flex: 1 }]}>{step}</Text>
              </View>
            ))}
          </View>

          <Pressable
            style={[styles.primaryButton, { backgroundColor: c.accent, marginTop: 16 }]}
            onPress={() => {
              setSession(null);
              setQuestion(null);
              setResult(null);
              setInput("");
              void start();
            }}
          >
            <Text style={styles.primaryButtonText}>Start another project</Text>
          </Pressable>
        </ScrollView>
      ) : loading && !question ? (
        <View style={styles.center}>
          <ActivityIndicator />
          <Text style={{ color: c.textMuted, marginTop: 12 }}>Loading builder…</Text>
        </View>
      ) : !question ? (
        <ScrollView contentContainerStyle={{ padding: 20 }}>
          {error ? <Text style={{ color: c.error, marginBottom: 12 }}>{error}</Text> : null}
          <Text style={{ color: c.textMuted, marginBottom: 16 }}>
            Could not start the project wizard. The connected agent may be too old for this flow.
          </Text>
          <Pressable onPress={() => void start()} style={[styles.primaryButton, { backgroundColor: c.accent }]}>
            <Text style={styles.primaryButtonText}>Retry</Text>
          </Pressable>
        </ScrollView>
      ) : (
        <View style={{ flex: 1 }}>
          {renderStageStrip()}
          <ScrollView
            contentContainerStyle={{ padding: 20, paddingBottom: 36 }}
            keyboardShouldPersistTaps="handled"
          >
            {renderPreviewCard()}

            <View style={[styles.hero, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.heroEyebrow, { color: c.accent }]}>
                {STAGE_META[currentStage].emoji} {STAGE_META[currentStage].title} · Step{" "}
                {Math.min(currentIndex + 1, progressTotal)} / {progressTotal}
              </Text>
              <Text style={[styles.heroTitle, { color: c.textPrimary }]}>
                {STAGE_META[currentStage].blurb}
              </Text>
              <View style={[styles.progressTrack, { backgroundColor: c.border }]}>
                <View
                  style={[styles.progressFill, { backgroundColor: c.accent, width: `${progressValue * 100}%` }]}
                />
              </View>
              {summary.length ? (
                <View style={styles.summaryRow}>
                  {summary.map((item) => (
                    <View key={item} style={[styles.summaryPill, { backgroundColor: c.accent + "14" }]}>
                      <Text style={[styles.summaryPillText, { color: c.textPrimary }]}>{item}</Text>
                    </View>
                  ))}
                </View>
              ) : null}
              <Pressable
                onPress={() => router.navigate("/(tabs)/designmode" as any)}
                style={[styles.designModeLink, { borderColor: c.border, backgroundColor: c.bg }]}
              >
                <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 13 }}>
                  Have a visual reference already? Import Figma in Design Mode.
                </Text>
              </Pressable>
            </View>

            {renderPresetRow()}

            {error ? <Text style={{ color: c.error, marginBottom: 12 }}>{error}</Text> : null}

            <View style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.panelTitle, { color: c.textPrimary }]}>{question.prompt}</Text>
              {question.help ? (
                <Text style={[styles.panelBody, { color: c.textMuted }]}>{question.help}</Text>
              ) : null}
              <View style={{ marginTop: 18 }}>{renderField()}</View>
            </View>

            <View style={styles.actions}>
              {question.kind !== "confirm" && question.kind !== "done" ? (
                <Pressable
                  style={[styles.secondaryButton, { borderColor: c.border, backgroundColor: c.bgCard }]}
                  onPress={() => void skipThis()}
                  disabled={loading || skipping}
                >
                  <Text style={[styles.secondaryButtonText, { color: c.textPrimary }]}>Skip this</Text>
                </Pressable>
              ) : null}
              <Pressable
                style={[styles.secondaryButton, { borderColor: c.border, backgroundColor: c.bgCard }]}
                onPress={() => void useDefaultsForRest()}
                disabled={loading || skipping}
              >
                <Text style={[styles.secondaryButtonText, { color: c.textPrimary }]}>
                  {skipping ? "Skipping…" : "Use defaults for the rest"}
                </Text>
              </Pressable>
              <Pressable
                style={[styles.primaryButton, { backgroundColor: c.accent, opacity: loading ? 0.6 : 1 }]}
                onPress={() => void handleNext()}
                disabled={loading || skipping}
              >
                <Text style={styles.primaryButtonText}>
                  {loading
                    ? "Working…"
                    : question.kind === "confirm" || question.kind === "done"
                      ? "Generate project"
                      : "Continue"}
                </Text>
              </Pressable>
            </View>
          </ScrollView>
        </View>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingBottom: 12,
    borderBottomWidth: 1,
  },
  center: { flex: 1, alignItems: "center", justifyContent: "center" },
  stageChip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    borderWidth: 1,
    borderRadius: 999,
    paddingHorizontal: 12,
    paddingVertical: 7,
  },
  stageEmoji: { fontSize: 14 },
  stageLabel: { fontSize: 12, fontWeight: "800", letterSpacing: 0.2 },
  previewCard: {
    borderWidth: 1,
    borderRadius: 24,
    padding: 18,
    marginBottom: 16,
    gap: 12,
  },
  previewTopRow: { flexDirection: "row", gap: 14, alignItems: "flex-start" },
  previewIcon: {
    width: 52,
    height: 52,
    borderRadius: 16,
    alignItems: "center",
    justifyContent: "center",
  },
  previewName: { color: "#ffffff", fontSize: 20, fontWeight: "800" },
  previewTagline: { color: "rgba(255,255,255,0.85)", fontSize: 14, marginTop: 4, lineHeight: 19 },
  previewPaletteRow: { flexDirection: "row", gap: 8 },
  previewSwatch: { width: 26, height: 26, borderRadius: 13, borderWidth: 1, borderColor: "rgba(255,255,255,0.2)" },
  previewNavRow: { flexDirection: "row", flexWrap: "wrap", gap: 6 },
  previewNavChip: {
    borderWidth: 1,
    borderRadius: 999,
    paddingHorizontal: 10,
    paddingVertical: 5,
  },
  previewNavChipText: { color: "rgba(255,255,255,0.88)", fontSize: 11, fontWeight: "700" },
  previewFacts: { flexDirection: "row", flexWrap: "wrap", gap: 12 },
  previewFact: { color: "rgba(255,255,255,0.72)", fontSize: 12, fontWeight: "700" },
  hero: {
    borderWidth: 1,
    borderRadius: 24,
    padding: 18,
    marginBottom: 16,
  },
  heroEyebrow: { fontSize: 12, fontWeight: "800", letterSpacing: 0.6, textTransform: "uppercase" },
  heroTitle: { fontSize: 22, fontWeight: "800", marginTop: 10, lineHeight: 29 },
  progressTrack: { height: 8, borderRadius: 999, overflow: "hidden", marginTop: 16 },
  progressFill: { height: "100%", borderRadius: 999 },
  summaryRow: { flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 14 },
  summaryPill: { borderRadius: 999, paddingHorizontal: 12, paddingVertical: 7 },
  summaryPillText: { fontSize: 12, fontWeight: "700" },
  designModeLink: { marginTop: 14, borderWidth: 1, borderRadius: 16, paddingHorizontal: 14, paddingVertical: 12 },
  presetPanel: { marginBottom: 16 },
  presetCard: {
    borderWidth: 1,
    borderRadius: 20,
    paddingHorizontal: 14,
    paddingVertical: 14,
    width: "48%",
    gap: 4,
  },
  presetTitle: { fontSize: 15, fontWeight: "800" },
  presetBlurb: { fontSize: 12, lineHeight: 17 },
  panel: {
    borderWidth: 1,
    borderRadius: 24,
    padding: 18,
  },
  panelTitle: { fontSize: 21, fontWeight: "800" },
  panelBody: { fontSize: 14, lineHeight: 21, marginTop: 8 },
  input: {
    borderWidth: 1,
    borderRadius: 18,
    paddingHorizontal: 14,
    paddingVertical: 14,
    fontSize: 16,
  },
  choiceGrid: { gap: 10 },
  choiceCard: {
    borderWidth: 1,
    borderRadius: 18,
    padding: 14,
  },
  choiceCardRow: { flexDirection: "row", alignItems: "flex-start", gap: 12 },
  choiceEmoji: { fontSize: 22, lineHeight: 26 },
  choiceLabel: { fontSize: 16, fontWeight: "700" },
  choiceHint: { fontSize: 12, marginTop: 4, lineHeight: 16 },
  swatchRow: { flexDirection: "row", flexWrap: "wrap", gap: 10 },
  swatch: {
    width: 36,
    height: 36,
    borderRadius: 18,
    borderWidth: 2,
  },
  actions: { gap: 10, marginTop: 16 },
  primaryButton: {
    borderRadius: 18,
    alignItems: "center",
    justifyContent: "center",
    paddingVertical: 15,
  },
  primaryButtonText: { color: "#fff", fontSize: 15, fontWeight: "800" },
  secondaryButton: {
    borderWidth: 1,
    borderRadius: 18,
    alignItems: "center",
    justifyContent: "center",
    paddingVertical: 14,
  },
  secondaryButtonText: { fontSize: 14, fontWeight: "700" },
  deviceCard: {
    borderWidth: 1,
    borderRadius: 16,
    padding: 14,
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
  stepText: { fontSize: 14, lineHeight: 21, marginTop: 0 },
  completionHero: {
    borderWidth: 1,
    borderRadius: 28,
    padding: 22,
    marginBottom: 16,
    gap: 10,
  },
  completionBadge: {
    color: "rgba(255,255,255,0.9)",
    fontSize: 12,
    fontWeight: "800",
    letterSpacing: 0.6,
    textTransform: "uppercase",
  },
  completionName: { color: "#ffffff", fontSize: 32, fontWeight: "800" },
  completionTagline: { color: "rgba(255,255,255,0.9)", fontSize: 15, lineHeight: 21 },
  completionPath: { color: "rgba(255,255,255,0.65)", fontSize: 11, fontFamily: "Menlo", marginTop: 10 },
});
