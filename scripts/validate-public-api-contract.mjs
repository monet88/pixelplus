#!/usr/bin/env node

import { existsSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const require = createRequire(import.meta.url);
const redoclyConfig = resolve(root, "redocly.yaml");
const baselineRepoPath = "contracts/openapi/baselines/pixelplus-public-api-v1.0.0.yaml";
const baselinePath = resolve(root, baselineRepoPath);
const baselineReleaseTag = "pixelplus-public-api-v1.0.0";
const httpMethods = new Set(["get", "put", "post", "delete", "patch", "options", "head", "trace"]);
const failures = [];
let docCache = null;
let baselineSource = "unavailable";

const OPERATION_DESCRIPTORS = [
  { path: "/models", method: "get", operationId: "listModels", idempotencyClass: "resource_retrieval", idempotencyHeader: "not_applicable", scopes: ["capabilities.read"] },
  { path: "/chat/completions", method: "post", operationId: "createChatCompletion", idempotencyClass: "chat_execution", idempotencyHeader: "optional", headerRef: "#/components/parameters/IdempotencyKey", scopes: ["chat.completions"] },
  { path: "/chat/executions/{execution_id}/cancel", method: "post", operationId: "cancelChatExecution", idempotencyClass: "resource_state_commands", idempotencyHeader: "not_required", scopes: ["chat.completions"] },
  { path: "/assets", method: "post", operationId: "createAsset", idempotencyClass: "durable_creation", idempotencyHeader: "required", headerRef: "#/components/parameters/RequiredIdempotencyKey", scopes: ["assets.write"] },
  { path: "/assets/{asset_id}", method: "get", operationId: "getAsset", idempotencyClass: "output_retrieval", idempotencyHeader: "not_applicable", scopes: ["assets.read"] },
  { path: "/assets/{asset_id}/content", method: "get", operationId: "getAssetContent", idempotencyClass: "output_retrieval", idempotencyHeader: "not_applicable", scopes: ["assets.read"] },
  { path: "/images/generations", method: "post", operationId: "createImageGeneration", idempotencyClass: "durable_creation", idempotencyHeader: "required", headerRef: "#/components/parameters/RequiredIdempotencyKey", scopes: ["images.generate"] },
  { path: "/images/edits", method: "post", operationId: "createImageEdit", idempotencyClass: "durable_creation", idempotencyHeader: "required", headerRef: "#/components/parameters/RequiredIdempotencyKey", scopes: ["images.edit"] },
  { path: "/images/inpaints", method: "post", operationId: "createImageInpaint", idempotencyClass: "durable_creation", idempotencyHeader: "required", headerRef: "#/components/parameters/RequiredIdempotencyKey", scopes: ["images.edit"] },
  { path: "/render-jobs/{job_id}", method: "get", operationId: "getRenderJob", idempotencyClass: "output_retrieval", idempotencyHeader: "not_applicable", scopes: ["jobs.read"] },
  { path: "/render-jobs/{job_id}/cancel", method: "post", operationId: "cancelRenderJob", idempotencyClass: "resource_state_commands", idempotencyHeader: "not_required", scopes: ["jobs.manage"] },
  { path: "/render-jobs/{job_id}/outputs/{output_entry_id}/retry", method: "post", operationId: "retryRenderJobOutput", idempotencyClass: "output_delivery_retry", idempotencyHeader: "not_required", scopes: ["jobs.manage"] },
  { path: "/provider-accounts", method: "post", operationId: "createProviderAccount", idempotencyClass: "durable_creation", idempotencyHeader: "required", headerRef: "#/components/parameters/RequiredIdempotencyKey", scopes: ["accounts.manage"] },
  { path: "/provider-accounts", method: "get", operationId: "listProviderAccounts", idempotencyClass: "resource_retrieval", idempotencyHeader: "not_applicable", scopes: ["accounts.read"] },
  { path: "/provider-accounts/{provider_account_id}", method: "get", operationId: "getProviderAccount", idempotencyClass: "resource_retrieval", idempotencyHeader: "not_applicable", scopes: ["accounts.read"] },
  { path: "/provider-accounts/{provider_account_id}", method: "delete", operationId: "deleteProviderAccount", idempotencyClass: "resource_state_commands", idempotencyHeader: "not_required", scopes: ["accounts.manage"] },
  { path: "/provider-accounts/{provider_account_id}/credentials", method: "post", operationId: "submitProviderCredential", idempotencyClass: "durable_creation", idempotencyHeader: "required", headerRef: "#/components/parameters/RequiredIdempotencyKey", scopes: ["accounts.manage"], secretSchemaRef: "#/components/schemas/DirectCredentialSubmissionRequest" },
  { path: "/provider-accounts/{provider_account_id}/oauth-authorizations", method: "post", operationId: "startOAuthAuthorization", idempotencyClass: "durable_creation", idempotencyHeader: "required", headerRef: "#/components/parameters/RequiredIdempotencyKey", scopes: ["accounts.manage"] },
  { path: "/provider-accounts/{provider_account_id}/oauth-authorizations/{authorization_id}", method: "get", operationId: "getOAuthAuthorization", idempotencyClass: "resource_retrieval", idempotencyHeader: "not_applicable", scopes: ["accounts.manage"] },
  { path: "/provider-accounts/{provider_account_id}/probe", method: "post", operationId: "probeProviderAccount", idempotencyClass: "resource_state_commands", idempotencyHeader: "not_required", scopes: ["accounts.manage"] },
  { path: "/provider-accounts/{provider_account_id}/reauthentication", method: "post", operationId: "reauthenticateProviderAccount", idempotencyClass: "durable_creation", idempotencyHeader: "required", headerRef: "#/components/parameters/RequiredIdempotencyKey", scopes: ["accounts.manage"], secretSchemaRef: "#/components/schemas/DirectReauthenticationRequest" },
  { path: "/provider-accounts/{provider_account_id}/disable", method: "post", operationId: "disableProviderAccount", idempotencyClass: "resource_state_commands", idempotencyHeader: "not_required", scopes: ["accounts.manage"] },
  { path: "/provider-accounts/{provider_account_id}/enable", method: "post", operationId: "enableProviderAccount", idempotencyClass: "resource_state_commands", idempotencyHeader: "not_required", scopes: ["accounts.manage"] },
  { path: "/provider-accounts/{provider_account_id}/capability-snapshot", method: "get", operationId: "getCapabilitySnapshot", idempotencyClass: "resource_retrieval", idempotencyHeader: "not_applicable", scopeAnyOf: ["accounts.read", "capabilities.read"] },
  { path: "/routing-policy", method: "get", operationId: "getRoutingPolicy", idempotencyClass: "resource_retrieval", idempotencyHeader: "not_applicable", scopes: ["routing.read"] },
  { path: "/routing-policy", method: "put", operationId: "replaceRoutingPolicy", idempotencyClass: "resource_state_commands", idempotencyHeader: "not_required", scopes: ["routing.manage"] },
];

const IDEMPOTENCY_CLASS_RULES = {
  chat_execution: { header: "optional" },
  durable_creation: { header: "required" },
  resource_state_commands: {
    header: "not_required",
    replay: "same_resource_state_transition_must_not_duplicate_external_work",
  },
  resource_retrieval: {
    header: "not_applicable",
    replay: "read_existing_resource_without_provider_or_job_execution",
  },
  output_retrieval: {
    header: "not_applicable",
    replay: "read_existing_resource_without_rendering_or_provider_execution",
  },
  output_delivery_retry: {
    header: "not_required",
    replay: "reuse_existing_manifest_and_placement_identity",
    forbidden: ["new_render_job", "new_provider_execution"],
  },
};

const descriptorByLabel = new Map(
  OPERATION_DESCRIPTORS.map((descriptor) => [`${descriptor.method.toUpperCase()} ${descriptor.path}`, descriptor]),
);
const approvedSecretBoundaries = new Map(
  OPERATION_DESCRIPTORS.filter((descriptor) => descriptor.secretSchemaRef).map(
    (descriptor) => [`${descriptor.method.toUpperCase()} ${descriptor.path}`, descriptor.secretSchemaRef],
  ),
);

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
  ErrorRequestTooLarge: "request_too_large",
  ErrorInvalidRequest: "invalid_request",
  ErrorAuthModeUnavailable: "auth_mode_unavailable",
  ErrorIdempotencyConflict: "idempotency_conflict",
  ErrorIdempotencyInProgress: "idempotency_in_progress",
  ErrorIdempotencyUncertain: "idempotency_uncertain",
};

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

