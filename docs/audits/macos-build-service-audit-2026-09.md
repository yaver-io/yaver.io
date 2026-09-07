# macOS build service audit — Yaver, SFMG, and Talos

Date: 2026-09-06

Currency basis: TCMB 2026-09-04 selling rates, USD/TRY 48.3195 and EUR/TRY 56.1581.

Tax basis: figures are ex-VAT unless stated otherwise. The quoted Mac price is 97,499 TL including 20% VAT, or 81,249.17 TL net if SIMKAB can fully credit the input VAT.

## Decision

Do not use AWS EC2 Mac as the default Yaver build box. It is technically capable and offers the cleanest API/KMS/EBS integration, but its mandatory 24-hour Dedicated Host allocation makes it dramatically more expensive than both buying a Mac mini and renting an Apple Silicon Mac from a specialist.

Use this staged design:

1. Run the daily Yaver remote-box and vibe-coding workload on an ephemeral Hetzner Linux VM. It handles TypeScript/React Native JS, Go, web, backend, tests, commits, and pushes.
2. Trigger a clean macOS job at an immutable Git commit for Xcode archive, codesign, and TestFlight upload. Android can run on the same Mac for operational simplicity, although Linux is cheaper for it.
3. During the trial phase, use Scaleway M4-M (32 GB, 1.02 TB) in batched 24-hour release windows. Automate encrypted restore/bootstrap so no certificate or credential is entered manually.
4. Buy the quoted M6/32 GB/512 GB Mac mini once macOS is needed interactively or on roughly six or more distinct rental days per month. Make it the persistent Yaver remote box and a GitHub self-hosted runner.
5. For managed cloud releases, use Codemagic M4 pay-as-you-go as the current-code-safe default and pilot Xcode Cloud as the lower-cost Apple-native lane. Keep GitHub-hosted macOS Actions only where its 14 GB disk is proven sufficient.

There is no product that simultaneously provides all four of these properties: true scale-to-zero billing, instant persistent state, a full 32 GB+ Mac, and an always-ready interactive remote desktop/agent. Apple's licensing model gives bare-metal Mac rentals a 24-hour minimum. “No manual setup” is achievable through automated rehydration; “no rehydration at all” requires keeping a Mac allocated or owning one.

## What the repositories actually require

The current code, rather than the Markdown documentation, establishes the following:

- Yaver's canonical release front door is `./deploy/deploy.sh <target>`.
- Yaver already contains a manually triggered GitHub-hosted TestFlight job on `macos-26`. It reconstructs a temporary signing keychain and App Store Connect API key from GitHub Secrets.
- Yaver's local TestFlight script requires at least 10 GiB free. Its Play deployment requires 16 GiB and configures an 8 GiB Gradle heap.
- SFMG and Talos both ship iOS through native Xcode/TestFlight scripts and Android through Gradle/Play scripts. Both iOS scripts require 20 GiB free before starting.
- Only Apple compilation, signing, simulator/device work, and TestFlight intrinsically need macOS. React Native JavaScript, Go, web/Cloudflare, Convex, most tests, Git, and Android packaging are portable to Linux.

Relevant code: Yaver's [release-mobile workflow](../../.github/workflows/release-mobile.yml), [canonical deploy wrapper](../../deploy/deploy.sh), [TestFlight deploy script](../../scripts/deploy-testflight.sh), and [Play deploy script](../../scripts/deploy-playstore.sh).

## Cost comparison

All TL conversions use the exchange rates above. They exclude tax, transfer, egress, and staff time unless explicitly noted.

