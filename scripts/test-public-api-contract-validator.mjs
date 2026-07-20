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

function run(path, env = {}) {
  return spawnSync(process.execPath, [validator, path], {
    cwd: root,
    encoding: "utf8",
    env: {
      ...process.env,
      CI: "",
      PIXELPLUS_PUBLIC_API_BASELINE: "",
      PIXELPLUS_PUBLIC_API_BASELINE_REF: "",
      PIXELPLUS_PUBLIC_API_ALLOW_TEST_BASELINE: "",
      ...env,
    },
    maxBuffer: 32 * 1024 * 1024,
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
    assert.equal(result.error, undefined, `${name}: validator process failed: ${result.error?.message}`);
    assert.notEqual(result.status, 0, `${name}: validator unexpectedly passed`);
    const output = `${result.stdout}\n${result.stderr}`;
    for (const message of Array.isArray(messages) ? messages : [messages]) {
      assert.match(output, message, `${name}: wrong failure`);
    }
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

function expectFailureAgainstBaseline(name, source, mutateBaseline, mutateCandidate, messages) {
  const baselineDoc = structuredClone(source);
  const candidateDoc = structuredClone(source);
  mutateBaseline(baselineDoc);
  mutateCandidate(candidateDoc);
  const directory = mkdtempSync(resolve(tmpdir(), "pixelplus-api-baseline-"));
  const baselinePath = resolve(directory, "baseline.yaml");
  const candidatePath = resolve(directory, "candidate.yaml");
  try {
    writeFileSync(baselinePath, `${JSON.stringify(baselineDoc, null, 2)}\n`);
    writeFileSync(candidatePath, `${JSON.stringify(candidateDoc, null, 2)}\n`);
    const result = run(candidatePath, {
      PIXELPLUS_PUBLIC_API_ALLOW_TEST_BASELINE: "1",
      PIXELPLUS_PUBLIC_API_BASELINE: baselinePath,
      PIXELPLUS_PUBLIC_API_BASELINE_REF: "",
    });
    assert.equal(result.error, undefined, `${name}: validator process failed: ${result.error?.message}`);
    assert.notEqual(result.status, 0, `${name}: validator unexpectedly passed`);
    const output = `${result.stdout}\n${result.stderr}`;
    for (const message of Array.isArray(messages) ? messages : [messages]) {
      assert.match(output, message, `${name}: wrong failure`);
    }
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

function expectSuccess(name, source, mutate) {
  const doc = structuredClone(source);
  mutate(doc);
  const directory = mkdtempSync(resolve(tmpdir(), "pixelplus-api-contract-"));
  const path = resolve(directory, "mutated.yaml");
  try {
    writeFileSync(path, `${JSON.stringify(doc, null, 2)}\n`);
    const result = run(path);
    assert.equal(result.error, undefined, `${name}: validator process failed: ${result.error?.message}`);
    assert.equal(result.status, 0, `${name}: ${result.stderr || result.stdout}`);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

const source = JSON.parse(readFileSync(artifact, "utf8"));
const baseline = run(artifact);
assert.equal(baseline.status, 0, baseline.stderr || baseline.stdout);

const headRef = spawnSync("git", ["rev-parse", "HEAD"], {
  cwd: root,
  encoding: "utf8",
});
assert.equal(headRef.status, 0, headRef.stderr || headRef.stdout);
const headSha = headRef.stdout.trim();
const headBaseline = run(artifact, { PIXELPLUS_PUBLIC_API_BASELINE_REF: headSha });
assert.equal(headBaseline.status, 0, headBaseline.stderr || headBaseline.stdout);
assert.match(headBaseline.stdout, new RegExp(`baseline_source=ref:${headSha}`));

const mutableRefBaseline = run(artifact, { PIXELPLUS_PUBLIC_API_BASELINE_REF: "HEAD" });
assert.notEqual(mutableRefBaseline.status, 0, "mutable baseline ref unexpectedly passed");
assert.match(
  `${mutableRefBaseline.stdout}\n${mutableRefBaseline.stderr}`,
  /PIXELPLUS_PUBLIC_API_BASELINE_REF must be a full immutable commit SHA/,
);

const missingRefBaseline = run(artifact, {
  PIXELPLUS_PUBLIC_API_BASELINE_REF: "0000000000000000000000000000000000000000",
});
assert.notEqual(missingRefBaseline.status, 0, "missing baseline ref unexpectedly passed");
assert.match(
  `${missingRefBaseline.stdout}\n${missingRefBaseline.stderr}`,
  /stable baseline unavailable from 0000000000000000000000000000000000000000/,
);

const forbiddenTestOverride = run(artifact, {
  PIXELPLUS_PUBLIC_API_BASELINE: artifact,
});
assert.notEqual(forbiddenTestOverride.status, 0, "unguarded test baseline override unexpectedly passed");
assert.match(
  `${forbiddenTestOverride.stdout}\n${forbiddenTestOverride.stderr}`,
  /PIXELPLUS_PUBLIC_API_BASELINE is restricted to isolated tests/,
);

const conflictingBaselineSources = run(artifact, {
  PIXELPLUS_PUBLIC_API_ALLOW_TEST_BASELINE: "1",
  PIXELPLUS_PUBLIC_API_BASELINE: artifact,
  PIXELPLUS_PUBLIC_API_BASELINE_REF: headSha,
});
assert.notEqual(conflictingBaselineSources.status, 0, "test baseline override shadowed immutable ref");
assert.match(
  `${conflictingBaselineSources.stdout}\n${conflictingBaselineSources.stderr}`,
  /PIXELPLUS_PUBLIC_API_BASELINE cannot override PIXELPLUS_PUBLIC_API_BASELINE_REF/,
);

const unpinnedCiBaseline = run(artifact, {
  CI: "true",
  PIXELPLUS_PUBLIC_API_BASELINE: "",
  PIXELPLUS_PUBLIC_API_BASELINE_REF: "",
});
assert.notEqual(unpinnedCiBaseline.status, 0, "CI worktree baseline unexpectedly passed");
assert.match(
  `${unpinnedCiBaseline.stdout}\n${unpinnedCiBaseline.stderr}`,
  /CI validation requires PIXELPLUS_PUBLIC_API_BASELINE_REF to be the pull-request base SHA/,
);

{
  const directory = mkdtempSync(resolve(tmpdir(), "pixelplus-api-baseline-"));
  const baselinePath = resolve(directory, "baseline.yaml");
  const candidatePath = resolve(directory, "candidate.yaml");
  const pinnedBaseline = structuredClone(source);
  const brokenCandidate = structuredClone(source);
  delete brokenCandidate.components.schemas.ChatCompletionResponse.properties.model;
  try {
    writeFileSync(baselinePath, `${JSON.stringify(pinnedBaseline, null, 2)}\n`);
    writeFileSync(candidatePath, `${JSON.stringify(brokenCandidate, null, 2)}\n`);
    const result = run(candidatePath, {
      PIXELPLUS_PUBLIC_API_ALLOW_TEST_BASELINE: "1",
      PIXELPLUS_PUBLIC_API_BASELINE: baselinePath,
      PIXELPLUS_PUBLIC_API_BASELINE_REF: "",
    });
    assert.notEqual(result.status, 0, "independent baseline unexpectedly allowed candidate co-edit");
    assert.match(
      `${result.stdout}\n${result.stderr}`,
      /baseline compatibility: components\.schemas\.ChatCompletionResponse property model cannot be removed/,
    );
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

expectFailure(
  "prototype version is rejected",
  source,
  (doc) => {
    doc.info.version = "0.0.0-prototype";
    doc["x-pixelplus-api-lifecycle"].semantic_version = "0.0.0-prototype";
  },
  /stable API semantic version must be valid SemVer without a prerelease/,
);
expectSuccess(
  "compatible patch release remains on v1",
  source,
  (doc) => {
    doc.info.version = "1.0.1";
    doc["x-pixelplus-api-lifecycle"].semantic_version = "1.0.1";
  },
);
expectSuccess(
  "compatible minor release remains on v1",
  source,
  (doc) => {
    doc.info.version = "1.1.0";
    doc["x-pixelplus-api-lifecycle"].semantic_version = "1.1.0";
  },
);
expectFailure(
  "version fields must match",
  source,
  (doc) => { doc["x-pixelplus-api-lifecycle"].semantic_version = "1.1.0"; },
  /info.version and lifecycle semantic_version must match/,
);
expectFailure(
  "semantic major remains aligned with v1",
  source,
  (doc) => {
    doc.info.version = "2.0.0";
    doc["x-pixelplus-api-lifecycle"].semantic_version = "2.0.0";
  },
  /semantic major 2 must match public API major v1 and server \/v1/,
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
  /contract-test ownership rejection boundaries must match the stable set/,
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
  /controlled contract-test ports must match the stable allowlist/,
);
expectFailure(
  "controlled IDs remain part of real-composition proof",
  source,
  (doc) => {
    doc["x-pixelplus-contract-testing"].controlled_implementations_at_ports = doc[
      "x-pixelplus-contract-testing"
    ].controlled_implementations_at_ports.filter((port) => port !== "id_generator");
  },
  /controlled contract-test ports must match the stable allowlist/,
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
    doc.info.version = "2.0.0";
    doc["x-pixelplus-api-lifecycle"].semantic_version = "2.0.0";
    doc.paths["/models"].get.responses["200"] = {
      $ref: "#/components/responses/Missing",
    };
  },
  [
    /semantic major 2 must match public API major v1 and server \/v1/,
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
  /contract-test ownership rejection boundaries must match the stable set/,
);
expectFailure(
  "contract tests retain the full observation set",
  source,
  (doc) => {
    doc["x-pixelplus-contract-testing"].required_observations = [
      "http_status_headers_and_body",
    ];
  },
  /contract tests must observe exactly the stable observation set/,
);

expectFailure(
  "inference operations require their stable authorization scope",
  source,
  (doc) => { delete doc.paths["/models"].get["x-required-scopes"]; },
  /GET \/models must declare exactly one authorization scope requirement form/,
);
expectFailure(
  "authorization scope allowlists are closed",
  source,
  (doc) => {
    doc.paths["/chat/completions"].post["x-required-scopes"].push("jobs.manage");
  },
  /POST \/chat\/completions authorization scopes must exactly require chat\.completions/,
);
expectFailure(
  "capability snapshot retains scope alternatives",
  source,
  (doc) => {
    doc.paths["/provider-accounts/{provider_account_id}/capability-snapshot"].get[
      "x-required-scope-any-of"
    ] = ["accounts.read"];
  },
  /GET \/provider-accounts\/\{provider_account_id\}\/capability-snapshot authorization scopes must allow any of accounts\.read, capabilities\.read/,
);
expectFailure(
  "scope metadata uses one requirement form",
  source,
  (doc) => {
    doc.paths["/models"].get["x-required-scope-any-of"] = ["capabilities.read"];
  },
  /GET \/models must declare exactly one authorization scope requirement form/,
);
expectFailure(
  "idempotency fingerprints include every side-effect input",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].request_fingerprint = ["operation_identity"];
  },
  /idempotency fingerprint must exactly include operation, normalized path\/query, and every side-effect-changing input/,
);
expectFailure(
  "in-progress replay cannot execute again",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].in_progress_replay = "execute_again";
  },
  /in-progress replay must not call the Adapter or create another side effect/,
);
expectFailure(
  "fingerprint mismatch preserves the original operation",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].fingerprint_mismatch = "replace_original";
  },
  /fingerprint mismatch must conflict without changing the original operation/,
);
expectFailure(
  "full execution retry owners are closed",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].retry_owners.forbidden_full_execution_retry_owners = [
      "http_middleware",
    ];
  },
  /forbidden full-execution retry owners must match the stable set/,
);
expectFailure(
  "controlled implementation ports are a closed allowlist",
  source,
  (doc) => {
    doc["x-pixelplus-contract-testing"].controlled_implementations_at_ports.push(
      "gateway_policy_engine",
    );
  },
  /controlled contract-test ports must match the stable allowlist/,
);
expectFailure(
  "migration instructions are required before removal",
  source,
  (doc) => {
    doc["x-pixelplus-api-lifecycle"].deprecation.migration_instructions_required = false;
  },
  /removal requires migration instructions covering the stable compatibility dimensions/,
);
expectFailure(
  "migration instructions cover every compatibility dimension",
  source,
  (doc) => {
    doc["x-pixelplus-api-lifecycle"].deprecation.migration_instructions_must_cover = [
      "request",
    ];
  },
  /removal requires migration instructions covering the stable compatibility dimensions/,
);
expectFailure(
  "old and successor contract suites overlap through support",
  source,
  (doc) => {
    doc[
      "x-pixelplus-api-lifecycle"
    ].deprecation.parallel_old_and_successor_contract_tests_until_support_window_ends = false;
  },
  /old and successor contract tests must run in parallel through the support window/,
);
expectFailure(
  "OpenAPI responses objects are structurally valid",
  source,
  (doc) => { doc.paths["/models"].get.responses = {}; },
  /OpenAPI structural validation failed/,
);
expectFailure(
  "request schemas cannot gain maxLength constraints",
  source,
  (doc) => { doc.components.schemas.ChatCompletionRequest.properties.model.maxLength = 64; },
  /baseline compatibility: POST \/chat\/completions request application\/json\.properties\.model cannot add request-narrowing maxLength/,
);
expectFailure(
  "request schemas cannot gain minimum constraints",
  source,
  (doc) => { doc.components.schemas.ChatCompletionRequest.properties.temperature.minimum = 0; },
  /cannot add request-narrowing minimum/,
);
expectFailure(
  "request schemas cannot gain maxItems constraints",
  source,
  (doc) => { doc.components.schemas.ChatCompletionRequest.properties.messages.maxItems = 100; },
  /cannot add request-narrowing maxItems/,
);
expectFailure(
  "request schemas cannot gain dependentRequired constraints",
  source,
  (doc) => {
    doc.components.schemas.ChatCompletionRequest.dependentRequired = {
      temperature: ["model"],
    };
  },
  /cannot add request-narrowing dependentRequired/,
);
expectFailure(
  "request schemas cannot gain minProperties constraints",
  source,
  (doc) => { doc.components.schemas.ChatCompletionRequest.minProperties = 2; },
  /cannot add request-narrowing minProperties/,
);
expectFailure(
  "request schemas cannot gain required properties",
  source,
  (doc) => {
    doc.components.schemas.ChatCompletionRequest.properties.client_trace = { type: "string" };
    doc.components.schemas.ChatCompletionRequest.required.push("client_trace");
  },
  /cannot add required property client_trace/,
);
expectSuccess(
  "request schemas may gain optional properties",
  source,
  (doc) => {
    doc.components.schemas.ChatCompletionRequest.properties.client_trace = { type: "string" };
  },
);
expectFailure(
  "response fields cannot gain const constraints",
  source,
  (doc) => {
    doc.components.schemas.ChatCompletionResponse.properties.model.const = "gpt-4o-mini";
  },
  /cannot add response-narrowing const/,
);
expectFailure(
  "response fields cannot gain maxLength constraints",
  source,
  (doc) => {
    doc.components.schemas.ChatCompletionResponse.properties.object.maxLength = 128;
  },
  /cannot add response-narrowing maxLength/,
);
expectFailure(
  "closed response objects cannot gain optional properties",
  source,
  (doc) => {
    doc.components.schemas.ChatCompletionResponse.properties.service_tier = { type: "string" };
  },
  /cannot add response property service_tier on a closed object/,
);

expectFailure(
  "direct credential request examples cannot contain raw material",
  source,
  (doc) => {
    doc.paths["/provider-accounts/{provider_account_id}/credentials"].post.requestBody.content[
      "application/json"
    ].examples = {
      LeakedCredential: {
        value: {
          credential: {
            credential_class: "web_session",
            material: "raw-cookie-value",
          },
        },
      },
    };
  },
  /POST \/provider-accounts\/\{provider_account_id\}\/credentials request application\/json\.examples\.LeakedCredential: example must not contain secret material/,
);
expectFailure(
  "schema singular examples cannot contain secrets",
  source,
  (doc) => {
    doc.components.schemas.ChatMessage.properties.content.example = "Bearer abcdefghijklmnop";
  },
  /components\.schemas\.ChatMessage\.properties\.content\.example: example must not contain secret material/,
);
expectFailure(
  "parameter examples cannot contain secrets",
  source,
  (doc) => {
    doc.paths["/assets/{asset_id}"].get.parameters[0].example = "Bearer abcdefghijklmnop";
  },
  /GET \/assets\/\{asset_id\} parameter path:asset_id\.example: example must not contain secret material/,
);
expectFailure(
  "request examples cannot contain token-shaped strings",
  source,
  (doc) => {
    doc.paths["/chat/completions"].post.requestBody.content["application/json"].examples.TokenLeak = {
      value: {
        model: "gpt-4.1",
        messages: [{ role: "user", content: "Bearer abcdefghijklmnop" }],
      },
    };
  },
  /POST \/chat\/completions request application\/json\.examples\.TokenLeak: example must not contain secret material/,
);
expectSuccess(
  "benign request examples remain valid",
  source,
  (doc) => {
    doc.paths["/chat/completions"].post.requestBody.content["application/json"].examples.Benign = {
      value: {
        model: "gpt-4.1",
        messages: [{ role: "user", content: "Summarize this document" }],
      },
    };
  },
);

expectFailure(
  "asset upload requires the stable forbidden outcome",
  source,
  (doc) => { delete doc.paths["/assets"].post.responses["403"]; },
  /POST \/assets must document 403 insufficient assets\.write via ErrorForbidden/,
);
expectFailure(
  "asset upload requires the stable request-too-large outcome",
  source,
  (doc) => { delete doc.paths["/assets"].post.responses["413"]; },
  /POST \/assets must document 413 request_too_large for uploads over L-ASSET-UPLOAD-MAX/,
);
expectFailure(
  "asset upload 413 keeps its canonical example",
  source,
  (doc) => {
    delete doc.paths["/assets"].post.responses["413"].content["application/json"].examples.ErrorRequestTooLarge;
  },
  /POST \/assets must document 413 request_too_large for uploads over L-ASSET-UPLOAD-MAX/,
);
expectFailure(
  "request-too-large example keeps its canonical code",
  source,
  (doc) => { doc.components.examples.ErrorRequestTooLarge.value.code = "invalid_request"; },
  /ErrorRequestTooLarge must demonstrate code=request_too_large/,
);

expectFailure(
  "stable response required fields cannot disappear",
  source,
  (doc) => {
    doc.components.schemas.ChatCompletionResponse.required = doc.components.schemas.ChatCompletionResponse.required.filter(
      (name) => name !== "model",
    );
  },
  /baseline compatibility: components\.schemas\.ChatCompletionResponse required property model cannot be removed/,
);
expectFailure(
  "stable response property types cannot change",
  source,
  (doc) => {
    doc.components.schemas.ChatCompletionResponse.properties.model.type = "integer";
    doc.components.schemas.ChatCompletionResponse.examples[0].model = 1;
    doc.paths["/chat/completions"].post.responses["200"].content[
      "application/json"
    ].examples.NonStreamSuccess.value.model = 1;
  },
  /baseline compatibility: components\.schemas\.ChatCompletionResponse\.properties\.model type cannot change/,
);
expectFailure(
  "required request bodies remain required",
  source,
  (doc) => { doc.paths["/chat/completions"].post.requestBody.required = false; },
  /baseline compatibility: POST \/chat\/completions request body requiredness cannot change/,
);
expectFailureAgainstBaseline(
  "optional request bodies cannot become required",
  source,
  (baselineDoc) => { baselineDoc.paths["/chat/completions"].post.requestBody.required = false; },
  (candidateDoc) => { candidateDoc.paths["/chat/completions"].post.requestBody.required = true; },
  /baseline compatibility: POST \/chat\/completions request body requiredness cannot change/,
);
expectFailure(
  "stable response statuses cannot disappear",
  source,
  (doc) => { delete doc.paths["/models"].get.responses["200"]; },
  /baseline compatibility: GET \/models response status 200 cannot be removed/,
);
expectFailure(
  "stable closed enums cannot change within v1",
  source,
  (doc) => { doc.components.schemas.Remediation.enum.push("new_remediation"); },
  /baseline compatibility: components\.schemas\.Remediation closed enum must remain unchanged/,
);
expectFailure(
  "new operations require one complete descriptor",
  source,
  (doc) => {
    doc.paths["/new-resource"] = {
      get: {
        operationId: "getNewResource",
        security: [{ ClientApiKey: [] }],
        "x-required-scopes": ["capabilities.read"],
        responses: {
          "200": {
            description: "New resource",
          },
        },
      },
    };
  },
  /GET \/new-resource must have an operation descriptor/,
);
expectFailure(
  "operation classes cannot overlap",
  source,
  (doc) => {
    doc["x-pixelplus-idempotency-policy"].operation_classes.output_retrieval.operations.push(
      "listModels",
    );
  },
  /listModels must belong to exactly one idempotency class/,
);

console.log("PASS: stable Public API validator mutation suite");
