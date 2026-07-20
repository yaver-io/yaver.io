import { describe, expect, it } from "bun:test";
import { buildNativeBuildRequest, nativeBuildFailureMessage, nativeBuildFailureTitle } from "../../mobile/src/lib/nativeBuild";

describe("nativeBuild UI mapping", () => {
  it("builds the shared build-native request contract", () => {
    const request = buildNativeBuildRequest("ios", {
      consumerVersion: "1.18.22",
      consumerBuild: "260",
      consumerSdkVersion: "1.0.0",
      consumerHermesBCVersion: 96,
    });
    expect(request).toMatchObject({
      platform: "ios",
      consumerVersion: "1.18.22",
      consumerBuild: "260",
      consumerSdkVersion: "1.0.0",
      consumerHermesBCVersion: 96,
    });
    expect(request.consumerNativeModules).toMatchObject({
      "expo-status-bar": expect.any(String),
      "react-native-reanimated": expect.any(String),
    });
  });

  it("maps version/family drift to Compatibility Blocked (fatal — corrupts the JSI contract)", () => {
    expect(nativeBuildFailureTitle({ code: "NATIVE_MODULE_VERSION_MISMATCH" })).toBe("Compatibility Blocked");
    expect(nativeBuildFailureTitle({ code: "REACT_VERSION_MISMATCH" })).toBe("Compatibility Blocked");
    expect(nativeBuildFailureTitle({ code: "FRAMEWORK_VERSION_MISMATCH" })).toBe("Compatibility Blocked");
  });

  it("maps a MISSING module to a non-blocking title (warning-only as of 2026-07-20)", () => {
    // A module absent from the host throws only if called unguarded — the app
    // still loads. The title must not read as a hard block.
    expect(nativeBuildFailureTitle({ code: "NATIVE_MODULE_INCOMPATIBLE" })).toBe("Some Features Unavailable");
  });

  it("maps Hermes bytecode mismatch distinctly", () => {
    expect(nativeBuildFailureTitle({ code: "BC_VERSION_MISMATCH" })).toBe("Hermes Version Mismatch");
  });

  it("falls back to Load Failed for generic errors", () => {
    expect(nativeBuildFailureTitle({ code: "SOMETHING_ELSE" })).toBe("Load Failed");
  });

  it("renders structured version mismatch details with build output tail", () => {
    expect(nativeBuildFailureMessage({
      phase: "compat",
      code: "NATIVE_MODULE_VERSION_MISMATCH",
      helpHint: "retry after aligning versions",
      output: "Bundled 2402 modules\nDone writing bundle output",
      nativeModuleVersionMismatches: [
        { name: "expo-mail-composer", projectVersion: "55.0.13", hostVersion: "15.0.8" },
        { name: "react-native-worklets", projectVersion: "0.7.4", hostVersion: "0.5.1" },
      ],
    })).toBe(
      "phase: compat\n" +
      "Yaver blocked restart because the project's native runtime contract does not match the mobile host.\n" +
      "expo-mail-composer: project 55.0.13 vs host 15.0.8\n" +
      "react-native-worklets: project 0.7.4 vs host 0.5.1\n" +
      "retry after aligning versions\n" +
      "---\n" +
      "Bundled 2402 modules\n" +
      "Done writing bundle output"
    );
  });

  it("renders missing native modules with build output tail", () => {
    expect(nativeBuildFailureMessage({
      phase: "compat",
      code: "NATIVE_MODULE_INCOMPATIBLE",
      incompatibleNativeModules: ["react-native-yaver-fictional-test-module"],
      output: "Done writing bundle output",
    })).toBe(
      "phase: compat\n" +
      "Some native modules this project declares are not in Yaver's mobile host. The app still loads; those features may be unavailable if it calls them.\n" +
      "Missing in Yaver: react-native-yaver-fictional-test-module\n" +
      "---\n" +
      "Done writing bundle output"
    );
  });

  it("renders framework runtime mismatches concisely", () => {
    expect(nativeBuildFailureMessage({
      phase: "compat",
      code: "FRAMEWORK_VERSION_MISMATCH",
      reactNativeVersionMismatch: {
        projectVersion: "0.81.6",
        hostVersion: "0.81.5",
      },
      expoVersionMismatch: {
        projectVersion: "54.0.33",
        hostVersion: "54.0.0",
      },
      helpHint: "align Expo/React Native exactly",
      output: "Done writing bundle output",
    })).toBe(
      "phase: compat\n" +
      "Yaver blocked restart because the guest app does not match the selected mobile host runtime family.\n" +
      "React Native: project 0.81.6 vs host 0.81.5\n" +
      "Expo: project 54.0.33 vs host 54.0.0\n" +
      "align Expo/React Native exactly\n" +
      "---\n" +
      "Done writing bundle output"
    );
  });
});
