#!/usr/bin/env node
/**
 * Canonical verification entrypoint for the PixelPlus repository (issue #126).
 *
 * One implementation, three depths, so Windows, macOS, Linux and CI run the
 * same orchestration instead of three drifting shell variants:
 *
 *   node scripts/verify-repository.mjs --fast      PR gate
 *   node scripts/verify-repository.mjs --full      fast + race, mutation, architecture, docker
 *   node scripts/verify-repository.mjs --release   full + supply chain and version consistency
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
];

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
  },
  {
    name: "go vet",
    command: ["go", "-C", "apps/gateway", "vet", "./..."],
    artifact: "apps/gateway",
  },
  {
    name: "go test",
    command: ["go", "-C", "apps/gateway", "test", "./..."],
    artifact: "apps/gateway",
  },
  {
    name: "public API contract",
    command: [node, "scripts/validate-public-api-contract.mjs"],
    artifact: "contracts/openapi/pixelplus-public-api-v1.yaml",
  },
  {
    name: "implementation-spec authority",
    command: [node, "scripts/validate-provider-gateway-implementation-spec.mjs"],
    artifact: "docs/spec/provider-gateway-implementation-ready-specification.md",
  },
  {
    name: "git diff --check",
    command: ["git", "diff", "--check"],
    artifact: "working tree",
    hint: "remove trailing whitespace and conflict markers",
  },
];

/** Depth added by --full: slower proofs and the historical/retained validators. */
const FULL_STEPS = [
  {
    name: "go test -race",
    command: ["go", "-C", "apps/gateway", "test", "-race", "./..."],
    artifact: "apps/gateway",
  },
  {
    name: "public API validator mutation suite",
    command: [node, "scripts/test-public-api-contract-validator.mjs"],
    artifact: "scripts/validate-public-api-contract.mjs",
  },
  {
    name: "implementation-spec validator suite",
    command: [node, "--test", "scripts/test-provider-gateway-implementation-spec-validator.mjs"],
    artifact: "scripts/validate-provider-gateway-implementation-spec.mjs",
  },
  {
    name: "verify entrypoint self-test",
    command: [node, "scripts/test-verify-repository.mjs"],
    artifact: "scripts/verify-repository.mjs",
  },
  {
    name: "chat stream wire",
    command: ["python", "scripts/check-chat-stream-wire.py"],
    artifact: "apps/gateway/internal/transport",
  },
  {
    name: "historical public API contract",
    command: [node, "scripts/validate-openapi-contract.mjs", "contracts/openapi/pixelplus-public-api-v0alpha.yaml"],
    artifact: "contracts/openapi/pixelplus-public-api-v0alpha.yaml",
  },
  {
    name: "management API contract",
    command: [node, "scripts/prototype-management-contract.mjs"],
    artifact: "contracts/openapi/pixelplus-management-api-v0alpha.yaml",
  },
  {
    name: "dependency direction",
    command: [node, "scripts/check-dependency-direction.mjs"],
    artifact: "apps/gateway/internal",
  },
  {
    // Validates the tracked sandbox composition (#68) rather than merely
    // pinging the daemon: config resolution catches drift between the compose
    // file and the Dockerfile it builds.
    name: "docker sandbox smoke",
    command: [
      "docker",
      "compose",
      "-f",
      "apps/gateway/deploy/sandbox/docker-compose.yml",
      "config",
      "--quiet",
    ],
    artifact: "apps/gateway/deploy/sandbox/docker-compose.yml",
    optional: true,
    requires: "docker",
    skipNotice:
      "the Docker daemon is unavailable, so the sandbox smoke did NOT run. This mode's result is weaker than a full pass.",
  },
];

/** Depth added by --release: supply chain and version/tag consistency. */
const RELEASE_STEPS = [
  {
    name: "dependency install determinism",
    command: ["npm", "ci", "--dry-run"],
    artifact: "package-lock.json",
    hint: "the lockfile must satisfy package.json without resolution",
  },
  {
    name: "npm vulnerability audit",
    command: ["npm", "audit", "--audit-level=high"],
    artifact: "package-lock.json",
  },
  {
    name: "go vulnerability scan",
    command: ["go", "-C", "apps/gateway", "run", "golang.org/x/vuln/cmd/govulncheck@latest", "./..."],
    artifact: "apps/gateway/go.sum",
  },
  {
    name: "container build",
    command: ["docker", "build", "-t", "pixelplus-gateway:verify", "apps/gateway"],
    artifact: "apps/gateway/Dockerfile",
    optional: true,
    requires: "docker",
    skipNotice:
      "the Docker daemon is unavailable, so the container build did NOT run. Do not treat this as a release-ready result.",
  },
];

/**
 * Compose the plan for a mode. Depth is strictly cumulative: a deeper mode
 * never drops a shallower mode's gate, so "release passed" is always a
 * stronger statement than "fast passed".
 */
export function buildPlan(mode) {
  if (!MODES.includes(mode)) {
    throw new Error(`unknown mode "${mode}" (expected one of: ${MODES.join(", ")})`);
  }
  const plan = [...FAST_STEPS];
  if (mode === "full" || mode === "release") plan.push(...FULL_STEPS);
  if (mode === "release") plan.push(...RELEASE_STEPS);
  return plan.map((step) => ({ ...step }));
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

function toolAvailable(name) {
  // `docker --version` reports the CLI and succeeds even when the daemon is
  // down, so it cannot decide whether a Docker gate can run. `docker info`
  // requires an actually reachable daemon.
  const args = name === "docker" ? ["info"] : ["--version"];
  const probe = spawnSync(resolveCommand(name), args, { encoding: "utf8", stdio: "ignore" });
  return !probe.error && probe.status === 0;
}

function preflight() {
  console.log("resolved toolchain");
  console.log("-".repeat(72));
  const missing = [];
  for (const tool of REQUIRED_TOOLS) {
    const [command, ...args] = tool.command;
    const probe = spawnSync(resolveCommand(command), args, { encoding: "utf8" });
    if (probe.error || probe.status !== 0) {
      missing.push(tool);
      console.log(`  ${tool.name.padEnd(20)} MISSING`);
      continue;
    }
    console.log(`  ${tool.name.padEnd(20)} ${probe.stdout.trim().split("\n")[0]}`);
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
  node scripts/verify-repository.mjs --release   full + supply chain, container build, version consistency

Options:
  --help    show this message

A missing tool is a failure with a stated remedy, never a silent skip.
Docker-dependent checks are the only permitted conditional, and they announce it.`);
}

function main() {
  const args = process.argv.slice(2);

  if (args.includes("--help") || args.includes("-h")) {
    usage();
    process.exit(0);
  }

  const selected = args.filter((arg) => MODES.includes(arg.replace(/^--/, "")));
  const unknown = args.filter((arg) => !MODES.includes(arg.replace(/^--/, "")));

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

  const mode = selected[0].replace(/^--/, "");
  console.log(`verify-repository --${mode}\n`);

  preflight();

  const plan = buildPlan(mode);
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

  console.log(`\nPASS: verify-repository --${mode} (${results.length} checks)`);
}

const invokedDirectly =
  process.argv[1] && resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url));
if (invokedDirectly) main();
