// The MCP connector's login bounce: the API's /oauth/authorize sends an
// anonymous browser to /login?return_to=/oauth/authorize?..., and after
// signing in the browser goes back there with a full page load (the authorize
// page is served by the API, not the SPA).
//
// oauthReturnTo returns the path to follow, or null for anything that is not
// a same-origin path under /oauth/: absolute URLs, protocol-relative
// "//host", backslashes, control characters, and dot segments that would
// leave /oauth/ ("/oauth/../x") are all ignored.
export function oauthReturnTo(raw: unknown, origin: string): string | null {
  if (typeof raw !== "string" || !raw.startsWith("/oauth/")) return null;
  // Browsers read "\" as "/" in a path; no legitimate return_to carries one.
  if (/[\\\u0000-\u001f\u007f]/.test(raw)) return null;
  let url: URL;
  try {
    url = new URL(raw, origin);
  } catch {
    return null;
  }
  if (url.origin !== new URL(origin).origin) return null;
  // After dot segments are resolved, the path must still be under /oauth/.
  if (!url.pathname.startsWith("/oauth/")) return null;
  return url.pathname + url.search;
}
