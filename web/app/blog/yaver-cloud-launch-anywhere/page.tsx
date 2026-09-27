import { permanentRedirect } from "next/navigation";

/** The supported replacement is a machine or VPS controlled by the user. */
export default function RetiredCloudLaunchPost() {
  permanentRedirect("/docs/self-hosting");
}
