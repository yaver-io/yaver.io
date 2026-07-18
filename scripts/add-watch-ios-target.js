#!/usr/bin/env node
/**
 * add-watch-ios-target.js — add the Yaver watchOS companion target to the
 * committed mobile/ios/Yaver.xcodeproj/project.pbxproj.
 *
 * The phone bridge is injected by mobile/plugins/withWatchBridge.js, but
 * WCSession still needs an embedded watchOS peer. This script makes that
 * target durable in the checked-in pbxproj, following add-mesh-ios-target.js.
 *
 * Idempotent. Run from repo root:
 *   node scripts/add-watch-ios-target.js
 */
const path = require("path");
const fs = require("fs");
const xcode = require(path.join(__dirname, "..", "mobile", "node_modules", "xcode"));

const PROJ = path.join(__dirname, "..", "mobile", "ios", "Yaver.xcodeproj", "project.pbxproj");
const TARGET = "YaverWatch";
const PRODUCT_NAME = "Yaver";
const PRODUCT_REF_BASENAME = `${TARGET}.app`;
const BUNDLE = "io.yaver.mobile.watch";
const TEAM = "5SJZ4KA39A";
const DEPLOY = "10.0";
const INFO_PLIST = "../../watch/YaverWatch/Info.plist";

const SOURCE_RELATIVE_TO_GROUP = [
  "YaverWatchApp.swift",
  "WatchStore.swift",
  "WatchProtocol.swift",
  "PhoneSession.swift",
  "SessionClient.swift",
  "AgentClient.swift",
  "Backend.swift",
  "Dictation.swift",
  "Haptics.swift",
  "Speech.swift",
  "Complications.swift",
  "YaverNativeCatalog.swift",
  "BoxLifecycle.swift",
  "Views/RootView.swift",
  "Views/ConfirmView.swift",
  "Views/SignInView.swift",
  "Views/SettingsView.swift",
  "Views/WakeProgressView.swift",
  // SettingsView links to GuestAccessView. The standalone watch/ project is
  // XcodeGen and globs the directory, so a new view is picked up there for
  // free — this list is hand-maintained and silently wasn't, which failed the
  // embedded build with "cannot find 'GuestAccessView' in scope".
  "Views/GuestAccessView.swift",
];
const SOURCES = SOURCE_RELATIVE_TO_GROUP.map((f) => `../../watch/YaverWatch/${f}`);
const settings = {
  PRODUCT_NAME: PRODUCT_NAME,
  PRODUCT_BUNDLE_IDENTIFIER: BUNDLE,
  DEVELOPMENT_TEAM: TEAM,
  CODE_SIGN_STYLE: "Automatic",
  INFOPLIST_FILE: INFO_PLIST,
  GENERATE_INFOPLIST_FILE: "NO",
  SWIFT_VERSION: "5.0",
  WATCHOS_DEPLOYMENT_TARGET: DEPLOY,
  TARGETED_DEVICE_FAMILY: "4",
  CURRENT_PROJECT_VERSION: "1",
  MARKETING_VERSION: "1.0.0",
  SDKROOT: "watchos",
  SKIP_INSTALL: "YES",
  ASSETCATALOG_COMPILER_APPICON_NAME: "AppIcon",
  LD_RUNPATH_SEARCH_PATHS: '"$(inherited) @executable_path/Frameworks @executable_path/../../Frameworks"',
};
const ASSET_CATALOG = "../../watch/YaverWatch/Assets.xcassets";

const proj = xcode.project(PROJ);
proj.parseSync();

const native = proj.pbxNativeTargetSection();
for (const k of Object.keys(native)) {
  const t = native[k];
  if (t && typeof t === "object" && stripQuotes(t.name) === TARGET) {
    repairTarget(k);
    fs.writeFileSync(PROJ, proj.writeSync());
    console.log(`✓ ${TARGET} target already present — repaired settings/paths.`);
    process.exit(0);
  }
}

const target = proj.addTarget(TARGET, "application", TARGET, BUNDLE);
const targetUuid = target.uuid;

const productRef = native[targetUuid].productReference;

const group = proj.addPbxGroup(SOURCES.concat([INFO_PLIST]), TARGET);
const mainGroup = proj.hash.project.objects.PBXGroup[proj.hash.project.objects.PBXProject[proj.getFirstProject().uuid].mainGroup];
mainGroup.children.push({ value: group.uuid, comment: TARGET });

proj.addBuildPhase(SOURCES, "PBXSourcesBuildPhase", "Sources", targetUuid);
proj.addBuildPhase([], "PBXFrameworksBuildPhase", "Frameworks", targetUuid);
proj.addBuildPhase([], "PBXResourcesBuildPhase", "Resources", targetUuid);
proj.addBuildPhase([PRODUCT_REF_BASENAME], "PBXCopyFilesBuildPhase", "Embed Watch Content", proj.getFirstTarget().uuid, "watch2_app", '"$(CONTENTS_FOLDER_PATH)/Watch"');

