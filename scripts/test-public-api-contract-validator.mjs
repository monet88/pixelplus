#!/usr/bin/env node

import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const artifact = resolve(root, "contracts/openapi/pixelplus-public-api-v1.yaml");
const validator = resolve(root, "scripts/validate-public-api-contract.mjs");

function run(path) {
  return spawnSync(process.execPath, [validator, path], {
    cwd: root,
    encoding: "utf8",
  });
}

function expectFailure(name, source, mutate, messages) {
  const doc = structuredClone(source);
  mutate(doc);
  const directory = mkdtempSync(resolve(tmpdir(), "pixelplus-api-contract-"));
  const path = resolve(directory, "mutated.yaml");
  try {
    writeFileSync(path, `${JSON.stringify(doc, null, 2)}\n`);
    const result = run(path);
    assert.notEqual(result.status, 0, `${name}: validator unexpectedly passed`);
    const output = `${result.stdout}\n${result.stderr}`;
    for (const message of Array.isArray(messages) ? messages : [messages]) {
      assert.match(output, message, `${name}: wrong failure`);
    }
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

const source = JSON.parse(readFileSync(artifact, "utf8"));
const baseline = run(artifact);
assert.equal(baseline.status, 0, baseline.stderr || baseline.stdout);

expectFailure(
  "prototype version is rejected",
  source,
  (doc) => { doc.info.version = "0.0.0-prototype"; },
  /info\.version must be 1\.0\.0/,
);
expectFailure(
  "missing management operation is rejected",
  source,
  (doc) => { delete doc.paths["/provider-accounts"]; },
  /missing required operation POST \/provider-accounts/,
);
expectFailure(
  "short deprecation window is rejected",
  source,
  (doc) => { doc["x-pixelplus-api-lifecycle"].deprecation.minimum_notice_days = 30; },
  /minimum deprecation notice must be at least 180 days/,
);
expectFailure(
  "chat idempotency remains optional",
  source,
  (doc) => {
    const parameter = doc.paths["/chat/completions"].post.parameters.find(
      (item) => item.$ref === "#/components/parameters/IdempotencyKey",
    );
    parameter.required = true;
  },
  /POST \/chat\/completions must keep Idempotency-Key optional/,
);
expectFailure(
  "render job creation requires idempotency",
  source,
  (doc) => {
    doc.paths["/images/generations"].post.parameters = [];
  },
  /POST \/images\/generations must require Idempotency-Key/,
);
expectFailure(
  "replay cannot create a second side effect",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].matching_replay = "execute_again";
  },
  /matching replay must return the original operation without a new side effect/,
);
expectFailure(
  "contract tests require real composition",
  source,
  (doc) => {
    doc["x-pixelplus-contract-testing"].composition = "handler_stub";
  },
  /contract tests must use real_gateway_composition/,
);
expectFailure(
  "secret idempotency records cannot retain material",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].secret_fingerprint_storage = "raw_request";
  },
  /secret fingerprints must use non_reversible_keyed_digest/,
);
expectFailure(
  "management secret ingress requires idempotency",
  source,
  (doc) => {
    doc.paths["/provider-accounts/{provider_account_id}/credentials"].post.parameters = [];
  },
  /POST \/provider-accounts\/\{provider_account_id\}\/credentials must require Idempotency-Key/,
);
expectFailure(
  "cross-operation key reuse conflicts",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].cross_operation_key_reuse = "new_scope";
  },
  /cross-operation key reuse must produce idempotency_conflict/,
);
expectFailure(
  "idempotency records retain the replay window",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].retention_hours = 1;
  },
  /idempotency records must retain replay identity for 24 hours/,
);
expectFailure(
  "output retrieval never starts execution",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].operation_classes.output_retrieval.replay = "render_again";
  },
  /output retrieval must read existing resources without rendering or Provider execution/,
);
expectFailure(
  "ownership denial precedes protected side effects",
  source,
  (doc) => {
    doc["x-pixelplus-contract-testing"].ownership_rejection_before = ["adapter_call"];
  },
  /ownership rejection before vault decrypt, Adapter call, and job enqueue/,
);
expectFailure(
  "closed enums cannot grow within v1",
  source,
  (doc) => {
    doc["x-pixelplus-api-lifecycle"].versioning.incompatible_changes = doc[
      "x-pixelplus-api-lifecycle"
    ].versioning.incompatible_changes.filter((rule) => rule !== "add_value_to_closed_enum");
  },
  /versioning policy must classify add_value_to_closed_enum as incompatible/,
);
expectFailure(
  "only real response extension points are declared",
  source,
  (doc) => {
    doc["x-pixelplus-api-lifecycle"].versioning.declared_response_extension_points = [
      "ChatCompletionResponse.x_pixelplus",
    ];
  },
  /declared response extension points must match the stable schemas/,
);
expectFailure(
  "idempotency operation classes match the header matrix",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].operation_classes.durable_creation.operations = doc[
      "x-pixelplus-idempotency-policy"
    ].operation_classes.durable_creation.operations.filter(
      (operationId) => operationId !== "createImageGeneration",
    );
  },
  /durable_creation idempotency class must match the stable operation matrix/,
);
expectFailure(
  "idempotency operation class requiredness cannot drift",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].operation_classes.chat_execution.header = "required";
  },
  /chat_execution idempotency class must match the stable operation matrix/,
);
expectFailure(
  "resource-state commands do not gain an HTTP replay key",
  source,
  (doc) => {
    doc.paths["/render-jobs/{job_id}/cancel"].post.parameters.push({
      $ref: "#/components/parameters/IdempotencyKey",
    });
  },
  /POST \/render-jobs\/\{job_id\}\/cancel must not use Idempotency-Key/,
);
expectFailure(
  "controlled clocks remain part of real-composition proof",
  source,
  (doc) => {
    doc["x-pixelplus-contract-testing"].controlled_implementations_at_ports = doc[
      "x-pixelplus-contract-testing"
    ].controlled_implementations_at_ports.filter((port) => port !== "clock");
  },
  /contract tests must allow controlled clock implementations at the port/,
);
expectFailure(
  "controlled IDs remain part of real-composition proof",
  source,
  (doc) => {
    doc["x-pixelplus-contract-testing"].controlled_implementations_at_ports = doc[
      "x-pixelplus-contract-testing"
    ].controlled_implementations_at_ports.filter((port) => port !== "id_generator");
  },
  /contract tests must allow controlled id_generator implementations at the port/,
);

