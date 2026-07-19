#!/usr/bin/env node

import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const httpMethods = new Set(["get", "put", "post", "delete", "patch", "options", "head", "trace"]);
const failures = [];
let docCache = null;

const requiredOperations = [
  ["/models", "get", "listModels"],
  ["/chat/completions", "post", "createChatCompletion"],
  ["/chat/executions/{execution_id}/cancel", "post", "cancelChatExecution"],
  ["/assets", "post", "createAsset"],
  ["/assets/{asset_id}", "get", "getAsset"],
  ["/assets/{asset_id}/content", "get", "getAssetContent"],
  ["/images/generations", "post", "createImageGeneration"],
  ["/images/edits", "post", "createImageEdit"],
  ["/images/inpaints", "post", "createImageInpaint"],
  ["/render-jobs/{job_id}", "get", "getRenderJob"],
  ["/render-jobs/{job_id}/cancel", "post", "cancelRenderJob"],
  ["/render-jobs/{job_id}/outputs/{output_entry_id}/retry", "post", "retryRenderJobOutput"],
  ["/provider-accounts", "post", "createProviderAccount"],
  ["/provider-accounts", "get", "listProviderAccounts"],
  ["/provider-accounts/{provider_account_id}", "get", "getProviderAccount"],
  ["/provider-accounts/{provider_account_id}", "delete", "deleteProviderAccount"],
  ["/provider-accounts/{provider_account_id}/credentials", "post", "submitProviderCredential"],
  ["/provider-accounts/{provider_account_id}/oauth-authorizations", "post", "startOAuthAuthorization"],
  ["/provider-accounts/{provider_account_id}/oauth-authorizations/{authorization_id}", "get", "getOAuthAuthorization"],
  ["/provider-accounts/{provider_account_id}/probe", "post", "probeProviderAccount"],
  ["/provider-accounts/{provider_account_id}/reauthentication", "post", "reauthenticateProviderAccount"],
  ["/provider-accounts/{provider_account_id}/disable", "post", "disableProviderAccount"],
  ["/provider-accounts/{provider_account_id}/enable", "post", "enableProviderAccount"],
  ["/provider-accounts/{provider_account_id}/capability-snapshot", "get", "getCapabilitySnapshot"],
  ["/routing-policy", "get", "getRoutingPolicy"],
  ["/routing-policy", "put", "replaceRoutingPolicy"],
];

const optionalIdempotencyOperations = new Map([
  ["POST /chat/completions", "#/components/parameters/IdempotencyKey"],
]);
const requiredIdempotencyOperations = new Map([
  ["POST /assets", "#/components/parameters/RequiredIdempotencyKey"],
  ["POST /images/generations", "#/components/parameters/RequiredIdempotencyKey"],
  ["POST /images/edits", "#/components/parameters/RequiredIdempotencyKey"],
  ["POST /images/inpaints", "#/components/parameters/RequiredIdempotencyKey"],
  ["POST /provider-accounts", "#/components/parameters/RequiredIdempotencyKey"],
  ["POST /provider-accounts/{provider_account_id}/credentials", "#/components/parameters/RequiredIdempotencyKey"],
  ["POST /provider-accounts/{provider_account_id}/oauth-authorizations", "#/components/parameters/RequiredIdempotencyKey"],
  ["POST /provider-accounts/{provider_account_id}/reauthentication", "#/components/parameters/RequiredIdempotencyKey"],
]);
const outputRetrievalOperations = new Set([
  "GET /assets/{asset_id}",
  "GET /assets/{asset_id}/content",
  "GET /render-jobs/{job_id}",
]);
const resourceRetrievalOperations = new Set([
  "GET /models",
  "GET /provider-accounts",
  "GET /provider-accounts/{provider_account_id}",
  "GET /provider-accounts/{provider_account_id}/oauth-authorizations/{authorization_id}",
  "GET /provider-accounts/{provider_account_id}/capability-snapshot",
  "GET /routing-policy",
]);
const outputDeliveryRetryOperations = new Set([
  "POST /render-jobs/{job_id}/outputs/{output_entry_id}/retry",
]);
const resourceStateCommandOperations = new Set([
  "POST /chat/executions/{execution_id}/cancel",
  "POST /render-jobs/{job_id}/cancel",
  "DELETE /provider-accounts/{provider_account_id}",
  "POST /provider-accounts/{provider_account_id}/probe",
  "POST /provider-accounts/{provider_account_id}/disable",
  "POST /provider-accounts/{provider_account_id}/enable",
  "PUT /routing-policy",
]);

