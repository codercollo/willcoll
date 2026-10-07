// `pnpm dev` from frontend/: starts the two Willcoll apps together.
//   platform  http://localhost:3001  sign-in, account, staff, billing, Super Admin
//   rentals   http://localhost:3000  the Rentals product
// Each has same-origin proxies aimed at the local backend:
//   rentals:   /v1/**            -> rentals-api   (RENTALS_PORT, else PORT, else 4000)
//              /_platform/v1/**  -> platform-api  (PLATFORM_PORT, else 4100)
//   platform:  /_platform/v1/**  -> platform-api
// The ports are read from ../backend/.env (the same file `make dev/up` uses),
// unless NUXT_API_INTERNAL_BASE / NUXT_API_PLATFORM_INTERNAL_BASE are already
// set in the environment. Nothing else from that file is read.
//
// Both run on `localhost`, and browsers do not scope cookies by port, so the one
// willcoll_session cookie signs you in to both. Each app's own URL (and the
// other's) is runtime config: NUXT_PUBLIC_APPS_PLATFORM / NUXT_PUBLIC_APPS_RENTALS.
import { spawn } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

// Ports: platform/package.json pins 3001; rentals uses Nuxt's default 3000.
const PLATFORM_APP_PORT = 3001;
const RENTALS_APP_PORT = 3000;

const envFile = fileURLToPath(new URL("../../backend/.env", import.meta.url));
const fromFile = {};
if (existsSync(envFile)) {
  for (const line of readFileSync(envFile, "utf8").split(/\r?\n/)) {
    const m = /^\s*([A-Z0-9_]+)\s*=\s*(.*?)\s*$/.exec(line);
    if (m) fromFile[m[1]] = m[2].replace(/^(["'])(.*)\1$/, "$2");
  }
}
const pick = (...names) => {
  for (const n of names) {
    const v = process.env[n] || fromFile[n];
    if (v) return v;
  }
};

const env = { ...process.env };
env.NUXT_API_INTERNAL_BASE ||= `http://localhost:${pick("RENTALS_PORT", "PORT") ?? 4000}`;
env.NUXT_API_PLATFORM_INTERNAL_BASE ||= `http://localhost:${pick("PLATFORM_PORT") ?? 4100}`;
env.NUXT_PUBLIC_APPS_PLATFORM ||= `http://localhost:${PLATFORM_APP_PORT}`;
env.NUXT_PUBLIC_APPS_RENTALS ||= `http://localhost:${RENTALS_APP_PORT}`;

console.log(`platform -> ${env.NUXT_PUBLIC_APPS_PLATFORM}   (sign in here)`);
console.log(`rentals  -> ${env.NUXT_PUBLIC_APPS_RENTALS}`);
console.log(`  /v1            proxied to ${env.NUXT_API_INTERNAL_BASE}   (rentals only)`);
console.log(`  /_platform/v1  proxied to ${env.NUXT_API_PLATFORM_INTERNAL_BASE}`);

const children = [
  ["platform", ["--filter", "platform", "dev"]],
  ["rentals", ["--filter", "rentals", "dev"]],
].map(([name, args]) => {
  const child = spawn("pnpm", [...args, ...process.argv.slice(2)], {
    stdio: "inherit",
    env,
    shell: true,
  });
  // One app dying takes the other down, so a half-working pair is never left running.
  child.on("exit", (code) => {
    console.log(`${name} exited (${code ?? 0})`);
    shutdown(code ?? 0);
  });
  return child;
});

let stopping = false;
function shutdown(code) {
  if (stopping) return;
  stopping = true;
  for (const c of children) c.kill("SIGTERM");
  process.exit(code);
}
for (const sig of ["SIGINT", "SIGTERM"]) process.on(sig, () => shutdown(0));