const cfgList = native[targetUuid].buildConfigurationList;
const configLists = proj.pbxXCConfigurationList();
const buildConfigs = proj.pbxXCBuildConfigurationSection();
const configRefs = configLists[cfgList].buildConfigurations.map((c) => c.value);
for (const ref of configRefs) {
  const bs = buildConfigs[ref].buildSettings;
  for (const [key, val] of Object.entries(settings)) bs[key] = val;
}

const project = proj.hash.project.objects.PBXProject[proj.getFirstProject().uuid];
project.attributes.TargetAttributes[targetUuid] = {
  DevelopmentTeam: TEAM,
  ProvisioningStyle: "Automatic",
};

repairTarget(targetUuid);

fs.writeFileSync(PROJ, proj.writeSync());
console.log(`✓ added ${TARGET} watchOS companion target → ${path.relative(process.cwd(), PROJ)}`);
console.log("  Next: build the Yaver iOS scheme; it now embeds Yaver.app under Watch/.");

function stripQuotes(s) {
  return String(s || "").replace(/^"|"$/g, "");
}

// Wire an explicit target dependency main → watch so the watchOS app builds
// BEFORE the iOS target's "Embed Watch Content" copy phase runs. Without this
// the archive fails with `lstat(.../Release-watchos/Yaver.app): No such file`
// because implicit-dependency resolution can't match (the watch product is
// PRODUCT_NAME=Yaver but its productReference is YaverWatch.app). The xcode lib's
// addTargetDependency silently no-ops when these sections don't yet exist
// (pbxProject.js guards on their presence), so create them first. Idempotent.
function ensureTargetDependency(mainUuid, depUuid) {
  const objs = proj.hash.project.objects;
  objs.PBXTargetDependency = objs.PBXTargetDependency || {};
  objs.PBXContainerItemProxy = objs.PBXContainerItemProxy || {};
  for (const [k, p] of Object.entries(objs.PBXContainerItemProxy)) {
    if (k.endsWith("_comment") || !p || typeof p !== "object") continue;
    if (stripQuotes(p.remoteGlobalIDString) === depUuid) return; // already wired
  }
  const nt = proj.pbxNativeTargetSection()[mainUuid];
  if (nt && !Array.isArray(nt.dependencies)) nt.dependencies = [];
  proj.addTargetDependency(mainUuid, [depUuid]);
}

function repairTarget(targetUuid) {
  const native = proj.pbxNativeTargetSection();
  const target = native[targetUuid];
  if (!target) return;

  const fileRefs = proj.pbxFileReferenceSection();
  const productRef = target.productReference;
  if (productRef && fileRefs[productRef]) {
    delete fileRefs[productRef].name;
    fileRefs[productRef].path = PRODUCT_REF_BASENAME;
    fileRefs[productRef].explicitFileType = "wrapper.application";
    fileRefs[productRef].includeInIndex = 0;
    fileRefs[`${productRef}_comment`] = PRODUCT_REF_BASENAME;
  }
  target.productType = "\"com.apple.product-type.application\"";

  for (const [key, ref] of Object.entries(fileRefs)) {
    if (key.endsWith("_comment") || !ref || typeof ref !== "object") continue;
    for (const prop of ["fileEncoding", "lastKnownFileType", "explicitFileType", "includeInIndex"]) {
      if (ref[prop] === undefined || ref[prop] === "undefined") delete ref[prop];
    }
    if (typeof ref.path === "string" && ref.path.startsWith("../../watch/YaverWatch/../../watch/YaverWatch/")) {
      ref.path = ref.path.replace("../../watch/YaverWatch/../../watch/YaverWatch/", "../../watch/YaverWatch/");
    }
  }

  const buildFiles = proj.pbxBuildFileSection();
  for (const [key, bf] of Object.entries(buildFiles)) {
    if (key.endsWith("_comment") || !bf || typeof bf !== "object") continue;
    if (bf.fileRef === productRef) {
      bf.fileRef_comment = PRODUCT_REF_BASENAME;
      buildFiles[`${key}_comment`] = `${PRODUCT_REF_BASENAME} in Embed Watch Content`;
    }
  }

  const groups = proj.hash.project.objects.PBXGroup || {};
  for (const [key, group] of Object.entries(groups)) {
    if (key.endsWith("_comment") || !group || typeof group !== "object") continue;
    if (group.name === TARGET) delete group.path;
    for (const child of group.children || []) {
      if (child.value === productRef) child.comment = PRODUCT_REF_BASENAME;
    }
  }

  const copyPhases = proj.hash.project.objects.PBXCopyFilesBuildPhase || {};
  for (const [key, phase] of Object.entries(copyPhases)) {
    if (key.endsWith("_comment") || copyPhases[`${key}_comment`] !== "Embed Watch Content") continue;
    for (const file of phase.files || []) {
      file.comment = PRODUCT_REF_BASENAME;
    }
  }

  const cfgList = target.buildConfigurationList;
  const configLists = proj.pbxXCConfigurationList();
  const buildConfigs = proj.pbxXCBuildConfigurationSection();
  const configRefs = (configLists[cfgList]?.buildConfigurations || []).map((c) => c.value);
  for (const ref of configRefs) {
    const bs = buildConfigs[ref]?.buildSettings;
    if (!bs) continue;
    for (const [key, val] of Object.entries(settings)) bs[key] = val;
  }

  // The target already existed, so the addBuildPhase(SOURCES,…) at first-create
  // never ran for any file added to SOURCE_RELATIVE_TO_GROUP later. Add any
  // source that isn't already referenced, so new watch files (e.g.
  // BoxLifecycle.swift, WakeProgressView.swift) actually compile into the
  // shipped watch app instead of failing the archive with "cannot find type".
  ensureSources(targetUuid);

  // The iOS app must depend on the watch target, else it never builds and the
  // Embed Watch Content copy phase fails at archive time.
  ensureTargetDependency(proj.getFirstTarget().uuid, targetUuid);

  // watchOS apps MUST ship an app-icon asset catalog, else App Store validation
  // rejects the export ("Missing Icons" / "CFBundleIconName is missing"). The
  // catalog lives at watch/YaverWatch/Assets.xcassets; add it to this target's
  // Resources build phase. addResourceFile is idempotent (hasFile guard).
  ensureResourceCatalog(targetUuid);
}

