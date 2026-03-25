"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { CONVEX_URL } from "@/lib/constants";

type Platform = "macos" | "linux" | "ios" | "android" | "unknown";

function detectPlatform(): Platform {
  if (typeof window === "undefined") return "unknown";
  const ua = navigator.userAgent.toLowerCase();
  if (ua.includes("iphone") || ua.includes("ipad")) return "ios";
  if (ua.includes("android")) return "android";
  if (ua.includes("mac")) return "macos";
  if (ua.includes("linux")) return "linux";
  return "unknown";
}

function formatSize(bytes: number): string {
  return `${(bytes / 1024 / 1024).toFixed(0)} MB`;
}

const GITHUB_CLI = "https://github.com/kivanccakmak/yaver.io/releases/latest";
const GITHUB_RELEASE = "https://github.com/kivanccakmak/yaver.io/releases/latest";

function ghCliUrl(filename: string): string {
  return `${GITHUB_CLI}/download/${filename}`;
}

export default function DownloadPage() {
  const [platform, setPlatform] = useState<Platform>("unknown");
  const [cliVersion, setCliVersion] = useState<string>("");
  const [mobileVersion, setMobileVersion] = useState<string>("");

  useEffect(() => {
    setPlatform(detectPlatform());

    // Fetch versions from config (no downloads list needed — all from GitHub)
    fetch(`${CONVEX_URL}/config`)
      .then((res) => res.json())
      .then((data) => {
        if (data.cliVersion) setCliVersion(data.cliVersion);
        if (data.mobileVersion) setMobileVersion(data.mobileVersion);
      })
      .catch(() => {});
  }, []);

  function ghButton(label: string, filename: string, primary = false) {
    return (
      <a
        key={label}
        href={ghCliUrl(filename)}
        className={primary ? "btn-primary py-2 px-4 text-xs" : "btn-secondary py-2 px-4 text-xs"}
      >
        {label}
      </a>
    );
  }

  const versionBadge = cliVersion ? (
    <span className="ml-2 rounded-full bg-surface-800 px-2 py-0.5 text-[10px] font-medium text-surface-400">
      v{cliVersion}
    </span>
  ) : null;

  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-4xl">
        <div className="mb-16 text-center">
          <h1 className="mb-4 text-3xl font-bold text-surface-50 md:text-4xl">
            Download
          </h1>
          <p className="text-sm text-surface-500">
            Install the CLI on your dev machine. Get the app on your phone.
            {versionBadge}
          </p>
        </div>

        {/* Desktop CLI */}
        <div className="mb-12">
          <h2 className="mb-6 text-xs font-semibold uppercase tracking-wider text-surface-500">
            Desktop CLI {cliVersion && <span className="normal-case tracking-normal text-surface-600">v{cliVersion}</span>}
          </h2>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            {[
              {
                name: "macOS",
                desc: "macOS 13+ (Apple Silicon & Intel)",
                highlighted: platform === "macos",
                buttons: [
                  { label: "Apple Silicon", file: "yaver-darwin-arm64", primary: true },
                  { label: "Intel", file: "yaver-darwin-amd64" },
                ],
              },
              {
                name: "Linux",
                desc: "x86_64 & ARM64",
                highlighted: platform === "linux",
                buttons: [
                  { label: "x86_64", file: "yaver-linux-amd64", primary: true },
                  { label: "ARM64", file: "yaver-linux-arm64" },
                ],
              },
            ].map((p) => (
              <div
                key={p.name}
                className={`card ${p.highlighted ? "border-surface-600" : ""}`}
              >
                {p.highlighted && (
                  <div className="mb-3 text-xs text-surface-400">
                    Detected your platform
                  </div>
                )}
                <h3 className="mb-1 text-base font-semibold text-surface-50">
                  {p.name}
                </h3>
                <p className="mb-5 text-xs text-surface-500">{p.desc}</p>
                <div className="flex flex-wrap gap-2">
                  {p.buttons.map((btn) =>
                    ghButton(btn.label, btn.file, btn.primary)
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* Package managers */}
        <div className="mb-12">
          <h2 className="mb-6 text-xs font-semibold uppercase tracking-wider text-surface-500">
            Package managers
          </h2>
          <div className="card space-y-4">
            <div>
              <p className="mb-2 text-xs text-surface-500">Homebrew (macOS / Linux)</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  brew install kivanccakmak/yaver/yaver
                </span>
              </div>
            </div>
            <div>
              <p className="mb-2 text-xs text-surface-500">Arch Linux (AUR)</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px] space-y-1">
                <div>
                  <span className="text-surface-500">$</span>{" "}
                  <span className="text-surface-300 select-all">
                    git clone https://github.com/kivanccakmak/aur-yaver.git && cd aur-yaver && makepkg -si
                  </span>
                </div>
              </div>
            </div>
            <div>
              <p className="mb-2 text-xs text-surface-500">Nix</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  nix run github:kivanccakmak/yaver.io -- version
                </span>
              </div>
            </div>
            <div>
              <p className="mb-2 text-xs text-surface-500">apt (Debian / Ubuntu)</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px] space-y-1">
                <div>
                  <span className="text-surface-500">$</span>{" "}
                  <span className="text-surface-300 select-all">
                    curl -fsSL https://raw.githubusercontent.com/kivanccakmak/apt-yaver/main/KEY.gpg | sudo gpg --dearmor -o /usr/share/keyrings/yaver.gpg
                  </span>
                </div>
                <div>
                  <span className="text-surface-500">$</span>{" "}
                  <span className="text-surface-300 select-all">
                    echo &quot;deb [signed-by=/usr/share/keyrings/yaver.gpg] https://raw.githubusercontent.com/kivanccakmak/apt-yaver/main ./ stable main&quot; | sudo tee /etc/apt/sources.list.d/yaver.list
                  </span>
                </div>
                <div>
                  <span className="text-surface-500">$</span>{" "}
                  <span className="text-surface-300 select-all">
                    sudo apt update && sudo apt install yaver
                  </span>
                </div>
              </div>
            </div>
            <div>
              <p className="mb-2 text-xs text-surface-500">RPM (Fedora / RHEL)</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  sudo rpm -i https://github.com/kivanccakmak/yaver.io/releases/latest/download/yaver_{cliVersion || "latest"}_aarch64.rpm
                </span>
              </div>
            </div>
            <div>
              <p className="mb-2 text-xs text-surface-500">Docker</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  docker run --rm kivanccakmak/yaver-cli version
                </span>
              </div>
            </div>
            <div>
              <p className="mb-2 text-xs text-surface-500">Quick install (macOS / Linux)</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  curl -fsSL https://yaver.io/install.sh | sh
                </span>
              </div>
            </div>
          </div>
        </div>

        {/* Update existing installation */}
        <div className="mb-12">
          <h2 className="mb-6 text-xs font-semibold uppercase tracking-wider text-surface-500">
            Update existing installation
          </h2>
          <div className="card space-y-4">
            <div>
              <p className="mb-2 text-xs text-surface-500">Homebrew (macOS / Linux)</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  brew upgrade yaver
                </span>
              </div>
            </div>
            <div>
              <p className="mb-2 text-xs text-surface-500">Quick update (macOS / Linux)</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  curl -fsSL https://yaver.io/install.sh | sh
                </span>
              </div>
            </div>
            <div>
              <p className="mb-2 text-xs text-surface-500">Check current version</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  yaver version
                </span>
              </div>
            </div>
          </div>
        </div>

        {/* Mobile app */}
        <div className="mb-12">
          <h2 className="mb-6 text-xs font-semibold uppercase tracking-wider text-surface-500">
            Mobile app {mobileVersion && <span className="normal-case tracking-normal text-surface-600">v{mobileVersion}</span>}
          </h2>

          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <div
              className={`card ${platform === "ios" ? "border-surface-600" : ""}`}
            >
              {platform === "ios" && (
                <div className="mb-3 text-xs text-surface-400">
                  Detected your platform
                </div>
              )}
              <h3 className="mb-1 text-base font-semibold text-surface-50">
                iOS
              </h3>
              <p className="mb-5 text-xs text-surface-500">
                iOS 16+. iPhone and iPad.
              </p>
              <div className="flex flex-wrap gap-2">
                <a
                  href="https://testflight.apple.com/join/yaver"
                  className="btn-primary py-2 px-4 text-xs"
                >
                  TestFlight Beta
                </a>
                <a
                  href="https://apps.apple.com/app/yaver/id6746057981"
                  className="btn-secondary py-2 px-4 text-xs"
                >
                  App Store
                </a>
              </div>
            </div>
            <div
              className={`card ${platform === "android" ? "border-surface-600" : ""}`}
            >
              {platform === "android" && (
                <div className="mb-3 text-xs text-surface-400">
                  Detected your platform
                </div>
              )}
              <h3 className="mb-1 text-base font-semibold text-surface-50">
                Android
              </h3>
              <p className="mb-5 text-xs text-surface-500">Android 12+.</p>
              <div className="flex flex-wrap gap-2">
                <a
                  href="https://play.google.com/store/apps/details?id=io.yaver.mobile"
                  className="btn-primary py-2 px-4 text-xs"
                >
                  Google Play
                </a>
                <a
                  href={`https://github.com/kivanccakmak/yaver.io/releases/latest/download/Yaver-latest.apk`}
                  className="btn-secondary py-2 px-4 text-xs"
                >
                  APK Download
                </a>
              </div>
            </div>
          </div>
        </div>

        {/* GitHub link */}
        <div className="text-center space-y-3">
          <a
            href={GITHUB_CLI}
            className="text-xs text-surface-400 hover:text-surface-50 underline underline-offset-2"
          >
            All releases on GitHub
          </a>
          <br />
          <Link
            href="/"
            className="text-xs text-surface-500 hover:text-surface-50"
          >
            Back to home
          </Link>
        </div>
      </div>
    </div>
  );
}
