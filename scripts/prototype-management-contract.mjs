#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const steps = [
  "scripts/validate-management-openapi-contract.mjs",
  "scripts/run-management-contract-scenarios.mjs",
];

for (const script of steps) {
  const result = spawnSync(process.execPath, [script], {
    cwd: root,
    stdio: "inherit",
  });
  if (result.error) {
    console.error(`FAIL: could not run ${script}: ${result.error.message}`);
    process.exit(1);
  }
  if (result.status !== 0) process.exit(result.status ?? 1);
}
