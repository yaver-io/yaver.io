#!/usr/bin/env node
/**
 * add-liveactivity-ios-target.js — add the YaverActivity widget extension to
 * the committed mobile/ios/Yaver.xcodeproj/project.pbxproj.
 *
 * This is what puts Yaver on the CarPlay Dashboard. Per Apple's CarPlay
 * Developer Guide (June 2026): "Your app does not need to be a CarPlay app to
 * support widgets and Live Activities in CarPlay." No entitlement, no category
 * — a Live Activity declaring supplementalActivityFamilies([.small]) is
 * rendered on the Dashboard.
 *
 * Two targets share ONE attributes file:
 *   - the app  → starts/updates the activity (native-liveactivity/ios/*)
 *   - the ext  → draws it (native-liveactivity/widget/*)
 * YaverActivityAttributes.swift compiles into BOTH, so the shape can't drift.
 *
 * Follows add-watch-ios-target.js. Idempotent. Run from repo root:
 *   node scripts/add-liveactivity-ios-target.js
 */
const path = require("path");
const fs = require("fs");
const xcode = require(path.join(__dirname, "..", "mobile", "node_modules", "xcode"));

const PROJ = path.join(__dirname, "..", "mobile", "ios", "Yaver.xcodeproj", "project.pbxproj");
const TARGET = "YaverActivity";
const PRODUCT_REF_BASENAME = `${TARGET}.appex`;
const BUNDLE = "io.yaver.mobile.activity";
const TEAM = "5SJZ4KA39A";
// supplementalActivityFamilies — the CarPlay Dashboard hook — is iOS 18.4+.
// The main app stays at 15.5; only this extension is gated.
const DEPLOY = "18.4";
const INFO_PLIST = "../native-liveactivity/widget/Info.plist";

// Extension sources: the widget UI + the SHARED attributes file.
const EXT_SOURCES = [
  "../native-liveactivity/widget/YaverLiveActivityBundle.swift",
  "../native-liveactivity/widget/YaverActivityViews.swift",
  "../native-liveactivity/shared/YaverActivityAttributes.swift",
];

// App-target sources: the RN bridge + the SAME shared attributes file.
const APP_SOURCES = [
  "../native-liveactivity/ios/YaverLiveActivity.swift",
  "../native-liveactivity/ios/YaverLiveActivity.m",
  "../native-liveactivity/shared/YaverActivityAttributes.swift",
];

const settings = {
  PRODUCT_NAME: TARGET,
  PRODUCT_BUNDLE_IDENTIFIER: BUNDLE,
  DEVELOPMENT_TEAM: TEAM,
  CODE_SIGN_STYLE: "Automatic",
  INFOPLIST_FILE: INFO_PLIST,
  GENERATE_INFOPLIST_FILE: "NO",
  SWIFT_VERSION: "5.0",
  IPHONEOS_DEPLOYMENT_TARGET: DEPLOY,
  TARGETED_DEVICE_FAMILY: '"1,2"',
  CURRENT_PROJECT_VERSION: "1",
  MARKETING_VERSION: "1.0.0",
  SDKROOT: "iphoneos",
  SKIP_INSTALL: "YES",
  LD_RUNPATH_SEARCH_PATHS:
    '"$(inherited) @executable_path/Frameworks @executable_path/../../Frameworks"',
};

const proj = xcode.project(PROJ);
proj.parseSync();

const native = proj.pbxNativeTargetSection();
for (const k of Object.keys(native)) {
  const t = native[k];
  if (t && typeof t === "object" && stripQuotes(t.name) === TARGET) {
    repairTarget(k);
    ensureAppSources();
    fs.writeFileSync(PROJ, proj.writeSync());
    console.log(`✓ ${TARGET} target already present — repaired settings/paths.`);
    process.exit(0);
  }
}

const target = proj.addTarget(TARGET, "app_extension", TARGET, BUNDLE);
const targetUuid = target.uuid;

const group = proj.addPbxGroup(EXT_SOURCES.concat([INFO_PLIST]), TARGET);
const mainGroup =
  proj.hash.project.objects.PBXGroup[
    proj.hash.project.objects.PBXProject[proj.getFirstProject().uuid].mainGroup
  ];
mainGroup.children.push({ value: group.uuid, comment: TARGET });

proj.addBuildPhase(EXT_SOURCES, "PBXSourcesBuildPhase", "Sources", targetUuid);
proj.addBuildPhase([], "PBXFrameworksBuildPhase", "Frameworks", targetUuid);
proj.addBuildPhase([], "PBXResourcesBuildPhase", "Resources", targetUuid);

