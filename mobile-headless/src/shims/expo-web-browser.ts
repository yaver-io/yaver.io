// Shim for expo-web-browser. Used to open OAuth URLs; no-op in headless.
export async function openAuthSessionAsync(url: string): Promise<any> {
  process.stdout.write(JSON.stringify({ __openAuth: url }) + "\n");
  return { type: "dismiss" };
}
export async function openBrowserAsync(url: string): Promise<any> {
  process.stdout.write(JSON.stringify({ __openBrowser: url }) + "\n");
  return { type: "dismiss" };
}
export const dismissAuthSession = () => {};
export default { openAuthSessionAsync, openBrowserAsync, dismissAuthSession };