| Option | Useful configuration | Billing floor | Approximate cost | State after compute stops | Fit |
|---|---:|---:|---:|---|---|
| Buy quoted Mac mini | M6, 32 GB, 512 GB | One-time | 81,249 TL net CAPEX | Persistent | Best if used regularly |
| Hetzner CAX31 dev lane | 8 Arm vCPU, 16 GB, 160 GB | Hourly, monthly cap | 1,179 TL/month | Snapshot/volume based | Excellent JS/Go dev lane; verify Android tools on Arm |
| Scaleway M4-M | M4, 32 GB, 1.02 TB | 24 hours | 391 TL/day or 11,175 TL/month | Local data is lost when deleted | Best burst Mac trial |
| Scaleway M4-XL | M4 Pro, 64 GB, 2.05 TB | 24 hours | 660 TL/day or 18,813 TL/month | Local data is lost when deleted | Faster batched builds |
| MacStadium M2 Pro | 32 GB, 2 TB | Monthly | 16,864 TL/month | Persistent | Simple managed always-on Mac |
| AWS Ireland M4 | M4, 24 GB + 500 GB gp3 | 24 hours | 1,569 TL/day; about 49,851 TL/month with baseline gp3 | EBS/AMI can persist | Great APIs, poor economics |
| AWS N. Virginia M2 Pro | M2 Pro, 32 GB | 24 hours | 1,809 TL/day; 55,026 TL/month compute-only | EBS/AMI can persist | Exact 32 GB AWS match, wrong region/cost |
| GitHub standard macOS | M1 3-core/7 GB or Intel 4-core/14 GB, 14 GB disk | Per job minute | 135 TL per 45-minute paid job | Fresh VM each job | Cheapest release-only lane if disk fits |
| GitHub larger M2 Pro | 5-core/14 GB, 14 GB disk | Per job minute | 222 TL per 45-minute job | Fresh VM each job | Faster but always billable; Team/Enterprise only |
| GitLab hosted M2 Pro | 6 vCPU, 16 GB, 50 GB disk | Premium plan | $29/user/month annually; 12× compute factor | Fresh VM each job | Better disk, but beta and GitLab migration cost |

### Buying economics

- Gross cash payment: 97,499 TL.
- Recoverable VAT assumption: 16,249.83 TL.
- Net economic CAPEX: 81,249.17 TL.
- Straight-line 36-month cost with no resale value: 2,256.92 TL/month.
- Straight-line 36-month cost with 25% residual value: 1,692.69 TL/month.
- Power, UPS, backup storage, internet, and hands-on recovery are extra. Even so, the gap to an always-on hosted Mac is large.

Break-even against net purchase CAPEX:

- Scaleway M4-M kept continuously: 7.27 months.
- MacStadium $349/month: 4.82 months.
- AWS Ireland M4 plus baseline 500 GB gp3: 1.63 months.
- Scaleway M4-M used one 24-hour block each week: about 1,694 TL/month, making rental economically preferable for nearly four years before performance/resale effects.
- AWS Ireland M4: 51.8 separate 24-hour allocations before storage. That is only about one release day per week for a year before compute alone equals the Mac's net purchase price.

The Mac mini is therefore the cheapest persistent Mac by a wide margin. The burst-rental case wins only when release work can be packed into relatively few 24-hour windows.

### 36-month view

| Usage pattern | Approximate 36-month ex-VAT cost | Interpretation |
|---|---:|---|
| Buy Mac mini | 81,249 TL before resale | One machine can do both remote development and releases |
| Scaleway M4-M, 4 rental days/month | 56,284 TL | Cheapest Mac lane when releases are genuinely batched |
| Scaleway M4-M, 6 rental days/month | 84,426 TL | Approximately the purchase crossover before resale |
| Scaleway M4-M continuously | 402,317 TL | Buying wins quickly |
| MacStadium $349 continuously | 607,086 TL | Convenience premium dominates |
| AWS Ireland M4 + baseline 500 GB gp3 continuously | 1,794,633 TL | Not economically credible for this workload |
| GitHub standard macOS, 12 paid 45-minute jobs/month | 58,239 TL | Could be lower or zero while within included minutes |
| Hetzner CAX31 continuously | 42,435 TL | Add to either rental-Mac option if it remains the daily workspace |

At the quoted rates, purchase and Scaleway M4-M cross at about 5.8 rental days/month over 36 months with no Mac resale value. Assuming a 25% Mac residual value lowers the crossover to about 4.3 days/month. If the purchased Mac also eliminates the Hetzner bill, buying becomes favorable sooner; if Hetzner remains useful regardless, its cost cancels out of the Mac decision.

## AWS deep audit

### Available hardware and region problem

EC2 Mac instances are bare-metal Dedicated Hosts with exactly one Mac instance per host. Current relevant families include:

- `mac2.metal`: M1, 16 GB.
- `mac2-m2.metal`: M2, 24 GB.
- `mac2-m2pro.metal`: M2 Pro, 32 GB.
- `mac-m4.metal`: M4, 24 GB.
- `mac-m4pro.metal`: M4 Pro, 48 GB.

Ireland currently offers M1 and M4 families but not M2 Pro/M4 Pro; Frankfurt currently lists no EC2 Mac families. The closest European AWS option is therefore a 24 GB M4 in Ireland. The closest RAM match is a 32 GB M2 Pro in a US region.