// NOTE: do NOT add an embed phase here. proj.addTarget(…, "app_extension")
// ALREADY creates a "Copy Files" phase on the app target that embeds the
// .appex into PlugIns (dstSubfolderSpec 13). Adding a second one makes two
// build phases produce the same output, and the archive dies with
// "Unexpected duplicate tasks" / "Multiple commands produce Yaver.app".
// dedupeEmbedPhases() below is the guard.

const cfgList = native[targetUuid].buildConfigurationList;
const configLists = proj.pbxXCConfigurationList();
const buildConfigs = proj.pbxXCBuildConfigurationSection();
for (const ref of configLists[cfgList].buildConfigurations.map((c) => c.value)) {
  const bs = buildConfigs[ref].buildSettings;
  for (const [key, val] of Object.entries(settings)) bs[key] = val;
}

const project = proj.hash.project.objects.PBXProject[proj.getFirstProject().uuid];
project.attributes.TargetAttributes[targetUuid] = {
  DevelopmentTeam: TEAM,
  ProvisioningStyle: "Automatic",
};

repairTarget(targetUuid);
ensureAppSources();

fs.writeFileSync(PROJ, proj.writeSync());
console.log(`✓ added ${TARGET} widget extension → ${path.relative(process.cwd(), PROJ)}`);
console.log("  CarPlay Dashboard Live Activity. Build the Yaver iOS scheme to embed it.");

function stripQuotes(s) {
  return String(s || "").replace(/^"|"$/g, "");
}

function repairTarget(targetUuid) {
  const nativeSec = proj.pbxNativeTargetSection();
  const t = nativeSec[targetUuid];
  if (!t) return;

  const fileRefs = proj.pbxFileReferenceSection();
  const productRef = t.productReference;
  if (productRef && fileRefs[productRef]) {
    delete fileRefs[productRef].name;
    fileRefs[productRef].path = PRODUCT_REF_BASENAME;
    fileRefs[productRef].explicitFileType = '"wrapper.app-extension"';
    fileRefs[productRef].includeInIndex = 0;
    fileRefs[`${productRef}_comment`] = PRODUCT_REF_BASENAME;
  }
  t.productType = '"com.apple.product-type.app-extension"';

  for (const [key, ref] of Object.entries(fileRefs)) {
    if (key.endsWith("_comment") || !ref || typeof ref !== "object") continue;
    for (const prop of ["fileEncoding", "lastKnownFileType", "explicitFileType", "includeInIndex"]) {
      if (ref[prop] === undefined || ref[prop] === "undefined") delete ref[prop];
    }
  }

  const cfg = t.buildConfigurationList;
  const lists = proj.pbxXCConfigurationList();
  const cfgs = proj.pbxXCBuildConfigurationSection();
  for (const ref of (lists[cfg]?.buildConfigurations || []).map((c) => c.value)) {
    const bs = cfgs[ref]?.buildSettings;
    if (!bs) continue;
    for (const [key, val] of Object.entries(settings)) bs[key] = val;
  }

  // addPbxGroup leaves path as the literal string "undefined". Xcode then
  // resolves children against mobile/ios/undefined/, so "../native-liveactivity"
  // collapses to mobile/ios/native-liveactivity and every source is "not found".
  // The group is a virtual folder — it must have no path at all.
  const groups = proj.hash.project.objects.PBXGroup || {};
  for (const [key, g] of Object.entries(groups)) {
    if (key.endsWith("_comment") || !g || typeof g !== "object") continue;
    if (stripQuotes(g.name) === TARGET) delete g.path;
  }

  ensureSources(targetUuid, EXT_SOURCES, TARGET);
  ensureTargetDependency(proj.getFirstTarget().uuid, targetUuid);
  dedupeEmbedPhases();
}

/**
 * Keep exactly ONE copy phase embedding YaverActivity.appex on the app target.
 * Two phases with the same output = "Unexpected duplicate tasks" at archive
 * time, which is fatal and only shows up on a device archive (the simulator
 * scheme is already broken for an unrelated reason). Idempotent, and it also
 * repairs a project that a previous buggy run of this script already polluted.
 */
