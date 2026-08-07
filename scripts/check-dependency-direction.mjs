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
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");

/**
 * Read the module path rather than duplicating it. A hardcoded literal makes
 * this gate fail OPEN on a fork or a module rename: the prefix filter would
 * match nothing, every internal dependency would be dropped, and the check
 * would print PASS while inspecting an empty list.
 */
function gatewayModulePath() {
  const goMod = resolve(root, "apps/gateway/go.mod");
  let text;
  try {
    text = readFileSync(goMod, "utf8");
  } catch (error) {
    console.error(`FAIL: cannot read ${goMod}: ${error.message}`);
    process.exit(1);
  }
  const match = text.match(/^\s*module\s+(\S+)\s*$/m);
  if (!match) {
    console.error(`FAIL: no module directive in ${goMod}; cannot scope the dependency check`);
    process.exit(1);
  }
  return match[1];
}

const MODULE = gatewayModulePath();

/** Inner layer -> the only internal packages it may reach. */
const RULES = [
  { layer: "domain", allows: ["internal/domain"] },
  { layer: "ports", allows: ["internal/domain", "internal/ports"] },
];

const failures = [];

for (const { layer, allows } of RULES) {
  const result = spawnSync(
    "go",
    [
      "-C",
      "apps/gateway",
      "list",
      // Without `-test`, go list follows only the default production graph, so
      // a forbidden inner-layer import that lives in a `_test.go` file is
      // silently omitted and this gate fails open.
      "-test",
      "-deps",
      "-f",
      "{{.ImportPath}}",
      `./internal/${layer}/...`,
    ],
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
    // `go list -test` emits synthetic entries: `pkg.test` (the generated test
    // binary) and `pkg [pkg.test]` (the test-augmented variant of a package).
    // Strip the bracketed suffix so the variant is judged as its real package,
    // and drop the generated binary, which is not a source dependency.
    .map((line) => line.replace(/\s+\[.*\]$/, ""))
    .filter((line) => line.length > 0 && !line.endsWith(".test"))
    .filter((line) => line === MODULE || line.startsWith(`${MODULE}/`))
    .map((line) => line.slice(MODULE.length + 1))
    // An external test package (`package foo_test` in internal/foo) is listed
    // as `internal/foo_test`. It is the same layer's own test surface, not a
    // dependency on an outer package, so judge it as its real package.
    .map((pkg) => pkg.replace(/_test$/, ""))
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
