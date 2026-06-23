import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "mobile-beta-testing-apple-google";
const post = postBySlug(POST_SLUG)!;
const POST_URL = `https://yaver.io/blog/${POST_SLUG}`;

export const metadata: Metadata = {
  title: `${post.title} — Yaver Blog`,
  description: post.description,
  alternates: { canonical: POST_URL },
  openGraph: {
    title: post.title,
    description: post.description,
    url: POST_URL,
    siteName: "Yaver",
    type: "article",
    publishedTime: post.date,
    authors: ["Yaver"],
    tags: [
      "TestFlight",
      "Google Play",
      "Internal Testing",
      "App Store Connect",
      "Beta Testing",
      "iOS",
      "Android",
      "Release",
    ],
    images: [{ url: "/og-image.png", width: 1200, height: 630 }],
  },
  twitter: {
    card: "summary_large_image",
    title: post.title,
    description: post.description,
    images: ["/og-image.png"],
  },
};

const articleLd = {
  "@context": "https://schema.org",
  "@type": "BlogPosting",
  headline: post.title,
  description: post.description,
  datePublished: post.date,
  dateModified: post.date,
  url: POST_URL,
  mainEntityOfPage: POST_URL,
  image: "https://yaver.io/og-image.png",
  author: { "@type": "Organization", name: "Yaver", url: "https://yaver.io" },
  publisher: {
    "@type": "Organization",
    name: "Yaver",
    url: "https://yaver.io",
    logo: { "@type": "ImageObject", url: "https://yaver.io/icon-512.png" },
  },
};

const code = (s: string) => (
  <code className="rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200">{s}</code>
);

function Block({ children }: { children: string }) {
  return (
    <pre className="overflow-x-auto rounded-xl border border-surface-800 bg-surface-900 p-4 text-[12px] leading-6 text-surface-200">
      <code>{children}</code>
    </pre>
  );
}

function Th({ children }: { children: React.ReactNode }) {
  return (
    <th className="border-b border-surface-700 px-3 py-2 text-left text-xs font-semibold uppercase tracking-wide text-surface-300">
      {children}
    </th>
  );
}

function Td({ children }: { children: React.ReactNode }) {
  return <td className="border-b border-surface-800 px-3 py-2 align-top text-surface-300">{children}</td>;
}

