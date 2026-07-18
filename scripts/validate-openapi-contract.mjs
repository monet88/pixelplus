#!/usr/bin/env node
/**
 * Prototype OpenAPI contract validator for PixelPlus #18.
 *
 * Usage (from repo root):
 *   node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml
 *
 * Zero new Node dependencies. The tracer is JSON-compatible YAML (JSON subset)
 * so it is loaded with JSON.parse. Draft 2020-12 example validation requires
 * Python with the `jsonschema` package in the validation environment.
 *
 * This is representation validation, not a runtime Gateway test and not a full
 * external OpenAPI metaschema check.
 */

import { spawnSync } from "node:child_process";
import { readFileSync, existsSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, "..");

const REQUIRED_PATHS = [
  ["/models", "get"],
  ["/chat/completions", "post"],
  ["/chat/executions/{execution_id}/cancel", "post"],
  ["/assets", "post"],
  ["/assets/{asset_id}", "get"],
  ["/assets/{asset_id}/content", "get"],
  ["/images/generations", "post"],
  ["/images/edits", "post"],
  ["/images/inpaints", "post"],
  ["/render-jobs/{job_id}", "get"],
  ["/render-jobs/{job_id}/cancel", "post"],
  ["/render-jobs/{job_id}/outputs/{output_entry_id}/retry", "post"],
];

const REQUIRED_ERROR_EXAMPLES = {
  ErrorCapabilityUnsupported: "capability_unsupported",
  ErrorCapabilityUnverified: "capability_unverified",
  ErrorSnapshotStale: "snapshot_stale",
  ErrorAccountNotUsable: "account_not_usable",
  ErrorRateLimit: "rate_limit",
  ErrorConcurrencyLimit: "concurrency_limit",
  ErrorQuotaExhausted: "quota_exhausted",
  ErrorProviderRateLimitedUnknown: "provider_rate_limited",
  ErrorProviderQuotaExhausted: "provider_quota_exhausted",
  ErrorProviderAuthExpired: "provider_auth_expired",
  ErrorProviderChallenged: "provider_challenged",
  ErrorUpstreamProtocolDrift: "upstream_protocol_drift",
  ErrorExecutionPossiblyCommitted: "execution_possibly_committed",
  ErrorResourceNotFound: "resource_not_found",
};

const REQUIRED_EVENT_TYPES = {
  ChatOpenEvent: "open",
  ChatDeltaEvent: "delta",
  ChatHeartbeatEvent: "heartbeat",
  ChatCompletedEvent: "completed",
  ChatFailedEvent: "failed",
  ChatCanceledEvent: "canceled",
};

const TERMINAL_EVENT_TYPES = new Set(["completed", "failed", "canceled"]);
const HTTP_METHODS = new Set([
  "get",
  "put",
  "post",
  "delete",
  "options",
  "head",
  "patch",
  "trace",
]);

const DESCRIPTION_INVARIANTS = [
  "open -> delta",
  "heartbeats allowed",
  "exactly one completed|failed|canceled terminal",
  "no post-terminal data",
  "commit-unknown",
  "never re-render",
  "no [done]",
  "disconnect is implicit cancel",
  "cancellation is not proof upstream stopped",
];

const failures = [];
const fail = (msg) => failures.push(msg);

function loadDoc(path) {
  if (!existsSync(path)) {
    throw new Error(`missing artifact: ${path}`);
  }
  const raw = readFileSync(path, "utf8");
  // JSON-compatible YAML: JSON is a YAML 1.2 subset.
  try {
    return JSON.parse(raw);
  } catch (err) {
    throw new Error(
      `failed to parse as JSON-compatible YAML (JSON subset): ${err.message}`,
    );
  }
}

function resolvePointer(doc, pointer) {
  if (!pointer.startsWith("#/")) {
    throw new Error(`external or unsupported $ref: ${pointer}`);
  }
  let node = doc;
  for (const part of pointer.slice(2).split("/")) {
    const key = part.replace(/~1/g, "/").replace(/~0/g, "~");
    if (node == null || typeof node !== "object" || !(key in node)) {
      throw new Error(`unresolvable $ref: ${pointer}`);
    }
    node = node[key];
  }
  return node;
}