AWS does not offer Spot or Reserved Instances for EC2 Mac. A host must remain allocated for at least 24 hours before release. Savings Plans can reduce long-running compute cost, but they do not turn the service into economical short per-build capacity.

Current official public-price-list rates used here:

- Ireland M4: $1.353/hour.
- N. Virginia M2 Pro 32 GB: $1.56/hour.
- N. Virginia M4 Pro 48 GB: $1.97/hour.

### Persistent state

AWS is the strongest provider for preserving an environment between allocations:

- Put the OS/toolchain in an encrypted EBS-backed AMI.
- Put caches and non-secret mutable state on a separate encrypted EBS volume with `DeleteOnTermination=false`.
- Store secrets in Secrets Manager/KMS and fetch them only after the instance receives its scoped machine identity.
- Stop, snapshot, and terminate the Mac while retaining the state volume or snapshots.

For optimal Mac performance AWS recommends 10,000 IOPS and 400 MiB/s on EBS. In Ireland, a 500 GB gp3 volume is about $44/month at baseline or $94.60/month at that recommended performance setting. A snapshot-only parked design can reduce this, at the cost of a longer cold start.

Stopping or terminating begins a hardware scrub. Apple Silicon scrubbing can take up to 4.5 hours, during which that host cannot relaunch an instance, although instance billing is paused. Fresh Mac launch readiness is typically 6–20 minutes before custom bootstrap and restore time. Capacity and host quota must also be obtained per family/region.

### Why AWS is not the default

AWS would be justified if SIMKAB needs AWS-native IAM/KMS/audit controls, already has large AWS credits, or intends to commercialize a multi-provider Yaver build service where the EC2 control plane is strategically valuable. It is not justified by build economics alone.

