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
// Keep the embedded product distinct from the phone's Yaver.app. Giving both
// targets PRODUCT_NAME=Yaver makes simulator/archive builds emit the same
// output directory and Xcode refuses the graph with "Multiple commands
// produce .../Yaver.app" before compiling either app.
const PRODUCT_NAME = "YaverWatch";
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
];
const SOURCES = SOURCE_RELATIVE_TO_GROUP.map((f) => `../../watch/YaverWatch/${f}`);
// v1 deliberately has no guest/invitation client UI. Old generated projects
// can still carry these file/build references after the source was removed;
// prune them on every repair so TestFlight cannot be blocked by stale inputs.
const RETIRED_SOURCES = new Set([
  "../../watch/YaverWatch/Views/GuestAccessView.swift",
]);
// Version keys must NOT be rewritten on the repair path. The committed
// pbxproj pins the REAL version (1.18.167, bumped by sync-versions.sh); this
// script only scaffolds new targets. Before 2026-08-12 both settings were
// applied unconditionally in repairTarget(), so EVERY deploy rewrote the
// watch + Live Activity targets to 1.0.0 — the committed MARKETING_VERSION
// was clobbered in the working tree and the archive shipped mismatched
// embedded targets. These keys are now applied on first creation only.
const VERSION_KEYS = new Set(["CURRENT_PROJECT_VERSION", "MARKETING_VERSION"]);

// applySettings writes the scaffold settings onto a build config. On the
// repair path (target already exists) version keys are skipped so committed
// values survive; on first creation they are written so a fresh target has
// sane defaults. Centralised so the create path and repairTarget agree.
function applySettings(bs, repair) {
  for (const [key, val] of Object.entries(settings)) {
    if (repair && VERSION_KEYS.has(key)) continue;
    bs[key] = val;
  }
}

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
    ensurePlainSSH(k);
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
  // First creation: write every setting including the version defaults.
  applySettings(bs, false);
}

const project = proj.hash.project.objects.PBXProject[proj.getFirstProject().uuid];
project.attributes.TargetAttributes[targetUuid] = {
  DevelopmentTeam: TEAM,
  ProvisioningStyle: "Automatic",
};

repairTarget(targetUuid);
ensurePlainSSH(targetUuid);

fs.writeFileSync(PROJ, proj.writeSync());
console.log(`✓ added ${TARGET} watchOS companion target → ${path.relative(process.cwd(), PROJ)}`);
console.log("  Next: build the Yaver iOS scheme; it now embeds YaverWatch.app under Watch/.");

function stripQuotes(s) {
  return String(s || "").replace(/^"|"$/g, "");
}

// Wire an explicit target dependency main → watch so the watchOS app builds
// BEFORE the iOS target's "Embed Watch Content" copy phase runs. Without this
// the archive fails with `lstat(.../Release-watchos/YaverWatch.app): No such file`
// because older generated projects could leave the watch product settings and
// its YaverWatch.app productReference out of sync. The xcode lib's
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
    // Repair path: preserve the committed MARKETING_VERSION /
    // CURRENT_PROJECT_VERSION — clobbering them to the 1.0.0 scaffold
    // default was the 2026-08-12 bug that shipped mismatched embedded
    // targets on every deploy.
    applySettings(bs, true);
  }

  removeRetiredSources(targetUuid);

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

function removeRetiredSources(targetUuid) {
  const objs = proj.hash.project.objects;
  const target = proj.pbxNativeTargetSection()[targetUuid];
  if (!target) return;
  const fileRefs = proj.pbxFileReferenceSection();
  const buildFiles = proj.pbxBuildFileSection();
  const sourcePhases = objs.PBXSourcesBuildPhase || {};
  const groups = objs.PBXGroup || {};

  for (const phaseRef of target.buildPhases || []) {
    const phase = sourcePhases[phaseRef.value];
    if (!phase || phase.isa !== "PBXSourcesBuildPhase") continue;
    phase.files = (phase.files || []).filter((entry) => {
      const buildFile = buildFiles[entry.value];
      const fileRefUuid = buildFile?.fileRef;
      const fileRef = fileRefUuid ? fileRefs[fileRefUuid] : null;
      const sourcePath = stripQuotes(fileRef?.path);
      if (!RETIRED_SOURCES.has(sourcePath)) return true;

      delete buildFiles[entry.value];
      delete buildFiles[`${entry.value}_comment`];
      if (fileRefUuid) {
        delete fileRefs[fileRefUuid];
        delete fileRefs[`${fileRefUuid}_comment`];
        for (const [groupKey, group] of Object.entries(groups)) {
          if (groupKey.endsWith("_comment") || !group || typeof group !== "object") continue;
          group.children = (group.children || []).filter((child) => child.value !== fileRefUuid);
        }
      }
      return false;
    });
  }
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

// Both the standalone watch project and the iPhone-embedded watch target
// link the SAME account-independent SSH package.
function ensurePlainSSH(targetUUID) {
 const objects=proj.hash.project.objects;
 const project=objects.PBXProject[proj.getFirstProject().uuid];
 const locals=objects.XCLocalSwiftPackageReference ||= {};
 let packageID=Object.keys(locals).find(id=>String(locals[id]?.relativePath || '').replace(/^"|"$/g,'')==='../../apple/PlainSSH');
 if(!packageID){packageID=proj.generateUuid();locals[packageID]={isa:"XCLocalSwiftPackageReference",relativePath:'"../../apple/PlainSSH"'};locals[packageID+"_comment"]="PlainSSH";}
 project.packageReferences ||= [];
 if(!project.packageReferences.some(ref=>ref.value===packageID))project.packageReferences.push({value:packageID,comment:"PlainSSH"});
 const products=objects.XCSwiftPackageProductDependency ||= {};
 const target=objects.PBXNativeTarget[targetUUID];target.packageProductDependencies ||= [];
 let productID=target.packageProductDependencies.find(ref=>products[ref.value]?.productName==="PlainSSH")?.value;
 if(!productID){productID=proj.generateUuid();products[productID]={isa:"XCSwiftPackageProductDependency",package:packageID,package_comment:"PlainSSH",productName:"PlainSSH"};products[productID+"_comment"]="PlainSSH";target.packageProductDependencies.push({value:productID,comment:"PlainSSH"});}
 let phase=target.buildPhases.map(ref=>objects.PBXFrameworksBuildPhase?.[ref.value]).find(Boolean);
 if(!phase){proj.addBuildPhase([],"PBXFrameworksBuildPhase","Frameworks",targetUUID);phase=target.buildPhases.map(ref=>objects.PBXFrameworksBuildPhase?.[ref.value]).find(Boolean);}
 if(!phase.files.some(ref=>objects.PBXBuildFile[ref.value]?.productRef===productID)){const id=proj.generateUuid();objects.PBXBuildFile[id]={isa:"PBXBuildFile",productRef:productID,productRef_comment:"PlainSSH"};objects.PBXBuildFile[id+"_comment"]="PlainSSH in Frameworks";phase.files.push({value:id,comment:"PlainSSH in Frameworks"});}
}
