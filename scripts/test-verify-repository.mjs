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
import { fileURLToPath, pathToFileURL } from "node:url";
import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const entrypoint = resolve(root, "scripts/verify-repository.mjs");

// `pathToFileURL`, not string concatenation: a Windows drive-letter path is not
// a valid URL path, so `file://${path}` makes this cross-platform test
// unloadable on the one platform the entrypoint exists to keep consistent.
const { buildPlan, renderSummary, requiredToolsFor, MODES, JOBS, REQUIRED_TOOLS } = await import(
  pathToFileURL(entrypoint).href
);

const names = (mode) => buildPlan(mode).map((step) => step.name);

// --- mode composition ------------------------------------------------------

assert.deepEqual(MODES, ["fast", "full", "release"], "three modes must exist");

const fast = names("fast");
const full = names("full");
const release = names("release");

// The exact gate set of each depth, not merely a subset and a count. Asserting
// only "contains these four" plus "is longer than fast" would let a plan drop a
// named gate and substitute an arbitrary step while this file still passed.
const FAST_STEPS = [
  "gofmt",
  "go vet",
  "go test",
  "public API contract",
  "implementation-spec authority",
  "git diff --check",
  "git diff --check (introduced commit)",
];
const FULL_ADDITIONS = [
  "go test -race",
  "public API validator mutation suite",
  "implementation-spec validator suite",
  "verify entrypoint self-test",
  "chat stream wire",
  "historical public API contract",
  "management API contract",
  "dependency direction",
  "docker sandbox smoke",
  "sandbox state semantics",
];
const RELEASE_ADDITIONS = [
  "dependency install determinism",
  "npm vulnerability audit",
  "go vulnerability scan",
  "container build",
];

assert.deepEqual(fast, FAST_STEPS, "--fast must be exactly the declared PR gate set");
assert.deepEqual(full, [...FAST_STEPS, ...FULL_ADDITIONS], "--full must be exactly fast + its depth");
assert.deepEqual(
  release,
  [...FAST_STEPS, ...FULL_ADDITIONS, ...RELEASE_ADDITIONS],
  "--release must be exactly full + its depth",
);

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

// The four PR-gate jobs from #123. `release-supply-chain` is deliberately not
// one of them.
const PR_JOBS = [
  "repository-hygiene",
  "gateway-unit-and-contract",
  "public-api-contract",
  "authority-consistency",
];

// Each PR job must own at least one --fast check, otherwise a required check
// would pass by running nothing.
for (const job of PR_JOBS) {
  assert.ok(buildPlan("fast", job).length > 0, `--fast --job=${job} must run real checks`);
}