// Ensure every entry in SOURCES is a compiled source of this target. Runs on
// the repair path (target already present), where the original
// addBuildPhase(SOURCES) never re-runs. Idempotent: skips any path already
// referenced. Uses the same PBX primitives as the first-create path.
function ensureSources(targetUuid) {
  const fileRefs = proj.pbxFileReferenceSection();
  const referenced = new Set();
  for (const k of Object.keys(fileRefs)) {
    if (k.endsWith("_comment")) continue;
    const r = fileRefs[k];
    if (r && typeof r === "object" && typeof r.path === "string") {
      referenced.add(stripQuotes(r.path));
    }
  }

  // Locate this target's group (created with name = TARGET) so new file
  // references live alongside the existing watch sources in the navigator.
  let groupUuid = null;
  const groups = proj.hash.project.objects.PBXGroup || {};
  for (const gk of Object.keys(groups)) {
    if (gk.endsWith("_comment")) continue;
    const g = groups[gk];
    if (g && typeof g === "object" && stripQuotes(g.name) === TARGET) {
      groupUuid = gk;
      break;
    }
  }

  for (const src of SOURCES) {
    if (referenced.has(src)) continue;
    // addSourceFile wires PBXBuildFile + PBXFileReference and appends to the
    // target's Sources phase (opt.target routes to the right phase).
    proj.addSourceFile(src, { target: targetUuid }, groupUuid || undefined);
  }
}

// proj.addResourceFile() crashes on a folder reference (.xcassets), so wire the
// asset catalog into the target's Resources phase by hand. Idempotent: bail if
// its fileRef already exists.
function ensureResourceCatalog(targetUuid) {
  const objs = proj.hash.project.objects;
  const fileRefs = proj.pbxFileReferenceSection();
  for (const k of Object.keys(fileRefs)) {
    if (k.endsWith("_comment")) continue;
    const r = fileRefs[k];
    if (r && typeof r === "object" && stripQuotes(r.path) === ASSET_CATALOG) return;
  }
  const fileRefUuid = proj.generateUuid();
  const buildFileUuid = proj.generateUuid();
  fileRefs[fileRefUuid] = {
    isa: "PBXFileReference",
    lastKnownFileType: "folder.assetcatalog",
    name: "Assets.xcassets",
    path: ASSET_CATALOG,
    sourceTree: '"<group>"',
  };
  fileRefs[`${fileRefUuid}_comment`] = "Assets.xcassets";

  const buildFiles = proj.pbxBuildFileSection();
  buildFiles[buildFileUuid] = { isa: "PBXBuildFile", fileRef: fileRefUuid, fileRef_comment: "Assets.xcassets" };
  buildFiles[`${buildFileUuid}_comment`] = "Assets.xcassets in Resources";

  const nt = proj.pbxNativeTargetSection()[targetUuid];
  const resPhases = objs.PBXResourcesBuildPhase || {};
  for (const ph of nt.buildPhases || []) {
    const phase = resPhases[ph.value];
    if (phase && phase.isa === "PBXResourcesBuildPhase") {
      phase.files = phase.files || [];
      phase.files.push({ value: buildFileUuid, comment: "Assets.xcassets in Resources" });
      break;
    }
  }
  const groups = objs.PBXGroup || {};
  for (const gk of Object.keys(groups)) {
    if (gk.endsWith("_comment")) continue;
    const g = groups[gk];
    if (g && typeof g === "object" && stripQuotes(g.name) === TARGET) {
      g.children = g.children || [];
      g.children.push({ value: fileRefUuid, comment: "Assets.xcassets" });
      break;
    }
  }
}