export default function MobileBetaTestingBlogPage() {
  return (
    <div className="px-6 py-20">
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(articleLd) }} />
      <article className="mx-auto max-w-3xl space-y-8 text-sm leading-7 text-surface-300">
        <Link href="/blog" className="inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50">
          &larr; Back to Blog
        </Link>

        <header className="space-y-4">
          <time dateTime={post.date} className="text-xs uppercase tracking-[0.2em] text-surface-500">
            {post.date}
          </time>
          <h1 className="text-3xl font-bold text-surface-50 md:text-4xl">
            Ship to testers: TestFlight &amp; Google Play internal testing, end to end
          </h1>
          <p className="text-surface-400">
            Getting a build into a tester&apos;s hands is two completely different bureaucracies wearing the
            same hat. Apple wants a paid Developer Program membership, App Store Connect roles, and the
            TestFlight app on the device. Google wants a Play Console account, a tester list, an opt-in
            link, and a service account if you ever want to automate the upload. This is the field guide
            for both — the accounts you need, who can invite whom, exactly how a tester downloads the
            build, and the one-command path Yaver uses to push to both stores from a laptop.
          </p>
        </header>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The mental model: two roles on each side</h2>
          <p>
            Every store distinguishes the <strong className="text-surface-100">developer</strong> (the
            account that owns the app and uploads builds) from the <strong className="text-surface-100">tester</strong>
            {" "}(a person allowed to download a pre-release build). On both stores, the developer adds
            the tester by <em>email</em>; the tester accepts in a dedicated app. The differences are all
            in the paperwork around those two roles.
          </p>
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-[13px]">
              <thead>
                <tr>
                  <Th>&nbsp;</Th>
                  <Th>Apple</Th>
                  <Th>Google</Th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <Td><strong className="text-surface-100">Developer account</strong></Td>
                  <Td>Apple Developer Program — {code("$99/yr")}</Td>
                  <Td>Google Play Console — {code("$25 once")}</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">Tester app</strong></Td>
                  <Td>TestFlight</Td>
                  <Td>Google Play Store (normal app)</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">Fastest tester tier</strong></Td>
                  <Td>Internal — up to 100, no review</Td>
                  <Td>Internal testing — up to 100, no review</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">Largest tier</strong></Td>
                  <Td>External — up to 10,000 (beta review)</Td>
                  <Td>Open testing — unlimited (review)</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">How tester joins</strong></Td>
                  <Td>Email invite or public TestFlight link</Td>
                  <Td>Opt-in URL (email list or link)</Td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Apple — the developer account</h2>
          <p>
            You need an <strong className="text-surface-100">Apple Developer Program</strong> membership
            ({code("$99/yr")}, enroll at <em>developer.apple.com/programs</em>). Enrollment is per Apple ID
            (individual) or per legal entity (organization — needs a D-U-N-S number). Once you&apos;re in,
            two consoles matter:
          </p>
          <ul className="space-y-2 list-disc pl-5">
            <li>
              <strong className="text-surface-100">developer.apple.com</strong> — certificates, identifiers
              (your bundle ID, e.g. {code("io.yaver.mobile")}), provisioning profiles, registered device
              UDIDs. Yaver&apos;s deploy script handles this for you with{" "}
              {code("-allowProvisioningUpdates")}, so you rarely touch it by hand.
            </li>
            <li>
              <strong className="text-surface-100">App Store Connect</strong> (appstoreconnect.apple.com) —
              the app record, builds, TestFlight, and your team&apos;s people &amp; roles.
            </li>
          </ul>
          <p className="mt-3">
            <strong className="text-surface-100">Roles</strong> live under
            {" "}<em>App Store Connect → Users and Access</em>. The ones that matter for shipping betas:
          </p>
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-[13px]">
              <thead>
                <tr>
                  <Th>Role</Th>
                  <Th>Can do</Th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <Td><strong className="text-surface-100">Account Holder</strong></Td>
                  <Td>Everything; the one Apple ID that signed the legal agreements. Only one exists.</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">Admin</strong></Td>
                  <Td>Manage users, apps, agreements, and all TestFlight testers.</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">App Manager</strong></Td>
                  <Td>Manage a specific app&apos;s builds and its TestFlight testers. The right role for a teammate who ships.</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">Developer</strong></Td>
                  <Td>Upload builds and create certs/profiles, but can&apos;t manage testers or release.</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">Marketing / Customer Support</strong></Td>
                  <Td>No build access — irrelevant for testing.</Td>
                </tr>
              </tbody>
            </table>
          </div>
          <p className="mt-3">
            To <strong className="text-surface-100">automate uploads</strong> you also create an{" "}
            <em>App Store Connect API key</em> under <em>Users and Access → Integrations → App Store
            Connect API</em>. You get an Issuer ID, a Key ID, and a one-time-download {code(".p8")} file.
            Those three are exactly what Yaver&apos;s deploy script consumes (see below).
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Apple — internal vs external testers</h2>
          <p>TestFlight has two tester pools, and the distinction is the whole game:</p>
          <ul className="space-y-3 list-disc pl-5">
            <li>
              <strong className="text-surface-100">Internal testers</strong> — up to{" "}
              <strong className="text-surface-100">100</strong> people who are already{" "}
              <em>Users</em> in your App Store Connect team (Admin / App Manager / Developer roles).
              They get every build the moment it finishes processing — <strong className="text-surface-100">no
              Beta App Review</strong>. This is the fast lane for your own team.
            </li>
            <li>
              <strong className="text-surface-100">External testers</strong> — up to{" "}
              <strong className="text-surface-100">10,000</strong> people who do <em>not</em> need to be on
              your team. You invite them by email or hand out a <strong className="text-surface-100">public
              TestFlight link</strong>. The <em>first</em> build in a group needs a one-time{" "}
              <strong className="text-surface-100">Beta App Review</strong> (usually hours, lighter than a
              full App Store review); later builds in the same group flow through automatically.
            </li>
          </ul>
          <Block>{`App Store Connect → your app → TestFlight
  ├─ Internal Testing → pick a group → add team members  (instant builds)
  └─ External Testing → create a group
       ├─ add testers by email,  OR
       └─ "Enable Public Link"  → share https://testflight.apple.com/join/XXXXXXXX`}</Block>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Apple — how a tester actually downloads</h2>
          <ol className="space-y-2 list-decimal pl-5">
            <li>Install <strong className="text-surface-100">TestFlight</strong> from the App Store (it&apos;s a free Apple app).</li>
            <li>
              Open the invite — either tap <em>View in TestFlight</em> in the email, or open the public
              {" "}{code("testflight.apple.com/join/…")} link, or paste the redeem code in TestFlight →{" "}
              <em>Redeem</em>.
            </li>
            <li>Tap <strong className="text-surface-100">Accept</strong>, then <strong className="text-surface-100">Install</strong>. The build lives in TestFlight, not the home-screen App Store.</li>
            <li>
              Builds expire after <strong className="text-surface-100">90 days</strong>. Testers get a push
              when a new build lands; they tap <em>Update</em> in TestFlight.
            </li>
          </ol>
          <p className="mt-3">
            One gotcha worth knowing: an internal tester must accept the App Store Connect <em>Users and
            Access</em> invitation (a separate email) <em>before</em> they show up as an eligible TestFlight
            internal tester.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Apple — Yaver&apos;s one-command upload</h2>
          <p>
            Yaver ships to TestFlight from the Mac (CI is intentionally off for iOS — GitHub runners
            don&apos;t carry your registered device UDIDs). The script auto-bumps the build number,
            archives, exports, and uploads:
          </p>
          <Block>{`# preferred: pull the App Store Connect API key from the encrypted vault
$(yaver vault env --project mobile)
./scripts/deploy-testflight.sh

# vault locked (common right after an auth-token rotation)? source the env file:
source ~/.appstoreconnect/yaver.env
./scripts/deploy-testflight.sh`}</Block>
          <p className="mt-3">
            Either path provides the same four values the API needs — {code("APP_STORE_KEY_PATH")} (the
            {" "}{code(".p8")}), {code("APP_STORE_KEY_ID")}, {code("APP_STORE_KEY_ISSUER")}, and
            {" "}{code("APPLE_TEAM_ID")}. After the upload finishes and the build leaves &ldquo;Processing,&rdquo;
            internal testers get it immediately; flip on an external group (and pass Beta App Review once)
            to reach everyone else. TestFlight rate-limits at ~15–20 uploads/app/day, so don&apos;t spin.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Google — the developer account</h2>
          <p>
            You need a <strong className="text-surface-100">Google Play Console</strong> account
            ({code("$25")} one-time, at <em>play.google.com/console</em>). Personal accounts created after
            late 2023 must complete <em>identity verification</em> and, for the public production track,
            a 14-day / 20-tester closed test before they can go live — but{" "}
            <strong className="text-surface-100">internal testing has no such gate</strong>, which is why
            it&apos;s the right first stop.
          </p>
          <p className="mt-3">
            <strong className="text-surface-100">Roles</strong> live under <em>Play Console → Users and
            permissions</em>: <em>Admin</em> (account-wide), and per-app permissions like <em>Release to
            testing tracks</em>, <em>Release to production</em>, and <em>View app information</em>. Give a
            teammate who only ships betas the &ldquo;Release to testing tracks&rdquo; permission on the one app.
          </p>
          <p className="mt-3">
            Google also app-signs for you. You upload an <strong className="text-surface-100">.aab</strong>
            {" "}signed with your <em>upload key</em>; Google re-signs with the <em>app signing key</em> it
            holds. (This matters for passkeys / deep links — the live SHA-256 to put in
            {" "}{code("assetlinks.json")} is the <em>app signing</em> one from <em>Setup → App
            integrity</em>, not your upload key.)
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Google — the four tracks</h2>
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-[13px]">
              <thead>
                <tr>
                  <Th>Track</Th>
                  <Th>Testers</Th>
                  <Th>Review?</Th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <Td><strong className="text-surface-100">Internal testing</strong></Td>
                  <Td>Up to 100 (email list)</Td>
                  <Td>No — available in minutes</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">Closed testing</strong></Td>
                  <Td>Email lists / Google Groups</Td>
                  <Td>Yes (first release)</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">Open testing</strong></Td>
                  <Td>Anyone with the link</Td>
                  <Td>Yes</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">Production</strong></Td>
                  <Td>The whole Play Store</Td>
                  <Td>Yes (full review)</Td>
                </tr>
              </tbody>
            </table>
          </div>
          <p className="mt-3">
            For day-to-day beta work you live on <strong className="text-surface-100">Internal
            testing</strong>: <em>Play Console → Testing → Internal testing → Testers</em>. Create an email
            list, add up to 100 Google-account emails, then copy the <strong className="text-surface-100">opt-in
            URL</strong> and send it to your testers.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Google — how a tester actually downloads</h2>
          <ol className="space-y-2 list-decimal pl-5">
            <li>The tester&apos;s email must be on the internal-testing tester list (or in a Group you added).</li>
            <li>They open the <strong className="text-surface-100">opt-in URL</strong> on the device, signed in with that same Google account, and tap <em>Become a tester</em>.</li>
            <li>
              They tap the <em>&ldquo;Download it on Google Play&rdquo;</em> link — the app opens in the normal
              {" "}<strong className="text-surface-100">Play Store</strong> (no separate app like TestFlight)
              and installs like any other app.
            </li>
            <li>Updates arrive through the Play Store automatically. Tester badge shows it&apos;s a test build.</li>
          </ol>
          <p className="mt-3">
            Two common surprises: the opt-in can take a few minutes to propagate after you add an email,
            and the tester <em>must</em> use the exact Google account that&apos;s on the list (not a
            work-profile alias).
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Google — the service account (automated upload)</h2>
          <p>
            Uploading by hand in the Console works, but to script it you authorize the{" "}
            <strong className="text-surface-100">Play Developer API</strong> with a Google Cloud{" "}
            <em>service account</em>:
          </p>
          <ol className="space-y-2 list-decimal pl-5">
            <li>In Google Cloud Console, create a service account and download its JSON key.</li>
            <li>In Play Console → <em>Users and permissions</em>, invite that service-account email and grant <em>Release to testing tracks</em> (or admin) on the app.</li>
            <li>Keep the JSON out of git. Yaver reads it from {code("keys/google-play-service-account.json")} (gitignored) or the {code("PLAY_STORE_KEY_FILE")} env var.</li>
          </ol>
          <p className="mt-3">Then the whole build-and-ship is one block:</p>
          <Block>{`# build the signed release .aab (auto-bumps versionCode against the Play API)
JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh

# upload it to the internal track
PLAY_STORE_KEY_FILE=keys/google-play-service-account.json \\
  python3 scripts/upload-playstore.py`}</Block>
          <p className="mt-3">
            The uploader is multi-tenant: {code("PLAY_PACKAGE_NAME")} and {code("PLAY_TRACK")} are
            env-overridable, so the same helper can ship a customer&apos;s app to <em>their</em>
            {" "}package/track. Defaults are {code("io.yaver.mobile")} on the {code("internal")} track. The
            release lands as a <strong className="text-surface-100">draft</strong> — open <em>Internal
            testing</em> in the Console and click <em>Review release → Rollout</em> to push it to your
            testers (internal testing has no Google review, so rollout is instant).
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Where the credentials live</h2>
          <p>
            None of these secrets belong in git (Yaver&apos;s repo is public). They live in exactly three
            places — the encrypted {code("yaver vault")}, gitignored local files, or CI secrets:
          </p>
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-[13px]">
              <thead>
                <tr>
                  <Th>Secret</Th>
                  <Th>Local home</Th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <Td>App Store Connect API key ({code(".p8")} + IDs)</Td>
                  <Td>{code("~/.appstoreconnect/yaver.env")} or vault project {code("mobile")}</Td>
                </tr>
                <tr>
                  <Td>iOS signing certificate</Td>
                  <Td>macOS Keychain (Developer ID / Apple Distribution)</Td>
                </tr>
                <tr>
                  <Td>Android upload keystore</Td>
                  <Td>{code("keys/yaver-upload.keystore")} + {code("mobile/android/keystore.properties")}</Td>
                </tr>
                <tr>
                  <Td>Play service account JSON</Td>
                  <Td>{code("keys/google-play-service-account.json")}</Td>
                </tr>
                <tr>
                  <Td>Play app-signing SHA-256</Td>
                  <Td>{code("~/.androidplay/yaver.env")} or vault key {code("ANDROID_RELEASE_SHA256")}</Td>
                </tr>
              </tbody>
            </table>
          </div>
          <p className="mt-3">
            The {code("yaver vault")} is the canonical source — {code("yaver vault env --project mobile")}
            {" "}emits the exports both deploy scripts read. Because the vault is encrypted with a key
            derived from your auth token (which rotates on every heartbeat), it can lock; the gitignored
            {" "}{code("~/.appstoreconnect/yaver.env")} and {code("~/.androidplay/yaver.env")} files are the
            friction-free fallback, and the scripts auto-source them.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The whole thing on one page</h2>
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-[13px]">
              <thead>
                <tr>
                  <Th>Step</Th>
                  <Th>Apple / TestFlight</Th>
                  <Th>Google / Play</Th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <Td><strong className="text-surface-100">1. Pay</strong></Td>
                  <Td>Developer Program, $99/yr</Td>
                  <Td>Play Console, $25 once</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">2. Create app</strong></Td>
                  <Td>App Store Connect → New App</Td>
                  <Td>Play Console → Create app</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">3. Auth key</strong></Td>
                  <Td>App Store Connect API key (.p8)</Td>
                  <Td>GCP service account JSON</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">4. Upload</strong></Td>
                  <Td>{code("./scripts/deploy-testflight.sh")}</Td>
                  <Td>{code("deploy-playstore.sh")} + {code("upload-playstore.py")}</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">5. Add testers</strong></Td>
                  <Td>TestFlight → Internal/External group</Td>
                  <Td>Internal testing → tester email list</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">6. Share</strong></Td>
                  <Td>Email invite / public link</Td>
                  <Td>Opt-in URL</Td>
                </tr>
                <tr>
                  <Td><strong className="text-surface-100">7. Tester installs</strong></Td>
                  <Td>TestFlight app → Install</Td>
                  <Td>Opt-in → Play Store → Install</Td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Faster than a store cycle</h2>
          <p>
            All of the above is for shipping a <em>signed, store-distributed</em> build to people you can
            reach by email. When you&apos;re iterating on a React Native app and just want to see a change
            on a phone <em>now</em>, you don&apos;t need any of it — Yaver pushes a Hermes bundle straight
            into the on-device container in seconds. That story is in{" "}
            <Link
              href="/blog/hermes-vs-webview-yaver-architecture"
              className="text-surface-100 underline decoration-dotted underline-offset-4 hover:text-surface-50"
            >
              Hermes Bytecode vs WebView
            </Link>
            . TestFlight and Play internal testing are for the moment you need to put a real, installable
            build in someone else&apos;s pocket.
          </p>
        </section>
      </article>
    </div>
  );
}
