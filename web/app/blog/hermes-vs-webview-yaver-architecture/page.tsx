import Link from "next/link";
import type { Metadata } from "next";
import { postBySlug } from "@/lib/blog";

const POST_SLUG = "hermes-vs-webview-yaver-architecture";
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
    tags: ["Hermes", "React Native", "WebView", "iOS", "Android", "Native Modules", "Mobile Dev"],
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

const codeChip = "rounded bg-surface-900 px-1.5 py-0.5 text-[12px] text-surface-200";

export default function HermesVsWebviewBlogPage() {
  return (
    <div className="px-6 py-20">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(articleLd) }}
      />
      <article className="mx-auto max-w-3xl space-y-8 text-sm leading-7 text-surface-300">
        <Link href="/blog" className="inline-flex items-center gap-1 text-xs text-surface-500 hover:text-surface-50">
          &larr; Back to Blog
        </Link>

        <header className="space-y-4">
          <time dateTime={post.date} className="text-xs uppercase tracking-[0.2em] text-surface-500">
            {post.date}
          </time>
          <h1 className="text-3xl font-bold text-surface-50 md:text-4xl">
            Hermes Bytecode vs WebView: How Yaver Tests Native Apps Without an App Store Cycle
          </h1>
          <p className="text-surface-400">
            How Yaver runs your in-progress React Native app on a real iPhone in 10 seconds — using
            Hermes bytecode for native frameworks and WebView for web frameworks. The architecture
            behind &ldquo;Open in Yaver,&rdquo; what each path can and can&apos;t do, and where the limits
            come from (Apple, mostly).
          </p>
        </header>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The problem: real phone, no rebuild</h2>
          <p>
            You make a code change. You want to see it on your iPhone. The conventional path is{" "}
            <code className={codeChip}>xcodebuild → Archive → TestFlight</code>, which is a 20-minute round
            trip and burns one of your 15 daily TestFlight uploads. Or it&apos;s{" "}
            <code className={codeChip}>expo run:ios --device</code>, which still recompiles native code on
            every cold start and needs Xcode + a Mac in the loop.
          </p>
          <p className="mt-3">
            Yaver&apos;s &ldquo;Open in Yaver&rdquo; flow does it in ~10 seconds, from any host OS — Linux,
            WSL, macOS, a remote VPS — into the same iPhone. No Xcode in the loop. No Mac requirement.
            The trick is to never compile native code at iteration time. Instead, the JavaScript half
            of your app is compiled to <strong className="text-surface-100">Hermes bytecode</strong> on
            the host and shipped to the phone, where Yaver loads it into a pre-built native container
            that&apos;s already on TestFlight or the App Store.
          </p>
          <p className="mt-3">
            Two technologies make this possible: <em>Hermes bytecode</em> for React Native / Expo apps,
            and <em>WebView</em> for Vite / Next.js / generic web frameworks. They&apos;re very different
            tools with very different power/limitation tradeoffs. Here&apos;s what each is and where it fits.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">What is Hermes bytecode?</h2>
          <p>
            <a href="https://hermesengine.dev" className="text-surface-100 underline decoration-dotted underline-offset-4 hover:text-surface-50" target="_blank" rel="noreferrer">Hermes</a> is
            Meta&apos;s open-source JavaScript engine, designed specifically for React Native. Unlike V8
            or JavaScriptCore which JIT-compile JS at runtime, Hermes uses ahead-of-time
            compilation: the build pipeline runs a compiler called <code className={codeChip}>hermesc</code>{" "}
            that turns your JavaScript source into a custom bytecode format (HBC — Hermes Bytecode).
            That bytecode ships inside the app instead of plain JS.
          </p>
          <p className="mt-3">
            HBC files have a 12-byte header: bytes 4-7 hold the magic number{" "}
            <code className={codeChip}>0x1F1903C1</code>, and bytes 8-11 hold the bytecode version
            (currently <code className={codeChip}>96</code> for RN 0.81.5). At runtime, Hermes mmaps
            the HBC file and interprets it directly — no parsing, no JIT, no extra startup cost. App
            cold-start drops by 30-50% vs JIT engines on the same hardware.
          </p>
          <p className="mt-3">
            Critically, <strong className="text-surface-100">HBC is interpreted code</strong> — it
            executes inside a sandboxed JS engine, never as native machine code. That&apos;s the property
            that lets us inject it into a running app on iOS without violating the App Store&apos;s code-
            signing policy.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Source code vs bundle vs HBC</h2>
          <p>
            Three different artifacts matter here, and conflating them is where most Hermes explainers
            get fuzzy. <strong className="text-surface-100">Source code</strong> is your project&apos;s{" "}
            <code className={codeChip}>.js</code>, <code className={codeChip}>.ts</code>,{" "}
            <code className={codeChip}>.tsx</code>, plus everything Metro pulls from{" "}
            <code className={codeChip}>node_modules</code>. <strong className="text-surface-100">A JS
            bundle</strong> is Metro&apos;s single-file output: one large JavaScript program with the module
            graph flattened, transformed, and ready for React Native to load. <strong className="text-surface-100">HBC</strong> is what you get after running that JS bundle through{" "}
            <code className={codeChip}>hermesc -emit-binary</code>: a Hermes-specific bytecode file that
            the Hermes VM can execute directly.
          </p>
          <p className="mt-3">
            So the pipeline is not &ldquo;TypeScript to native code.&rdquo; It is{" "}
            <code className={codeChip}>TS/JS source → Metro JS bundle → Hermes bytecode</code>. The
            native side was already compiled earlier, when the Yaver app itself was built and shipped.
            At iteration time we are only swapping the guest program that runs <em>inside</em> that
            native host.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Why HBC is not assembly code</h2>
          <p>
            Hermes bytecode can look &ldquo;low level,&rdquo; but it is still VM bytecode, not CPU assembly. Assembly
            is an almost one-to-one textual representation of machine instructions for a real processor:
            ARM64, x86-64, and so on. HBC is an instruction stream for the Hermes virtual machine. The
            CPU never executes HBC directly. Hermes reads it, decodes Hermes opcodes, manages stack
            frames, objects, strings, closures, and garbage collection, and only then uses native code
            from the already-signed Hermes engine binary to do the actual work.
          </p>
          <p className="mt-3">
            That distinction is the whole App Store story. Shipping new ARM64 code to an iPhone at
            runtime would trip code-signing rules immediately. Shipping new Hermes bytecode is allowed
            because the executable part is still the Hermes interpreter already inside Yaver&apos;s signed
            binary. The downloaded file is data for that interpreter, not a new Mach-O image.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">How iOS and Android allow runtime code injection</h2>
          <p>
            Apple&apos;s App Store Review Guidelines (
            <a href="https://developer.apple.com/app-store/review/guidelines/#3.3.2" className="text-surface-100 underline decoration-dotted underline-offset-4 hover:text-surface-50" target="_blank" rel="noreferrer">3.3.2</a>
            ) explicitly forbid downloading and running unsigned executable code at runtime — except for{" "}
            <em>interpreted code</em> running in a JavaScript engine, embedded scripting language, or
            similar virtual machine, as long as it doesn&apos;t change the app&apos;s primary purpose, doesn&apos;t
            install other apps, and doesn&apos;t introduce new features outside what Apple has reviewed.
          </p>
          <p className="mt-3">
            That carve-out is what makes Hermes-based hot reload legal. The bytecode is interpreted by
            a JS VM that&apos;s already inside Yaver&apos;s signed binary; we&apos;re not loading new native code, we&apos;re
            loading new <em>data for the JS interpreter to run</em>.
          </p>
          <p className="mt-3">
            Android has the same carve-out and is more permissive on top of it: it allows{" "}
            <code className={codeChip}>dlopen</code> of arbitrary <code className={codeChip}>.so</code>{" "}
            files, which means a third-party app can theoretically <em>also</em> load new native code
            at runtime. iOS doesn&apos;t allow that. This asymmetry will become important when we get to
            limitations.
          </p>
          <p className="mt-3">
            Practically: Yaver&apos;s iOS binary contains a React Native host built with{" "}
            <code className={codeChip}>ExpoReactNativeFactory</code> +{" "}
            <code className={codeChip}>RCTAppDependencyProvider</code>. When you tap &ldquo;Open in
            Yaver,&rdquo; the phone asks the agent for a native bundle build at{" "}
            <code className={codeChip}>POST /dev/build-native</code>, the agent runs Metro and{" "}
            <code className={codeChip}>hermesc</code>, then returns build-specific URLs like{" "}
            <code className={codeChip}>/dev/native-bundle?build=...</code>. The phone downloads that
            HBC file, validates it, saves it to{" "}
            <code className={codeChip}>Documents/bundles/main.jsbundle</code>, and Yaver swaps its
            current bridge with a fresh one running your guest bytecode. Bridge swap typically takes
            around 2 seconds because Yaver waits for Hermes&apos; concurrent GC to release the old bridge
            before bringing up the new one.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The full pipeline, end to end</h2>
          <p>
            The production &ldquo;Open in Yaver&rdquo; path is a host-build plus phone-fetch loop:
          </p>
          <ol className="mt-3 space-y-2 list-decimal pl-5">
            <li>The phone calls the agent&apos;s <code className={codeChip}>POST /dev/build-native</code> endpoint with the target platform plus its own Yaver app version, SDK version, and Hermes bytecode version.</li>
            <li>The agent resolves the project path, checks whether the previous build cache belongs to the same consumer contract, and clears stale output if the host app build changed.</li>
            <li>The agent runs Metro or Expo export to produce a plain JS bundle at <code className={codeChip}>.yaver-build/main.jsbundle</code> plus an assets directory.</li>
            <li>Yaver injects a small guest-safety prelude into that JS bundle before compilation so a few host-optional modules fail more cleanly.</li>
            <li>The agent runs <code className={codeChip}>hermesc -emit-binary</code> over that bundle, replacing the JS file with HBC.</li>
            <li>The agent validates the output locally: Hermes magic at offset 4, bytecode version at offset 8, size sanity, and MD5 of the full file.</li>
            <li>The agent computes compatibility metadata from the project&apos;s dependencies and Yaver&apos;s embedded <code className={codeChip}>sdk-manifest.json</code>, then persists the finished artifact under a build ID so the phone can fetch it even if no dev server is actively serving.</li>
            <li>The phone downloads <code className={codeChip}>/dev/native-bundle?build=...</code>, reads the <code className={codeChip}>X-Yaver-Bundle-Metadata</code> header, re-validates size, MD5, magic, bytecode version, SDK version, RN support range, and native-module compatibility, then only after that triggers a bridge reload.</li>
          </ol>
          <p className="mt-3">
            That last point matters: the build and the load are separate phases. The agent does not
            stream raw source code into the device runtime. It hands the phone a sealed build artifact
            plus a metadata contract, and the phone refuses to load it if either the bytes or the host
            assumptions drift.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The versioning handshake in detail</h2>
          <p>
            Yaver&apos;s handshake is really several checks stacked together.
          </p>
          <ul className="mt-3 space-y-2 list-disc pl-5">
            <li><strong className="text-surface-100">Consumer contract:</strong> the phone includes its Yaver app version, build number, SDK version, platform, and Hermes BC version in the build request. The agent uses that to decide whether an old <code className={codeChip}>.yaver-build</code> directory can be reused or must be cleared.</li>
            <li><strong className="text-surface-100">Compiler contract:</strong> after <code className={codeChip}>hermesc</code> runs, the agent reads the output header and rejects the file if the produced BC version is not what the host expects.</li>
            <li><strong className="text-surface-100">Integrity contract:</strong> the agent sends metadata containing size, MD5, module name, format, BC version, builder OS/arch, and framework versions. The phone re-checks the downloaded bytes against that metadata before saving anything.</li>
            <li><strong className="text-surface-100">Runtime contract:</strong> the agent compares the guest project&apos;s React, React Native, Expo, and native-module dependency graph against the host app&apos;s <code className={codeChip}>sdk-manifest.json</code>. If the guest expects modules or versions the host does not provide, Yaver blocks the reload instead of letting the app crash after startup.</li>
          </ul>
          <p className="mt-3">
            This is why the handshake has to happen both on the agent and on the phone. The agent knows
            the project graph; the phone knows the exact binary it is running. Neither side can prove
            compatibility alone.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Yaver&apos;s runtime-family model</h2>
          <p>
            The practical limit is that one mobile host binary cannot safely pretend to be every Expo /
            React Native runtime ever released. So Yaver treats the host as a small set of{" "}
            <strong className="text-surface-100">runtime families</strong>: pinned combinations of Expo,
            React Native, React, Hermes bytecode version, and the native-module manifest compiled into
            the container.
          </p>
          <p className="mt-3">
            When the phone asks the agent for a build, the host advertises the runtime families it can
            execute. The agent fingerprints the guest app&apos;s installed Expo / RN / React versions,
            chooses the closest host family, and logs that decision before it bundles anything. If the
            match is exact, the normal Hermes path continues. If the guest drifts outside those
            families, Yaver surfaces the closest host family plus the supported family list instead of
            failing with a vague &ldquo;version mismatch.&rdquo;
          </p>
          <p className="mt-3">
            This gives Yaver a clean product shape: <strong className="text-surface-100">fast direct
            load</strong> for a few high-value runtime families, and a{" "}
            <strong className="text-surface-100">native build fallback</strong> for everything else.
            The fallback carries the guest app&apos;s own native runtime, so it handles projects that sit
            outside Yaver&apos;s bundled families without pretending a single container can safely host every
            native contract.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">What hermesc does, and what it does not do</h2>
          <p>
            <code className={codeChip}>hermesc</code> is just the compiler. It does not stay resident,
            it does not manage hot reload sessions, and it does not execute JavaScript in real time.
            Yaver launches it as a subprocess during the build step, points it at Metro&apos;s JS output,
            and waits for an HBC file. After that, <code className={codeChip}>hermesc</code> is done.
          </p>
          <p className="mt-3">
            The runtime work is handled by the Hermes engine embedded inside the Yaver app. That engine
            reads the bytecode, creates the JS runtime, exposes TurboModules and Fabric through React
            Native&apos;s New Architecture, and runs garbage collection while the guest app is live. So if
            you want the short version: <strong className="text-surface-100">hermesc is build-time; Hermes is runtime.</strong>
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">What is WebView?</h2>
          <p>
            WebView is the iOS/Android primitive for embedding a browser inside a native app —{" "}
            <code className={codeChip}>WKWebView</code> on iOS,{" "}
            <code className={codeChip}>android.webkit.WebView</code> on Android. It loads HTML, CSS,
            and JS over HTTP and renders them just like Safari or Chrome would. There&apos;s no
            compilation step. Whatever lives at the URL is what gets shown.
          </p>
          <p className="mt-3">
            Yaver uses WebView for the <em>web framework</em> hot-reload paths: Vite, Next.js,
            anything that produces HTML+JS as its output. The agent runs the framework&apos;s dev server
            on the host, the relay proxies HTTP requests through to it, and the phone embeds the
            result in WebView. Vite&apos;s native HMR works through this transparently — edit a file,
            the browser reloads, the WebView reflects the change in &lt;1 second.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">WebView&apos;s real limit: it&apos;s a browser</h2>
          <p>
            WebView gives you a browser, and a browser can do roughly what a Safari tab can do. That&apos;s a
            lot — modern web APIs cover Workers, WASM, IndexedDB, WebSockets, WebGL, the Notifications
            API, the File System Access API, and{" "}
            <code className={codeChip}>getUserMedia</code> for camera/mic. But it&apos;s not the same as
            native:
          </p>
          <ul className="mt-3 space-y-2 list-disc pl-5">
            <li>No direct access to iOS Keychain / Android Keystore (only browser cookies + IndexedDB).</li>
            <li>No background tasks, no push notifications without a wrapping native shell.</li>
            <li>No Bluetooth, no NFC (except on Chromium Android, with user opt-in).</li>
            <li>No TouchID / FaceID prompt — only WebAuthn flows.</li>
            <li>No StoreKit — in-app purchases require a native bridge.</li>
            <li>Camera and microphone are prompt-gated through{" "}
              <code className={codeChip}>navigator.mediaDevices</code>; they work but the UX is generic
              browser permission prompts, not native ones.</li>
          </ul>
          <p className="mt-3">
            For a Vite app or Next.js app, this is fine — those apps are already designed for the web,
            so they don&apos;t expect Keychain or StoreKit. But you can&apos;t use WebView to test a React Native
            app, because RN apps depend on the native bridge for almost everything visible on screen
            (gesture handler, navigation, animations, even basic layout via Yoga).
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Where Hermes wins: real native, real APIs</h2>
          <p>
            The Hermes path puts your app inside Yaver&apos;s actual native bridge. Your bundle gets the
            same TurboModules, the same Fabric renderer, and the same JSI surface as if it had been
            archived through Xcode and uploaded to TestFlight. That means:
          </p>
          <ul className="mt-3 space-y-2 list-disc pl-5">
            <li><strong className="text-surface-100">Real native UI</strong> — animations run on the
              native thread via Reanimated 3, gesture handling is real, scroll views are real{" "}
              <code className={codeChip}>UIScrollView</code>.</li>
            <li><strong className="text-surface-100">Real native APIs</strong> — Keychain, biometric
              prompts, push notifications, in-app purchases, Bluetooth, NFC.</li>
            <li><strong className="text-surface-100">Real performance</strong> — same cold-start
              profile, same memory profile, same frame rate as a production build of the same app.</li>
            <li><strong className="text-surface-100">Real bugs</strong> — if your code has a race in
              the native thread, you&apos;ll see it. If a TurboModule throws on iOS but not Android,
              you&apos;ll see it. WebView can&apos;t reproduce most of these.</li>
          </ul>
          <p className="mt-3">
            For React Native / Expo apps, this is a categorical upgrade over a WebView-based
            preview. It&apos;s why Yaver insists on Hermes for those frameworks and never falls back to
            WebView for &ldquo;Open in Yaver&rdquo; — see the
            <code className={codeChip}>NEVER use WebView to load third-party apps</code> rule baked
            into the project&apos;s contract.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">How the Yaver container app uses Hermes</h2>
          <p>
            The Yaver phone app is a super-host: one signed native app with React Native, Hermes, Expo
            infrastructure, and a large set of prelinked native modules already compiled in. Your guest
            app does not bring its own copy of Hermes, Fabric, or TurboModules. It runs inside Yaver&apos;s
            copies.
          </p>
          <p className="mt-3">
            On iOS the loader path is explicit. <code className={codeChip}>YaverBundleLoader</code>{" "}
            downloads the bundle, validates the metadata header, writes the HBC file to disk, stores
            the selected module name, and posts a reload notification.{" "}
            <code className={codeChip}>AppDelegate.safeReloadBridge()</code> then invalidates the old
            bridge, polls up to 3 seconds for deallocation so Hermes GC does not touch freed memory,
            and finally creates a fresh guest bridge with{" "}
            <code className={codeChip}>ExpoReactNativeFactory</code> and{" "}
            <code className={codeChip}>RCTAppDependencyProvider</code>. That last part is why guest
            bundles get real New Architecture behavior instead of a half-working legacy bridge.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Hermes&apos; real limit: the native side has to be pre-bundled</h2>
          <p>
            HBC is JavaScript. It can call any native module that&apos;s already registered in Yaver&apos;s
            bridge. It cannot summon a new one. So if your app declares a native dependency that
            Yaver&apos;s super-host doesn&apos;t already register —{" "}
            <code className={codeChip}>react-native-record-screen</code>, say, or some niche Bluetooth
            wrapper — calling it at runtime resolves to nil, throws an Objective-C{" "}
            <code className={codeChip}>NSException</code>, and crashes Hermes during the JSError
            conversion path. Silent until the crash. Painful.
          </p>
          <p className="mt-3">
            This is the structural limit of the model. iOS forbids loading new signed native code at
            runtime, so Yaver can&apos;t download missing modules on demand. Whatever&apos;s native must already
            be inside Yaver&apos;s signed binary — that means inside{" "}
            <code className={codeChip}>mobile/package.json</code> and registered via autolinking,
            packaged into the iOS / Android super-host, and shipped through TestFlight / Play Console.
          </p>
          <p className="mt-3">
            Yaver currently registers 92 native modules. We picked them by surveying our own
            projects and the most common React Native dependencies. That covers most apps most of the
            time, but the long tail is real. Every new third-party app a user wants to test risks
            hitting a module that isn&apos;t there.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">The compat handshake (shipped)</h2>
          <p>
            Today&apos;s Yaver does <em>defense in depth</em> on this. When you tap &ldquo;Open in Yaver,&rdquo;
            the agent compiles the bundle and then diffs your project&apos;s{" "}
            <code className={codeChip}>package.json</code> against Yaver&apos;s embedded{" "}
            <code className={codeChip}>sdk-manifest.json</code>. Any deps that look native but
            aren&apos;t registered in the host show up in the build response as{" "}
            <code className={codeChip}>incompatibleNativeModules: [&quot;...&quot;]</code>. The mobile
            app then shows a clear &ldquo;Incompatible native modules&rdquo; dialog before doing the
            bridge swap, listing exactly what&apos;s missing.
          </p>
          <p className="mt-3">
            The build metadata also carries host SDK version, host Expo / React / React Native
            versions, supported RN range, and likely-breaking native-module version drift. The phone
            validates those again before reload, so the build can be blocked even if the bytes are
            structurally valid. This is not just &ldquo;is the file corrupt?&rdquo; It is &ldquo;does this guest app
            belong inside this exact Yaver binary?&rdquo;
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Roadmap: shrinking the long tail</h2>
          <p>
            The handshake is step one. Three more layers in the queue:
          </p>
          <ul className="mt-3 space-y-3 list-disc pl-5">
            <li>
              <strong className="text-surface-100">Auto-stub at build time.</strong> For each
              missing module, the agent injects a JS-side proxy that returns a controlled rejection
              (&ldquo;X is not available inside Yaver&rdquo;) instead of throwing NSException. Apps
              that gate optional features behind <code className={codeChip}>if (Module.isAvailable)</code>{" "}
              keep working; the specific feature is just disabled. Stops crashes for ~80% of cases.
            </li>
            <li>
              <strong className="text-surface-100">Popular-module preload.</strong> Audit which
              modules are most commonly missing, add the top 30-50 to Yaver&apos;s super-host as
              one-time integration work. Binary grows ~30-50%, but the wall recedes for most users.
            </li>
            <li>
              <strong className="text-surface-100">Per-project Yaver build.</strong> The durable
              answer. For a specific user&apos;s project, build a custom Yaver-X binary in CI with that
              project&apos;s native modules linked in, ship via that user&apos;s Apple Developer account or
              ad-hoc enterprise distribution. Same model EAS Build uses for production apps; we&apos;d
              apply it to the dev container itself.
            </li>
            <li>
              <strong className="text-surface-100">Android dynamic loading.</strong> Android allows{" "}
              <code className={codeChip}>dlopen</code>; an Android-only fast path could ship native
              modules over the wire to the phone. Doesn&apos;t help iPhone users, but proves the model
              and gives Android-first teams a friction-free path. (See{" "}
              <Link href="https://github.com/kivanccakmak/yaver.io/blob/main/docs/android-dynamic-native-modules.md" className="text-surface-100 underline decoration-dotted underline-offset-4 hover:text-surface-50">
                docs/android-dynamic-native-modules.md
              </Link>{" "}
              for the architectural sketch.)
            </li>
          </ul>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">Help shrink the long tail — invitation to PR</h2>
          <p>
            If you&apos;re using Yaver and you hit a missing module dialog, the cleanest fix is to add
            that module. Yaver is open-source and the integration is a focused PR. The contract:
          </p>
          <ol className="mt-3 space-y-2 list-decimal pl-5">
            <li>Add the npm package to <code className={codeChip}>mobile/package.json</code>.</li>
            <li>Add the matching entry to <code className={codeChip}>mobile/sdk-manifest.json</code>{" "}
              under <code className={codeChip}>nativeModules</code> (key = npm name, value = installed
              version).</li>
            <li>Mirror the manifest to the four other tracked copies: Android assets, iOS Yaver target,
              <code className={codeChip}>cli/</code>, and{" "}
              <code className={codeChip}>desktop/agent/</code>. The{" "}
              <code className={codeChip}>TestSDKManifestInSync</code> Go test fails the build if the
              agent copy drifts from the mobile master.</li>
            <li>Run <code className={codeChip}>cd mobile/ios &amp;&amp; pod install</code> for iOS,
              <code className={codeChip}>cd mobile/android &amp;&amp; ./gradlew clean</code> for Android.</li>
            <li>Test that <code className={codeChip}>NativeModules.X</code> resolves at runtime in
              Yaver&apos;s super-host — load any small RN app that uses it and verify the module is
              accessible.</li>
            <li>Open a PR with the manifest diff, a one-line smoke-test scenario, and the Hermes BC
              version you tested against (currently 96).</li>
          </ol>
          <p className="mt-3">
            The full step-by-step is in the{" "}
            <Link href="https://github.com/kivanccakmak/yaver.io/blob/main/README.md#hermes-reload--when-it-crashes-and-how-to-fix-it" className="text-surface-100 underline decoration-dotted underline-offset-4 hover:text-surface-50">
              README
            </Link>{" "}
            and{" "}
            <Link href="https://github.com/kivanccakmak/yaver.io/blob/main/docs/native-module-architecture.md" className="text-surface-100 underline decoration-dotted underline-offset-4 hover:text-surface-50">
              docs/native-module-architecture.md
            </Link>
            . Adding a module to the manifest before the corresponding native code is wired pushes the
            crash from a build-time warning to a runtime SIGSEGV — the manifest is a public commitment,
            not a wishlist. Please don&apos;t skip the smoke-test step.
          </p>
        </section>

        <section>
          <h2 className="mb-3 text-xl font-semibold text-surface-100">tl;dr</h2>
          <ul className="space-y-2 list-disc pl-5">
            <li>
              Hermes bytecode lets Yaver inject your in-progress React Native app into a real
              iPhone app without rebuilding native code, because Apple permits interpreted code at
              runtime. iOS isn&apos;t a closed device — it just demands JS, not machine code.
            </li>
            <li>
              WebView lets Yaver preview Vite / Next.js dev servers on the phone, but it can&apos;t reach
              real native APIs, so it&apos;s only used for genuinely web frameworks.
            </li>
            <li>
              Hermes&apos; structural limit: any native module the guest app calls has to be pre-bundled
              into Yaver&apos;s super-host. We&apos;ve registered 92 of them; the long tail is real.
            </li>
            <li>
              The real pipeline is source code → Metro JS bundle → Hermes bytecode → metadata-validated
              bridge reload inside Yaver&apos;s native container. `hermesc` only compiles; Hermes runtime
              inside the app does the execution.
            </li>
            <li>
              The native module list is open-source. PRs with new modules + manifest entries +
              smoke-test scenarios are the cleanest contributions.
            </li>
          </ul>
        </section>
      </article>
    </div>
  );
}
