#!/usr/bin/env node
/**
 * Canonical verification entrypoint for the PixelPlus repository (issue #126).
 *
 * One implementation, three depths, so Windows, macOS, Linux and CI run the
 * same orchestration instead of three drifting shell variants:
 *
 *   node scripts/verify-repository.mjs --fast      PR gate
 *   node scripts/verify-repository.mjs --full      fast + race, mutation, architecture, docker
 *   node scripts/verify-repository.mjs --release   full + supply chain checks
 *
 * `--release` deliberately claims only what it proves. Version/tag consistency
 * is NOT verified here; that invariant belongs to the release workflow (#70).
 *
 * CI does not restate the command list. It runs this entrypoint once per
 * required check with `--job=<name>`, so the PR gate and a local run cannot
 * drift into two definitions of "verified".
 *
 * Design rules that follow directly from the ticket:
 *
 *   - A missing tool is a FAILURE with a stated remedy. A required check is
 *     never silently skipped, because a skipped gate reads as a green one.
 *   - Docker-dependent checks are the only permitted conditional, and they
 *     announce the skip loudly in the summary.
 *   - Every mode fails fast, but always prints a closing summary containing
 *     each command, its duration, its exit code, and the failing artifact.
 *
 * Zero new dependencies, consistent with the other validators in this folder.
 */

import { spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");

export const MODES = ["fast", "full", "release"];

/**
 * CI job names, from issue #123. A step declares which job owns it so the
 * workflow can select a slice with `--job=<name>` instead of restating the
 * commands. `release-supply-chain` is not a PR gate; it exists so the release
 * steps still carry an owner rather than a null one.
 */
export const JOBS = [
  "repository-hygiene",
  "gateway-unit-and-contract",
  "public-api-contract",
  "authority-consistency",
  "release-supply-chain",
];


/**
 * Tools every mode needs before any gate runs. Preflight resolves and prints
 * these so a drifting runner image is visible in the log rather than being
 * discovered halfway through a suite.
 */
export const REQUIRED_TOOLS = [
  {
    name: "go",
    command: ["go", "version"],
    remedy: "install the Go toolchain pinned in .tool-versions (`mise install`)",
  },
  {
    name: "node",
    command: [process.execPath, "--version"],
    remedy: "install the Node version pinned in .tool-versions (`mise install`)",
  },
  {
    name: "python",
    command: ["python", "--version"],
    remedy: "install the Python version pinned in .tool-versions (`mise install`)",
  },
  {
    name: "python jsonschema",
    command: [
      "python",
      "-c",
      "import importlib.metadata as m; print('jsonschema', m.version('jsonschema'))",
    ],
    remedy: "python -m pip install -r requirements-validation.txt",
  },
  {
    name: "git",
    command: ["git", "--version"],
    remedy: "install git; the repository-hygiene gate inspects the commit with it",
  },
  {
    name: "npm",
    command: ["npm", "--version"],
    remedy: "install the npm version pinned in package.json#packageManager",
  },
  {
    name: "bash",
    command: ["bash", "--version"],
    remedy: "install bash; the sandbox smoke controller is a bash script",
  },
  {
    name: "curl",
    command: ["curl", "--version"],
    remedy: "install curl; the sandbox smoke probes the container's HTTP surface with it",
  },
];

/**
 * Which tools a plan actually needs. A whole-mode run needs all of them, but a
 * single `--job` slice does not: making the Go job prove a Python interpreter
 * would be theatre, and installing one just to satisfy preflight is worse.
 * Derived from the commands themselves plus any `needs` a step declares for a
 * tool it shells out to indirectly.
 */
export function requiredToolsFor(plan) {
  const needed = new Set();
  for (const step of plan) {
    // Deliberately environment-independent: this returns what the plan *needs*,
    // never what the current machine *has*. Probing `requires` here to drop a
    // skipped step's helpers would make the answer differ between machines and
    // make the self-test's coverage assertion unprovable. A conditional step's
    // helpers therefore fail CLOSED (too strict, never too lax), which is the
    // policy direction #126 requires.
    const [command] = step.command;
    if (command === "go" || command === "gofmt") needed.add("go");
    if (command === node) needed.add("node");
    if (command === "git") needed.add("git");
    if (command === "npm") needed.add("npm");
    if (command === "bash") needed.add("bash");
    // Only the steps that actually validate against a schema declare
    // `needs: ["python jsonschema"]`. A plain Python script (the chat stream
    // wire check) must not be able to fail preflight over a library it never
    // imports.
    if (command === "python") needed.add("python");
    for (const extra of step.needs ?? []) needed.add(extra);
  }
  return REQUIRED_TOOLS.filter((tool) => needed.has(tool.name));
}

const node = process.execPath;

/**
 * Windows resolves `npm`/`npx` only via their `.cmd` shims. Using `shell: true`
 * to paper over that would concatenate arguments instead of escaping them,
 * which breaks any argument containing spaces (notably `python -c "<code>"`)
 * and trips DEP0190. Rewriting just the shim name keeps every spawn shell-free.
 */
const SHIMMED_ON_WINDOWS = new Set(["npm", "npx"]);

function resolveCommand(command) {
  if (process.platform === "win32" && SHIMMED_ON_WINDOWS.has(command)) {
    return `${command}.cmd`;
  }
  return command;
}

/** The PR gate. Everything here must be cheap enough to run on every push. */
const FAST_STEPS = [
  {
    name: "gofmt",
    // `gofmt -l` exits 0 even when it lists unformatted files, so the listing
    // itself is the failure signal. CI must never reformat and continue: the
    // reviewed commit has to be the verified artifact.
    command: ["gofmt", "-l", "apps/gateway"],
    failOnStdout: true,
    artifact: "apps/gateway",
    hint: "run `gofmt -w apps/gateway` and commit the result",
    job: "repository-hygiene",
  },
  {
    name: "go vet",
    command: ["go", "-C", "apps/gateway", "vet", "./..."],
    artifact: "apps/gateway",
    job: "gateway-unit-and-contract",
  },
  {
    name: "go test",
    command: ["go", "-C", "apps/gateway", "test", "./..."],
    artifact: "apps/gateway",
    job: "gateway-unit-and-contract",
  },
  {
    name: "public API contract",
    command: [node, "scripts/validate-public-api-contract.mjs"],
    artifact: "contracts/openapi/pixelplus-public-api-v1.yaml",
    job: "public-api-contract",
    // The validator shells out to python jsonschema, so preflight must resolve
    // it even though the command above is a Node one.
    needs: ["python", "python jsonschema"],
  },
  {
    name: "implementation-spec authority",
    command: [node, "scripts/validate-provider-gateway-implementation-spec.mjs"],
    artifact: "docs/spec/provider-gateway-implementation-ready-specification.md",
    job: "authority-consistency",
  },
  {
    name: "git diff --check",
    command: ["git", "diff", "--check"],
    artifact: "working tree",
    hint: "remove trailing whitespace and conflict markers",
    job: "repository-hygiene",
  },
  {
    // `git diff --check` alone compares the worktree to the index, so after a
    // CI checkout it has nothing to inspect and always reads green. The
    // introduced commit range is what CI must actually screen, so screen it.
    // On a pull request the checkout is a merge commit, making HEAD^..HEAD
    // exactly the PR's changes; on a push it is the pushed commit.
    name: "git diff --check (introduced commit)",
    command: ["git", "diff", "--check", "HEAD^", "HEAD"],
    artifact: "HEAD",
    hint: "remove trailing whitespace and conflict markers from the commit",
    job: "repository-hygiene",
  },
];

/** Depth added by --full: slower proofs and the historical/retained validators. */
const FULL_STEPS = [
  {
    name: "go test -race",
    command: ["go", "-C", "apps/gateway", "test", "-race", "./..."],
    artifact: "apps/gateway",
    job: "gateway-unit-and-contract",
  },
  {
    name: "public API validator mutation suite",
    command: [node, "scripts/test-public-api-contract-validator.mjs"],
    artifact: "scripts/validate-public-api-contract.mjs",
    job: "public-api-contract",
    needs: ["python", "python jsonschema"],
  },
  {
    name: "implementation-spec validator suite",
    command: [node, "--test", "scripts/test-provider-gateway-implementation-spec-validator.mjs"],
    artifact: "scripts/validate-provider-gateway-implementation-spec.mjs",
    job: "authority-consistency",
  },
  {
    name: "verify entrypoint self-test",
    command: [node, "scripts/test-verify-repository.mjs"],
    artifact: "scripts/verify-repository.mjs",
    job: "authority-consistency",
  },
  {
    name: "chat stream wire",
    command: ["python", "scripts/check-chat-stream-wire.py"],
    artifact: "apps/gateway/internal/transport",
    job: "authority-consistency",
  },
  {
    name: "historical public API contract",
    command: [node, "scripts/validate-openapi-contract.mjs", "contracts/openapi/pixelplus-public-api-v0alpha.yaml"],
    artifact: "contracts/openapi/pixelplus-public-api-v0alpha.yaml",
    job: "public-api-contract",
  },
  {
    name: "management API contract",
    command: [node, "scripts/prototype-management-contract.mjs"],
    artifact: "contracts/openapi/pixelplus-management-api-v0alpha.yaml",
    job: "public-api-contract",
  },
  {
    name: "dependency direction",
    command: [node, "scripts/check-dependency-direction.mjs"],
    artifact: "apps/gateway/internal",
    job: "gateway-unit-and-contract",
  },
  {
    // #68 ships a disposable sandbox controller that builds the image, starts
    // the hardened container, probes it and tears it down. A Compose `config`
    // parse would only prove the YAML is well formed, so a broken Dockerfile or
    // a container that never becomes ready would still pass --full. Run the
    // real smoke flow instead.
    name: "docker sandbox smoke",
    command: ["bash", "apps/gateway/deploy/sandbox/sandbox.sh", "smoke"],
    artifact: "apps/gateway/deploy/sandbox/sandbox.sh",
    optional: true,
    requires: "docker",
    needs: ["curl"],
    job: "gateway-unit-and-contract",
    skipNotice:
      "the Docker daemon is unavailable, so the sandbox smoke did NOT run. This mode's result is weaker than a full pass.",
  },
  {
    // The smoke above proves the container comes up and answers; it says nothing
    // about what the teardown discards. #125 was exactly that gap: `stop` logged
    // "no state retained" while keeping the named volume, so a probe could pass
    // on a leftover row from an earlier run. This gate pins the two lifecycle
    // modes — ephemeral by default, persistence only via an explicit
    // --keep-state — and carries its own negative control, so a regression that
    // silently starts retaining state fails here instead of being absorbed.
    name: "sandbox state semantics",
    command: ["bash", "apps/gateway/deploy/sandbox/verify-sandbox-semantics.sh"],
    artifact: "apps/gateway/deploy/sandbox/verify-sandbox-semantics.sh",
    optional: true,
    requires: "docker",
    job: "gateway-unit-and-contract",
    skipNotice:
      "the Docker daemon is unavailable, so the sandbox state semantics proof did NOT run. Ephemeral teardown is unverified in this mode.",
  },
];

/** Depth added by --release: supply chain proofs. Version/tag consistency is
 * intentionally not claimed here — see the header note. */
const RELEASE_STEPS = [
  {
    name: "dependency install determinism",
    command: ["npm", "ci", "--dry-run"],
    artifact: "package-lock.json",
    hint: "the lockfile must satisfy package.json without resolution",
    job: "release-supply-chain",
  },
  {
    name: "npm vulnerability audit",
    command: ["npm", "audit", "--audit-level=high"],
    artifact: "package-lock.json",
    job: "release-supply-chain",
  },
  {
    name: "go vulnerability scan",
    command: ["go", "-C", "apps/gateway", "run", "golang.org/x/vuln/cmd/govulncheck@latest", "./..."],
    artifact: "apps/gateway/go.sum",
    job: "release-supply-chain",
  },
  {
    name: "container build",
    command: ["docker", "build", "-t", "pixelplus-gateway:verify", "apps/gateway"],
    artifact: "apps/gateway/Dockerfile",
    optional: true,
    requires: "docker",
    job: "release-supply-chain",
    skipNotice:
      "the Docker daemon is unavailable, so the container build did NOT run. Do not treat this as a release-ready result.",
  },
];

/**
 * Compose the plan for a mode. Depth is strictly cumulative: a deeper mode
 * never drops a shallower mode's gate, so "release passed" is always a
 * stronger statement than "fast passed".
 *
 * `job` narrows the plan to one CI job's slice without changing which mode a
 * step belongs to, so a workflow selects from the same list a developer runs
 * rather than maintaining a parallel one.
 */
export function buildPlan(mode, job = null) {
  if (!MODES.includes(mode)) {
    throw new Error(`unknown mode "${mode}" (expected one of: ${MODES.join(", ")})`);
  }
  if (job !== null && !JOBS.includes(job)) {
    throw new Error(`unknown job "${job}" (expected one of: ${JOBS.join(", ")})`);
  }
  const plan = [...FAST_STEPS];
  if (mode === "full" || mode === "release") plan.push(...FULL_STEPS);
  if (mode === "release") plan.push(...RELEASE_STEPS);
  return plan.filter((step) => job === null || step.job === job).map((step) => ({ ...step }));
}

function formatDuration(ms) {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

/**
 * Render the closing summary. This runs even when a gate fails, because "which
 * command failed, on what artifact, after how long" is the whole point of
 * having one entrypoint.
 */
export function renderSummary(results) {
  const label = {
    pass: "PASS   ",
    fail: "FAIL   ",
    skipped: "SKIPPED",
  };
  const lines = ["", "=".repeat(72), "verification summary", "=".repeat(72)];
  for (const result of results) {
    const exit = result.exitCode === null || result.exitCode === undefined ? "-" : result.exitCode;
    let line = `${label[result.status] ?? result.status}  ${result.name.padEnd(38)} ${formatDuration(
      result.durationMs,
    ).padStart(7)}  exit=${exit}`;
    if (result.artifact && result.status === "fail") line += `\n           artifact: ${result.artifact}`;
    if (result.hint && result.status === "fail") line += `\n           remedy:   ${result.hint}`;
    if (result.status === "skipped" && result.skipNotice) {
      line += `\n           ${result.skipNotice}`;
    }
    lines.push(line);
  }
  lines.push("=".repeat(72));
  return lines.join("\n");
}

/**
 * One probe policy for every tool question this script asks, so a change here
 * (a timeout, an environment tweak) cannot land on the preflight path while
 * missing the conditional-gate path.
 *
 * `docker --version` reports the CLI and succeeds even when the daemon is down,
 * so it cannot decide whether a Docker gate can run. `docker info` requires an
 * actually reachable daemon.
 */
const PROBE_ARGS = { docker: ["info"] };

function probeTool(command, args) {
  const probeArgs = args ?? PROBE_ARGS[command] ?? ["--version"];
  const result = spawnSync(resolveCommand(command), probeArgs, {
    encoding: "utf8",
    stdio: args ? "pipe" : "ignore",
  });
  return {
    ok: !result.error && result.status === 0,
    version: (result.stdout ?? "").trim().split("\n")[0],
  };
}

function toolAvailable(name) {
  return probeTool(name).ok;
}

function preflight(tools) {
  console.log("resolved toolchain");
  console.log("-".repeat(72));
  const missing = [];
  for (const tool of tools) {
    const [command, ...args] = tool.command;
    const probe = probeTool(command, args);
    if (!probe.ok) {
      missing.push(tool);
      console.log(`  ${tool.name.padEnd(20)} MISSING`);
      continue;
    }
    console.log(`  ${tool.name.padEnd(20)} ${probe.version}`);
  }
  if (missing.length > 0) {
    console.error("\nFAIL: required tooling is missing. A missing tool is a failure, not a skip.");
    for (const tool of missing) {
      console.error(`- ${tool.name}: ${tool.remedy}`);
    }
    process.exit(1);
  }
  console.log("-".repeat(72));
}

function runStep(step) {
  const started = Date.now();

  if (step.requires === "docker" && !toolAvailable("docker")) {
    console.log(`\n>>> ${step.name}\n    SKIPPED: ${step.skipNotice}`);
    return { ...step, status: "skipped", durationMs: 0, exitCode: null };
  }

  const [command, ...args] = step.command;
  console.log(`\n>>> ${step.name}\n    $ ${step.command.join(" ")}`);

  // Only gates whose *output* is the failure signal need their stdout captured.
  // Everything else streams straight through, so a chatty validator cannot
  // exceed spawnSync's buffer and report ENOBUFS as if the gate itself failed.
  // A verification harness that manufactures failures is worse than none.
  const capture = Boolean(step.failOnStdout);
  const result = spawnSync(resolveCommand(command), args, {
    cwd: root,
    encoding: "utf8",
    stdio: capture ? "pipe" : "inherit",
    maxBuffer: 64 * 1024 * 1024,
  });
  const durationMs = Date.now() - started;

  if (capture) {
    if (result.stdout) process.stdout.write(result.stdout);
    if (result.stderr) process.stderr.write(result.stderr);
  }

  const spawnFailed = Boolean(result.error);
  const listedFiles = capture && result.stdout.trim().length > 0;
  const failed = spawnFailed || result.status !== 0 || listedFiles;

  if (spawnFailed) {
    console.error(`    could not execute ${command}: ${result.error.message}`);
  }
  if (listedFiles) {
    console.error(`    ${step.name} reported files; that listing is the failure.`);
  }

  return {
    ...step,
    status: failed ? "fail" : "pass",
    durationMs,
    exitCode: spawnFailed ? null : result.status,
  };
}

function usage() {
  console.log(`Canonical verification entrypoint for the PixelPlus repository.

Usage:
  node scripts/verify-repository.mjs --fast      PR gate: format, vet, test, contracts, hygiene
  node scripts/verify-repository.mjs --full      fast + race, mutation suites, architecture, docker smoke
  node scripts/verify-repository.mjs --release   full + supply chain, container build

Options:
  --job=<name>   run only the steps owned by one CI job. CI uses this so the
                 workflow never restates the command list.
                 Jobs: ${JOBS.join(", ")}
  --help         show this message

A missing tool is a failure with a stated remedy, never a silent skip.
Docker-dependent checks are the only permitted conditional, and they announce it.`);
}

function main() {
  const args = process.argv.slice(2);

  if (args.includes("--help") || args.includes("-h")) {
    usage();
    process.exit(0);
  }

  const jobArgs = args.filter((arg) => arg.startsWith("--job="));
  const rest = args.filter((arg) => !arg.startsWith("--job="));

  const selected = rest.filter((arg) => MODES.includes(arg.replace(/^--/, "")));
  const unknown = rest.filter((arg) => !MODES.includes(arg.replace(/^--/, "")));

  if (unknown.length > 0) {
    console.error(`FAIL: unknown argument(s): ${unknown.join(", ")}`);
    usage();
    process.exit(2);
  }
  if (selected.length !== 1) {
    console.error("FAIL: exactly one mode is required (--fast, --full, or --release)");
    usage();
    process.exit(2);
  }
  if (jobArgs.length > 1) {
    console.error("FAIL: at most one --job= may be given");
    usage();
    process.exit(2);
  }

  const job = jobArgs.length === 1 ? jobArgs[0].slice("--job=".length) : null;
  if (job !== null && !JOBS.includes(job)) {
    console.error(`FAIL: unknown job "${job}" (expected one of: ${JOBS.join(", ")})`);
    usage();
    process.exit(2);
  }

  const mode = selected[0].replace(/^--/, "");
  console.log(`verify-repository --${mode}${job ? ` --job=${job}` : ""}\n`);

  const plan = buildPlan(mode, job);

  // A `--job` slice that matches nothing is a workflow bug, and silently
  // exiting 0 would turn it into a green required check — the exact failure
  // mode this entrypoint exists to prevent.
  if (plan.length === 0) {
    console.error(`FAIL: no checks are owned by job "${job}" in --${mode}`);
    process.exit(2);
  }

  preflight(requiredToolsFor(plan));

  const results = [];
  let failure = null;

  for (const step of plan) {
    const result = runStep(step);
    results.push(result);
    if (result.status === "fail") {
      // Fail fast, but still print the summary below.
      failure = result;
      break;
    }
  }

  console.log(renderSummary(results));

  const skipped = results.filter((result) => result.status === "skipped");
  if (skipped.length > 0) {
    console.log(`\nNOTE: ${skipped.length} check(s) were skipped. This run is not a full pass.`);
  }

  if (failure) {
    const remaining = plan.length - results.length;
    console.error(
      `\nFAIL: ${failure.name} failed after ${formatDuration(failure.durationMs)}` +
        (remaining > 0 ? ` (${remaining} later check(s) never ran)` : ""),
    );
    process.exit(1);
  }

  console.log(`\nPASS: verify-repository --${mode}${job ? ` --job=${job}` : ""} (${results.length} checks)`);
}

const invokedDirectly =
  process.argv[1] && resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url));
if (invokedDirectly) main();