function walkRefs(node, path = "$") {
  let count = 0;
  if (Array.isArray(node)) {
    node.forEach((item, i) => {
      count += walkRefs(item, `${path}[${i}]`);
    });
    return count;
  }
  if (node && typeof node === "object") {
    if ("$ref" in node) {
      count += 1;
      const ref = node.$ref;
      if (typeof ref !== "string" || !ref.startsWith("#/")) {
        fail(`${path}: external/invalid $ref ${JSON.stringify(ref)}`);
      } else {
        try {
          resolvePointer(docCache, ref);
        } catch (err) {
          fail(`${path}: ${err.message}`);
        }
      }
    }
    for (const [k, v] of Object.entries(node)) {
      count += walkRefs(v, `${path}.${k}`);
    }
  }
  return count;
}

function detectRefCycles(node, path = "$", stack = new Set()) {
  if (Array.isArray(node)) {
    node.forEach((item, i) => detectRefCycles(item, `${path}[${i}]`, stack));
    return;
  }
  if (!node || typeof node !== "object") return;

  if ("$ref" in node) {
    const ref = node.$ref;
    if (typeof ref === "string" && ref.startsWith("#/")) {
      if (stack.has(ref)) {
        fail(`${path}: cyclic $ref ${ref}`);
      } else {
        try {
          const nextStack = new Set(stack);
          nextStack.add(ref);
          detectRefCycles(resolvePointer(docCache, ref), `${path}->${ref}`, nextStack);
        } catch (err) {
          fail(`${path}: ${err.message}`);
        }
      }
    }
  }

  for (const [key, value] of Object.entries(node)) {
    if (key !== "$ref") detectRefCycles(value, `${path}.${key}`, stack);
  }
}

function schemaContainsTenantId(node, doc, seenRefs = new Set()) {
  if (Array.isArray(node)) {
    return node.some((item) => schemaContainsTenantId(item, doc, seenRefs));
  }
  if (!node || typeof node !== "object") return false;

  if (typeof node.$ref === "string" && node.$ref.startsWith("#/")) {
    if (!seenRefs.has(node.$ref)) {
      const nextSeen = new Set(seenRefs);
      nextSeen.add(node.$ref);
      try {
        if (schemaContainsTenantId(resolvePointer(doc, node.$ref), doc, nextSeen)) {
          return true;
        }
      } catch {
        return false;
      }
    }
  }

  const props = node.properties;
  if (props && typeof props === "object" && "tenant_id" in props) return true;
  if (Array.isArray(node.required) && node.required.includes("tenant_id")) {
    return true;
  }
  return Object.entries(node).some(
    ([key, value]) =>
      key !== "$ref" && schemaContainsTenantId(value, doc, seenRefs),
  );
}

function resolveObjectRef(node, doc) {
  let current = node;
  const seen = new Set();
  while (
    current &&
    typeof current === "object" &&
    typeof current.$ref === "string" &&
    current.$ref.startsWith("#/")
  ) {
    if (seen.has(current.$ref)) {
      throw new Error(`cyclic $ref: ${current.$ref}`);
    }
    seen.add(current.$ref);
    current = resolvePointer(doc, current.$ref);
  }
  return current;
}

function operationAcceptsTenantId(pathKey, pathItem, operation, doc) {
  if (pathKey.includes("{tenant_id}")) return true;

  const parameters = [
    ...(Array.isArray(pathItem.parameters) ? pathItem.parameters : []),
    ...(Array.isArray(operation.parameters) ? operation.parameters : []),
  ];
  for (const rawParameter of parameters) {
    let parameter;
    try {
      parameter = resolveObjectRef(rawParameter, doc);
    } catch {
      continue;
    }
    if (
      parameter &&
      typeof parameter === "object" &&
      parameter.name === "tenant_id"
    ) {
      return true;
    }
  }

  let requestBody;
  try {
    requestBody = resolveObjectRef(operation.requestBody, doc);
  } catch {
    return false;
  }
  const content = requestBody?.content;
  if (!content || typeof content !== "object") return false;
  return Object.values(content).some((mediaType) =>
    schemaContainsTenantId(mediaType?.schema, doc),
  );
}