const requiredErrorExamples = {
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
  ErrorAuthenticationFailed: "authentication_failed",
  ErrorForbidden: "forbidden",
  ErrorInvalidRequest: "invalid_request",
  ErrorAuthModeUnavailable: "auth_mode_unavailable",
  ErrorIdempotencyConflict: "idempotency_conflict",
  ErrorIdempotencyInProgress: "idempotency_in_progress",
  ErrorIdempotencyUncertain: "idempotency_uncertain",
};

const approvedSecretBoundaries = new Map([
  ["POST /provider-accounts/{provider_account_id}/credentials", "#/components/schemas/DirectCredentialSubmissionRequest"],
  ["POST /provider-accounts/{provider_account_id}/reauthentication", "#/components/schemas/DirectReauthenticationRequest"],
]);
const secretKeys = new Set([
  "material",
  "access_token",
  "refresh_token",
  "cookie",
  "cookies",
  "set-cookie",
  "set_cookie",
  "ciphertext",
  "device_code",
  "authorization_code",
  "pkce_verifier",
  "client_secret",
  "password",
  "sso_token",
]);
const secretKeyCanonical = new Set(
  [...secretKeys].map((name) => name.toLowerCase().replace(/[^a-z0-9]/g, "")),
);
const secretValuePatterns = [
  /bearer\s+[a-z0-9._~+\/-]{8,}/i,
  /sk-pxp_[a-z0-9_-]+_[a-z0-9_-]+/i,
  /refresh[_-]?token\s*[:=]/i,
  /access[_-]?token\s*[:=]/i,
  /set-cookie\s*:/i,
];

const fail = (message) => failures.push(message);
const operationKey = (path, method) => `${method.toUpperCase()} ${path}`;

function loadDocument(path) {
  if (!existsSync(path)) throw new Error(`missing artifact: ${path}`);
  try {
    return JSON.parse(readFileSync(path, "utf8"));
  } catch (error) {
    throw new Error(`failed to parse JSON-compatible YAML: ${error.message}`);
  }
}

function resolvePointer(doc, pointer) {
  if (typeof pointer !== "string" || !pointer.startsWith("#/")) {
    throw new Error(`external or unsupported $ref: ${JSON.stringify(pointer)}`);
  }
  let node = doc;
  for (const rawPart of pointer.slice(2).split("/")) {
    const part = rawPart.replace(/~1/g, "/").replace(/~0/g, "~");
    if (!node || typeof node !== "object" || !(part in node)) {
      throw new Error(`unresolvable $ref: ${pointer}`);
    }
    node = node[part];
  }
  return node;
}

function resolveObject(node, doc, seen = new Set()) {
  if (!node || typeof node !== "object" || Array.isArray(node)) return node;
  if (typeof node.$ref !== "string") return node;
  if (seen.has(node.$ref)) throw new Error(`cyclic $ref: ${node.$ref}`);
  const nextSeen = new Set(seen);
  nextSeen.add(node.$ref);
  const resolved = resolveObject(resolvePointer(doc, node.$ref), doc, nextSeen);
  const siblings = Object.fromEntries(Object.entries(node).filter(([key]) => key !== "$ref"));
  return { ...resolved, ...siblings };
}

function walkRefs(node, path = "$", stack = new Set()) {
  if (Array.isArray(node)) {
    node.forEach((item, index) => walkRefs(item, `${path}[${index}]`, stack));
    return;
  }
  if (!node || typeof node !== "object") return;

  if ("$ref" in node) {
    const ref = node.$ref;
    try {
      const target = resolvePointer(docCache, ref);
      if (stack.has(ref)) {
        fail(`${path}: cyclic $ref ${ref}`);
      } else {
        const nextStack = new Set(stack);
        nextStack.add(ref);
        walkRefs(target, `${path}->${ref}`, nextStack);
      }
    } catch (error) {
      fail(`${path}: ${error.message}`);
    }
  }

  for (const [key, value] of Object.entries(node)) {
    if (key !== "$ref") walkRefs(value, `${path}.${key}`, stack);
  }
}

function isSecretName(value) {
  return typeof value === "string"
    && secretKeyCanonical.has(value.toLowerCase().replace(/[^a-z0-9]/g, ""));
}

function valueContainsSecretMaterial(value) {
  if (Array.isArray(value)) return value.some(valueContainsSecretMaterial);
  if (value && typeof value === "object") {
    return Object.entries(value).some(
      ([key, nested]) => isSecretName(key) || valueContainsSecretMaterial(nested),
    );
  }
  return typeof value === "string"
    && secretValuePatterns.some((pattern) => pattern.test(value));
}

