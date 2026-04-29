import { describe, expect, it } from "bun:test";
import { buildNativeBuildRequest, nativeBuildFailureMessage, nativeBuildFailureTitle } from "../../mobile/src/lib/nativeBuild";

describe("nativeBuild UI mapping", () => {
  it("builds the shared build-native request contract", () => {
    expect(buildNativeBuildRequest("ios", {
      consumerVersion: "1.18.22",
      consumerBuild: "260",
      consumerSdkVersion: "1.0.0",
      consumerHermesBCVersion: 96,
    })).toEqual({
      platform: "ios",
      consumerVersion: "1.18.22",
      consumerBuild: "260",
      consumerSdkVersion: "1.0.0",
      consumerHermesBCVersion: 96,
    });
  });

  it("maps compatibility-family codes to Compatibility Blocked", () => {
    expect(nativeBuildFailureTitle({ code: "NATIVE_MODULE_INCOMPATIBLE" })).toBe("Compatibility Blocked");
    expect(nativeBuildFailureTitle({ code: "NATIVE_MODULE_VERSION_MISMATCH" })).toBe("Compatibility Blocked");
    expect(nativeBuildFailureTitle({ code: "REACT_VERSION_MISMATCH" })).toBe("Compatibility Blocked");
  });

  it("maps Hermes bytecode mismatch distinctly", () => {
    expect(nativeBuildFailureTitle({ code: "BC_VERSION_MISMATCH" })).toBe("Hermes Version Mismatch");
  });

  it("falls back to Load Failed for generic errors", () => {
    expect(nativeBuildFailureTitle({ code: "SOMETHING_ELSE" })).toBe("Load Failed");
  });

  it("renders concise structured version mismatch details without raw output spam", () => {
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
      "retry after aligning versions"
    );
  });

  it("renders missing native modules without raw build output", () => {
    expect(nativeBuildFailureMessage({
      phase: "compat",
      code: "NATIVE_MODULE_INCOMPATIBLE",
      incompatibleNativeModules: ["react-native-yaver-fictional-test-module"],
      output: "Done writing bundle output",
    })).toBe(
      "phase: compat\n" +
      "Yaver blocked restart because the project uses native modules the mobile host does not include.\n" +
      "Missing in Yaver: react-native-yaver-fictional-test-module"
    );
  });
});
