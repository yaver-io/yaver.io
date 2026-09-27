import { permanentRedirect } from "next/navigation";

/** Retired with the hosted Cloud Workspace product. Preserve old inbound links
 * without advertising or activating hosted compute. */
export default function RetiredCloudImagePost() {
  permanentRedirect("/docs/self-hosting");
}