function schemaContainsSecretMaterial(node, seenRefs = new Set()) {
  if (Array.isArray(node)) return node.some((item) => schemaContainsSecretMaterial(item, seenRefs));
  if (!node || typeof node !== "object") return false;

  if (typeof node.$ref === "string" && node.$ref.startsWith("#/") && !seenRefs.has(node.$ref)) {
    const nextSeen = new Set(seenRefs);
    nextSeen.add(node.$ref);
    try {
      if (schemaContainsSecretMaterial(resolvePointer(docCache, node.$ref), nextSeen)) return true;
    } catch {
      return false;
    }
  }

  if (Object.keys(node.properties || {}).some(isSecretName)) return true;
  if ((node.required || []).some(isSecretName)) return true;
  return Object.entries(node).some(
    ([key, value]) => key !== "$ref" && schemaContainsSecretMaterial(value, seenRefs),
  );
}

function schemaContainsTenantId(node, seenRefs = new Set()) {
  if (Array.isArray(node)) return node.some((item) => schemaContainsTenantId(item, seenRefs));
  if (!node || typeof node !== "object") return false;

  if (typeof node.$ref === "string" && node.$ref.startsWith("#/") && !seenRefs.has(node.$ref)) {
    const nextSeen = new Set(seenRefs);
    nextSeen.add(node.$ref);
    try {
      if (schemaContainsTenantId(resolvePointer(docCache, node.$ref), nextSeen)) return true;
    } catch {
      return false;
    }
  }

  if (Object.hasOwn(node.properties || {}, "tenant_id")) return true;
  if ((node.required || []).includes("tenant_id")) return true;
  return Object.entries(node).some(
    ([key, value]) => key !== "$ref" && schemaContainsTenantId(value, seenRefs),
  );
}

function operationUsesClientApiKey(operation, doc) {
  const requirements = operation.security ?? doc.security;
  return Array.isArray(requirements)
    && requirements.length > 0
    && requirements.every((requirement) => (
      requirement
      && typeof requirement === "object"
      && Object.keys(requirement).length === 1
      && Array.isArray(requirement.ClientApiKey)
      && requirement.ClientApiKey.length === 0
    ));
}

function getRequestBody(operation) {
  try {
    return resolveObject(operation.requestBody, docCache);
  } catch {
    return null;
  }
}

function getParameters(pathItem, operation) {
  return [
    ...(Array.isArray(pathItem.parameters) ? pathItem.parameters : []),
    ...(Array.isArray(operation.parameters) ? operation.parameters : []),
  ];
}

function findIdempotencyParameters(pathItem, operation) {
  return getParameters(pathItem, operation).filter((rawParameter) => {
    try {
      return resolveObject(rawParameter, docCache)?.name?.toLowerCase() === "idempotency-key";
    } catch {
      return false;
    }
  });
}

function validateIdempotencyParameter(path, method, pathItem, operation, expectedRef, required) {
  const label = operationKey(path, method);
  const matches = findIdempotencyParameters(pathItem, operation);
  if (matches.length !== 1) {
    fail(`${label} must ${required ? "require" : "keep"} Idempotency-Key${required ? "" : " optional"}`);
    return;
  }
  const raw = matches[0];
  let parameter;
  try {
    parameter = resolveObject(raw, docCache);
  } catch (error) {
    fail(`${label} has invalid Idempotency-Key parameter: ${error.message}`);
    return;
  }
  if (raw.$ref !== expectedRef || parameter.required !== required) {
    fail(`${label} must ${required ? "require" : "keep"} Idempotency-Key${required ? "" : " optional"}`);
  }
  if (parameter.in !== "header" || parameter.schema?.type !== "string" || parameter.schema?.minLength !== 1) {
    fail(`${label} Idempotency-Key must be a non-empty string header`);
  }
}

function validateIdempotencyConflictResponse(path, method, operation) {
  const label = operationKey(path, method);
  const response = operation.responses?.["409"];
  const examples = response?.content?.["application/json"]?.examples || {};
  for (const name of ["ErrorIdempotencyConflict", "ErrorIdempotencyInProgress", "ErrorIdempotencyUncertain"]) {
    if (examples[name]?.$ref !== `#/components/examples/${name}`) {
      fail(`${label} 409 response must expose ${name}`);
    }
  }
}

