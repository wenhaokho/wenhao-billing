// Checks oauthReturnTo (the login bounce of the MCP connector). The frontend
// has no test runner; scripts/check.sh runs this with
// `node --experimental-strip-types src/router/returnTo.check.mjs`.
import { oauthReturnTo } from "./returnTo.ts";

const origin = "https://billing.example.com";
const cases = [
  ["/oauth/authorize?response_type=code&client_id=a&state=x%20y", "/oauth/authorize?response_type=code&client_id=a&state=x%20y"],
  ["/oauth/authorize", "/oauth/authorize"],
  ["//evil.com", null],
  ["//evil.com/oauth/authorize", null],
  ["/oauth/../x", null],
  ["/oauth/%2e%2e/dashboard", null],
  ["/oauth/..%2fx", "/oauth/..%2fx"],
  ["https://evil.com/oauth/authorize", null],
  ["https://billing.example.com/oauth/authorize", null],
  ["javascript:alert(1)", null],
  ["/oauth\\..\\x", null],
  ["/oauth/\\evil.com", null],
  ["/oauth/a\nb", null],
  ["/oauthx", null],
  ["/dashboard", null],
  ["", null],
  [undefined, null],
  [["/oauth/authorize"], null],
];
let bad = 0;
for (const [input, want] of cases) {
  const got = oauthReturnTo(input, origin);
  const ok = got === want;
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"} ${JSON.stringify(input)} -> ${JSON.stringify(got)}${ok ? "" : ` (want ${JSON.stringify(want)})`}`);
}
console.log(bad ? `${bad} FAILED` : `all ${cases.length} passed`);
process.exit(bad ? 1 : 0);