function exactSet(actual, expected) {
  if (!Array.isArray(actual) || !Array.isArray(expected)) return false;
  const actualSet = new Set(actual);
  const expectedSet = new Set(expected);
  return actualSet.size === actual.length
    && expectedSet.size === expected.length
    && actualSet.size === expectedSet.size
    && [...expectedSet].every((value) => actualSet.has(value));
}

function parseStableSemVer(value) {
  if (typeof value !== "string") return null;
  const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.exec(value);
  if (!match) return null;
  return {
    major: Number(match[1]),
    minor: Number(match[2]),
    patch: Number(match[3]),
    prerelease: match[4] || null,
  };
}

function validateDescriptorIntegrity() {
  const labels = new Set();
  const operationIds = new Set();
  for (const descriptor of OPERATION_DESCRIPTORS) {
    const label = operationKey(descriptor.path, descriptor.method);
    if (labels.has(label)) fail(`duplicate operation descriptor ${label}`);
    if (operationIds.has(descriptor.operationId)) fail(`duplicate operation descriptor id ${descriptor.operationId}`);
    if (!IDEMPOTENCY_CLASS_RULES[descriptor.idempotencyClass]) {
      fail(`${descriptor.operationId} must declare a known idempotency class`);
    } else if (descriptor.idempotencyHeader !== IDEMPOTENCY_CLASS_RULES[descriptor.idempotencyClass].header) {
      fail(`${descriptor.operationId} idempotency header must match its class rule`);
    }
    labels.add(label);
    operationIds.add(descriptor.operationId);
  }
}