function validateOperationSecurityAndInput(path, method, pathItem, operation) {
  const label = operationKey(path, method);
  if (!operationUsesClientApiKey(operation, docCache)) {
    fail(`${label} must require ClientApiKey security`);
  }
  if (path.includes("{tenant_id}")) fail(`${label} must not accept tenant_id in the path`);

  for (const rawParameter of getParameters(pathItem, operation)) {
    let parameter;
    try {
      parameter = resolveObject(rawParameter, docCache);
    } catch {
      continue;
    }
    if (parameter?.name === "tenant_id" || schemaContainsTenantId(parameter?.schema)) {
      fail(`${label} must not accept tenant_id parameters`);
    }
    if (isSecretName(parameter?.name) || schemaContainsSecretMaterial(parameter?.schema)) {
      fail(`${label} must not accept secret material in parameters`);
    }
  }

  const requestBody = getRequestBody(operation);
  for (const mediaType of Object.values(requestBody?.content || {})) {
    if (schemaContainsTenantId(mediaType?.schema)) fail(`${label} request body must not accept tenant_id`);
  }
}

function validateSecretBoundaries(path, method, operation) {
  const label = operationKey(path, method);
  const requestBody = getRequestBody(operation);
  const mediaTypes = Object.values(requestBody?.content || {});
  const secretSchemas = mediaTypes.filter((mediaType) => schemaContainsSecretMaterial(mediaType?.schema));
  const approvedRef = approvedSecretBoundaries.get(label);

  if (secretSchemas.length > 0 && !approvedRef) {
    fail(`${label}: secret-bearing request body is outside an approved direct ingress boundary`);
  }
  if (approvedRef) {
    if (secretSchemas.length !== 1 || secretSchemas[0]?.schema?.$ref !== approvedRef) {
      fail(`${label}: approved secret boundary must use ${approvedRef}`);
    }
  }

  for (const [status, response] of Object.entries(operation.responses || {})) {
    let resolvedResponse;
    try {
      resolvedResponse = resolveObject(response, docCache);
    } catch {
      continue;
    }
    for (const mediaType of Object.values(resolvedResponse?.content || {})) {
      if (schemaContainsSecretMaterial(mediaType?.schema)) {
        fail(`${label} ${status}: response schema must not expose secret material`);
      }
      if (valueContainsSecretMaterial(mediaType?.example) || valueContainsSecretMaterial(mediaType?.examples)) {
        fail(`${label} ${status}: response examples must not expose secret material`);
      }
    }
  }
}

function collectExamples(doc) {
  const items = [];
  const seenSchemaNodes = new Set();

  function collectSchemaExamples(schema, label) {
    if (!schema || typeof schema !== "object" || seenSchemaNodes.has(schema)) return;
    seenSchemaNodes.add(schema);
    if (Array.isArray(schema.examples)) {
      schema.examples.forEach((value, index) => items.push({
        label: `${label}.examples[${index}]`,
        schema,
        value,
      }));
    }
    for (const [key, value] of Object.entries(schema)) {
      if (key !== "examples") collectSchemaExamples(value, `${label}.${key}`);
    }
  }

  for (const [name, schema] of Object.entries(doc.components?.schemas || {})) {
    collectSchemaExamples(schema, `components.schemas.${name}`);
  }

  const canonicalError = { $ref: "#/components/schemas/CanonicalError" };
  for (const [name, rawExample] of Object.entries(doc.components?.examples || {})) {
    let example;
    try {
      example = resolveObject(rawExample, doc);
    } catch {
      continue;
    }
    if (Object.hasOwn(example || {}, "value")) {
      items.push({ label: `components.examples.${name}`, schema: canonicalError, value: example.value });
    }
  }

  function collectMediaExamples(mediaType, label) {
    const schema = mediaType?.schema;
    if (!schema || typeof schema !== "object") return;
    if (Object.hasOwn(mediaType, "example")) {
      items.push({ label: `${label}.example`, schema, value: mediaType.example });
    }
    for (const [name, rawExample] of Object.entries(mediaType.examples || {})) {
      let example;
      try {
        example = resolveObject(rawExample, doc);
      } catch {
        continue;
      }
      if (Object.hasOwn(example || {}, "value")) {
        items.push({ label: `${label}.examples.${name}`, schema, value: example.value });
      }
    }
  }

  for (const [path, pathItem] of Object.entries(doc.paths || {})) {
    for (const [method, operation] of Object.entries(pathItem || {})) {
      if (!httpMethods.has(method) || !operation) continue;
      let requestBody;
      try {
        requestBody = resolveObject(operation.requestBody, doc);
      } catch (error) {
        fail(`${operationKey(path, method)} request: ${error.message}`);
      }
      for (const [mediaTypeName, mediaType] of Object.entries(requestBody?.content || {})) {
        collectMediaExamples(mediaType, `${operationKey(path, method)} request ${mediaTypeName}`);
      }
      for (const [status, rawResponse] of Object.entries(operation.responses || {})) {
        let response;
        try {
          response = resolveObject(rawResponse, doc);
        } catch (error) {
          fail(`${operationKey(path, method)} response ${status}: ${error.message}`);
          continue;
        }
        for (const [mediaTypeName, mediaType] of Object.entries(response?.content || {})) {
          collectMediaExamples(mediaType, `${operationKey(path, method)} response ${status} ${mediaTypeName}`);
        }
      }
    }
  }

  return items;
}