// And the four PR jobs must cover --fast *by themselves*. Checking the full
// JOBS list here would let a fast gate be owned by `release-supply-chain` —
// a job no PR runs — so it would be omitted from every required check while
// the partition assertion still passed.
const prSliced = PR_JOBS.flatMap((job) => buildPlan("fast", job).map((step) => step.name));
assert.deepEqual(
  [...prSliced].sort(),
  fast.slice().sort(),
  "the four PR jobs must cover every --fast check; a fast gate owned by a non-PR job never runs on a PR",
);
for (const step of buildPlan("fast")) {
  assert.ok(
    PR_JOBS.includes(step.job),
    `${step.name} is a fast gate, so it must be owned by a PR job, not ${step.job}`,
  );
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

// A whole-mode run still resolves everything. --release is the mode that
// exercises every declared tool (it is the only one that invokes npm), so a
// tool a release step shells out to cannot go undeclared and be discovered
// only when a runner tries to spawn it.
assert.deepEqual(
  requiredToolsFor(buildPlan("release")).map((tool) => tool.name).sort(),
  REQUIRED_TOOLS.map((tool) => tool.name).sort(),
  "--release exercises every declared tool, so preflight must resolve all of them",
);

const releaseToolNames = requiredToolsFor(buildPlan("release", "release-supply-chain")).map(
  (tool) => tool.name,
);
for (const tool of ["npm", "go"]) {
  assert.ok(releaseToolNames.includes(tool), `the release supply-chain job invokes ${tool}`);
}

// The hygiene job shells out to git, so preflight must state that requirement
// with a remedy instead of surfacing a terse spawn error mid-run.
assert.ok(
  requiredToolsFor(buildPlan("fast", "repository-hygiene"))
    .map((tool) => tool.name)
    .includes("git"),
  "the hygiene job runs git, so preflight must resolve it",
);

// The chat stream wire check is plain Python and imports no schema library, so
// its slice must not be able to fail preflight over jsonschema.
const wireToolNames = requiredToolsFor(
  buildPlan("full", "authority-consistency").filter((step) => step.name === "chat stream wire"),
).map((tool) => tool.name);
assert.ok(wireToolNames.includes("python"), "the chat stream wire check needs a Python interpreter");
assert.ok(
  !wireToolNames.includes("python jsonschema"),
  "the chat stream wire check must not demand jsonschema; it never imports it",
);

// The sandbox smoke is a bash script that probes the container with curl. Both
// were previously undeclared, so on a runner without them the gate died with a
// raw spawn error instead of preflight's stated remedy — the failure shape #126
// exists to eliminate.
const smokeToolNames = requiredToolsFor(
  buildPlan("full", "gateway-unit-and-contract").filter((step) => step.name === "docker sandbox smoke"),
).map((tool) => tool.name);
for (const tool of ["bash", "curl"]) {
  assert.ok(
    smokeToolNames.includes(tool),
    `the sandbox smoke shells out to ${tool}, so preflight must state that requirement`,
  );
}

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
// An exact version, not a tag or a range: `npm@latest` and `npm@^11` both
// satisfy `startsWith("npm@")` while letting the resolved npm drift build to
// build, which is the opposite of pinning.
assert.match(
  pkg.packageManager ?? "",
  /^npm@\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/,
  "packageManager must pin npm to an exact version, not a tag or range",
);
assert.ok(pkg.engines?.node, "engines.node must be declared");
assert.ok(
  !JSON.stringify(pkg.dependencies ?? {}).includes("sandcastle") &&
    !JSON.stringify(pkg.devDependencies ?? {}).includes("sandcastle"),
  "@ai-hero/sandcastle is unreferenced supply-chain surface and must be removed",
);

const requirements = readFileSync(resolve(root, "requirements-validation.txt"), "utf8");
assert.match(requirements, /^jsonschema==\d/m, "python jsonschema must be pinned exactly");

// Container base images: version AND digest (#125). A mutable tag makes
// `docker build --pull` resolve to whatever the registry serves today, so
// calling that build reproducible is a claim the Dockerfile does not support.
// Asserting per-FROM rather than over the whole file, so adding a new
// unpinned stage cannot hide behind the pinned ones.
const dockerfile = readFileSync(resolve(root, "apps/gateway/Dockerfile"), "utf8");
const fromLines = dockerfile.split(/\r?\n/).filter((line) => /^FROM\s/.test(line));
assert.ok(fromLines.length >= 3, "the gateway Dockerfile must declare its build, statedir and runtime stages");
for (const line of fromLines) {
  assert.match(
    line,
    /^FROM\s+\S+:\S+@sha256:[0-9a-f]{64}(?:\s+AS\s+\S+)?$/,
    `every base image must pin version and digest, but got "${line}"`,
  );
}

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

// The empty-slice case is distinct from the unknown-job case and is the one
// that can read green: `release-supply-chain` is a *declared* job that owns no
// --fast check, so without the guard the run would exit 0 having verified
// nothing. That is exactly the silent-skip failure #126 exists to prevent.
assert.equal(buildPlan("fast", "release-supply-chain").length, 0, "precondition: the slice is empty");
const emptySlice = spawnSync(
  process.execPath,
  [entrypoint, "--fast", "--job=release-supply-chain"],
  { encoding: "utf8" },
);
assert.equal(
  emptySlice.status,
  2,
  "a declared job with no checks in the mode must exit 2, not pass green having run nothing",
);

// --- CI must call the entrypoint, not reimplement it ------------------------

// This is the assertion that keeps #126's "only implementation" claim honest.
// Every job in the workflow has to reach the gates through this script; the
// moment one inlines `gofmt` or `go test` again, the PR gate and a local run are
// two definitions of "verified" and the comment at the top of ci.yml is a lie.
const workflow = readFileSync(resolve(root, ".github/workflows/ci.yml"), "utf8");

// The public API gate compares against a baseline blob read from an immutable
// commit. Without `PIXELPLUS_PUBLIC_API_BASELINE_REF` the validator fails
// closed under CI, and without full history `git show <sha>:<path>` cannot read
// that blob — so a checkout narrowed back to depth 1, or a dropped env, turns a
// real compatibility oracle into a red gate for the wrong reason.
assert.match(
  workflow,
  /PIXELPLUS_PUBLIC_API_BASELINE_REF:\s*\$\{\{\s*github\.event\.pull_request\.base\.sha\s*\|\|\s*github\.event\.before\s*\}\}/,
  "the public-api-contract job must supply the immutable baseline SHA; the validator fails closed without it",
);
assert.match(
  workflow,
  /fetch-depth:\s*0/,
  "reading the baseline blob from the base commit requires full history",
);
assert.match(
  workflow,
  /fetch-depth:\s*2/,
  "the hygiene gate screens HEAD^..HEAD, which a depth-1 checkout does not contain",
);

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
// Rather than denylisting a handful of current command spellings — which a
// second verification definition can simply avoid — parse every shell line CI
// actually runs and allow only the two categories the workflow is permitted to
// contain: the verify entrypoint, and pinned dependency installation.
const runLines = [];
{
  const lines = workflow.split("\n");
  for (let index = 0; index < lines.length; index += 1) {
    const inline = lines[index].match(/^\s*run:\s*(?!\|)(\S.*)$/);
    if (inline) {
      runLines.push(inline[1].trim());
      continue;
    }
    const block = lines[index].match(/^(\s*)run:\s*\|\s*$/);
    if (!block) continue;
    const indent = block[1].length;
    for (let cursor = index + 1; cursor < lines.length; cursor += 1) {
      const line = lines[cursor];
      if (line.trim() === "") continue;
      const lineIndent = line.length - line.trimStart().length;
      if (lineIndent <= indent) break;
      runLines.push(line.trim());
    }
  }
}

assert.ok(runLines.length > 0, "the workflow must contain run steps for this check to mean anything");

// The verify entrypoint, and nothing else that verifies.
const ALLOWED_RUN = [
  /^node scripts\/verify-repository\.mjs --fast --job=[a-z-]+$/,
  /^node scripts\/test-verify-repository\.mjs$/,
  /^npm ci$/,
  /^python -m pip install -r requirements-validation\.txt$/,
];
for (const line of runLines) {
  assert.ok(
    ALLOWED_RUN.some((pattern) => pattern.test(line)),
    `ci.yml runs "${line}", which is neither the verify entrypoint nor a pinned dependency install; ` +
      "a second definition of \"verified\" is exactly what #126 forbids",
  );
}

console.log("PASS: verify-repository orchestrator (modes, skip policy, summary, pinning)");