function dedupeEmbedPhases() {
  const objs = proj.hash.project.objects;
  const cp = objs.PBXCopyFilesBuildPhase || {};
  const bf = proj.pbxBuildFileSection();
  const fr = proj.pbxFileReferenceSection();
  const appTarget = proj.pbxNativeTargetSection()[proj.getFirstTarget().uuid];
  if (!appTarget) return;

  const embedsAppex = (phase) =>
    (phase.files || []).some((f) => {
      const b = bf[f.value];
      const ref = b && fr[b.fileRef];
      return ref && stripQuotes(ref.path) === PRODUCT_REF_BASENAME;
    });

  let kept = false;
  appTarget.buildPhases = (appTarget.buildPhases || []).filter((ph) => {
    const phase = cp[ph.value];
    if (!phase || !embedsAppex(phase)) return true;
    if (!kept) {
      // Normalize the survivor's name — addTarget calls it a bare "Copy Files".
      phase.name = '"Embed Foundation Extensions"';
      cp[`${ph.value}_comment`] = "Embed Foundation Extensions";
      ph.comment = "Embed Foundation Extensions";
      kept = true;
      return true;
    }
    delete cp[ph.value];
    delete cp[`${ph.value}_comment`];
    return false;
  });
}

/** The RN bridge + shared attributes must compile into the APP target too. */
function ensureAppSources() {
  ensureSources(proj.getFirstTarget().uuid, APP_SOURCES, appGroupUuid());
}

/**
 * The main app's source group. Can't be looked up by name — the project has TWO
 * PBXGroups called "Yaver" — and xcode's addSourceFile() crashes when handed no
 * group at all (it falls back to a nonexistent "Plugins" group). So identify it
 * structurally: the group that contains AppDelegate.swift.
 */
function appGroupUuid() {
  const groups = proj.hash.project.objects.PBXGroup || {};
  const fileRefs = proj.pbxFileReferenceSection();
  for (const gk of Object.keys(groups)) {
    if (gk.endsWith("_comment")) continue;
    const g = groups[gk];
    if (!g || typeof g !== "object") continue;
    for (const child of g.children || []) {
      const ref = fileRefs[child.value];
      const p = ref && typeof ref.path === "string" ? stripQuotes(ref.path) : "";
      if (p.endsWith("AppDelegate.swift")) return gk;
    }
  }
  throw new Error("could not locate the app source group (no AppDelegate.swift)");
}

function ensureSources(targetUuid, sources, groupUuidOrName) {
  const fileRefs = proj.pbxFileReferenceSection();
  const referencedIn = new Set();
  // A file can legitimately belong to two targets (the shared attributes
  // file), so dedupe per-target by walking THIS target's Sources phase rather
  // than the global file-reference table.
  const nt = proj.pbxNativeTargetSection()[targetUuid];
  const sourcePhases = proj.hash.project.objects.PBXSourcesBuildPhase || {};
  const buildFiles = proj.pbxBuildFileSection();
  for (const ph of nt?.buildPhases || []) {
    const phase = sourcePhases[ph.value];
    if (!phase || phase.isa !== "PBXSourcesBuildPhase") continue;
    for (const f of phase.files || []) {
      const bf = buildFiles[f.value];
      const ref = bf && fileRefs[bf.fileRef];
      if (ref && typeof ref.path === "string") referencedIn.add(stripQuotes(ref.path));
    }
  }

  // Accept either a uuid (app target) or a group NAME (the extension's own
  // group, created by addPbxGroup above).
  let groupUuid = groupUuidOrName;
  const groups = proj.hash.project.objects.PBXGroup || {};
  if (groupUuid && !groups[groupUuid]) {
    groupUuid = undefined;
    for (const gk of Object.keys(groups)) {
      if (gk.endsWith("_comment")) continue;
      const g = groups[gk];
      if (g && typeof g === "object" && stripQuotes(g.name) === groupUuidOrName) {
        groupUuid = gk;
        break;
      }
    }
  }
  if (!groupUuid) {
    // addSourceFile() dereferences a "Plugins" group that this project doesn't
    // have and throws on null. Never call it without a real group.
    throw new Error(`no PBXGroup resolved for sources: ${sources.join(", ")}`);
  }

  for (const src of sources) {
    if (referencedIn.has(src)) continue;
    proj.addSourceFile(src, { target: targetUuid }, groupUuid);
  }
}

function ensureTargetDependency(mainUuid, depUuid) {
  const objs = proj.hash.project.objects;
  objs.PBXTargetDependency = objs.PBXTargetDependency || {};
  objs.PBXContainerItemProxy = objs.PBXContainerItemProxy || {};
  for (const [k, p] of Object.entries(objs.PBXContainerItemProxy)) {
    if (k.endsWith("_comment") || !p || typeof p !== "object") continue;
    if (stripQuotes(p.remoteGlobalIDString) === depUuid) return;
  }
  const nt = proj.pbxNativeTargetSection()[mainUuid];
  if (nt && !Array.isArray(nt.dependencies)) nt.dependencies = [];
  proj.addTargetDependency(mainUuid, [depUuid]);
}
