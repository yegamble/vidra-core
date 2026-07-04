#!/usr/bin/env node
// Generates a curated Postman v2.1 collection + environment for vidra-core from
// api/openapi.yaml. Zero dependencies (Node stdlib only) so it runs anywhere the
// repo does, with no network. It covers the SYSTEM probes + the AUTH happy/error
// paths with real assertions, chaining the registered account's token through
// the authenticated requests — enough for `newman run` to validate a live
// backend end to end.
//
// It is "generated from the contract" in two senses: the collection name/version
// come from the spec's info block, and every request path is checked to EXIST in
// api/openapi.yaml — if a route is renamed/removed, regeneration fails loudly so
// the collection can never silently drift from the documented API.
//
// Regenerate:   node docs/postman/generate.mjs   (or `make postman`)
//
// For a FULL, every-endpoint collection instead of this curated one, use
// openapi-to-postman:  npx --yes openapi-to-postmanv2 -s api/openapi.yaml \
//   -o docs/postman/vidra-core.full.postman_collection.json -p

import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, "..", "..");
const specPath = join(repoRoot, "api", "openapi.yaml");
const spec = readFileSync(specPath, "utf8");

// Pull name + version from the OpenAPI info block (best-effort; falls back).
const title = (spec.match(/^\s{2}title:\s*(.+)$/m)?.[1] ?? "Vidra Core").trim();
const version = (spec.match(/^\s{2}version:\s*["']?([^"'\n]+)/m)?.[1] ?? "0.0.0").trim();

// pathDocumented asserts a contract path appears as an OpenAPI path item. Root
// probes (/healthz, /readyz, /version) and /api/v1/* are all in the spec.
function pathDocumented(p) {
  return new RegExp(`^\\s{2}${p.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}:`, "m").test(spec);
}

const jsonHeader = { key: "Content-Type", value: "application/json" };

// The curated request list. Each entry becomes a Postman item; `path` is checked
// against the contract. `tests` are JS lines run by Postman/Newman after the
// response. Auth requests reuse the token captured by the register/login steps.
const requests = [
  // ---- System (unauthenticated probes + public discovery) ----
  {
    folder: "System",
    name: "Liveness (healthz)",
    method: "GET",
    path: "/healthz",
    tests: [
      `pm.test("200 OK", () => pm.response.to.have.status(200));`,
      `pm.test("status ok", () => pm.expect(pm.response.json().status).to.eql("ok"));`,
    ],
  },
  {
    folder: "System",
    name: "Readiness (readyz)",
    method: "GET",
    path: "/readyz",
    tests: [
      // 200 when deps are up; 503 when a dependency is down — both are contract-valid.
      `pm.test("200 or 503", () => pm.expect([200, 503]).to.include(pm.response.code));`,
    ],
  },
  {
    folder: "System",
    name: "Build version",
    method: "GET",
    path: "/version",
    tests: [
      `pm.test("200 OK", () => pm.response.to.have.status(200));`,
      `pm.test("names vidra", () => pm.expect(pm.response.json().name).to.eql("vidra"));`,
    ],
  },
  {
    folder: "System",
    name: "NodeInfo discovery",
    method: "GET",
    path: "/api/v1/nodeinfo",
    tests: [`pm.test("200 OK", () => pm.response.to.have.status(200));`],
  },
  {
    folder: "System",
    name: "Public instance config",
    method: "GET",
    path: "/api/v1/instance",
    tests: [
      `pm.test("200 OK", () => pm.response.to.have.status(200));`,
      `pm.test("has registration flag", () => pm.expect(pm.response.json()).to.have.property("registration_enabled"));`,
    ],
  },

  // ---- Auth (happy + error paths; token chained onward) ----
  {
    folder: "Auth",
    name: "Register (happy) → captures token",
    method: "POST",
    path: "/api/v1/auth/register",
    // Randomise the identity so re-runs against a persistent DB don't 409.
    prerequest: [
      `const n = Date.now().toString(36) + Math.floor(Math.random()*1e4).toString(36);`,
      `pm.collectionVariables.set("authUsername", "postman_" + n);`,
      `pm.collectionVariables.set("authEmail", "postman_" + n + "@vidra.test");`,
    ],
    body: { username: "{{authUsername}}", email: "{{authEmail}}", password: "{{authPassword}}" },
    tests: [
      `pm.test("201 Created", () => pm.response.to.have.status(201));`,
      `const j = pm.response.json();`,
      `pm.test("returns a bearer token", () => pm.expect(j.token).to.be.a("string").and.not.empty);`,
      `pm.collectionVariables.set("token", j.token);`,
      `if (j.refresh_token) pm.collectionVariables.set("refreshToken", j.refresh_token);`,
    ],
  },
  {
    folder: "Auth",
    name: "Register (validation error)",
    method: "POST",
    path: "/api/v1/auth/register",
    body: { username: "a", email: "not-an-email", password: "short" },
    tests: [
      `pm.test("422 Unprocessable Entity", () => pm.response.to.have.status(422));`,
      `pm.test("carries an error", () => pm.expect(pm.response.json()).to.have.property("error"));`,
    ],
  },
  {
    folder: "Auth",
    name: "Login (happy)",
    method: "POST",
    path: "/api/v1/auth/login",
    body: { email: "{{authEmail}}", password: "{{authPassword}}" },
    tests: [
      `pm.test("200 OK", () => pm.response.to.have.status(200));`,
      `const j = pm.response.json();`,
      `if (j.token) pm.collectionVariables.set("token", j.token);`,
      `pm.test("issued a session (token or mfa challenge)", () => pm.expect(j.token || j.mfa_required).to.be.ok);`,
    ],
  },
  {
    folder: "Auth",
    name: "Login (wrong password)",
    method: "POST",
    path: "/api/v1/auth/login",
    body: { email: "{{authEmail}}", password: "wrong-password-xxxxx" },
    tests: [`pm.test("401 Unauthorized", () => pm.response.to.have.status(401));`],
  },
  {
    folder: "Auth",
    name: "Current account (me)",
    method: "GET",
    path: "/api/v1/auth/me",
    bearer: true,
    tests: [
      `pm.test("200 OK", () => pm.response.to.have.status(200));`,
      `pm.test("is the registered user", () => pm.expect(pm.response.json().username).to.eql(pm.collectionVariables.get("authUsername")));`,
    ],
  },

  // ---- Admin (first registered account is admin on a fresh DB) ----
  {
    folder: "Admin",
    name: "System status (build + rate_limits)",
    method: "GET",
    path: "/api/v1/admin/system",
    bearer: true,
    tests: [
      // 200 when the caller is an admin (first account on a fresh DB); 403 otherwise.
      `pm.test("200 (admin) or 403 (non-admin)", () => pm.expect([200, 403]).to.include(pm.response.code));`,
      `if (pm.response.code === 200) {`,
      `  const j = pm.response.json();`,
      `  pm.test("reports software + effective rate_limits", () => {`,
      `    pm.expect(j.software.name).to.eql("vidra");`,
      `    pm.expect(j.rate_limits).to.have.property("window_seconds");`,
      `  });`,
      `}`,
    ],
  },
];

// Fail closed if any curated path drifted out of the contract.
const missing = [...new Set(requests.map((r) => r.path))].filter((p) => !pathDocumented(p));
if (missing.length) {
  console.error(`Path(s) not found in api/openapi.yaml (contract drift): ${missing.join(", ")}`);
  process.exit(1);
}

// Build the folder tree, preserving first-seen folder order.
const folders = [];
const byFolder = new Map();
for (const r of requests) {
  if (!byFolder.has(r.folder)) {
    const folderItem = { name: r.folder, item: [] };
    byFolder.set(r.folder, folderItem);
    folders.push(folderItem);
  }
  byFolder.get(r.folder).item.push(toItem(r));
}

function toItem(r) {
  const url = {
    raw: `{{baseUrl}}${r.path}`,
    host: ["{{baseUrl}}"],
    path: r.path.replace(/^\//, "").split("/"),
  };
  const request = { method: r.method, header: [], url };
  if (r.body) {
    request.header.push(jsonHeader);
    request.body = { mode: "raw", raw: JSON.stringify(r.body, null, 2), options: { raw: { language: "json" } } };
  }
  if (r.bearer) {
    request.auth = { type: "bearer", bearer: [{ key: "token", value: "{{token}}", type: "string" }] };
  }
  const event = [];
  if (r.prerequest) {
    event.push({ listen: "prerequest", script: { type: "text/javascript", exec: r.prerequest } });
  }
  if (r.tests) {
    event.push({ listen: "test", script: { type: "text/javascript", exec: r.tests } });
  }
  return { name: r.name, event, request, response: [] };
}

const collection = {
  info: {
    name: `${title} — smoke`,
    _postman_id: "vidra-core-smoke",
    description:
      "Curated system + auth happy/error smoke collection for vidra-core, generated from api/openapi.yaml by docs/postman/generate.mjs. " +
      `Contract version ${version}. Run against a live backend with newman (see docs/postman/README.md).`,
    schema: "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
  },
  item: folders,
  variable: [
    { key: "authUsername", value: "", type: "string" },
    { key: "authEmail", value: "", type: "string" },
    { key: "authPassword", value: "postman-Sup3rSecret", type: "string" },
    { key: "token", value: "", type: "string" },
    { key: "refreshToken", value: "", type: "string" },
  ],
};

const environment = {
  id: "vidra-core-local",
  name: "vidra-core (local)",
  values: [
    { key: "baseUrl", value: "http://localhost:8080", type: "default", enabled: true },
  ],
  _postman_variable_scope: "environment",
};

const collPath = join(here, "vidra-core.postman_collection.json");
const envPath = join(here, "vidra-core.postman_environment.json");
writeFileSync(collPath, JSON.stringify(collection, null, 2) + "\n");
writeFileSync(envPath, JSON.stringify(environment, null, 2) + "\n");
console.log(`Wrote ${collPath}`);
console.log(`Wrote ${envPath}`);
console.log(`Covered ${requests.length} requests across ${folders.length} folders (contract v${version}).`);
