#!/usr/bin/env node
/**
 * Mutation-style tests for the canonical verify entrypoint (issue #126).
 *
 * The orchestrator is tested at its seams rather than by running the real
 * (multi-minute) verification: `buildPlan` decides *what* runs, and
 * `renderSummary` decides what an operator sees afterwards. Those two are the
 * behaviours the acceptance criteria constrain, so those are what is proved
 * here. Zero new dependencies, consistent with the other validators.
 *
 * Run from the repository root: `node scripts/test-verify-repository.mjs`.
 */

import assert from "node:assert/strict";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const entrypoint = resolve(root, "scripts/verify-repository.mjs");

const { buildPlan, renderSummary, requiredToolsFor, MODES, JOBS, REQUIRED_TOOLS } = await import(
  `file://${entrypoint.replaceAll("\\", "/")}`
);

const names = (mode) => buildPlan(mode).map((step) => step.name);

// --- mode composition ------------------------------------------------------

assert.deepEqual(MODES, ["fast", "full", "release"], "three modes must exist");

const fast = names("fast");
const full = names("full");
const release = names("release");

for (const required of [
  "gofmt",
  "go vet",
  "go test",
  "public API contract",
  "implementation-spec authority",
  "git diff --check",
]) {
  assert.ok(fast.includes(required), `--fast must include the ${required} gate`);
}

// Depth is cumulative: a deeper mode never drops a shallower mode's gate,
// otherwise "release passed" would be weaker than "fast passed".
for (const step of fast) {
  assert.ok(full.includes(step), `--full must retain the fast gate ${step}`);
}
for (const step of full) {
  assert.ok(release.includes(step), `--release must retain the full gate ${step}`);
}

assert.ok(full.length > fast.length, "--full must add depth over --fast");
assert.ok(release.length > full.length, "--release must add depth over --full");

for (const required of [
  "go test -race",
  "public API validator mutation suite",
  "chat stream wire",
  "dependency direction",
]) {
  assert.ok(full.includes(required), `--full must include the ${required} gate`);
}

assert.throws(() => buildPlan("nope"), /unknown mode/i, "an unknown mode must fail loudly");

// --- CI slices come from this plan, not a second command list ---------------

// #126 requires this entrypoint to be the only implementation. The workflow
// selects slices with `--job=`, so every step must name a real job and the
// slices must partition the mode exactly: a step owned by no job would be a
// gate that runs locally and silently never runs in CI.
for (const mode of MODES) {
  const plan = buildPlan(mode);
  for (const step of plan) {
    assert.ok(JOBS.includes(step.job), `${step.name} must be owned by a declared CI job`);
  }
  const sliced = JOBS.flatMap((job) => buildPlan(mode, job).map((step) => step.name));
  assert.deepEqual(
    [...sliced].sort(),
    plan.map((step) => step.name).sort(),
    `the --job slices of --${mode} must cover it exactly, with no step counted twice`,
  );
}

assert.throws(() => buildPlan("fast", "nope"), /unknown job/i, "an unknown job must fail loudly");

// The four PR-gate jobs from #123 must each own at least one --fast check,
// otherwise a required check would pass by running nothing.
for (const job of [
  "repository-hygiene",
  "gateway-unit-and-contract",
  "public-api-contract",
  "authority-consistency",
]) {
  assert.ok(buildPlan("fast", job).length > 0, `--fast --job=${job} must run real checks`);
}

// --- preflight scope --------------------------------------------------------

// A single job slice must not demand a toolchain it never invokes: requiring a
// Python interpreter from the Go job would push CI to install one just to
// satisfy preflight.
const goToolNames = requiredToolsFor(buildPlan("fast", "gateway-unit-and-contract")).map(
  (tool) => tool.name,
);
assert.ok(goToolNames.includes("go"), "the gateway job needs the Go toolchain");
assert.ok(!goToolNames.includes("python"), "the gateway job must not require Python");

// The public API validator shells out to python jsonschema, so preflight has to
// resolve it even though the step's own command is a Node one.
const apiToolNames = requiredToolsFor(buildPlan("fast", "public-api-contract")).map(
  (tool) => tool.name,
);
assert.ok(
  apiToolNames.includes("python jsonschema"),
  "the public API job validates with python jsonschema, so preflight must check it",
);

// A whole-mode run still resolves everything.
assert.deepEqual(
  requiredToolsFor(buildPlan("full")).map((tool) => tool.name).sort(),
  REQUIRED_TOOLS.map((tool) => tool.name).sort(),
  "--full exercises every declared tool, so preflight must resolve all of them",
);

// --- the silent-skip failure mode ------------------------------------------

