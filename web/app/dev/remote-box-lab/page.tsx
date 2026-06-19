import { notFound } from "next/navigation";
import RemoteBoxLabClient from "./remote-box-lab-client";

export default function RemoteBoxLabPage() {
  if (process.env.NODE_ENV === "production" && process.env.YAVER_REMOTE_BOX_LAB !== "1") {
    notFound();
  }
  return <RemoteBoxLabClient />;
}
