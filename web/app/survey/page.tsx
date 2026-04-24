"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { CONVEX_URL } from "@/lib/constants";
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";

const IDENTITIES = [
  { id: "developer", label: "Developer" },
  { id: "business", label: "Business Owner" },
  { id: "student", label: "Student / Academic" },
  { id: "other", label: "Other" },
] as const;

const LANGUAGES = [
  "JavaScript/TypeScript",
  "Python",
  "Go",
  "Rust",
  "Java",
  "C/C++",
  "Ruby",
  "PHP",
  "Swift",
  "Kotlin",
  "C#",
  "Other",
] as const;

const EXPERIENCE_LEVELS = ["Junior", "Mid-Level", "Senior", "Staff/Lead"] as const;
const USE_CASES = [
  "Work / Business",
  "Hobby Projects",
  "Academic / Research",
  "Open Source",
  "Freelance / Consulting",
  "Other",
] as const;

function ChoiceButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`rounded-xl border px-3 py-2 text-sm transition-colors ${
        active
          ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-100"
          : "border-surface-800 bg-surface-900 text-surface-300 hover:border-surface-700 hover:text-surface-100"
      }`}
    >
      {children}
    </button>
  );
}

export default function SurveyPage() {
  const router = useRouter();
  const { user, token, isLoading, isAuthenticated, surveyCompleted } = useAuth();
  const { devices, refreshDevices } = useDevices(token);

  const [fullName, setFullName] = useState("");
  const [identity, setIdentity] = useState<string>("developer");
  const [languages, setLanguages] = useState<string[]>(["JavaScript/TypeScript"]);
  const [experience, setExperience] = useState<string>("Mid-Level");
  const [useCase, setUseCase] = useState<string>("Work / Business");
  const [status, setStatus] = useState<"idle" | "submitting" | "error">("idle");
  const [error, setError] = useState<string>("");
  const [refreshingDevices, setRefreshingDevices] = useState(false);

  useEffect(() => {
    if (!user?.name) return;
    setFullName((current) => current || user.name || "");
  }, [user?.name]);

  useEffect(() => {
    if (!isLoading && !isAuthenticated) {
      router.replace("/auth?return=/survey");
    }
  }, [isAuthenticated, isLoading, router]);

  useEffect(() => {
    if (!isLoading && devices.length > 0) {
      router.replace("/dashboard");
    }
  }, [devices.length, isLoading, router]);

  const isDeveloper = identity === "developer";
  const hasMachine = devices.length > 0;
  const surveyDoneWithoutMachine = surveyCompleted && !hasMachine;

  const visibleDevices = useMemo(
    () => devices.slice(0, 4),
    [devices],
  );

  const toggleLanguage = (language: string) => {
    setLanguages((current) =>
      current.includes(language)
        ? current.filter((entry) => entry !== language)
        : [...current, language],
    );
  };

  const handleRefreshDevices = async () => {
    setRefreshingDevices(true);
    try {
      await refreshDevices();
    } finally {
      setRefreshingDevices(false);
    }
  };

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!token) return;

    setStatus("submitting");
    setError("");

    try {
      const response = await fetch(`${CONVEX_URL}/survey/submit`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
          isDeveloper,
          fullName: fullName.trim() || undefined,
          languages: isDeveloper && languages.length > 0 ? languages : undefined,
          experienceLevel: isDeveloper ? experience : undefined,
          role: identity,
          useCase,
        }),
      });

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.error || "Failed to submit survey");
      }

      router.replace("/dashboard");
      router.refresh();
    } catch (err) {
      setStatus("error");
      setError(err instanceof Error ? err.message : "Failed to submit survey");
    }
  };

  if (isLoading) {
    return (
      <div className="flex min-h-[80vh] items-center justify-center">
        <div className="h-6 w-6 animate-spin rounded-full border-2 border-surface-600 border-t-emerald-400" />
      </div>
    );
  }

  if (!isAuthenticated || !token) {
    return null;
  }

  return (
    <div className="mx-auto flex min-h-[calc(100vh-4rem)] w-full max-w-6xl flex-col gap-6 px-6 py-10 lg:flex-row">
      <section className="lg:w-[28rem]">
        <div className="rounded-3xl border border-surface-800 bg-surface-950/90 p-6 shadow-2xl shadow-black/20">
          <div className="mb-5">
            <p className="text-xs font-semibold uppercase tracking-[0.18em] text-emerald-300">Finish Setup</p>
            <h1 className="mt-2 text-3xl font-semibold tracking-tight text-surface-50">
              {surveyDoneWithoutMachine ? "No machine connected yet" : "Tell us a bit about you"}
            </h1>
            <p className="mt-3 text-sm leading-6 text-surface-400">
              {surveyDoneWithoutMachine
                ? "Your account is ready, but this web signup has not connected any machine yet. Install Yaver on a dev box, sign in there, then refresh this page."
                : "Complete the short survey, then connect a machine to this account so the dashboard has somewhere to route work."}
            </p>
          </div>

          {!surveyDoneWithoutMachine ? (
            <form onSubmit={handleSubmit} className="space-y-6">
              <div>
                <label className="mb-2 block text-xs font-semibold uppercase tracking-[0.16em] text-surface-500">
                  Name
                </label>
                <input
                  value={fullName}
                  onChange={(event) => setFullName(event.target.value)}
                  placeholder="Your name"
                  className="w-full rounded-xl border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-100 outline-none transition-colors focus:border-surface-500"
                />
              </div>

              <div>
                <label className="mb-2 block text-xs font-semibold uppercase tracking-[0.16em] text-surface-500">
                  Identity
                </label>
                <div className="grid grid-cols-2 gap-2">
                  {IDENTITIES.map((item) => (
                    <ChoiceButton key={item.id} active={identity === item.id} onClick={() => setIdentity(item.id)}>
                      {item.label}
                    </ChoiceButton>
                  ))}
                </div>
              </div>

              {isDeveloper ? (
                <>
                  <div>
                    <label className="mb-2 block text-xs font-semibold uppercase tracking-[0.16em] text-surface-500">
                      Languages
                    </label>
                    <div className="flex flex-wrap gap-2">
                      {LANGUAGES.map((language) => (
                        <ChoiceButton
                          key={language}
                          active={languages.includes(language)}
                          onClick={() => toggleLanguage(language)}
                        >
                          {language}
                        </ChoiceButton>
                      ))}
                    </div>
                  </div>

                  <div>
                    <label className="mb-2 block text-xs font-semibold uppercase tracking-[0.16em] text-surface-500">
                      Experience
                    </label>
                    <div className="grid grid-cols-2 gap-2">
                      {EXPERIENCE_LEVELS.map((level) => (
                        <ChoiceButton key={level} active={experience === level} onClick={() => setExperience(level)}>
                          {level}
                        </ChoiceButton>
                      ))}
                    </div>
                  </div>
                </>
              ) : null}

              <div>
                <label className="mb-2 block text-xs font-semibold uppercase tracking-[0.16em] text-surface-500">
                  Primary Use Case
                </label>
                <div className="grid grid-cols-2 gap-2">
                  {USE_CASES.map((option) => (
                    <ChoiceButton key={option} active={useCase === option} onClick={() => setUseCase(option)}>
                      {option}
                    </ChoiceButton>
                  ))}
                </div>
              </div>

              {error ? (
                <div className="rounded-2xl border border-red-500/20 bg-red-500/10 px-4 py-3 text-sm text-red-300">
                  {error}
                </div>
              ) : null}

              <div className="flex items-center gap-3">
                <button
                  type="submit"
                  disabled={status === "submitting"}
                  className="rounded-xl bg-surface-50 px-4 py-3 text-sm font-semibold text-surface-950 transition-colors hover:bg-surface-200 disabled:opacity-50"
                >
                  {status === "submitting" ? "Saving..." : "Save and continue"}
                </button>
                <span className="text-xs text-surface-500">
                  You can change this later.
                </span>
              </div>
            </form>
          ) : (
            <div className="rounded-2xl border border-amber-500/20 bg-amber-500/10 px-4 py-4 text-sm text-amber-100">
              Survey saved. The next step is connecting at least one machine to this account.
            </div>
          )}
        </div>
      </section>

      <section className="min-w-0 flex-1">
        <div className="rounded-3xl border border-surface-800 bg-surface-950/70 p-6">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <p className="text-xs font-semibold uppercase tracking-[0.18em] text-surface-500">Machines</p>
              <h2 className="mt-2 text-2xl font-semibold text-surface-50">
                {hasMachine ? "Machine detected" : "You have no machine on this account"}
              </h2>
              <p className="mt-2 max-w-2xl text-sm leading-6 text-surface-400">
                {hasMachine
                  ? "A Yaver machine is already registered to this account. You can head to the dashboard, or refresh if you just authenticated another box."
                  : "This browser signup did not register any device. Install Yaver on the machine that should run your tasks, authenticate it with the same account, then refresh devices here."}
              </p>
            </div>
            <button
              type="button"
              onClick={handleRefreshDevices}
              disabled={refreshingDevices}
              className="rounded-xl border border-surface-700 bg-surface-900 px-4 py-2 text-sm font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50 disabled:opacity-50"
            >
              {refreshingDevices ? "Refreshing..." : "Refresh devices"}
            </button>
          </div>

          {!hasMachine ? (
            <div className="mt-6 grid gap-4 lg:grid-cols-[1.1fr_0.9fr]">
              <div className="rounded-2xl border border-surface-800 bg-surface-900/80 p-5">
                <p className="text-xs font-semibold uppercase tracking-[0.18em] text-emerald-300">Quick Start</p>
                <div className="mt-4 space-y-3 text-sm text-surface-300">
                  <div>
                    <div className="text-xs uppercase tracking-[0.16em] text-surface-500">1. Install</div>
                    <code className="mt-1 block rounded-xl bg-surface-950 px-3 py-2 text-[13px] text-surface-100">npm install -g yaver-cli</code>
                  </div>
                  <div>
                    <div className="text-xs uppercase tracking-[0.16em] text-surface-500">2. Sign in on that machine</div>
                    <code className="mt-1 block rounded-xl bg-surface-950 px-3 py-2 text-[13px] text-surface-100">yaver auth</code>
                  </div>
                  <div>
                    <div className="text-xs uppercase tracking-[0.16em] text-surface-500">3. Start the agent</div>
                    <code className="mt-1 block rounded-xl bg-surface-950 px-3 py-2 text-[13px] text-surface-100">yaver serve</code>
                  </div>
                </div>
              </div>

              <div className="rounded-2xl border border-amber-500/20 bg-amber-500/10 p-5">
                <p className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-200">If Auth Already Ran</p>
                <p className="mt-3 text-sm leading-6 text-amber-100/90">
                  If browser OAuth already succeeded on the machine but it still does not appear here, clear stale local auth and sign in again.
                </p>
                <code className="mt-4 block rounded-xl bg-surface-950 px-3 py-2 text-[13px] text-surface-100">yaver auth factory-reset</code>
                <div className="mt-4 flex flex-wrap gap-3">
                  <Link href="/download" className="rounded-xl border border-surface-700 bg-surface-950 px-4 py-2 text-sm font-medium text-surface-100 hover:border-surface-600">
                    Download Yaver
                  </Link>
                  <Link href="/manuals/cli-setup" className="rounded-xl border border-amber-500/30 bg-amber-500/10 px-4 py-2 text-sm font-medium text-amber-100 hover:bg-amber-500/15">
                    CLI setup guide
                  </Link>
                </div>
              </div>
            </div>
          ) : (
            <div className="mt-6 space-y-3">
              {visibleDevices.map((device) => (
                <div key={device.id} className="rounded-2xl border border-emerald-500/20 bg-emerald-500/[0.05] px-4 py-3">
                  <div className="flex flex-wrap items-center gap-2">
                    <p className="text-sm font-semibold text-surface-100">{device.name}</p>
                    <span className={`inline-flex h-2.5 w-2.5 rounded-full ${device.online ? "bg-emerald-400" : "bg-surface-600"}`} />
                    <span className="text-xs text-surface-400">{device.online ? "online" : "offline"}</span>
                  </div>
                  <p className="mt-1 text-xs text-surface-500">
                    {device.platform || "unknown"}{device.host ? ` · ${device.host}:${device.port}` : ""}
                  </p>
                </div>
              ))}

              <div className="pt-2">
                <Link href="/dashboard" className="rounded-xl bg-surface-50 px-4 py-3 text-sm font-semibold text-surface-950 hover:bg-surface-200">
                  Open dashboard
                </Link>
              </div>
            </div>
          )}
        </div>
      </section>
    </div>
  );
}
