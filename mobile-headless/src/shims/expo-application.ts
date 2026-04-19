// Shim for expo-application. The mobile lib layer only reads static
// identifiers; never called during headless test flows.
export const applicationId = "io.yaver.mobile";
export const applicationName = "Yaver";
export const nativeBuildVersion = "1";
export const nativeApplicationVersion = "1.17.22";
export default { applicationId, applicationName, nativeBuildVersion, nativeApplicationVersion };
