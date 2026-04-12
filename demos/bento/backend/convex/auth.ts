// Convex HTTP actions that handle OAuth callbacks for Bento.
// Providers enabled: Apple, Google, Email+password.
//
// Each handler receives the provider-issued code, exchanges it
// for an access token, fetches the user profile, and upserts a
// row into the users table. The real secret values live in Convex
// env vars — set them with `npx convex env set`.
import { httpRouter } from "convex/server";
import { httpAction } from "./_generated/server";

const http = httpRouter();

// Stub handlers — fill in with your preferred OAuth client.
http.route({
  path: "/auth/callback/apple",
  method: "GET",
  handler: httpAction(async () => new Response("TODO: apple callback")),
});
http.route({
  path: "/auth/callback/google",
  method: "GET",
  handler: httpAction(async () => new Response("TODO: google callback")),
});
http.route({
  path: "/auth/callback/microsoft",
  method: "GET",
  handler: httpAction(async () => new Response("TODO: microsoft callback")),
});

export default http;