function validateOpenApiStructure(path) {
  let redoclyCli;
  try {
    redoclyCli = require.resolve("@redocly/cli/bin/cli.js");
  } catch (error) {
    fail(`OpenAPI structural validation failed: @redocly/cli is unavailable: ${error.message}`);
    return;
  }
  const result = spawnSync(process.execPath, [redoclyCli, "lint", path, "--config", redoclyConfig], {
    cwd: root,
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
  });
  if (result.error) {
    fail(`OpenAPI structural validation failed: ${result.error.message}`);
  } else if (result.status !== 0) {
    const detail = `${result.stdout || ""}\n${result.stderr || ""}`.trim().replace(/\x1b\[[0-9;]*m/g, "");
    fail(`OpenAPI structural validation failed${detail ? `: ${detail}` : ""}`);
  }
}

function loadDocument(path) {
  if (!existsSync(path)) throw new Error(`missing artifact: ${path}`);
  try {
    return JSON.parse(readFileSync(path, "utf8"));
  } catch (error) {
    throw new Error(`failed to parse JSON-compatible YAML: ${error.message}`);
  }
}

function gitShowBlob(ref, repoPath) {
  const result = spawnSync("git", ["show", `${ref}:${repoPath}`], {
    cwd: root,
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
  });
  if (result.error || result.status !== 0) {
    const detail = (result.stderr || result.stdout || result.error?.message || "").trim();
    throw new Error(`stable baseline unavailable from ${ref}: ${detail || `git show exited ${result.status}`}`);
  }
  return result.stdout;
}

function releaseTagExists(tag) {
  const result = spawnSync("git", ["rev-parse", "-q", "--verify", `refs/tags/${tag}`], {
    cwd: root,
    encoding: "utf8",
  });
  return !result.error && result.status === 0;
}

function parseBaselineDocument(content) {
  try {
    return JSON.parse(content);
  } catch (error) {
    throw new Error(`failed to parse JSON-compatible YAML: ${error.message}`);
  }
}

function loadBaselineDocument() {
  const refOverride = process.env.PIXELPLUS_PUBLIC_API_BASELINE_REF;
  const pathOverride = process.env.PIXELPLUS_PUBLIC_API_BASELINE;
  if (refOverride) {
    if (!/^[0-9a-f]{40}$/i.test(refOverride)) {
      throw new Error("PIXELPLUS_PUBLIC_API_BASELINE_REF must be a full immutable commit SHA");
    }
    if (pathOverride) {
      throw new Error("PIXELPLUS_PUBLIC_API_BASELINE cannot override PIXELPLUS_PUBLIC_API_BASELINE_REF");
    }
    baselineSource = `ref:${refOverride}`;
    return parseBaselineDocument(gitShowBlob(refOverride, baselineRepoPath));
  }

  if (pathOverride) {
    if (process.env.PIXELPLUS_PUBLIC_API_ALLOW_TEST_BASELINE !== "1") {
      throw new Error("PIXELPLUS_PUBLIC_API_BASELINE is restricted to isolated tests");
    }
    const path = resolve(root, pathOverride);
    baselineSource = `file:${path}`;
    return loadDocument(path);
  }

  if (releaseTagExists(baselineReleaseTag)) {
    const taggedContent = gitShowBlob(baselineReleaseTag, baselineRepoPath);
    if (
      existsSync(baselinePath)
      && stableJson(parseBaselineDocument(readFileSync(baselinePath, "utf8")))
        !== stableJson(parseBaselineDocument(taggedContent))
    ) {
      throw new Error(
        `stable baseline file diverges from release tag ${baselineReleaseTag}; post-release baseline edits are forbidden within v1`,
      );
    }
    baselineSource = `tag:${baselineReleaseTag}`;
    return parseBaselineDocument(taggedContent);
  }

  if (process.env.CI) {
    throw new Error("CI validation requires PIXELPLUS_PUBLIC_API_BASELINE_REF to be the pull-request base SHA");
  }

  baselineSource = "worktree:pre-release";
  return loadDocument(baselinePath);
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

function validateAssetUploadOutcomes(doc) {
  const responses = doc.paths?.["/assets"]?.post?.responses || {};
  const forbidden = responses["403"]?.content?.["application/json"];
  if (
    forbidden?.schema?.$ref !== "#/components/schemas/CanonicalError"
    || forbidden?.examples?.ErrorForbidden?.$ref !== "#/components/examples/ErrorForbidden"
  ) {
    fail("POST /assets must document 403 insufficient assets.write via ErrorForbidden");
  }

  const requestTooLarge = responses["413"]?.content?.["application/json"];
  if (
    requestTooLarge?.schema?.$ref !== "#/components/schemas/CanonicalError"
    || requestTooLarge?.examples?.ErrorRequestTooLarge?.$ref
      !== "#/components/examples/ErrorRequestTooLarge"
  ) {
    fail("POST /assets must document 413 request_too_large for uploads over L-ASSET-UPLOAD-MAX");
  }
}

function validateOperationScopes(path, method, operation, descriptor) {
  const label = operationKey(path, method);
  const hasAllOf = Object.hasOwn(operation, "x-required-scopes");
  const hasAnyOf = Object.hasOwn(operation, "x-required-scope-any-of");
  if (hasAllOf === hasAnyOf) {
    fail(`${label} must declare exactly one authorization scope requirement form`);
    return;
  }
  if (descriptor.scopeAnyOf) {
    if (!hasAnyOf || !exactSet(operation["x-required-scope-any-of"], descriptor.scopeAnyOf)) {
      fail(`${label} authorization scopes must allow any of ${descriptor.scopeAnyOf.join(", ")}`);
    }
  } else if (!hasAllOf || !exactSet(operation["x-required-scopes"], descriptor.scopes)) {
    fail(`${label} authorization scopes must exactly require ${descriptor.scopes.join(", ")}`);
  }
}

function validateOperationSecurityAndInput(path, method, pathItem, operation, descriptor) {
  const label = operationKey(path, method);
  if (!operationUsesClientApiKey(operation, docCache)) {
    fail(`${label} must require ClientApiKey security`);
  }
  if (descriptor) validateOperationScopes(path, method, operation, descriptor);
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
    if (Object.hasOwn(schema, "example")) {
      items.push({ label: `${label}.example`, schema, value: schema.example });
    }
    if (Array.isArray(schema.examples)) {
      schema.examples.forEach((value, index) => items.push({
        label: `${label}.examples[${index}]`,
        schema,
        value,
      }));
    }
    for (const [key, value] of Object.entries(schema)) {
      if (key !== "example" && key !== "examples") collectSchemaExamples(value, `${label}.${key}`);
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
      for (const rawParameter of getParameters(pathItem, operation)) {
        let parameter;
        try {
          parameter = resolveObject(rawParameter, doc);
        } catch {
          continue;
        }
        const label = `${operationKey(path, method)} parameter ${parameterIdentity(parameter)}`;
        if (Object.hasOwn(parameter || {}, "example")) {
          items.push({ label: `${label}.example`, schema: parameter.schema, value: parameter.example });
        }
        for (const [name, rawExample] of Object.entries(parameter?.examples || {})) {
          let example;
          try {
            example = resolveObject(rawExample, doc);
          } catch {
            continue;
          }
          if (Object.hasOwn(example || {}, "value")) {
            items.push({ label: `${label}.examples.${name}`, schema: parameter.schema, value: example.value });
          }
        }
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

function validateExampleSecrets(items) {
  for (const item of items) {
    if (valueContainsSecretMaterial(item.value)) {
      fail(`${item.label}: example must not contain secret material`);
    }
  }
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
  const semanticVersion = parseStableSemVer(doc.info?.version);
  if (!semanticVersion || semanticVersion.prerelease) {
    fail("stable API semantic version must be valid SemVer without a prerelease");
  }
  if (doc.info?.version !== lifecycle.semantic_version) {
    fail("info.version and lifecycle semantic_version must match");
  }
  if (
    semanticVersion
    && (lifecycle.public_api_major !== `v${semanticVersion.major}`
      || doc.servers?.length !== 1
      || doc.servers[0]?.url !== `/v${semanticVersion.major}`)
  ) {
    fail(`semantic major ${semanticVersion.major} must match public API major ${lifecycle.public_api_major} and server ${doc.servers?.[0]?.url}`);
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
  if (!exactSet(versioning.declared_response_extension_points, expectedExtensionPoints)) {
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
    deprecation.migration_instructions_required !== true
    || !exactSet(deprecation.migration_instructions_must_cover, [
      "request",
      "response",
      "error",
      "authorization_scope",
      "idempotency",
    ])
  ) {
    fail("removal requires migration instructions covering the stable compatibility dimensions");
  }
  if (deprecation.parallel_old_and_successor_contract_tests_until_support_window_ends !== true) {
    fail("old and successor contract tests must run in parallel through the support window");
  }
  if (
    deprecation.notice_headers?.deprecation !== "RFC 9745 Structured Field Date"
    || deprecation.notice_headers?.sunset !== "RFC 8594 HTTP-date"
    || deprecation.notice_headers?.migration_link_relation !== "deprecation"
  ) {
    fail("deprecation policy must use RFC 9745 Deprecation, RFC 8594 Sunset, and rel=deprecation Link");
  }
}

function validateOperationClass(operationClasses, name, rule, expectedOperationIds) {
  const operationClass = operationClasses?.[name];
  if (
    operationClass?.header !== rule.header
    || !exactSet(operationClass?.operations, expectedOperationIds)
  ) {
    fail(`${name} idempotency class must match the stable operation matrix`);
  }
  if (rule.replay && operationClass?.replay !== rule.replay) {
    const messages = {
      resource_state_commands: "resource-state commands must not duplicate external work",
      resource_retrieval: "resource retrieval must read existing state without Provider or job execution",
      output_retrieval: "output retrieval must read existing resources without rendering or Provider execution",
      output_delivery_retry: "output delivery retry must reuse placement identity without a new Render Job or Provider execution",
    };
    fail(messages[name]);
  }
  if (rule.forbidden && !exactSet(operationClass?.forbidden, rule.forbidden)) {
    fail("output delivery retry must reuse placement identity without a new Render Job or Provider execution");
  }
}

function validateIdempotencyClassPartition(operationClasses) {
  const membership = new Map();
  for (const operationClass of Object.values(operationClasses || {})) {
    for (const operationId of operationClass?.operations || []) {
      membership.set(operationId, (membership.get(operationId) || 0) + 1);
    }
  }
  for (const descriptor of OPERATION_DESCRIPTORS) {
    if (membership.get(descriptor.operationId) !== 1) {
      fail(`${descriptor.operationId} must belong to exactly one idempotency class`);
    }
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
  if (!exactSet(policy.scope, ["authenticated_tenant", "client_api_key", "key"])) {
    fail("idempotency scope must be authenticated Tenant + Client API Key + key");
  }
  if (!exactSet(policy.request_fingerprint, [
    "operation_identity",
    "normalized_path_and_query_inputs",
    "all_request_inputs_that_can_change_the_side_effect",
  ])) {
    fail("idempotency fingerprint must exactly include operation, normalized path/query, and every side-effect-changing input");
  }
  if (policy.cross_operation_key_reuse !== "idempotency_conflict") {
    fail("cross-operation key reuse must produce idempotency_conflict");
  }
  if (policy.header !== "Idempotency-Key") {
    fail("idempotency policy header must be Idempotency-Key");
  }
  if (policy.standardization_status !== "pixelplus_contract_informed_by_expired_ietf_draft") {
    fail("idempotency standardization status must remain the stable PixelPlus contract value");
  }
  if (policy.matching_replay !== "return_original_operation_without_new_side_effect") {
    fail("matching replay must return the original operation without a new side effect");
  }
  if (policy.in_progress_replay !== "do_not_call_adapter_or_create_another_side_effect") {
    fail("in-progress replay must not call the Adapter or create another side effect");
  }
  if (policy.fingerprint_mismatch !== "idempotency_conflict_without_changing_original_operation") {
    fail("fingerprint mismatch must conflict without changing the original operation");
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
  const retryOwners = policy.retry_owners || {};
  if (
    retryOwners.chat !== "chat_execution_layer"
    || retryOwners.render_job !== "render_job_execution_layer"
    || retryOwners.committed_or_unknown !== "replacement_execution_forbidden"
  ) {
    fail("idempotency policy must assign one execution retry owner and forbid replacement after committed or unknown");
  }
  if (!exactSet(retryOwners.forbidden_full_execution_retry_owners, [
    "http_middleware",
    "adapter",
    "routing",
    "queue",
    "worker_redelivery",
  ])) {
    fail("forbidden full-execution retry owners must match the stable set");
  }
  if (!exactSet(Object.keys(retryOwners), [
    "chat",
    "render_job",
    "committed_or_unknown",
    "forbidden_full_execution_retry_owners",
  ])) {
    fail("idempotency retry owner fields must match the stable policy");
  }
  const operationClasses = policy.operation_classes || {};
  for (const [name, rule] of Object.entries(IDEMPOTENCY_CLASS_RULES)) {
    const operationIds = OPERATION_DESCRIPTORS
      .filter((descriptor) => descriptor.idempotencyClass === name)
      .map((descriptor) => descriptor.operationId);
    validateOperationClass(operationClasses, name, rule, operationIds);
  }
  validateIdempotencyClassPartition(operationClasses);
}

function resolveForComparison(node, doc, seen = new Set()) {
  if (!node || typeof node !== "object" || Array.isArray(node)) return node;
  if (typeof node.$ref !== "string") return node;
  if (seen.has(node.$ref)) return node;
  let resolved;
  try {
    resolved = resolvePointer(doc, node.$ref);
  } catch {
    return node;
  }
  const siblings = Object.fromEntries(Object.entries(node).filter(([key]) => key !== "$ref"));
  return { ...resolveForComparison(resolved, doc, new Set([...seen, node.$ref])), ...siblings };
}

const COMPATIBILITY_KEYWORDS = [
  "type",
  "format",
  "const",
  "pattern",
  "minimum",
  "maximum",
  "exclusiveMinimum",
  "exclusiveMaximum",
  "minLength",
  "maxLength",
  "minItems",
  "maxItems",
  "uniqueItems",
  "minProperties",
  "maxProperties",
  "dependentRequired",
  "additionalProperties",
];

const REQUEST_NARROWING_KEYWORDS = new Set([
  "const",
  "pattern",
  "minimum",
  "maximum",
  "exclusiveMinimum",
  "exclusiveMaximum",
  "minLength",
  "maxLength",
  "minItems",
  "maxItems",
  "uniqueItems",
  "minProperties",
  "maxProperties",
  "dependentRequired",
]);

function stableJson(value) {
  if (Array.isArray(value)) return `[${value.map(stableJson).join(",")}]`;
  if (value && typeof value === "object") {
    return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${stableJson(value[key])}`).join(",")}}`;
  }
  return JSON.stringify(value);
}

function compareSchemaCompatibility(
  currentNode,
  baselineNode,
  currentDoc,
  baselineDoc,
  label,
  direction = "shared",
  seen = { refs: new Set(), inlinePairs: new WeakMap() },
) {
  if (
    currentNode && baselineNode
    && typeof currentNode === "object" && typeof baselineNode === "object"
    && (currentNode.$ref || baselineNode.$ref)
  ) {
    const refPair = `${baselineNode.$ref || "inline"}|${currentNode.$ref || "inline"}`;
    if (seen.refs.has(refPair)) return;
    seen.refs.add(refPair);
  } else if (
    currentNode && baselineNode
    && typeof currentNode === "object" && typeof baselineNode === "object"
  ) {
    let currentNodes = seen.inlinePairs.get(baselineNode);
    if (!currentNodes) {
      currentNodes = new WeakSet();
      seen.inlinePairs.set(baselineNode, currentNodes);
    }
    if (currentNodes.has(currentNode)) return;
    currentNodes.add(currentNode);
  }

  const current = resolveForComparison(currentNode, currentDoc);
  const baseline = resolveForComparison(baselineNode, baselineDoc);
  if (!current || !baseline || typeof current !== "object" || typeof baseline !== "object") return;

  for (const keyword of COMPATIBILITY_KEYWORDS) {
    if (!Object.hasOwn(baseline, keyword)) {
      if (
        REQUEST_NARROWING_KEYWORDS.has(keyword)
        && Object.hasOwn(current, keyword)
        && (direction === "request" || direction === "response")
      ) {
        fail(
          `baseline compatibility: ${label} cannot add ${direction}-narrowing ${keyword}`,
        );
      }
      continue;
    }
    if (!Object.hasOwn(current, keyword) || stableJson(current[keyword]) !== stableJson(baseline[keyword])) {
      fail(`baseline compatibility: ${label} ${keyword} cannot change`);
    }
  }
  if (!Array.isArray(baseline.enum) && Array.isArray(current.enum)) {
    fail(`baseline compatibility: ${label} cannot add a closed enum`);
  }
  if (Array.isArray(baseline.required) && !exactSet(current.required || [], baseline.required)) {
    const missing = baseline.required.find((property) => !(current.required || []).includes(property));
    if (missing) {
      fail(`baseline compatibility: ${label} required property ${missing} cannot be removed`);
    } else if (direction === "request") {
      const added = (current.required || []).find((property) => !baseline.required.includes(property));
      fail(`baseline compatibility: ${label} cannot add required property ${added}`);
    } else {
      fail(`baseline compatibility: ${label} required properties cannot change`);
    }
  } else if (direction === "request" && !Array.isArray(baseline.required) && Array.isArray(current.required)) {
    for (const property of current.required) {
      fail(`baseline compatibility: ${label} cannot add required property ${property}`);
    }
  }
  if (Array.isArray(baseline.enum) && !exactSet(current.enum, baseline.enum)) {
    fail(`baseline compatibility: ${label} closed enum must remain unchanged`);
  }

  for (const [property, baselineProperty] of Object.entries(baseline.properties || {})) {
    const currentProperty = current.properties?.[property];
    if (!currentProperty) {
      fail(`baseline compatibility: ${label} property ${property} cannot be removed`);
      continue;
    }
    compareSchemaCompatibility(
      currentProperty,
      baselineProperty,
      currentDoc,
      baselineDoc,
      `${label}.properties.${property}`,
      direction,
      seen,
    );
  }

  if (direction === "response" && baseline.additionalProperties === false) {
    for (const property of Object.keys(current.properties || {})) {
      if (!Object.hasOwn(baseline.properties || {}, property)) {
        fail(`baseline compatibility: ${label} cannot add response property ${property} on a closed object`);
      }
    }
  }

  if (baseline.items && current.items) {
    compareSchemaCompatibility(
      current.items,
      baseline.items,
      currentDoc,
      baselineDoc,
      `${label}.items`,
      direction,
      seen,
    );
  }
  for (const keyword of ["allOf", "oneOf", "anyOf"]) {
    if (!Array.isArray(baseline[keyword])) continue;
    if (!Array.isArray(current[keyword]) || current[keyword].length !== baseline[keyword].length) {
      fail(`baseline compatibility: ${label} ${keyword} structure cannot change`);
      continue;
    }
    baseline[keyword].forEach((schema, index) => compareSchemaCompatibility(
      current[keyword][index],
      schema,
      currentDoc,
      baselineDoc,
      `${label}.${keyword}[${index}]`,
      direction,
      seen,
    ));
  }
}

function parameterIdentity(parameter) {
  return `${parameter?.in || "unknown"}:${parameter?.name || "unknown"}`;
}

function compareOperationCompatibility(currentDoc, baselineDoc, descriptor) {
  const label = operationKey(descriptor.path, descriptor.method);
  const currentPathItem = currentDoc.paths?.[descriptor.path];
  const baselinePathItem = baselineDoc.paths?.[descriptor.path];
  const current = currentPathItem?.[descriptor.method];
  const baseline = baselinePathItem?.[descriptor.method];
  if (!baseline) {
    fail(`baseline compatibility: ${label} is missing from the stable baseline`);
    return;
  }
  if (!current) {
    fail(`baseline compatibility: ${label} cannot be removed`);
    return;
  }
  if (current.operationId !== baseline.operationId) {
    fail(`baseline compatibility: ${label} operationId cannot change`);
  }
  if (JSON.stringify(current.security ?? currentDoc.security) !== JSON.stringify(baseline.security ?? baselineDoc.security)) {
    fail(`baseline compatibility: ${label} security requirement cannot change`);
  }
  for (const extension of ["x-required-scopes", "x-required-scope-any-of"]) {
    const baselineScopes = baseline[extension];
    if (baselineScopes && !exactSet(current[extension], baselineScopes)) {
      fail(`baseline compatibility: ${label} authorization scope requirement cannot change`);
    }
  }

  const currentParameters = new Map(getParameters(currentPathItem, current).map((raw) => {
    const parameter = resolveForComparison(raw, currentDoc);
    return [parameterIdentity(parameter), { raw, parameter }];
  }));
  for (const rawBaselineParameter of getParameters(baselinePathItem, baseline)) {
    const baselineParameter = resolveForComparison(rawBaselineParameter, baselineDoc);
    const currentParameter = currentParameters.get(parameterIdentity(baselineParameter));
    if (!currentParameter) {
      fail(`baseline compatibility: ${label} parameter ${parameterIdentity(baselineParameter)} cannot be removed`);
      continue;
    }
    if (currentParameter.parameter.required !== baselineParameter.required) {
      fail(`baseline compatibility: ${label} parameter ${parameterIdentity(baselineParameter)} requiredness cannot change`);
    }
    compareSchemaCompatibility(
      currentParameter.parameter.schema,
      baselineParameter.schema,
      currentDoc,
      baselineDoc,
      `${label} parameter ${parameterIdentity(baselineParameter)}`,
      "request",
    );
  }
  for (const { parameter } of currentParameters.values()) {
    if (parameter.required === true) {
      const baselineHasParameter = getParameters(baselinePathItem, baseline).some((raw) => (
        parameterIdentity(resolveForComparison(raw, baselineDoc)) === parameterIdentity(parameter)
      ));
      if (!baselineHasParameter) {
        fail(`baseline compatibility: ${label} cannot add required parameter ${parameterIdentity(parameter)}`);
      }
    }
  }

  const currentRequest = resolveForComparison(current.requestBody, currentDoc);
  const baselineRequest = resolveForComparison(baseline.requestBody, baselineDoc);
  if (Boolean(currentRequest?.required) !== Boolean(baselineRequest?.required)) {
    fail(`baseline compatibility: ${label} request body requiredness cannot change`);
  }
  for (const [mediaType, baselineMedia] of Object.entries(baselineRequest?.content || {})) {
    const currentMedia = currentRequest?.content?.[mediaType];
    if (!currentMedia) {
      fail(`baseline compatibility: ${label} request media type ${mediaType} cannot be removed`);
    } else {
      compareSchemaCompatibility(
        currentMedia.schema,
        baselineMedia.schema,
        currentDoc,
        baselineDoc,
        `${label} request ${mediaType}`,
        "request",
      );
    }
  }

  for (const [status, rawBaselineResponse] of Object.entries(baseline.responses || {})) {
    const rawCurrentResponse = current.responses?.[status];
    if (!rawCurrentResponse) {
      fail(`baseline compatibility: ${label} response status ${status} cannot be removed`);
      continue;
    }
    const currentResponse = resolveForComparison(rawCurrentResponse, currentDoc);
    const baselineResponse = resolveForComparison(rawBaselineResponse, baselineDoc);
    for (const [mediaType, baselineMedia] of Object.entries(baselineResponse?.content || {})) {
      const currentMedia = currentResponse?.content?.[mediaType];
      if (!currentMedia) {
        fail(`baseline compatibility: ${label} response ${status} media type ${mediaType} cannot be removed`);
      } else {
        compareSchemaCompatibility(
          currentMedia.schema,
          baselineMedia.schema,
          currentDoc,
          baselineDoc,
          `${label} response ${status} ${mediaType}`,
          "response",
        );
      }
    }
  }
}

function compareCompatibleSurface(currentDoc) {
  let baselineDoc;
  try {
    baselineDoc = loadBaselineDocument();
  } catch (error) {
    fail(error.message.startsWith("stable baseline") ? error.message : `stable baseline unavailable: ${error.message}`);
    return;
  }
  for (const descriptor of OPERATION_DESCRIPTORS) {
    compareOperationCompatibility(currentDoc, baselineDoc, descriptor);
  }
  for (const [name, baselineSchema] of Object.entries(baselineDoc.components?.schemas || {})) {
    const currentSchema = currentDoc.components?.schemas?.[name];
    if (!currentSchema) {
      fail(`baseline compatibility: components.schemas.${name} cannot be removed`);
      continue;
    }
    compareSchemaCompatibility(
      currentSchema,
      baselineSchema,
      currentDoc,
      baselineDoc,
      `components.schemas.${name}`,
    );
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
  if (!exactSet(policy.controlled_implementations_at_ports, [
    "adapter",
    "credential_vault",
    "persistence",
    "job_runtime",
    "clock",
    "id_generator",
  ])) {
    fail("controlled contract-test ports must match the stable allowlist");
  }
  if (!exactSet(policy.forbidden_test_seams, [
    "handler_stub",
    "private_function",
    "concrete_database_schema",
    "goroutine_layout",
  ])) {
    fail("forbidden contract-test seams must match the stable set");
  }
  if (!exactSet(policy.ownership_rejection_before, [
    "vault_decrypt",
    "adapter_call",
    "job_enqueue",
  ])) {
    fail("contract-test ownership rejection boundaries must match the stable set");
  }
  if (!exactSet(policy.required_observations, [
    "http_status_headers_and_body",
    "durable_resource_identity",
    "adapter_call_count",
    "vault_read_write_decrypt_and_revoke_counts",
    "persistence_and_job_side_effect_counts",
    "same_tenant_and_non_enumeration_behavior",
    "idempotency_replay_conflict_and_uncertain_outcomes",
  ])) {
    fail("contract tests must observe exactly the stable observation set");
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

  validateOpenApiStructure(path);
  validateDescriptorIntegrity();

  if (doc.openapi !== "3.1.1") fail(`openapi must be 3.1.1, got ${JSON.stringify(doc.openapi)}`);
  if (doc.jsonSchemaDialect !== "https://json-schema.org/draft/2020-12/schema") {
    fail("jsonSchemaDialect must be JSON Schema Draft 2020-12");
  }
  if (doc["x-pixelplus-artifact-status"] !== "stable") {
    fail("x-pixelplus-artifact-status must be stable");
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
  validateAssetUploadOutcomes(doc);
  compareCompatibleSurface(doc);
  walkRefs(doc);

  const seenOperationIds = new Set();
  let operationCount = 0;
  for (const [pathKey, pathItem] of Object.entries(doc.paths || {})) {
    for (const [method, operation] of Object.entries(pathItem || {})) {
      if (!httpMethods.has(method) || !operation) continue;
      operationCount += 1;
      const label = operationKey(pathKey, method);
      const descriptor = descriptorByLabel.get(label);
      if (!descriptor) fail(`${label} must have an operation descriptor`);
      validateOperationSecurityAndInput(pathKey, method, pathItem, operation, descriptor);
      validateSecretBoundaries(pathKey, method, operation);
      if (!operation.operationId) {
        fail(`${operationKey(pathKey, method)} must declare operationId`);
      } else if (seenOperationIds.has(operation.operationId)) {
        fail(`duplicate operationId ${operation.operationId}`);
      } else {
        seenOperationIds.add(operation.operationId);
      }

      const idempotencyParameters = findIdempotencyParameters(pathItem, operation);
      if (descriptor?.idempotencyHeader === "optional") {
        validateIdempotencyParameter(pathKey, method, pathItem, operation, descriptor.headerRef, false);
        validateIdempotencyConflictResponse(pathKey, method, operation);
      } else if (descriptor?.idempotencyHeader === "required") {
        validateIdempotencyParameter(pathKey, method, pathItem, operation, descriptor.headerRef, true);
        validateIdempotencyConflictResponse(pathKey, method, operation);
      } else if (idempotencyParameters.length > 0) {
        fail(`${label} must not use Idempotency-Key`);
      }
    }
  }

  for (const descriptor of OPERATION_DESCRIPTORS) {
    const operation = doc.paths?.[descriptor.path]?.[descriptor.method];
    if (!operation) {
      fail(`missing required operation ${operationKey(descriptor.path, descriptor.method)}`);
    } else if (operation.operationId !== descriptor.operationId) {
      fail(`${operationKey(descriptor.path, descriptor.method)} operationId must be ${descriptor.operationId}`);
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
  validateExampleSecrets(examples);
  const validatedExamples = validateExamplesWithPython(doc, examples);

  if (failures.length > 0) {
    console.error(`FAIL: stable Public API contract has ${failures.length} violation(s)`);
    for (const message of failures) console.error(`- ${message}`);
    process.exit(1);
  }

  console.log(
    `PASS: stable Public API contract (${operationCount} operations, ${validatedExamples} Draft 2020-12 examples, baseline_source=${baselineSource})`,
  );
}

main();