function validateExamplesWithPython(doc, items) {
  const python = String.raw`
import json, sys
from jsonschema import Draft202012Validator

def resolve_pointer(doc, pointer):
    if not pointer.startswith("#/"):
        raise ValueError(f"external ref: {pointer}")
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
            if ref in seen:
                raise ValueError(f"cyclic ref: {ref}")
            resolved = expand(resolve_pointer(doc, ref), doc, seen | {ref})
            siblings = {k: v for k, v in node.items() if k != "$ref"}
            return resolved if not siblings else {"allOf": [resolved, expand(siblings, doc, seen)]}
        return {k: expand(v, doc, seen) for k, v in node.items()}
    if isinstance(node, list):
        return [expand(value, doc, seen) for value in node]
    return node

payload = json.load(sys.stdin)
failures = []
validated = 0
for item in payload["items"]:
    try:
        Draft202012Validator(expand(item["schema"], payload["doc"])).validate(item["value"])
        validated += 1
    except Exception as exc:
        failures.append(f"{item['label']}: example failed Draft 2020-12 validation: {exc}")
print(json.dumps({"validated": validated, "failures": failures}))
`;

  const result = spawnSync("python", ["-c", python], {
    cwd: root,
    input: JSON.stringify({ doc, items }),
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
  });
  if (result.error) {
    fail(`python jsonschema unavailable: ${result.error.message}`);
    return 0;
  }
  if (result.status !== 0) {
    const message = (result.stderr || result.stdout || "").trim();
    fail(`python jsonschema helper failed: ${message || `exit ${result.status}`}`);
    return 0;
  }
  try {
    const parsed = JSON.parse(result.stdout.trim().split("\n").filter(Boolean).pop());
    for (const message of parsed.failures || []) fail(message);
    return parsed.validated || 0;
  } catch (error) {
    fail(`cannot parse python jsonschema output: ${error.message}`);
    return 0;
  }
}

function validateLifecyclePolicy(doc) {
  const lifecycle = doc["x-pixelplus-api-lifecycle"];
  if (!lifecycle || typeof lifecycle !== "object") {
    fail("missing x-pixelplus-api-lifecycle policy");
    return;
  }
  if (lifecycle.public_api_major !== "v1" || lifecycle.semantic_version !== "1.0.0") {
    fail("API lifecycle must bind semantic version 1.0.0 to URL major v1");
  }
  const versioning = lifecycle.versioning || {};
  if (
    versioning.policy !== "semantic_versioning_2.0.0"
    || versioning.major_in_url !== true
    || versioning.backward_compatible_within_major !== true
    || versioning.breaking_change_requires_new_major !== true
  ) {
    fail("versioning policy must require backward compatibility within v1 and a new major for breaking changes");
  }
  const incompatible = new Set(versioning.incompatible_changes || []);
  for (const rule of [
    "change_authentication_or_scope_requirement",
    "add_value_to_closed_enum",
    "make_optional_idempotency_key_required",
    "make_required_idempotency_key_optional",
  ]) {
    if (!incompatible.has(rule)) fail(`versioning policy must classify ${rule} as incompatible`);
  }

  const expectedExtensionPoints = [
    "CanonicalError.code",
    "CanonicalError.operation",
    "CanonicalError.retry_after_class",
    "CanonicalError.safe_context",
  ];
  if (
    JSON.stringify([...(versioning.declared_response_extension_points || [])].sort())
    !== JSON.stringify([...expectedExtensionPoints].sort())
  ) {
    fail("declared response extension points must match the stable schemas");
  }

  const deprecation = lifecycle.deprecation || {};
  if (!Number.isInteger(deprecation.minimum_notice_days) || deprecation.minimum_notice_days < 180) {
    fail("minimum deprecation notice must be at least 180 days");
  }
  if (
    deprecation.successor_general_availability_required !== true
    || deprecation.removal_requires_new_major !== true
    || deprecation.deprecation_does_not_change_behavior !== true
    || deprecation.sunset_not_before_deprecation !== true
  ) {
    fail("deprecation must preserve behavior, require a generally available successor, and remove only in a later major");
  }
  if (
    deprecation.notice_headers?.deprecation !== "RFC 9745 Structured Field Date"
    || deprecation.notice_headers?.sunset !== "RFC 8594 HTTP-date"
    || deprecation.notice_headers?.migration_link_relation !== "deprecation"
  ) {
    fail("deprecation policy must use RFC 9745 Deprecation, RFC 8594 Sunset, and rel=deprecation Link");
  }
}