function usesClientApiKey(operation, doc) {
  const securities = operation.security ?? doc.security;
  if (!Array.isArray(securities) || securities.length === 0) return false;
  return securities.every((requirement) => {
    if (!requirement || typeof requirement !== "object") return false;
    const schemes = Object.keys(requirement);
    return (
      schemes.length === 1 &&
      schemes[0] === "ClientApiKey" &&
      Array.isArray(requirement.ClientApiKey) &&
      requirement.ClientApiKey.length === 0
    );
  });
}

function gatherDescriptionCorpus(node, chunks = []) {
  if (Array.isArray(node)) {
    node.forEach((item) => gatherDescriptionCorpus(item, chunks));
    return chunks;
  }
  if (node && typeof node === "object") {
    for (const [k, v] of Object.entries(node)) {
      if ((k === "description" || k === "summary") && typeof v === "string") {
        chunks.push(v);
      } else {
        gatherDescriptionCorpus(v, chunks);
      }
    }
  }
  return chunks;
}

function collectExamplePairs(node, path = "$", out = []) {
  if (Array.isArray(node)) {
    node.forEach((item, i) => collectExamplePairs(item, `${path}[${i}]`, out));
    return out;
  }
  if (!node || typeof node !== "object") return out;

  if (Array.isArray(node.examples) && "type" in node) {
    node.examples.forEach((ex, i) => {
      out.push({ label: `${path}.examples[${i}]`, value: ex, schema: node });
    });
  }

  if (node.examples && typeof node.examples === "object" && !Array.isArray(node.examples)) {
    for (const [name, payload] of Object.entries(node.examples)) {
      if (payload && typeof payload === "object" && "value" in payload) {
        const schema = payload.schema ?? node.schema ?? null;
        out.push({
          label: `${path}.examples.${name}`,
          value: payload.value,
          schema: schema && typeof schema === "object" ? schema : null,
        });
      }
    }
  }

  if ("example" in node && "type" in node) {
    out.push({ label: `${path}.example`, value: node.example, schema: node });
  }

  for (const [k, v] of Object.entries(node)) {
    if (k === "examples" || k === "example") continue;
    collectExamplePairs(v, `${path}.${k}`, out);
  }
  return out;
}

let docCache = null;

