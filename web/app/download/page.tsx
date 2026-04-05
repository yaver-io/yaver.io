"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { CONVEX_URL } from "@/lib/constants";

const GITHUB_RELEASE = "https://github.com/kivanccakmak/yaver.io/releases/latest";

export default function DownloadPage() {
  const [cliVersion, setCliVersion] = useState<string>("");
  const [mobileVersion, setMobileVersion] = useState<string>("");

  useEffect(() => {
    fetch(`${CONVEX_URL}/config`)
      .then((res) => res.json())
      .then((data) => {
        if (data.cliVersion) setCliVersion(data.cliVersion);
        if (data.mobileVersion) setMobileVersion(data.mobileVersion);
      })
      .catch(() => {});
  }, []);

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

        {/* Go Agent */}
        <div className="mb-12">
          <h2 className="mb-6 text-xs font-semibold uppercase tracking-wider text-surface-500">
            Go Agent {cliVersion && <span className="normal-case tracking-normal text-surface-600">v{cliVersion}</span>}
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
              <p className="mb-2 text-xs text-surface-500">Scoop (Windows)</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">&gt;</span>{" "}
                <span className="text-surface-300 select-all">
                  scoop bucket add yaver https://github.com/kivanccakmak/scoop-yaver && scoop install yaver
                </span>
              </div>
            </div>
            <p className="text-xs text-surface-500">
              Or download binaries from{" "}
              <a href={GITHUB_RELEASE} target="_blank" rel="noopener noreferrer" className="text-surface-300 underline hover:text-surface-100">GitHub Releases</a>.
            </p>
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

        {/* SDKs & CLI tools */}
        <div className="mb-12">
          <h2 className="mb-6 text-xs font-semibold uppercase tracking-wider text-surface-500">
            SDKs &amp; CLI tools
          </h2>
          <div className="card space-y-4">
            <div>
              <p className="mb-2 text-xs text-surface-500">Push-to-Device CLI (React Native developers)</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  npm install -g yaver-cli
                </span>
              </div>
            </div>
            <div>
              <p className="mb-2 text-xs text-surface-500">JavaScript / TypeScript SDK</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  npm install yaver-sdk
                </span>
              </div>
            </div>
            <div>
              <p className="mb-2 text-xs text-surface-500">Python SDK</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  pip install yaver
                </span>
              </div>
            </div>
            <div>
              <p className="mb-2 text-xs text-surface-500">Flutter / Dart SDK</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  flutter pub add yaver
                </span>
              </div>
            </div>
            <div>
              <p className="mb-2 text-xs text-surface-500">Feedback SDK (React Native)</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  npm install yaver-feedback-react-native
                </span>
              </div>
            </div>
            <div>
              <p className="mb-2 text-xs text-surface-500">Feedback SDK (Web)</p>
              <div className="rounded-lg bg-surface-950 px-4 py-3 font-mono text-[13px]">
                <span className="text-surface-500">$</span>{" "}
                <span className="text-surface-300 select-all">
                  npm install @yaver/feedback-web
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
            <div className="card">
              <h3 className="mb-1 text-base font-semibold text-surface-50">
                iOS
              </h3>
              <p className="mb-5 text-xs text-surface-500">
                iOS 16+. iPhone and iPad.
              </p>
              <div className="flex flex-wrap gap-2">
                <a
                  href="https://apps.apple.com/app/yaver/id6746057981"
                  className="btn-primary py-2 px-4 text-xs"
                >
                  App Store
                </a>
              </div>
            </div>
            <div className="card">
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
            href={GITHUB_RELEASE}
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