// This is the single most important assertion in the file. #126 exists because
// a check that is skipped for a missing tool reads as green. Only Docker may be
// conditional, and only while announcing itself.
const conditional = buildPlan("release").filter((step) => step.optional);
for (const step of conditional) {
  assert.equal(
    step.requires,
    "docker",
    `only Docker-dependent steps may be optional, but ${step.name} is`,
  );
  assert.ok(step.skipNotice, `${step.name} must announce a skip rather than pass quietly`);
}

for (const step of buildPlan("release")) {
  assert.ok(step.command.length > 0, `${step.name} must define a real command`);
  if (!step.optional) {
    assert.ok(
      step.requires !== "docker",
      `${step.name} depends on Docker so it cannot be unconditionally required`,
    );
  }
}

assert.ok(REQUIRED_TOOLS.length > 0, "required tools must be declared for preflight");
for (const tool of REQUIRED_TOOLS) {
  assert.ok(tool.remedy, `missing ${tool.name} must state a remedy, not just fail`);
}

// --- summary reporting -----------------------------------------------------

const summary = renderSummary([
  { name: "gofmt", status: "pass", durationMs: 1200, exitCode: 0 },
  { name: "go test", status: "fail", durationMs: 8000, exitCode: 1, artifact: "apps/gateway" },
  { name: "docker smoke", status: "skipped", durationMs: 0, exitCode: null },
]);

assert.match(summary, /gofmt/, "summary must list every command run");
assert.match(summary, /go test/, "summary must list the failing command");
assert.match(summary, /docker smoke/, "summary must list the skipped command");
assert.match(summary, /1\.2s|1200/, "summary must report duration");
assert.match(summary, /apps\/gateway/, "summary must name the failing artifact");
assert.match(summary, /SKIPPED/i, "a skip must be visually loud, not silent");
assert.match(summary, /FAIL/i, "a failure must be stated in the summary");

// --- toolchain pinning is tracked, not assumed -----------------------------

const toolVersions = readFileSync(resolve(root, ".tool-versions"), "utf8");
for (const tool of ["go", "node", "python"]) {
  assert.match(toolVersions, new RegExp(`^${tool} \\d`, "m"), `${tool} must be pinned`);
}

const pkg = JSON.parse(readFileSync(resolve(root, "package.json"), "utf8"));
assert.ok(pkg.packageManager?.startsWith("npm@"), "packageManager must pin npm");
assert.ok(pkg.engines?.node, "engines.node must be declared");
assert.ok(
  !JSON.stringify(pkg.dependencies ?? {}).includes("sandcastle") &&
    !JSON.stringify(pkg.devDependencies ?? {}).includes("sandcastle"),
  "@ai-hero/sandcastle is unreferenced supply-chain surface and must be removed",
);

const requirements = readFileSync(resolve(root, "requirements-validation.txt"), "utf8");
assert.match(requirements, /^jsonschema==\d/m, "python jsonschema must be pinned exactly");

// --- the entrypoint is real and self-describing ----------------------------

const help = spawnSync(process.execPath, [entrypoint, "--help"], { encoding: "utf8" });
assert.equal(help.status, 0, "--help must succeed");
assert.match(help.stdout, /--fast/, "--help must document the modes");

const bogus = spawnSync(process.execPath, [entrypoint, "--wat"], { encoding: "utf8" });
assert.notEqual(bogus.status, 0, "an unknown flag must be a non-zero exit, not a default run");

const bogusJob = spawnSync(process.execPath, [entrypoint, "--fast", "--job=wat"], {
  encoding: "utf8",
});
assert.notEqual(bogusJob.status, 0, "an unknown --job must be a non-zero exit, not an empty pass");

// --- CI must call the entrypoint, not reimplement it ------------------------

// This is the assertion that keeps #126's "only implementation" claim honest.
// Every job in the workflow has to reach the gates through this script; the
// moment one inlines `gofmt` or `go test` again, the PR gate and a local run are
// two definitions of "verified" and the comment at the top of ci.yml is a lie.
const workflow = readFileSync(resolve(root, ".github/workflows/ci.yml"), "utf8");
for (const job of [
  "repository-hygiene",
  "gateway-unit-and-contract",
  "public-api-contract",
  "authority-consistency",
]) {
  assert.match(
    workflow,
    new RegExp(`verify-repository\\.mjs --fast --job=${job}`),
    `ci.yml must run the verify entrypoint for ${job} rather than restating its commands`,
  );
}
for (const inlined of [
  "gofmt -l",
  "go -C apps/gateway vet",
  "go -C apps/gateway test",
  "node scripts/validate-",
  "node scripts/test-",
  "node scripts/check-",
  "python scripts/",
]) {
  assert.ok(
    !workflow.includes(inlined),
    `ci.yml must not inline "${inlined}"; it belongs to the verify entrypoint`,
  );
}

console.log("PASS: verify-repository orchestrator (modes, skip policy, summary, pinning)");