function expectedOperationIds(labels) {
  return [...labels].map((label) => {
    const [method, ...pathParts] = label.split(" ");
    return docCache.paths?.[pathParts.join(" ")]?.[method.toLowerCase()]?.operationId;
  });
}

function validateOperationClass(operationClasses, name, expectedHeader, expectedOperationIds) {
  const operationClass = operationClasses?.[name];
  const actualOperationIds = operationClass?.operations;
  if (
    operationClass?.header !== expectedHeader
    || !Array.isArray(actualOperationIds)
    || JSON.stringify([...actualOperationIds].sort()) !== JSON.stringify([...expectedOperationIds].sort())
  ) {
    fail(`${name} idempotency class must match the stable operation matrix`);
  }
}

function validateIdempotencyPolicy(doc) {
  const policy = doc["x-pixelplus-idempotency-policy"];
  if (!policy || typeof policy !== "object") {
    fail("missing x-pixelplus-idempotency-policy");
    return;
  }
  if (policy.retention_hours !== 24 || policy.expired_record_reuse !== "new_request") {
    fail("idempotency records must retain replay identity for 24 hours and treat post-expiry reuse as a new request");
  }
  if (JSON.stringify(policy.scope) !== JSON.stringify(["authenticated_tenant", "client_api_key", "key"])) {
    fail("idempotency scope must be authenticated Tenant + Client API Key + key");
  }
  if (!(policy.request_fingerprint || []).includes("operation_identity")) {
    fail("idempotency fingerprint must include operation identity");
  }
  if (policy.cross_operation_key_reuse !== "idempotency_conflict") {
    fail("cross-operation key reuse must produce idempotency_conflict");
  }
  if (policy.matching_replay !== "return_original_operation_without_new_side_effect") {
    fail("matching replay must return the original operation without a new side effect");
  }
  if (policy.uncertain_owner !== "no_claim_stealing_or_reexecution") {
    fail("uncertain idempotency ownership must forbid claim stealing and re-execution");
  }
  if (policy.secret_fingerprint_storage !== "non_reversible_keyed_digest") {
    fail("secret fingerprints must use non_reversible_keyed_digest");
  }
  if (policy.raw_secret_storage !== "forbidden") {
    fail("idempotency records must forbid raw secret storage");
  }
  if (
    policy.retry_owners?.chat !== "chat_execution_layer"
    || policy.retry_owners?.render_job !== "render_job_execution_layer"
    || policy.retry_owners?.committed_or_unknown !== "replacement_execution_forbidden"
  ) {
    fail("idempotency policy must assign one execution retry owner and forbid replacement after committed or unknown");
  }
  const operationClasses = policy.operation_classes || {};
  validateOperationClass(
    operationClasses,
    "chat_execution",
    "optional",
    expectedOperationIds(optionalIdempotencyOperations.keys()),
  );
  validateOperationClass(
    operationClasses,
    "durable_creation",
    "required",
    expectedOperationIds(requiredIdempotencyOperations.keys()),
  );
  validateOperationClass(
    operationClasses,
    "resource_state_commands",
    "not_required",
    expectedOperationIds(resourceStateCommandOperations),
  );
  validateOperationClass(
    operationClasses,
    "resource_retrieval",
    "not_applicable",
    expectedOperationIds(resourceRetrievalOperations),
  );
  validateOperationClass(
    operationClasses,
    "output_retrieval",
    "not_applicable",
    expectedOperationIds(outputRetrievalOperations),
  );
  validateOperationClass(
    operationClasses,
    "output_delivery_retry",
    "not_required",
    expectedOperationIds(outputDeliveryRetryOperations),
  );

  const resourceStateCommands = operationClasses.resource_state_commands;
  if (resourceStateCommands?.replay !== "same_resource_state_transition_must_not_duplicate_external_work") {
    fail("resource-state commands must not duplicate external work");
  }
  const resourceRetrieval = operationClasses.resource_retrieval;
  if (
    resourceRetrieval?.header !== "not_applicable"
    || resourceRetrieval?.replay !== "read_existing_resource_without_provider_or_job_execution"
  ) {
    fail("resource retrieval must read existing state without Provider or job execution");
  }
  const outputRetrieval = operationClasses.output_retrieval;
  if (
    outputRetrieval?.header !== "not_applicable"
    || outputRetrieval?.replay !== "read_existing_resource_without_rendering_or_provider_execution"
  ) {
    fail("output retrieval must read existing resources without rendering or Provider execution");
  }
  const outputRetry = operationClasses.output_delivery_retry;
  if (
    outputRetry?.replay !== "reuse_existing_manifest_and_placement_identity"
    || !(outputRetry?.forbidden || []).includes("new_render_job")
    || !(outputRetry?.forbidden || []).includes("new_provider_execution")
  ) {
    fail("output delivery retry must reuse placement identity without a new Render Job or Provider execution");
  }
}