function validateWithPythonJsonSchema(doc, examplePairs) {
  // Build a compact payload for Python Draft202012 validation.
  const items = [];
  for (const pair of examplePairs) {
    const schema = pair.schema;
    if (!schema || typeof schema !== "object") continue;
    if (schema.format === "binary") continue;
    items.push({ label: pair.label, schema, value: pair.value });
  }

  if (items.length === 0) {
    return { validated: 0, pyAvailable: true };
  }

  const py = `
import json, sys
from jsonschema import Draft202012Validator

def resolve_pointer(doc, pointer):
    if not pointer.startswith("#/"):
        raise ValueError(f"external $ref: {pointer}")
    node = doc
    for part in pointer[2:].split("/"):
        part = part.replace("~1", "/").replace("~0", "~")
        node = node[part]
    return node

def expand(node, doc, seen=None):
    if seen is None:
        seen = set()
    if isinstance(node, dict):
        if "$ref" in node:
            ref = node["$ref"]
            if not isinstance(ref, str) or not ref.startswith("#/"):
                raise ValueError(f"external $ref: {ref}")
            if ref in seen:
                raise ValueError(f"cyclic $ref: {ref}")
            next_seen = set(seen)
            next_seen.add(ref)
            resolved = expand(resolve_pointer(doc, ref), doc, next_seen)
            siblings = {k: v for k, v in node.items() if k != "$ref"}
            if not siblings:
                return resolved
            return {"allOf": [resolved, expand(siblings, doc, seen)]}
        return {k: expand(v, doc, seen) for k, v in node.items()}
    if isinstance(node, list):
        return [expand(v, doc, seen) for v in node]
    return node

payload = json.load(sys.stdin)
doc = payload["doc"]
failures = []
validated = 0
for item in payload["items"]:
    try:
        schema = expand(item["schema"], doc)
        Draft202012Validator(schema).validate(item["value"])
        validated += 1
    except Exception as exc:
        failures.append(f"{item['label']}: example failed schema validation: {exc}")
print(json.dumps({"validated": validated, "failures": failures}))
`;

  const input = JSON.stringify({ doc, items });
  const result = spawnSync("python", ["-c", py], {
    input,
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
    cwd: ROOT,
  });

  if (result.error) {
    fail(`python jsonschema unavailable: ${result.error.message}`);
    return { validated: 0, pyAvailable: false };
  }
  if (result.status !== 0) {
    const errText = (result.stderr || result.stdout || "").trim();
    if (/No module named ['"]jsonschema['"]/i.test(errText)) {
      fail(
        "python jsonschema is required for #18 Draft 2020-12 example validation; validation cannot continue",
      );
      return { validated: 0, pyAvailable: false };
    }
    fail(`python jsonschema helper failed: ${errText || `exit ${result.status}`}`);
    return { validated: 0, pyAvailable: false };
  }

  let parsed;
  try {
    parsed = JSON.parse(result.stdout.trim().split("\n").filter(Boolean).pop());
  } catch (err) {
    fail(`cannot parse python validator output: ${err.message}; raw=${result.stdout}`);
    return { validated: 0, pyAvailable: true };
  }
  for (const f of parsed.failures || []) fail(f);
  return { validated: parsed.validated || 0, pyAvailable: true };
}

function main() {
  const rel =
    process.argv[2] || "contracts/openapi/pixelplus-public-api-v0alpha.yaml";
  const path = resolve(ROOT, rel);

  let doc;
  try {
    doc = loadDoc(path);
    docCache = doc;
  } catch (err) {
    console.error(`FAIL: ${err.message}`);
    process.exit(1);
  }

  if (doc.openapi !== "3.1.1") {
    fail(`openapi must be 3.1.1, got ${JSON.stringify(doc.openapi)}`);
  }
  const info = doc.info || {};
  if (info.version !== "0.0.0-prototype") {
    fail(`info.version must be 0.0.0-prototype, got ${JSON.stringify(info.version)}`);
  }
  const dialect = String(doc.jsonSchemaDialect || "");
  if (!dialect.includes("2020-12")) {
    fail(`jsonSchemaDialect must be JSON Schema 2020-12, got ${JSON.stringify(dialect)}`);
  }
  const status = doc["x-pixelplus-artifact-status"];
  if (status !== "prototype") {
    fail(`top-level x-pixelplus-artifact-status must be "prototype", got ${JSON.stringify(status)}`);
  }

  const servers = doc.servers || [];
  const hasV1 = servers.some((s) => {
    if (!s || typeof s !== "object") return false;
    const url = String(s.url || "");
    return url === "/v1" || url.endsWith("/v1") || url.endsWith("/v1/");
  });
  if (!hasV1) fail("servers must include a /v1 base URL");

  const components = doc.components || {};
  const securitySchemes = components.securitySchemes || {};
  if (!securitySchemes.ClientApiKey) {
    fail("components.securitySchemes.ClientApiKey missing");
  } else {
    const scheme = securitySchemes.ClientApiKey;
    if (scheme.type !== "http" || scheme.scheme !== "bearer") {
      fail("ClientApiKey must be http bearer");
    }
    if (!String(scheme.bearerFormat || "").includes("sk-pxp_<public_locator>_<secret>")) {
      fail("ClientApiKey bearerFormat must document sk-pxp_<public_locator>_<secret>");
    }
  }

  const schemas = components.schemas || {};
  const examples = components.examples || {};
  const paths = doc.paths || {};

  if ("x-pixelplus-status" in info || "x-pixelplus-artifact-status" in info) {
    fail("prototype status must use only top-level x-pixelplus-artifact-status");
  }
  if (paths["/models"]?.get?.responses?.["404"]) {
    fail("GET /models must not advertise resource-id 404 semantics");
  }

  const idempotencyParameter = paths["/chat/completions"]?.post?.parameters?.find(
    (parameter) => parameter?.name === "Idempotency-Key",
  );
  if (idempotencyParameter?.schema && "maxLength" in idempotencyParameter.schema) {
    fail("Idempotency-Key must not freeze an unowned numeric maximum in #18");
  }

  const modelOffer = schemas.ModelOffer || {};
  const offerStatuses = modelOffer.properties?.operation_status?.enum || [];
  if (
    JSON.stringify([...offerStatuses].sort()) !==
    JSON.stringify(["conditionally_supported", "verified"])
  ) {
    fail("ModelOffer operation_status must contain only offerable capability states");
  }
  if (modelOffer.properties?.offerable?.const !== true) {
    fail("ModelOffer.offerable must be const true on the client-facing model list");
  }
  if (modelOffer.properties?.freshness?.const !== "fresh") {
    fail("ModelOffer.freshness must be const fresh on the client-facing model list");
  }
  if ((modelOffer.properties?.streaming_class?.enum || []).includes("unsupported")) {
    fail("ModelOffer.streaming_class must not expose unsupported rows as offers");
  }
  const modelObject = schemas.ModelObject || {};
  if (!(modelObject.required || []).includes("x_pixelplus")) {
    fail("ModelObject must require x_pixelplus offer metadata");
  }
  if ((modelObject.properties?.x_pixelplus?.properties?.offers?.minItems || 0) < 1) {
    fail("ModelObject.x_pixelplus.offers must contain at least one offerable pair");
  }

  const openRequired = new Set(schemas.ChatOpenEvent?.required || []);
  for (const field of ["type", "id", "model", "created", "x_pixelplus"]) {
    if (!openRequired.has(field)) fail(`ChatOpenEvent must require ${field}`);
  }
  const metadataRequired = new Set(schemas.ChatSafeMetadata?.required || []);
  for (const field of ["request_id", "execution_id"]) {
    if (!metadataRequired.has(field)) {
      fail(`ChatSafeMetadata must require ${field} for actionable stream cancellation`);
    }
  }

  if (schemas.CanonicalError?.properties?.retry_after_seconds?.minimum !== 1) {
    fail("CanonicalError.retry_after_seconds must have minimum 1 second");
  }

  const outputRules = schemas.OutputEntry?.allOf || [];
  const hasOutputAssetRule = outputRules.some(
    (rule) =>
      rule?.if?.properties?.delivery_state?.const === "available" &&
      (rule?.then?.required || []).includes("asset_id") &&
      (rule?.else?.not?.required || []).includes("asset_id"),
  );
  if (!hasOutputAssetRule) {
    fail("OutputEntry must require asset_id only when delivery_state=available");
  }

  const requiredAssetFields = [
    "asset_id",
    "kind",
    "content_type",
    "byte_size",
    "checksum",
    "origin",
    "created_at",
    "retention_class",
  ];
  const assetRequired = new Set(schemas.Asset?.required || []);
  for (const field of requiredAssetFields) {
    if (!assetRequired.has(field)) fail(`Asset must require locked logical field ${field}`);
  }
  if (schemas.Asset?.properties?.media_type) {
    fail("Asset must use locked content_type naming rather than media_type");
  }

  for (const schemaName of [
    "ChatCompletionRequest",
    "ImageGenerationRequest",
    "ImageEditRequest",
    "ImageInpaintRequest",
  ]) {
    const nSchema = schemas[schemaName]?.properties?.n;
    if (nSchema && "maximum" in nSchema) {
      fail(`${schemaName}.n must not freeze an unowned numeric maximum in #18`);
    }
  }

  const chatFailedOperation = schemas.ChatFailedEvent?.examples?.[0]?.error?.operation;
  if (chatFailedOperation !== "chat_streaming") {
    fail("ChatFailedEvent example must identify operation=chat_streaming");
  }
  const pathFailedOperation =
    paths["/chat/completions"]?.post?.responses?.["200"]?.content?.[
      "text/event-stream"
    ]?.examples?.FailedEvent?.value?.error?.operation;
  if (pathFailedOperation !== "chat_streaming") {
    fail("POST /chat/completions FailedEvent must identify operation=chat_streaming");
  }

  const chat429Examples =
    paths["/chat/completions"]?.post?.responses?.["429"]?.content?.[
      "application/json"
    ]?.examples || {};
  for (const name of [
    "ErrorRateLimit",
    "ErrorConcurrencyLimit",
    "ErrorQuotaExhausted",
    "ErrorProviderRateLimitedUnknown",
    "ErrorProviderQuotaExhausted",
  ]) {
    if (!(name in chat429Examples)) {
      fail(`POST /chat/completions 429 must include distinct example ${name}`);
    }
  }

  const refCount = walkRefs(doc);
  detectRefCycles(doc);

  let opCount = 0;
  for (const [pathKey, method] of REQUIRED_PATHS) {
    const item = paths[pathKey];
    if (!item || typeof item !== "object" || !item[method]) {
      fail(`missing operation ${method.toUpperCase()} ${pathKey}`);
      continue;
    }
    opCount += 1;
  }

  for (const [pathKey, pathItem] of Object.entries(paths)) {
    if (pathKey === "/v1" || pathKey.startsWith("/v1/")) {
      fail(`path key should be relative to /v1 server, found ${pathKey}`);
    }
    if (!pathItem || typeof pathItem !== "object") continue;
    for (const [method, operation] of Object.entries(pathItem)) {
      if (!HTTP_METHODS.has(method) || !operation || typeof operation !== "object") {
        continue;
      }
      const operationLabel = `${method.toUpperCase()} ${pathKey}`;
      if (!usesClientApiKey(operation, doc)) {
        fail(`${operationLabel} must require only ClientApiKey authentication`);
      }
      if (operationAcceptsTenantId(pathKey, pathItem, operation, doc)) {
        fail(`${operationLabel} accepts client-supplied tenant_id authority`);
      }
    }
  }

  for (const name of [
    "ChatCompletionRequest",
    "ImageGenerationRequest",
    "ImageEditRequest",
    "ImageInpaintRequest",
    "ChatXPixelPlus",
  ]) {
    const schema = schemas[name];
    if (!schema) {
      fail(`missing request schema ${name}`);
      continue;
    }
    if (schemaContainsTenantId(schema, doc)) {
      fail(`request schema ${name} exposes tenant_id`);
    }
  }

  for (const [name, code] of Object.entries(REQUIRED_ERROR_EXAMPLES)) {
    const payload = examples[name];
    if (!payload || typeof payload !== "object" || !("value" in payload)) {
      fail(`missing component example ${name}`);
      continue;
    }
    const value = payload.value;
    if (!value || typeof value !== "object" || value.code !== code) {
      fail(`example ${name} must have code=${code}`);
      continue;
    }
    for (const field of [
      "code",
      "category",
      "status_class",
      "retryability",
      "remediation",
      "request_id",
    ]) {
      if (!(field in value)) fail(`example ${name} missing required field ${field}`);
    }
    if (
      ["ErrorRateLimit", "ErrorConcurrencyLimit", "ErrorQuotaExhausted"].includes(
        name,
      )
    ) {
      if (value.category !== "admission" || value.failure_stage !== "admission") {
        fail(`${name} must remain a pre-execution admission error`);
      }
    }
    if (
      ["ErrorProviderRateLimitedUnknown", "ErrorProviderQuotaExhausted"].includes(
        name,
      ) &&
      (value.category !== "execution" ||
        !String(value.failure_stage || "").startsWith("upstream_"))
    ) {
      fail(`${name} must remain a post-admission Provider runtime error`);
    }
    if (name === "ErrorProviderRateLimitedUnknown") {
      if (value.commit_status !== "unknown") {
        fail("ErrorProviderRateLimitedUnknown must set commit_status=unknown");
      }
      if (value.retryability !== "new_request_only") {
        fail("ErrorProviderRateLimitedUnknown must set retryability=new_request_only");
      }
      if ("retry_after_seconds" in value) {
        fail(
          "ErrorProviderRateLimitedUnknown must omit retry_after_seconds (non-time gate)",
        );
      }
    }
    if (name === "ErrorResourceNotFound" && "resource_reference" in value) {
      fail("ErrorResourceNotFound must omit resource_reference");
    }
  }

  const observedTerminals = new Set();
  for (const [schemaName, typeConst] of Object.entries(REQUIRED_EVENT_TYPES)) {
    const schema = schemas[schemaName];
    if (!schema || typeof schema !== "object") {
      fail(`missing event schema ${schemaName}`);
      continue;
    }
    const typeSchema = (schema.properties || {}).type;
    if (!typeSchema || typeSchema.const !== typeConst) {
      fail(`${schemaName}.type const must be ${JSON.stringify(typeConst)}`);
    }
    if (TERMINAL_EVENT_TYPES.has(typeConst)) observedTerminals.add(typeConst);
  }
  if (
    observedTerminals.size !== TERMINAL_EVENT_TYPES.size ||
    [...TERMINAL_EVENT_TYPES].some((t) => !observedTerminals.has(t))
  ) {
    fail(
      `terminal event set must be exactly ${[...TERMINAL_EVENT_TYPES].sort()}, got ${[...observedTerminals].sort()}`,
    );
  }
  const streamSchema = schemas.ChatStreamEvent;
  if (!streamSchema || !Array.isArray(streamSchema.oneOf)) {
    fail("ChatStreamEvent must be a oneOf over event payloads");
  }

  const corpus = gatherDescriptionCorpus(doc).join("\n").toLowerCase();
  for (const token of DESCRIPTION_INVARIANTS) {
    if (!corpus.includes(token)) {
      fail(`description corpus missing invariant phrase: ${JSON.stringify(token)}`);
    }
  }

  const examplePairs = collectExamplePairs(doc);
  // Component examples are error envelopes; attach CanonicalError for validation.
  for (const pair of examplePairs) {
    if (pair.label.startsWith("$.components.examples.") && !pair.schema) {
      pair.schema = schemas.CanonicalError || null;
    }
  }

  const { validated } = validateWithPythonJsonSchema(doc, examplePairs);

  if (failures.length) {
    console.error("FAIL: OpenAPI contract prototype validation failed");
    for (const item of failures) console.error(`  - ${item}`);
    console.error(
      `checked: paths=${Object.keys(paths).length} required_ops=${REQUIRED_PATHS.length} refs=${refCount} error_examples=${Object.keys(REQUIRED_ERROR_EXAMPLES).length} validated_examples=${validated}`,
    );
    process.exit(1);
  }

  console.log("PASS: OpenAI-compatible inference contract prototype");
  console.log(`  artifact: ${rel}`);
  console.log(`  openapi: ${doc.openapi}`);
  console.log(`  version: ${info.version}`);
  console.log(`  artifact-status: ${status}`);
  console.log(`  dialect: ${dialect}`);
  console.log(`  paths: ${Object.keys(paths).length}`);
  console.log(
    `  required operations: ${REQUIRED_PATHS.length} (present=${opCount})`,
  );
  console.log(`  schemas: ${Object.keys(schemas).length}`);
  console.log(
    `  component error examples: ${Object.keys(REQUIRED_ERROR_EXAMPLES).length}`,
  );
  console.log(
    `  stream event types: ${Object.keys(REQUIRED_EVENT_TYPES).length} (terminals=${[...TERMINAL_EVENT_TYPES].sort()})`,
  );
  console.log(`  internal $refs walked: ${refCount}`);
  console.log(`  examples schema-validated: ${validated}`);
  console.log(
    "  note: prototype validation only; no full external OpenAPI metaschema check",
  );
  console.log(
    "  note: Draft 2020-12 example validation via installed Python jsonschema (no new dep)",
  );
}

main();