Sources: [EC2 Mac instance guide](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-mac-instances.html), [EC2 Mac stop/release behavior](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/mac-instance-stop.html), [instance availability by region](https://docs.aws.amazon.com/ec2/latest/instancetypes/ec2-instance-regions.html), [Dedicated Host pricing](https://aws.amazon.com/ec2/dedicated-hosts/pricing/), [Ireland EC2 price list](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/current/eu-west-1/index.csv), [N. Virginia EC2 price list](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/current/us-east-1/index.csv), [EBS-backed AMIs](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/creating-an-ami-ebs.html), and [Secrets Manager](https://docs.aws.amazon.com/secretsmanager/latest/userguide/intro.html).

## Specialist hosted Macs

### Scaleway: best burst option

The M4-M is the closest rental to the quoted 32 GB Mac: 32 GB RAM and 1.02 TB local SSD for €0.29/hour, with a €199 monthly figure and a 24-hour minimum. It is materially cheaper than AWS.

The important limitation is persistence: deletion is irreversible and destroys the local data. The correct design is an idempotent image/bootstrap plus encrypted remote cache/state backup. The user should see a single Yaver “Wake build Mac” action, not the restore mechanics.

Sources: [Scaleway Apple Silicon prices](https://www.scaleway.com/en/pricing/apple-silicon/) and [Apple Silicon quickstart and deletion behavior](https://www.scaleway.com/en/docs/apple-silicon/quickstart/).

### MacStadium: best simple always-on rental

MacStadium offers persistent dedicated Macs without AWS host allocation mechanics. A current 32 GB M2 Pro/2 TB tier is $349/month. It is operationally simple, but the quoted Mac mini pays for itself in under five months at the current exchange rate. Use it only if datacenter operations and provider support are worth that premium or purchasing hardware is temporarily impossible.

Source: [MacStadium pricing](https://www.macstadium.com/pricing).

## Managed cloud CI after purchasing the Mac

The purchased Mac does not have to be a CI runner. It can remain the persistent personal/Yaver box while production releases run on ephemeral paid cloud machines.

For Yaver, one complete Apple release set is not one build. The current deploy code has four distinct archive/upload lanes:

1. iOS TestFlight, which already embeds the watchOS companion and includes the CarPlay-capable iOS target.
2. tvOS TestFlight.
3. visionOS TestFlight.
4. macOS desktop TestFlight.

The `watchos` target does not upload a separate app because the current watch app ships inside the iOS archive. SFMG and Talos currently each add one iOS/TestFlight archive, not the four standalone Yaver lanes.

Until measured CI timings exist, the comparison below uses a three-hour example for Yaver's four-job Apple set. This is a cost model, not a runtime claim.

| Managed service | Fixed requirement | Variable charge | Three-hour example | Practical result |
|---|---:|---:|---:|---|
| Xcode Cloud included tier | Apple Developer membership already required | 25 compute hours/month included | 0 incremental cost for about 8 such sets/month | Cheapest if project compatibility is proven |
| Xcode Cloud 100-hour tier | $49.99/month | Tiered hours, unused hours do not roll over | 2,415 TL/month for up to about 33 sets | Best high-volume Apple-native economics |
| Codemagic M2 | No base fee stated for pay-as-you-go | $0.095/min | 826 TL/set | Good economical fallback |
| Codemagic M4 | No base fee stated for pay-as-you-go | $0.114/min | 992 TL/set | Recommended current-code-safe default |
| GitHub standard macOS | GitHub Free can be used | $0.062/min after allowance | 539 TL/set | Cheapest raw rate, but only 14 GB disk |
| GitHub M2 Pro larger | GitHub Team/Enterprise; Team starts at $4/user/month | $0.102/min, always billable | 887 TL/set plus seats | Still only 14 GB disk |
| GitLab M2 Pro | Premium $29/user/month annually | 12× compute-minute factor; $10/1,000 extra units | 2,160 units/set; 1,044 TL overage value | 50 GB disk, but beta and migration overhead |
| Bitrise M4 Large budget | $180/month | Budget includes up to 6,000 M4 Large minutes | 8,697 TL/month | Makes sense only at much higher volume or for its mobile tooling |

At the current FX rate, monthly Codemagic M4 examples are approximately 992 TL for one three-hour Apple set, 3,966 TL for four, and 7,932 TL for eight. Actual billing should be calculated as `sum(job minutes) × rate`; splitting the platforms into separate jobs does not add a subscription fee and avoids per-job timeout problems.

If the combined Yaver + SFMG + Talos Apple release set measures five hours rather than three, one run would cost approximately 1,653 TL on Codemagic M4, 1,377 TL on Codemagic M2, 899 TL on GitHub standard macOS, or 1,479 TL on GitHub M2 Pro. GitLab M2 Pro would consume 3,600 weighted minutes and have a 1,740 TL overage value. Xcode Cloud's included 25 hours would cover five such combined release sets per month. These examples deliberately expose the linear formula; measured minutes should replace both scenarios after the first cold runs.

### Recommended hosted release order

1. **Codemagic M4 now.** It can execute the repository's canonical shell deploy scripts, provides a 10-core M4/16 GB machine, and its current Xcode 26.4 image reports roughly 142 GB free. The image includes iOS, watchOS, tvOS, and visionOS runtimes. This is enough headroom for Yaver, SFMG, and Talos without weakening their real disk preflights.
2. **Pilot Xcode Cloud.** Apple includes 25 hours/month with the Developer Program and supports private GitHub repositories directly. It also integrates signing, TestFlight, and App Store Connect. The blocker is repository shape: Apple requires a continuously present, consistent Xcode project/workspace and warns that third-party tools which dynamically generate or edit it can make configuration or builds fail. Yaver currently modifies native targets during its deploy path, so it needs a real compatibility build before becoming the default.
3. **Retain the existing GitHub Yaver iOS job, but do not choose GitHub as the all-platform builder yet.** Its 14 GB disk is below SFMG/Talos's 20 GiB free-space guards and leaves very little room for four Apple SDK/archive lanes. Even GitHub's paid 12-core Intel and M2 Pro larger runners list the same 14 GB SSD.
4. **Do not migrate to GitLab just for Apple builds.** Its 50 GB disk is better than GitHub, but its hosted macOS service is beta, can queue or hang on limited AWS Mac capacity, requires Premium/Ultimate, and its M2 Pro effective overage is $0.12 per actual minute. Codemagic M4 is $0.114/min without moving the repositories.

For cloud CI, signing should be reconstructed automatically on each ephemeral worker. Store the P12/P8/JKS and their passwords in the provider's encrypted secret store, create a temporary keychain during the job, and destroy it afterward. That satisfies “never configure it again” for the operator without turning a long-lived CI machine into a secret warehouse.

Sources: [Apple Developer Program and included Xcode Cloud hours](https://developer.apple.com/programs/whats-included/), [Xcode Cloud plans](https://developer.apple.com/xcode-cloud/get-started/), [Xcode Cloud private SCM and project requirements](https://developer.apple.com/documentation/xcode/Setting-up-your-project-to-use-Xcode-Cloud), [Codemagic pricing](https://docs.codemagic.io/billing/pricing/), [Codemagic Xcode 26.4 machine and SDK specification](https://docs.codemagic.io/specs-macos/xcode-26-4/), and [Bitrise pricing](https://bitrise.io/pricing).

## GitHub Actions and GitLab CI for private repositories

### GitHub Actions

For private repositories, GitHub Free and Free organizations include 2,000 standard-runner minutes per month; Pro and Team include 3,000. Standard macOS overage is $0.062/minute. Standard runners are also free for public repositories, and self-hosted runner execution does not consume hosted minutes.

At a 45-minute build:

- Standard macOS paid usage: $2.79, approximately 135 TL.
- M2 Pro larger runner: $4.59, approximately 222 TL.
- Twelve paid standard builds/month: about 1,618 TL.
- If all build time fits the private-repository allowance, the marginal runner cost is zero.

However, each job receives a fresh machine, so signing material must be injected and removed on every run. This is already implemented for Yaver TestFlight. More importantly, both standard and larger GitHub macOS runners list only 14 GB SSD. That is below the 20 GiB free-space preflight in SFMG and Talos, and leaves little room around Yaver's 10/16 GiB requirements. A larger GitHub runner adds CPU/RAM but does not solve the listed disk limit.

GitHub Actions has the lowest general-purpose raw hosted-Mac rate if each repository's cold build is proven on the runner. Its disk constraint prevents calling it the all-stack default today, and it cannot host Yaver's always-ready interactive remote box.

Sources: [GitHub Actions billing and included quotas](https://docs.github.com/en/billing/concepts/product-billing/github-actions), [runner rates](https://docs.github.com/en/billing/reference/actions-runner-pricing), and [runner specifications](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).

### GitLab CI

GitLab's macOS hosted runners are currently beta and require Premium or Ultimate. Premium is $29/user/month billed annually and includes 10,000 compute minutes for the group. The M1 runner consumes minutes at a 6× factor; the M2 Pro runner consumes them at 12×, so 10,000 included units represent only about 833 actual M2 Pro minutes, or roughly eighteen 45-minute jobs. The M2 Pro runner provides 16 GB RAM and 50 GB disk, which is much better aligned with SFMG/Talos than GitHub's listed 14 GB disk.

Each GitLab macOS job must create its own keychain. The beta is backed by limited AWS bare-metal capacity and GitLab documents possible queueing/hangs. Because the repositories already use GitHub, GitLab is not the preferred migration solely for Mac builds; consider it only if its 50 GB worker solves a proven GitHub disk problem and the organization already wants GitLab Premium.

Sources: [GitLab pricing](https://about.gitlab.com/pricing/), [hosted macOS runners](https://docs.gitlab.com/ci/runners/hosted_runners/macos/), and [compute-minute factors](https://docs.gitlab.com/ci/pipelines/compute_minutes/).

## Proposed no-manual-configuration architecture

The UX can be pay-as-you-go while preserving state, even when the machine is disposable:

```text
Developer / Yaver app
        |
        | task + immutable commit SHA
        v
Hetzner Linux workspace  ---- commit/push ----> private GitHub repository
        |                                           |
        | ordinary JS/Go/web/Android work           | signed build request
        v                                           v
   cheap compute                          Yaver mac-builder controller
                                                     |
                                    allocate/restore/probe Mac
                                                     |
                                           archive + sign + upload
                                                     |
                                      snapshot/backup + safe release
```

### State layers

1. **Toolchain image:** pinned macOS/Xcode, Node, Go, JDK, Android SDK/NDK, CocoaPods, Fastlane, Yaver CLI, and build scripts.
2. **Encrypted mutable state:** dependency caches, DerivedData policy, checked-out repositories, and runner registration metadata. Repositories are still rebuilt from an immutable commit, never trusted as the deploy source merely because a worktree happens to exist.
3. **Secret source of truth:** Apple distribution P12 and password, App Store Connect P8/key ID/issuer/team, Android keystore and passwords, Play service-account JSON, and scoped Convex/Cloudflare/npm credentials.
4. **Ephemeral materialization:** create a dedicated build keychain, import the P12, set the key partition list, write P8/JKS files with owner-only permissions, build, then destroy the temporary files/keychain before release.

Never upload the entire local home directory or `~/.yaver/local-secrets.env`. The latter contains machine-local login/sudo/keychain credentials and the repository rules explicitly prohibit syncing it to cloud or GitHub secrets.

### Boot and readiness contract

Every wake operation should perform capability probes rather than inventory checks:

- Can the Yaver agent answer `/info`, rather than merely having a PID?
- Can `xcodebuild` find the required SDK and archive a minimal signing probe?
- Can `codesign` access the private key non-interactively?
- Can App Store Connect authenticate with the configured API key?
- Can Gradle read the intended signing configuration without exposing values?
- Is there enough real free disk for the selected repository?

Only then should the worker become `ready`.

### Proposed Yaver lifecycle

```text
parked -> provisioning -> restoring -> probing -> ready
                                             |
                                             v
building -> verifying -> snapshotting -> release_scheduled -> parked
    |             |              |
    +-------------+--------------+----> named failure + route to fix
```

The controller should expose a provider-neutral `mac_builder` interface with AWS and Scaleway drivers. A future command can look like:

```text
yaver deploy ios --commit <sha> --builder mac-cloud
```

Required product behavior:

- Show the 24-hour minimum, accrued cost, and earliest safe release time before allocation.
- Queue and coalesce Yaver/SFMG/Talos builds into the same paid 24-hour window.
- Enforce one heavy Gradle/Xcode build at a time unless measured capacity proves parallelism safe.
- Snapshot/backup and verify it before destroying a worker; a failed snapshot blocks automatic destruction.
- Use stable reason codes for quota denial, no capacity, restore failure, signing-key import failure, Xcode/SDK mismatch, insufficient disk, App Store rejection, and cleanup failure.
- Give every failure an invocable route to fix and stream the repair output.
- Put a hard TTL cleanup alarm outside the worker so a crashed controller cannot leave an expensive Mac allocated indefinitely.

## Recommended rollout

### Phase 1 — measure, without committing to hardware

- Keep daily coding on Hetzner.
- Put Yaver's existing GitHub TestFlight lane through a real cold build and record time, free disk, cache hit rate, and signing success.
- Add equivalent non-submitting build/sign validation for SFMG and Talos first; do not upload until explicitly approved.
- Rent one Scaleway M4-M 24-hour block and run all three repositories serially. Record provisioning-to-ready, build duration, maximum disk, and restore time.

### Phase 2 — choose by measured frequency

- Up to about four batched Mac days/month: Scaleway M4-M.
- Roughly five to six distinct Mac days/month: compare the measured friction and expected Mac resale value; this is the crossover zone.
- More than six distinct Mac days/month, frequent interactive remote use, or a desire for instant readiness: buy the Mac mini.
- AWS only for an explicit AWS-control-plane/compliance requirement or credits, not merely because it is called pay-as-you-go.

### Phase 3 — productize in Yaver

Implement the provider-neutral lifecycle, cost guard, encrypted restore, operation probes, and release queue. The same interface can later choose a purchased/self-hosted Mac, Scaleway, AWS, or a CI job without changing the user's deploy command.

## Tax and accounting notes

AWS Turkey states that it is the local seller, charges Turkish VAT, and issues local tax invoices. Entering SIMKAB's valid VKN/TRN puts it on the invoice and can support input VAT credit subject to the company's circumstances. That means AWS and the Mac purchase should both be compared ex-VAT if the VAT is fully recoverable; VAT still affects cash timing.

Scaleway, Hetzner, MacStadium, GitHub, and GitLab may be imported electronic/cloud services for a Turkish company. Reverse-charge VAT, withholding, invoice acceptance, and capitalization/depreciation treatment need confirmation from SIMKAB's accountant. The figures in this report do not assume an income-tax depreciation benefit.

Source: [AWS Turkey tax help](https://aws.amazon.com/tax-help/turkey/).

## Assumptions and limitations

- The 97,499 TL Apple quote and full 20% VAT recoverability are supplied assumptions, not independent tax advice.
- Exchange-rate conversions are point-in-time and will move. Source: [TCMB 2026-09-04 exchange rates](https://www.tcmb.gov.tr/kurlar/202609/04092026.xml).
- CI examples assume a 45-minute job; actual costs must be replaced with measured cold-build durations from all three repositories.
- Provider availability and AWS Dedicated Host quotas are account-, region-, and availability-zone-specific.
- Scaleway's automated state restoration is a proposed Yaver design. Its documentation explicitly says deleted server data is lost.
- Purchase comparisons exclude financing cost, AppleCare, UPS, electricity, local internet, replacement downtime, staff time, and resale transaction costs.
- No cloud resource was provisioned, changed, or deleted during this audit.