function validateContractTestingPolicy(doc) {
  const policy = doc["x-pixelplus-contract-testing"];
  if (!policy || typeof policy !== "object") {
    fail("missing x-pixelplus-contract-testing policy");
    return;
  }
  if (policy.entrypoint !== "public_http_surface") {
    fail("contract tests must enter through the public HTTP surface");
  }
  if (policy.composition !== "real_gateway_composition") {
    fail("contract tests must use real_gateway_composition");
  }
  const controlledPorts = new Set(policy.controlled_implementations_at_ports || []);
  for (const port of ["adapter", "credential_vault", "persistence", "job_runtime", "clock", "id_generator"]) {
    if (!controlledPorts.has(port)) fail(`contract tests must allow controlled ${port} implementations at the port`);
  }
  const forbiddenSeams = new Set(policy.forbidden_test_seams || []);
  for (const seam of ["handler_stub", "private_function", "concrete_database_schema", "goroutine_layout"]) {
    if (!forbiddenSeams.has(seam)) fail(`contract tests must forbid the ${seam} seam`);
  }
  if (
    !(policy.ownership_rejection_before || []).includes("vault_decrypt")
    || !(policy.ownership_rejection_before || []).includes("adapter_call")
    || !(policy.ownership_rejection_before || []).includes("job_enqueue")
  ) {
    fail("contract tests must assert ownership rejection before vault decrypt, Adapter call, and job enqueue");
  }
  const requiredObservations = new Set(policy.required_observations || []);
  for (const observation of [
    "http_status_headers_and_body",
    "durable_resource_identity",
    "adapter_call_count",
    "vault_read_write_decrypt_and_revoke_counts",
    "persistence_and_job_side_effect_counts",
    "same_tenant_and_non_enumeration_behavior",
    "idempotency_replay_conflict_and_uncertain_outcomes",
  ]) {
    if (!requiredObservations.has(observation)) {
      fail(`contract tests must observe ${observation}`);
    }
  }
  if (policy.concrete_interface_and_package_layout !== "deferred_to_issue_21") {
    fail("concrete test ports and package layout must remain issue #21 scope");
  }
}