expectFailure(
  "operations outside replay classes reject Idempotency-Key",
  source,
  (doc) => {
    doc.paths["/models"].get.parameters = [{
      $ref: "#/components/parameters/IdempotencyKey",
    }];
  },
  /GET \/models must not use Idempotency-Key/,
);
expectFailure(
  "broken response refs preserve accumulated policy failures",
  source,
  (doc) => {
    doc.info.version = "9.9.9";
    doc.paths["/models"].get.responses["200"] = {
      $ref: "#/components/responses/Missing",
    };
  },
  [
    /info\.version must be 1\.0\.0/,
    /GET \/models response 200: unresolvable \$ref: #\/components\/responses\/Missing/,
    /stable Public API contract has \d+ violation\(s\)/,
  ],
);

expectFailure(
  "all stable resource reads have an idempotency class",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].operation_classes.resource_retrieval = {
      operations: [],
      header: "not_applicable",
      replay: "read_existing_resource_without_provider_or_job_execution",
    };
  },
  /resource_retrieval idempotency class must match the stable operation matrix/,
);
expectFailure(
  "resource-state replay cannot duplicate external work",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].operation_classes.resource_state_commands.replay =
      "may_duplicate_external_work";
  },
  /resource-state commands must not duplicate external work/,
);
expectFailure(
  "ownership denial precedes job enqueue",
  source,
  (doc) => {
    doc["x-pixelplus-contract-testing"].ownership_rejection_before = [
      "vault_decrypt",
      "adapter_call",
    ];
  },
  /ownership rejection before vault decrypt, Adapter call, and job enqueue/,
);
expectFailure(
  "contract tests retain the full observation set",
  source,
  (doc) => {
    doc["x-pixelplus-contract-testing"].required_observations = [
      "http_status_headers_and_body",
    ];
  },
  /contract tests must observe persistence_and_job_side_effect_counts/,
);

console.log("PASS: stable Public API validator mutation suite");
