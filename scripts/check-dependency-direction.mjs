#!/usr/bin/env node
/**
 * Dependency-direction check for the gateway's hexagonal layout (issue #126).
 *
 * Scope note, deliberately narrow: this repository does not document a full
 * layer-by-layer import matrix, so this check encodes only the invariant that
 * is already empirically true of the tracked tree and is uncontroversial for a
 * ports-and-adapters design - the inner layers do not depend on the outer ones:
 *
 *   internal/domain  imports no other internal package
 *   internal/ports   imports at most internal/domain
 *
 * Everything else (application, adapters, infrastructure, transport,
 * composition) is intentionally NOT gated here. Inventing a rule the repository
 * never agreed to would produce a gate that fails for reasons no ADR supports.
 * Widening this check should follow a documented decision, not this script.
 *
 * Run from the repository root: `node scripts/check-dependency-direction.mjs`.
 */

import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const MODULE = "github.com/monet88/pixelplus/apps/gateway";

/** Inner layer -> the only internal packages it may reach. */
const RULES = [
  { layer: "domain", allows: ["internal/domain"] },
  { layer: "ports", allows: ["internal/domain", "internal/ports"] },
];

const failures = [];

for (const { layer, allows } of RULES) {
  const result = spawnSync(
    "go",
    ["-C", "apps/gateway", "list", "-deps", "-f", "{{.ImportPath}}", `./internal/${layer}/...`],
    // No `shell: true`: it concatenates rather than escapes arguments, and the
    // `-f {{.ImportPath}}` template contains braces the shell would mangle.
    { cwd: root, encoding: "utf8" },
  );

  if (result.error) {
    console.error(`FAIL: cannot run go list: ${result.error.message}`);
    process.exit(1);
  }
  if (result.status !== 0) {
    console.error(`FAIL: go list failed for internal/${layer}`);
    if (result.stderr) console.error(result.stderr.trim());
    process.exit(1);
  }

  const internalDeps = result.stdout
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.startsWith(MODULE))
    .map((line) => line.slice(MODULE.length + 1))
    .filter((pkg) => pkg.startsWith("internal/"));

  for (const dep of internalDeps) {
    const permitted = allows.some((prefix) => dep === prefix || dep.startsWith(`${prefix}/`));
    if (!permitted) {
      failures.push(`internal/${layer} must not depend on ${dep}`);
    }
  }
}

if (failures.length > 0) {
  console.error(`FAIL: dependency direction has ${failures.length} violation(s)`);
  for (const message of [...new Set(failures)]) console.error(`- ${message}`);
  console.error("\nInner layers must not depend on outer ones. Invert the dependency behind a port.");
  process.exit(1);
}

console.log(
  `PASS: dependency direction (${RULES.map((rule) => `internal/${rule.layer}`).join(", ")} depend only inward)`,
);