function main() {
  const requestedPath = process.argv[2] || "contracts/openapi/pixelplus-public-api-v1.yaml";
  const path = resolve(root, requestedPath);
  let doc;
  try {
    doc = loadDocument(path);
    docCache = doc;
  } catch (error) {
    console.error(`FAIL: ${error.message}`);
    process.exit(1);
  }

  if (doc.openapi !== "3.1.1") fail(`openapi must be 3.1.1, got ${JSON.stringify(doc.openapi)}`);
  if (doc.jsonSchemaDialect !== "https://json-schema.org/draft/2020-12/schema") {
    fail("jsonSchemaDialect must be JSON Schema Draft 2020-12");
  }
  if (doc.info?.version !== "1.0.0") fail(`info.version must be 1.0.0, got ${JSON.stringify(doc.info?.version)}`);
  if (doc["x-pixelplus-artifact-status"] !== "stable") {
    fail("x-pixelplus-artifact-status must be stable");
  }
  if (doc.servers?.length !== 1 || doc.servers[0]?.url !== "/v1") {
    fail("stable Public API must use the single /v1 server base");
  }
  if (
    doc.components?.securitySchemes?.ClientApiKey?.type !== "http"
    || doc.components.securitySchemes.ClientApiKey.scheme !== "bearer"
  ) {
    fail("ClientApiKey must be an HTTP bearer security scheme");
  }

  validateLifecyclePolicy(doc);
  validateIdempotencyPolicy(doc);
  validateContractTestingPolicy(doc);
  walkRefs(doc);

  const seenOperationIds = new Set();
  let operationCount = 0;
  for (const [pathKey, pathItem] of Object.entries(doc.paths || {})) {
    for (const [method, operation] of Object.entries(pathItem || {})) {
      if (!httpMethods.has(method) || !operation) continue;
      operationCount += 1;
      validateOperationSecurityAndInput(pathKey, method, pathItem, operation);
      validateSecretBoundaries(pathKey, method, operation);
      if (!operation.operationId) {
        fail(`${operationKey(pathKey, method)} must declare operationId`);
      } else if (seenOperationIds.has(operation.operationId)) {
        fail(`duplicate operationId ${operation.operationId}`);
      } else {
        seenOperationIds.add(operation.operationId);
      }

      const label = operationKey(pathKey, method);
      const idempotencyParameters = findIdempotencyParameters(pathItem, operation);
      if (optionalIdempotencyOperations.has(label)) {
        validateIdempotencyParameter(pathKey, method, pathItem, operation, optionalIdempotencyOperations.get(label), false);
        validateIdempotencyConflictResponse(pathKey, method, operation);
      } else if (requiredIdempotencyOperations.has(label)) {
        validateIdempotencyParameter(pathKey, method, pathItem, operation, requiredIdempotencyOperations.get(label), true);
        validateIdempotencyConflictResponse(pathKey, method, operation);
      } else if (idempotencyParameters.length > 0) {
        fail(`${label} must not use Idempotency-Key`);
      }
    }
  }

  for (const [pathKey, method, operationId] of requiredOperations) {
    const operation = doc.paths?.[pathKey]?.[method];
    if (!operation) {
      fail(`missing required operation ${operationKey(pathKey, method)}`);
    } else if (operation.operationId !== operationId) {
      fail(`${operationKey(pathKey, method)} operationId must be ${operationId}`);
    }
  }

  const canonicalError = doc.components?.schemas?.CanonicalError;
  const remediation = doc.components?.schemas?.Remediation;
  if (!canonicalError || canonicalError.properties?.remediation?.$ref !== "#/components/schemas/Remediation") {
    fail("CanonicalError must use the shared Remediation schema");
  }
  for (const category of ["authentication", "authorization", "admission", "validation", "routing", "capability", "credential", "execution", "delivery", "dependency", "internal"]) {
    if (!canonicalError?.properties?.category?.enum?.includes(category)) {
      fail(`CanonicalError category must include ${category}`);
    }
  }
  if (canonicalError?.properties?.operation?.enum || canonicalError?.properties?.operation?.pattern !== "^[a-z][a-z0-9_]*$") {
    fail("CanonicalError.operation must be a declared open token extension point");
  }
  for (const value of ["reduce_payload", "provider_remediation", "asset_lifecycle", "retry_same_idempotency_key", "submit_new_request", "delete_and_recreate"]) {
    if (!remediation?.enum?.includes(value)) fail(`Remediation must include ${value}`);
  }

  for (const [name, code] of Object.entries(requiredErrorExamples)) {
    const example = doc.components?.examples?.[name];
    if (example?.value?.code !== code) fail(`${name} must demonstrate code=${code}`);
  }
  const notFound = doc.components?.examples?.ErrorResourceNotFound?.value;
  if (notFound && ("resource_reference" in notFound || valueContainsSecretMaterial(notFound))) {
    fail("ErrorResourceNotFound must be non-enumerating and secret-free");
  }
  if (valueContainsSecretMaterial(doc.components?.examples || {})) {
    fail("component examples must not contain secret material");
  }

  const examples = collectExamples(doc);
  const validatedExamples = validateExamplesWithPython(doc, examples);

  if (failures.length > 0) {
    console.error(`FAIL: stable Public API contract has ${failures.length} violation(s)`);
    for (const message of failures) console.error(`- ${message}`);
    process.exit(1);
  }

  console.log(
    `PASS: stable Public API contract (${operationCount} operations, ${validatedExamples} Draft 2020-12 examples)`,
  );
}

main();
