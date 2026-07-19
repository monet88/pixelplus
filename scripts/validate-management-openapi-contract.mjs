#!/usr/bin/env node
/**
 * Prototype OpenAPI contract validator for PixelPlus #19 management evidence.
 *
 * Usage (from repo root):
 *   node scripts/validate-management-openapi-contract.mjs contracts/openapi/pixelplus-management-api-v0alpha.yaml
 *
 * Zero new Node dependencies. The tracer is JSON-compatible YAML (JSON subset)
 * so it is loaded with JSON.parse. Draft 2020-12 example validation requires
 * Python with the `jsonschema` package in the validation environment.
 *
 * This is representation validation, not a runtime Gateway/E2E test and not a
 * full external OpenAPI metaschema check.
 */

import { existsSync, readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, "..");
const HTTP_METHODS = new Set(["get", "put", "post", "delete", "patch", "options", "head", "trace"]);

const REQUIRED_OPERATIONS = [
  ["/provider-accounts", "post", "accounts.manage"],
  ["/provider-accounts", "get", "accounts.read"],
  ["/provider-accounts/{provider_account_id}", "get", "accounts.read"],
  ["/provider-accounts/{provider_account_id}", "delete", "accounts.manage"],
  ["/provider-accounts/{provider_account_id}/credentials", "post", "accounts.manage"],
  ["/provider-accounts/{provider_account_id}/oauth-authorizations", "post", "accounts.manage"],
  ["/provider-accounts/{provider_account_id}/oauth-authorizations/{authorization_id}", "get", "accounts.manage"],
  ["/provider-accounts/{provider_account_id}/probe", "post", "accounts.manage"],
  ["/provider-accounts/{provider_account_id}/reauthentication", "post", "accounts.manage"],
  ["/provider-accounts/{provider_account_id}/disable", "post", "accounts.manage"],
  ["/provider-accounts/{provider_account_id}/enable", "post", "accounts.manage"],
  ["/provider-accounts/{provider_account_id}/capability-snapshot", "get", "accounts.read|capabilities.read"],
  ["/routing-policy", "get", "routing.read"],
  ["/routing-policy", "put", "routing.manage"],
];

const REQUIRED_ERROR_EXAMPLES = {
  ErrorAuthenticationFailed: "authentication_failed",
  ErrorForbidden: "forbidden",
  ErrorResourceNotFound: "resource_not_found",
  ErrorInvalidRequest: "invalid_request",
  ErrorAccountNotUsable: "account_not_usable",
  ErrorAuthModeUnavailable: "auth_mode_unavailable",
  ErrorCapabilityUnverified: "capability_unverified",
  ErrorSnapshotStale: "snapshot_stale",
};

const APPROVED_SECRET_FIELDS = new Map([
  ["CredentialSubmission", new Set(["material"])],
  ["DirectCredentialSubmissionRequest", new Set(["material"])],
  ["DirectReauthenticationRequest", new Set(["material"])],
]);
const APPROVED_SECRET_BOUNDARIES = new Map([
  [
    "POST /provider-accounts/{provider_account_id}/credentials",
    "#/components/schemas/DirectCredentialSubmissionRequest",
  ],
  [
    "POST /provider-accounts/{provider_account_id}/reauthentication",
    "#/components/schemas/DirectReauthenticationRequest",
  ],
]);
const SECRET_KEYS = new Set([
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
const SECRET_VALUE_PATTERNS = [
  /bearer\s+[a-z0-9._~+\/-]{8,}/i,
  /sk-pxp_[a-z0-9_-]+_[a-z0-9_-]+/i,
  /refresh[_-]?token\s*[:=]/i,
  /access[_-]?token\s*[:=]/i,
  /set-cookie\s*:/i,
];

const SECRET_KEY_CANONICAL = new Set(
  [...SECRET_KEYS].map((name) => name.replace(/[^a-z0-9]/g, "")),
);
const isSecretName = (value) => {
  if (typeof value !== "string") return false;
  return SECRET_KEY_CANONICAL.has(value.toLowerCase().replace(/[^a-z0-9]/g, ""));
};
const patternMayDeclareSecretName = (value) => {
  if (typeof value !== "string") return false;
  const canonical = value.toLowerCase().replace(/[^a-z0-9]/g, "");
  return [...SECRET_KEY_CANONICAL].some((name) => canonical.includes(name));
};

const failures = [];
let docCache = null;
const fail = (message) => failures.push(message);

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
  let current = doc;
  for (const rawPart of pointer.slice(2).split("/")) {
    const part = rawPart.replace(/~1/g, "/").replace(/~0/g, "~");
    if (!current || typeof current !== "object" || !(part in current)) {
      throw new Error(`unresolvable $ref: ${pointer}`);
    }
    current = current[part];
  }
  return current;
}

function resolveObject(node, doc, seen = new Set()) {
  let current = node;
  while (current && typeof current === "object" && typeof current.$ref === "string") {
    if (seen.has(current.$ref)) throw new Error(`cyclic $ref: ${current.$ref}`);
    seen.add(current.$ref);
    current = resolvePointer(doc, current.$ref);
  }
  return current;
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

function schemaContainsTenantId(node, doc, seenRefs = new Set()) {
  if (Array.isArray(node)) return node.some((item) => schemaContainsTenantId(item, doc, seenRefs));
  if (!node || typeof node !== "object") return false;

  if (typeof node.$ref === "string" && node.$ref.startsWith("#/") && !seenRefs.has(node.$ref)) {
    const nextSeen = new Set(seenRefs);
    nextSeen.add(node.$ref);
    try {
      if (schemaContainsTenantId(resolvePointer(doc, node.$ref), doc, nextSeen)) return true;
    } catch {
      return false;
    }
  }

  if (node.properties && typeof node.properties === "object" && "tenant_id" in node.properties) return true;
  if (Array.isArray(node.required) && node.required.includes("tenant_id")) return true;
  return Object.entries(node).some(([key, value]) => key !== "$ref" && schemaContainsTenantId(value, doc, seenRefs));
}

function operationAcceptsTenantId(pathKey, pathItem, operation, doc) {
  if (pathKey.includes("{tenant_id}")) return true;
  const parameters = [
    ...(Array.isArray(pathItem.parameters) ? pathItem.parameters : []),
    ...(Array.isArray(operation.parameters) ? operation.parameters : []),
  ];
  for (const rawParameter of parameters) {
    try {
      const parameter = resolveObject(rawParameter, doc);
      if (parameter?.name === "tenant_id") return true;
    } catch {
      // Ref failures are reported by the reference walk.
    }
  }

  let requestBody;
  try {
    requestBody = resolveObject(operation.requestBody, doc);
  } catch {
    return false;
  }
  return Object.values(requestBody?.content || {}).some((mediaType) => schemaContainsTenantId(mediaType?.schema, doc));
}

function usesClientApiKey(operation, doc) {
  const security = operation.security ?? doc.security;
  if (!Array.isArray(security) || security.length === 0) return false;
  return security.every((requirement) => {
    if (!requirement || typeof requirement !== "object") return false;
    const names = Object.keys(requirement);
    return names.length === 1 && names[0] === "ClientApiKey" && Array.isArray(requirement.ClientApiKey) && requirement.ClientApiKey.length === 0;
  });
}

function operationHasScope(operation, expected) {
  if (expected.includes("|")) {
    const expectedScopes = expected.split("|").sort();
    const actual = [...(operation["x-required-scope-any-of"] || [])].sort();
    return JSON.stringify(actual) === JSON.stringify(expectedScopes);
  }
  return Array.isArray(operation["x-required-scopes"]) && operation["x-required-scopes"].length === 1 && operation["x-required-scopes"][0] === expected;
}

function collectExamples(node, path = "$", out = []) {
  if (Array.isArray(node)) {
    node.forEach((item, index) => collectExamples(item, `${path}[${index}]`, out));
    return out;
  }
  if (!node || typeof node !== "object") return out;

  if (Array.isArray(node.examples) && "type" in node) {
    node.examples.forEach((value, index) => out.push({ label: `${path}.examples[${index}]`, value, schema: node }));
  }
  if (node.examples && typeof node.examples === "object" && !Array.isArray(node.examples)) {
    for (const [name, payload] of Object.entries(node.examples)) {
      if (payload && typeof payload === "object" && "value" in payload) {
        out.push({ label: `${path}.examples.${name}`, value: payload.value, schema: payload.schema ?? node.schema ?? null });
      } else if (payload && typeof payload === "object" && typeof payload.$ref === "string") {
        try {
          const resolved = resolvePointer(docCache, payload.$ref);
          if (resolved && typeof resolved === "object" && "value" in resolved) {
            out.push({ label: `${path}.examples.${name}->${payload.$ref}`, value: resolved.value, schema: node.schema ?? null });
          }
        } catch {
          // Reference failures are reported separately.
        }
      }
    }
  }
  if ("example" in node && ("schema" in node || "type" in node)) {
    out.push({ label: `${path}.example`, value: node.example, schema: node.schema ?? node });
  }

  for (const [key, value] of Object.entries(node)) {
    if (key !== "examples" && key !== "example") collectExamples(value, `${path}.${key}`, out);
  }
  return out;
}

function scanExampleForSecrets(value, label, path = "$") {
  if (Array.isArray(value)) {
    value.forEach((item, index) => scanExampleForSecrets(item, label, `${path}[${index}]`));
    return;
  }
  if (value && typeof value === "object") {
    for (const [key, nested] of Object.entries(value)) {
      if (isSecretName(key)) fail(`${label}${path}.${key}: secret-bearing key is forbidden in OpenAPI examples`);
      scanExampleForSecrets(nested, label, `${path}.${key}`);
    }
    return;
  }
  if (typeof value === "string" && SECRET_VALUE_PATTERNS.some((pattern) => pattern.test(value))) {
    fail(`${label}${path}: possible secret material is forbidden in OpenAPI examples`);
  }
}

function valueContainsSecretMaterial(value) {
  if (Array.isArray(value)) return value.some(valueContainsSecretMaterial);
  if (value && typeof value === "object") {
    return Object.entries(value).some(
      ([key, nested]) => isSecretName(key) || valueContainsSecretMaterial(nested),
    );
  }
  return typeof value === "string"
    && SECRET_VALUE_PATTERNS.some((pattern) => pattern.test(value));
}

function propertyNamesDeclareSecret(node, seenRefs = new Set()) {
  if (Array.isArray(node)) return node.some((item) => propertyNamesDeclareSecret(item, seenRefs));
  if (!node || typeof node !== "object") return false;

  if (typeof node.$ref === "string" && node.$ref.startsWith("#/") && !seenRefs.has(node.$ref)) {
    const nextSeen = new Set(seenRefs);
    nextSeen.add(node.$ref);
    try {
      if (propertyNamesDeclareSecret(resolvePointer(docCache, node.$ref), nextSeen)) return true;
    } catch {
      return false;
    }
  }

  if (isSecretName(node.const)) return true;
  if ((node.enum || []).some((name) => isSecretName(name))) return true;
  if (patternMayDeclareSecretName(node.pattern)) return true;
  return Object.entries(node).some(
    ([key, value]) => key !== "$ref" && propertyNamesDeclareSecret(value, seenRefs),
  );
}

function schemaDeclaresSecretName(node) {
  if (!node || typeof node !== "object" || Array.isArray(node)) return false;
  const properties = node.properties || {};
  if ((node.required || []).some((name) => isSecretName(name) && !(name in properties))) return true;

  if (Object.keys(node.patternProperties || {}).some((pattern) => patternMayDeclareSecretName(pattern))) return true;
  if (propertyNamesDeclareSecret(node.propertyNames)) return true;
  for (const keyword of ["default", "const", "enum"]) {
    if (valueContainsSecretMaterial(node[keyword])) return true;
  }

  for (const [name, dependencies] of Object.entries(node.dependentRequired || {})) {
    if (isSecretName(name)) return true;
    if ((dependencies || []).some((dependency) => isSecretName(dependency) && !(dependency in properties))) return true;
  }
  if (Object.keys(node.dependentSchemas || {}).some((name) => isSecretName(name))) return true;
  return false;
}

function scanSecretSchemas(schemas) {
  function visit(node, schemaName, path, seenRefs = new Set()) {
    if (Array.isArray(node)) {
      node.forEach((item, index) => visit(item, schemaName, `${path}[${index}]`, seenRefs));
      return;
    }
    if (!node || typeof node !== "object") return;

    if (typeof node.$ref === "string" && node.$ref.startsWith("#/") && !seenRefs.has(node.$ref)) {
      const nextSeen = new Set(seenRefs);
      nextSeen.add(node.$ref);
      try {
        visit(resolvePointer(docCache, node.$ref), schemaName, `${path}->${node.$ref}`, nextSeen);
      } catch {
        return;
      }
    }

    if (schemaDeclaresSecretName(node)) {
      fail(`components.schemas.${schemaName}${path}: secret field declaration outside approved properties boundary`);
    }

    if (node.properties && typeof node.properties === "object") {
      for (const [propertyName, propertySchema] of Object.entries(node.properties)) {
        if (isSecretName(propertyName)) {
          const allowedFields = APPROVED_SECRET_FIELDS.get(schemaName);
          if (!allowedFields?.has(propertyName)) {
            fail(`components.schemas.${schemaName}${path}.properties.${propertyName}: secret field outside approved request boundary`);
          }
          if (propertySchema?.writeOnly !== true) {
            fail(`components.schemas.${schemaName}${path}.properties.${propertyName}: secret field must be writeOnly`);
          }
        }
      }
    }

    for (const [key, value] of Object.entries(node)) {
      if (key !== "$ref") visit(value, schemaName, `${path}.${key}`, seenRefs);
    }
  }

  for (const [schemaName, schema] of Object.entries(schemas)) visit(schema, schemaName, "");
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

  if (schemaDeclaresSecretName(node)) return true;
  if (Object.keys(node.properties || {}).some((name) => isSecretName(name))) return true;
  return Object.entries(node).some(([key, value]) => key !== "$ref" && schemaContainsSecretMaterial(value, seenRefs));
}

function validateSecretIngressBoundaries(paths, components) {
  function scanParameter(rawParameter, label) {
    let parameter;
    try {
      parameter = resolveObject(rawParameter, docCache);
    } catch {
      return;
    }
    if (isSecretName(parameter?.name)) {
      fail(`${label}: secret-bearing parameter names are forbidden`);
    }
    if (schemaContainsSecretMaterial(parameter?.schema)) {
      fail(`${label}: secret-bearing parameter schemas are forbidden`);
    }
    for (const [mediaTypeName, mediaType] of Object.entries(parameter?.content || {})) {
      if (schemaContainsSecretMaterial(mediaType?.schema)) {
        fail(`${label} ${mediaTypeName}: secret-bearing parameter content is forbidden`);
      }
    }
  }

  function scanHeaders(headers, label) {
    for (const [headerName, rawHeader] of Object.entries(headers || {})) {
      if (isSecretName(headerName)) {
        fail(`${label}.${headerName}: secret-bearing header names are forbidden`);
      }
      let header;
      try {
        header = resolveObject(rawHeader, docCache);
      } catch {
        continue;
      }
      if (schemaContainsSecretMaterial(header?.schema)) {
        fail(`${label}.${headerName}: secret-bearing header schemas are forbidden`);
      }
      for (const [mediaTypeName, mediaType] of Object.entries(header?.content || {})) {
        if (schemaContainsSecretMaterial(mediaType?.schema)) {
          fail(`${label}.${headerName} ${mediaTypeName}: secret-bearing header content is forbidden`);
        }
      }
    }
  }

  function scanEncodingHeaders(content, label) {
    for (const [mediaTypeName, mediaType] of Object.entries(content || {})) {
      for (const [propertyName, encoding] of Object.entries(mediaType?.encoding || {})) {
        if (isSecretName(propertyName)) {
          fail(`${label}.${mediaTypeName}.encoding.${propertyName}: secret-bearing encoded property names are forbidden`);
        }
        scanHeaders(encoding?.headers, `${label}.${mediaTypeName}.encoding.${propertyName}.headers`);
      }
    }
  }

  function scanResponse(rawResponse, label) {
    let response;
    try {
      response = resolveObject(rawResponse, docCache);
    } catch {
      return;
    }
    scanHeaders(response?.headers, `${label}.headers`);
    if (Object.keys(response?.links || {}).length > 0) {
      fail(`${label}: response links are outside the locked management prototype surface`);
    }
    for (const [mediaTypeName, mediaType] of Object.entries(response?.content || {})) {
      if (schemaContainsSecretMaterial(mediaType?.schema)) {
        fail(`${label} ${mediaTypeName}: secret-bearing response schemas are forbidden`);
      }
    }
    scanEncodingHeaders(response?.content, `${label}.content`);
  }

  for (const [name, parameter] of Object.entries(components.parameters || {})) {
    scanParameter(parameter, `components.parameters.${name}`);
  }
  scanHeaders(components.headers, "components.headers");
  for (const [name, response] of Object.entries(components.responses || {})) {
    scanResponse(response, `components.responses.${name}`);
  }

  for (const [name, rawRequestBody] of Object.entries(components.requestBodies || {})) {
    let requestBody;
    try {
      requestBody = resolveObject(rawRequestBody, docCache);
    } catch {
      continue;
    }
    for (const [mediaTypeName, mediaType] of Object.entries(requestBody?.content || {})) {
      if (schemaContainsSecretMaterial(mediaType?.schema)) {
        fail(`components.requestBodies.${name} ${mediaTypeName}: reusable secret-bearing request bodies are forbidden`);
      }
    }
    scanEncodingHeaders(requestBody?.content, `components.requestBodies.${name}.content`);
  }

  for (const [pathKey, pathItem] of Object.entries(paths)) {
    if (!pathItem || typeof pathItem !== "object") continue;
    for (const [index, parameter] of (pathItem.parameters || []).entries()) {
      scanParameter(parameter, `paths.${pathKey}.parameters[${index}]`);
    }

    for (const [method, operation] of Object.entries(pathItem)) {
      if (!HTTP_METHODS.has(method) || !operation || typeof operation !== "object") continue;
      const operationLabel = `${method.toUpperCase()} ${pathKey}`;
      for (const [index, parameter] of (operation.parameters || []).entries()) {
        scanParameter(parameter, `${operationLabel} parameters[${index}]`);
      }

      let requestBody;
      try {
        requestBody = resolveObject(operation.requestBody, docCache);
      } catch {
        requestBody = null;
      }
      const content = requestBody?.content || {};
      const expectedRef = APPROVED_SECRET_BOUNDARIES.get(operationLabel);
      const approvedSchema = content["application/json"]?.schema;
      const exactApprovedSchema = expectedRef
        && approvedSchema?.$ref === expectedRef
        && Object.keys(approvedSchema).length === 1;
      if (
        expectedRef
        && (
          !exactApprovedSchema
          || !schemaContainsSecretMaterial(approvedSchema)
        )
      ) {
        fail(`${operationLabel}: application/json must use the approved secret request schema directly`);
      }

      for (const [mediaTypeName, mediaType] of Object.entries(content)) {
        const approvedMediaType = mediaTypeName === "application/json" && exactApprovedSchema;
        if (schemaContainsSecretMaterial(mediaType?.schema) && !approvedMediaType) {
          fail(`${operationLabel} ${mediaTypeName}: secret material is outside an approved request boundary`);
        }
      }
      scanEncodingHeaders(content, `${operationLabel} requestBody.content`);

      for (const [status, rawResponse] of Object.entries(operation.responses || {})) {
        scanResponse(rawResponse, `${operationLabel} responses.${status}`);
      }
    }
  }
}

function validateExamplesWithPython(doc, examples) {
  const items = examples
    .filter((item) => item.schema && typeof item.schema === "object")
    .map((item) => ({ label: item.label, schema: item.schema, value: item.value }));

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
    cwd: ROOT,
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

function gatherText(node, chunks = []) {
  if (Array.isArray(node)) {
    node.forEach((item) => gatherText(item, chunks));
  } else if (node && typeof node === "object") {
    for (const [key, value] of Object.entries(node)) {
      if ((key === "description" || key === "summary") && typeof value === "string") chunks.push(value);
      else gatherText(value, chunks);
    }
  }
  return chunks;
}

function rootSchemaExamples(node, label, out = [], seenRefs = new Set()) {
  if (!node || typeof node !== "object") return out;

  if (typeof node.$ref === "string" && !seenRefs.has(node.$ref)) {
    const nextSeen = new Set(seenRefs);
    nextSeen.add(node.$ref);
    try {
      rootSchemaExamples(resolvePointer(docCache, node.$ref), `${label}->${node.$ref}`, out, nextSeen);
    } catch (error) {
      fail(`${label}: ${error.message}`);
    }
  }

  if ("example" in node) out.push(node.example);
  if (Array.isArray(node.examples)) out.push(...node.examples);
  for (const keyword of ["allOf", "anyOf", "oneOf"]) {
    for (const [index, branch] of (node[keyword] || []).entries()) {
      rootSchemaExamples(branch, `${label}.${keyword}[${index}]`, out, seenRefs);
    }
  }
  return out;
}

function responseExamples(operation, status) {
  let response;
  try {
    response = resolveObject(operation?.responses?.[status], docCache);
  } catch (error) {
    fail(`response ${status}: ${error.message}`);
    return [];
  }
  const content = response?.content;
  if (!content || typeof content !== "object") return [];

  const examples = [];
  for (const [mediaTypeName, mediaType] of Object.entries(content)) {
    if (!mediaType || typeof mediaType !== "object") continue;
    if ("example" in mediaType) examples.push(mediaType.example);

    for (const [name, rawExample] of Object.entries(mediaType.examples || {})) {
      let exampleObject = rawExample;
      try {
        if (exampleObject?.$ref) exampleObject = resolvePointer(docCache, exampleObject.$ref);
      } catch (error) {
        fail(`response ${status} ${mediaTypeName} example ${name}: ${error.message}`);
        continue;
      }
      if (!exampleObject || typeof exampleObject !== "object" || !("value" in exampleObject)) {
        fail(`response ${status} ${mediaTypeName} example ${name}: Example Object must contain value`);
        continue;
      }
      examples.push(exampleObject.value);
    }

    rootSchemaExamples(
      mediaType.schema,
      `response ${status} ${mediaTypeName} schema`,
      examples,
    );
  }

  return examples;
}

function exampleClaimsProbeSuccess(example) {
  const health = example?.account?.health;
  return health?.summary_state === "healthy"
    || (health?.conditions || []).some((condition) => condition?.reason === "probe_succeeded");
}

function main() {
  const relativePath = process.argv[2] || "contracts/openapi/pixelplus-management-api-v0alpha.yaml";
  const artifactPath = resolve(ROOT, relativePath);
  let doc;
  try {
    doc = loadDocument(artifactPath);
    docCache = doc;
  } catch (error) {
    console.error(`FAIL: ${error.message}`);
    process.exit(1);
  }

  if (doc.openapi !== "3.1.1") fail(`openapi must be 3.1.1, got ${JSON.stringify(doc.openapi)}`);
  const info = doc.info || {};
  if (info.version !== "0.0.0-prototype") fail(`info.version must be 0.0.0-prototype`);
  if ("x-pixelplus-status" in info || "x-pixelplus-artifact-status" in info) {
    fail("prototype status must use only top-level x-pixelplus-artifact-status");
  }
  if (doc["x-pixelplus-artifact-status"] !== "prototype") fail(`top-level x-pixelplus-artifact-status must be prototype`);
  if (!String(doc.jsonSchemaDialect || "").includes("2020-12")) fail(`jsonSchemaDialect must be Draft 2020-12`);
  const servers = doc.servers || [];
  if (
    servers.length !== 1
    || String(servers[0]?.url || "").replace(/\/$/, "") !== "/v1"
    || Object.keys(servers[0]?.variables || {}).length > 0
  ) {
    fail(`servers must contain only the fixed /v1 base without variables`);
  }

  const securitySchemes = doc.components?.securitySchemes || {};
  if (JSON.stringify(Object.keys(securitySchemes).sort()) !== JSON.stringify(["ClientApiKey"])) {
    fail(`ClientApiKey must be the only security scheme on the locked management surface`);
  }
  const scheme = securitySchemes.ClientApiKey;
  if (!scheme || scheme.type !== "http" || scheme.scheme !== "bearer") fail(`ClientApiKey must be an http bearer scheme`);
  if (!String(scheme?.bearerFormat || "").includes("sk-pxp_<public_locator>_<secret>")) fail(`ClientApiKey bearerFormat must document sk-pxp_<public_locator>_<secret>`);

  const paths = doc.paths || {};
  if (Object.keys(doc.components?.pathItems || {}).length > 0) {
    fail(`reusable path items are outside the locked management prototype surface`);
  }
  for (const [pathKey, pathItem] of Object.entries(paths)) {
    if (pathItem?.$ref) fail(`${pathKey}: path item $ref is outside the locked management prototype surface`);
  }

  const reusableParameters = doc.components?.parameters || {};
  const providerAccountParameter = reusableParameters.ProviderAccountId;
  const oauthAuthorizationParameter = reusableParameters.OAuthAuthorizationId;
  if (
    providerAccountParameter?.name !== "provider_account_id"
    || providerAccountParameter?.in !== "path"
    || providerAccountParameter?.required !== true
    || providerAccountParameter?.schema?.pattern !== "^pa_[A-Za-z0-9_]+$"
  ) {
    fail(`components.parameters.ProviderAccountId must lock the Provider Account path identifier`);
  }
  if (
    oauthAuthorizationParameter?.name !== "authorization_id"
    || oauthAuthorizationParameter?.in !== "path"
    || oauthAuthorizationParameter?.required !== true
    || oauthAuthorizationParameter?.schema?.pattern !== "^oauth_[A-Za-z0-9_]+$"
  ) {
    fail(`components.parameters.OAuthAuthorizationId must lock the OAuth authorization path identifier`);
  }
  for (const [pathKey, pathItem] of Object.entries(paths)) {
    const refs = (pathItem?.parameters || []).map((parameter) => parameter?.$ref);
    if (pathKey.includes("{provider_account_id}") && !refs.includes("#/components/parameters/ProviderAccountId")) {
      fail(`${pathKey}: must reuse components.parameters.ProviderAccountId`);
    }
    if (pathKey.includes("{authorization_id}") && !refs.includes("#/components/parameters/OAuthAuthorizationId")) {
      fail(`${pathKey}: must reuse components.parameters.OAuthAuthorizationId`);
    }
  }

  const expectedOperationKeys = REQUIRED_OPERATIONS
    .map(([pathKey, method]) => `${method.toUpperCase()} ${pathKey}`)
    .sort();
  const actualOperationKeys = [];
  for (const [pathKey, pathItem] of Object.entries(paths)) {
    for (const method of Object.keys(pathItem || {})) {
      if (HTTP_METHODS.has(method)) actualOperationKeys.push(`${method.toUpperCase()} ${pathKey}`);
    }
  }
  actualOperationKeys.sort();
  if (JSON.stringify(actualOperationKeys) !== JSON.stringify(expectedOperationKeys)) {
    fail(`operation surface must contain exactly the 14 locked management operations`);
  }
  if (
    Object.keys(doc.webhooks || {}).length > 0
    || Object.keys(doc.components?.callbacks || {}).length > 0
    || Object.keys(doc.components?.links || {}).length > 0
  ) {
    fail(`webhooks, callbacks, and reusable links are outside the locked management prototype surface`);
  }

  for (const [pathKey, method, scope] of REQUIRED_OPERATIONS) {
    const operation = paths[pathKey]?.[method];
    if (!operation) {
      fail(`missing required operation ${method.toUpperCase()} ${pathKey}`);
      continue;
    }
    if (!usesClientApiKey(operation, doc)) fail(`${method.toUpperCase()} ${pathKey}: ClientApiKey security required`);
    if (!operationHasScope(operation, scope)) fail(`${method.toUpperCase()} ${pathKey}: required scope metadata must be ${scope}`);
  }

  for (const [pathKey, pathItem] of Object.entries(paths)) {
    if (!pathItem || typeof pathItem !== "object") continue;
    for (const [method, operation] of Object.entries(pathItem)) {
      if (!HTTP_METHODS.has(method)) continue;
      if (!usesClientApiKey(operation, doc)) fail(`${method.toUpperCase()} ${pathKey}: invalid or optional security`);
      if (operationAcceptsTenantId(pathKey, pathItem, operation, doc)) fail(`${method.toUpperCase()} ${pathKey}: tenant_id must not be client supplied`);
      if (Object.keys(operation.callbacks || {}).length > 0) fail(`${method.toUpperCase()} ${pathKey}: callbacks are outside the locked management prototype surface`);
    }
  }

  walkRefs(doc);
  const schemas = doc.components?.schemas || {};
  scanSecretSchemas(schemas);
  validateSecretIngressBoundaries(paths, doc.components || {});

  const account = schemas.ProviderAccount || {};
  for (const required of ["lifecycle_state", "health", "administrative_controls", "credential"]) {
    if (!(account.required || []).includes(required)) fail(`ProviderAccount must require ${required}`);
  }
  if ("tenant_id" in (account.properties || {})) fail(`ProviderAccount response must not expose tenant_id`);
  if (!schemas.HealthSummary || !schemas.AdministrativeControls) fail(`health and administrative controls must have separate schemas`);

  const lifecycleStates = schemas.LifecycleState?.enum || [];
  const expectedLifecycle = ["active", "deleted", "disabled", "draft", "pending_probe", "pending_validation", "reauth_required", "revoked"];
  if (JSON.stringify([...lifecycleStates].sort()) !== JSON.stringify(expectedLifecycle)) fail(`LifecycleState must contain the eight locked states`);

  const probeOperation = paths["/provider-accounts/{provider_account_id}/probe"]?.post;
  const probeText = gatherText(probeOperation).join(" ").toLowerCase();
  for (const phrase of [
    "active-origin auth-class failure moves to reauth_required",
    "disabled administrative intent remains disabled",
    "transient failure remains retryable in pending_probe",
  ]) {
    if (!probeText.includes(phrase)) fail(`probe operation must document ${phrase}`);
  }

  const enableOperation = paths["/provider-accounts/{provider_account_id}/enable"]?.post;
  const enableText = gatherText(enableOperation).join(" ").toLowerCase();
  for (const phrase of [
    "pending_probe",
    "current-credential-version",
    "separate",
    "provider_probe",
    "does not claim the probe has run",
    "rejects while any authorization, validation, replacement, or one-shot probe marker is in flight",
    "non-usable",
  ]) {
    if (!enableText.includes(phrase)) fail(`enable operation must document ${phrase}`);
  }
  const enableExamples = responseExamples(enableOperation, "202");
  if (enableExamples.length === 0) fail(`enable 202 response must include a semantic example`);
  for (const [index, enableExample] of enableExamples.entries()) {
    if (enableExample?.account?.lifecycle_state !== "pending_probe") {
      fail(`enable 202 example ${index + 1} must remain pending_probe before probe success`);
    }
    if (exampleClaimsProbeSuccess(enableExample)) {
      fail(`enable 202 example ${index + 1} must not claim healthy probe success before provider_probe`);
    }
  }

  const operationFacts = schemas.OperationFacts;
  const fiveOperations = ["chat", "chat_streaming", "image_generation", "image_edit", "inpaint"];
  if (JSON.stringify([...(operationFacts?.required || [])].sort()) !== JSON.stringify([...fiveOperations].sort())) fail(`OperationFacts must require all five primary operations`);
  const statuses = schemas.CapabilityStatus?.enum || [];
  if (JSON.stringify([...statuses].sort()) !== JSON.stringify(["conditionally_supported", "unsupported", "unverified", "verified"])) fail(`CapabilityStatus vocabulary mismatch`);
  const freshness = schemas.SnapshotFreshness?.enum || [];
  if (JSON.stringify([...freshness].sort()) !== JSON.stringify(["fresh", "invalid", "stale"])) fail(`Snapshot freshness vocabulary mismatch`);
  const snapshotRequired = schemas.CapabilitySnapshot?.required || [];
  for (const required of ["provider_account_id", "credential_version", "verified_at", "freshness", "provenance", "operations", "models"]) {
    if (!snapshotRequired.includes(required)) fail(`CapabilitySnapshot must require ${required}`);
  }
  const snapshotText = gatherText(schemas.CapabilitySnapshot).join(" ").toLowerCase();
  for (const phrase of ["per-account", "credential version", "stale", "unverified", "reference evidence"]) {
    if (!snapshotText.includes(phrase)) fail(`CapabilitySnapshot must document ${phrase}`);
  }
  const snapshotRead = paths["/provider-accounts/{provider_account_id}/capability-snapshot"]?.get;
  const snapshotReadText = gatherText(snapshotRead).join(" ").toLowerCase();
  const snapshotReadConflict = JSON.stringify(snapshotRead?.responses?.["409"] || {});
  if (!snapshotReadConflict.includes("ErrorCapabilityUnverified")) fail(`GET capability snapshot must use 409 only when no snapshot exists yet`);
  if (snapshotReadConflict.includes("ErrorSnapshotStale")) fail(`GET capability snapshot must return existing stale/invalid evidence instead of rejecting the read`);
  for (const phrase of ["stale or invalid evidence", "capability_unverified", "never authorizes execution"]) {
    if (!snapshotReadText.includes(phrase)) fail(`GET capability snapshot must document ${phrase}`);
  }

  const routingFields = schemas.RoutingPolicyFields || {};
  if (routingFields.properties?.fallback_enabled?.default !== false) fail(`RoutingPolicy fallback_enabled default must be false`);
  for (const field of ["candidate_accounts", "selection_order", "fallback_enabled", "fallback_chain", "fallback_auth_modes", "affinity", "lease_policy"]) {
    if (!(routingFields.required || []).includes(field)) fail(`RoutingPolicyFields must require ${field}`);
  }
  const routingPutText = gatherText(paths["/routing-policy"]?.put).join(" ").toLowerCase();
  for (const phrase of ["foreign/unknown", "zero vault decrypt", "zero adapter call", "no partial write", "explicit account pins never silently fall back"]) {
    if (!routingPutText.includes(phrase)) fail(`PUT /routing-policy must document ${phrase}`);
  }

  const credentialSchema = schemas.CredentialSubmission;
  const credentialMaterial = credentialSchema?.properties?.material;
  if (credentialMaterial?.writeOnly !== true) fail(`CredentialSubmission.material must be writeOnly`);
  if ((credentialMaterial?.minLength || 0) < 8) fail(`CredentialSubmission.material must reject trivially short material`);
  try {
    if (!credentialMaterial?.pattern || new RegExp(credentialMaterial.pattern).test(`bad${String.fromCharCode(10)}material`)) {
      fail(`CredentialSubmission.material must reject control characters`);
    }
  } catch (error) {
    fail(`CredentialSubmission.material pattern is invalid: ${error.message}`);
  }

  const credentialIntakeOperation = paths["/provider-accounts/{provider_account_id}/credentials"]?.post;
  const credentialIntakeResponses = credentialIntakeOperation?.responses || {};
  const credentialIntakeText = gatherText(credentialIntakeOperation).join(" ").toLowerCase();
  const credentialIntakeExamples = responseExamples(credentialIntakeOperation, "202");
  if (!JSON.stringify(credentialIntakeResponses["409"] || {}).includes("ErrorAccountNotUsable")) {
    fail(`direct credential intake must expose account_not_usable for non-draft lifecycle rejection`);
  }
  for (const phrase of ["pending_validation", "separate server-owned validation", "revokes the staged version", "no adapter call"]) {
    if (!credentialIntakeText.includes(phrase)) fail(`direct credential intake must document ${phrase}`);
  }
  if (credentialIntakeExamples.length === 0) fail(`direct credential intake 202 response must include a semantic example`);
  for (const [index, credentialExample] of credentialIntakeExamples.entries()) {
    if (credentialExample?.account?.lifecycle_state !== "pending_validation") {
      fail(`direct credential intake 202 example ${index + 1} must expose pending_validation before server-owned validation completes`);
    }
    if (exampleClaimsProbeSuccess(credentialExample)) {
      fail(`direct credential intake 202 example ${index + 1} must not claim probe success`);
    }
  }

  const oauthStartOperation = paths["/provider-accounts/{provider_account_id}/oauth-authorizations"]?.post;
  const oauthStartResponses = oauthStartOperation?.responses || {};
  const oauthStartText = gatherText(oauthStartOperation).join(" ").toLowerCase();
  if (!JSON.stringify(oauthStartResponses["400"] || {}).includes("ErrorInvalidRequest")) {
    fail(`OAuth authorization start must expose invalid_request for unsupported Auth Mode or purpose`);
  }
  if (!JSON.stringify(oauthStartResponses["409"] || {}).includes("ErrorAccountNotUsable")) {
    fail(`OAuth authorization start must expose account_not_usable for lifecycle rejection`);
  }
  if (!oauthStartText.includes("disable intent remains sticky across authorization, exchange, and probe")) {
    fail(`OAuth authorization start must document sticky disable intent across connect and reauthentication`);
  }
  if (!oauthStartText.includes("reauthentication start is rejected while any authorization, validation, replacement, or one-shot probe marker is in flight")) {
    fail(`OAuth authorization start must document the complete reauthentication single-flight gate`);
  }

  const oauthPollOperation = paths["/provider-accounts/{provider_account_id}/oauth-authorizations/{authorization_id}"]?.get;
  const oauthPollText = gatherText(oauthPollOperation).join(" ").toLowerCase();
  const oauthPollExamples = responseExamples(oauthPollOperation, "200");
  for (const phrase of ["failed is terminal", "complete_oauth remediation", "stores no credential", "connect failure restores draft", "preserves disabled intent"]) {
    if (!oauthPollText.includes(phrase)) fail(`OAuth authorization poll must document ${phrase}`);
  }
  const oauthFailureExamples = oauthPollExamples.filter((example) => example?.status === "failed");
  if (oauthFailureExamples.length === 0) fail(`OAuth authorization poll must include a failed/remediation example`);
  for (const [index, example] of oauthFailureExamples.entries()) {
    if (example.remediation !== "complete_oauth") {
      fail(`OAuth failed example ${index + 1} must use safe complete_oauth remediation`);
    }
  }

  const reauthenticationOperation = paths["/provider-accounts/{provider_account_id}/reauthentication"]?.post;
  const reauthenticationExamples = responseExamples(reauthenticationOperation, "202");
  const reauthenticationText = gatherText(reauthenticationOperation).join(" ").toLowerCase();
  const reauthenticationConflict = JSON.stringify(reauthenticationOperation?.responses?.["409"] || {});
  const currentAccountOperation = paths["/provider-accounts/{provider_account_id}"]?.get;
  const currentAccountExamples = responseExamples(currentAccountOperation, "200");
  const currentVersionsByAccount = new Map();

  if (currentAccountExamples.length === 0) fail(`GET Provider Account 200 response must include a current-version example`);
  for (const [index, currentExample] of currentAccountExamples.entries()) {
    const accountId = currentExample?.provider_account_id;
    const version = currentExample?.credential?.version;
    if (typeof accountId !== "string" || !Number.isInteger(version) || version < 1) {
      fail(`GET Provider Account example ${index + 1} must include provider_account_id and a positive current credential version`);
      continue;
    }
    if (currentVersionsByAccount.has(accountId) && currentVersionsByAccount.get(accountId) !== version) {
      fail(`GET Provider Account examples disagree on current credential version for ${accountId}`);
      continue;
    }
    currentVersionsByAccount.set(accountId, version);
  }

  if (reauthenticationExamples.length === 0) fail(`reauthentication 202 response must include a semantic example`);
  for (const [index, reauthenticationExample] of reauthenticationExamples.entries()) {
    const account = reauthenticationExample?.account;
    if (account?.lifecycle_state !== "pending_validation") {
      fail(`reauthentication 202 example ${index + 1} must expose pending_validation before server-owned validation completes`);
    }
    if (exampleClaimsProbeSuccess(reauthenticationExample)) {
      fail(`reauthentication 202 example ${index + 1} must not claim healthy probe success before cutover`);
    }
    if ("pending_version" in (account?.credential || {})) {
      fail(`reauthentication 202 example ${index + 1} must not expose an internal pending_version`);
    }

    const accountId = account?.provider_account_id;
    const version = account?.credential?.version;
    if (typeof accountId !== "string" || !Number.isInteger(version) || version < 1) {
      fail(`reauthentication 202 example ${index + 1} must include provider_account_id and the positive old current credential version`);
      continue;
    }
    if (!currentVersionsByAccount.has(accountId)) {
      fail(`reauthentication 202 example ${index + 1} has no matching current Provider Account example for ${accountId}`);
    } else if (version !== currentVersionsByAccount.get(accountId)) {
      fail(`reauthentication 202 example ${index + 1} must preserve the old public current credential version`);
    }
  }
  if (!reauthenticationText.includes("public credential version remains the old current version until cutover")) fail(`reauthentication 202 response must not publish the pending version as current`);
  if (!reauthenticationText.includes("pending-version provider_probe")) fail(`reauthentication must document pending-version provider_probe cutover`);
  for (const phrase of ["observable pending_validation", "server-owned validation", "revokes/discards the pending version", "safe reauthenticate remediation", "direct replacement is rejected while any authorization, validation, replacement, or one-shot probe marker is in flight"]) {
    if (!reauthenticationText.includes(phrase)) fail(`reauthentication must document ${phrase}`);
  }
  if (!reauthenticationConflict.includes("ErrorAccountNotUsable") || reauthenticationConflict.includes("ErrorAuthModeUnavailable")) {
    fail(`reauthentication 409 must represent account_not_usable lifecycle rejection`);
  }

  const noDecryptOperations = [
    ["GET", "/provider-accounts", paths["/provider-accounts"]?.get],
    ["GET", "/provider-accounts/{provider_account_id}", paths["/provider-accounts/{provider_account_id}"]?.get],
    ["DELETE", "/provider-accounts/{provider_account_id}", paths["/provider-accounts/{provider_account_id}"]?.delete],
    ["POST", "/provider-accounts/{provider_account_id}/disable", paths["/provider-accounts/{provider_account_id}/disable"]?.post],
    ["GET", "/provider-accounts/{provider_account_id}/oauth-authorizations/{authorization_id}", paths["/provider-accounts/{provider_account_id}/oauth-authorizations/{authorization_id}"]?.get],
    ["GET", "/provider-accounts/{provider_account_id}/capability-snapshot", paths["/provider-accounts/{provider_account_id}/capability-snapshot"]?.get],
    ["GET", "/routing-policy", paths["/routing-policy"]?.get],
    ["PUT", "/routing-policy", paths["/routing-policy"]?.put],
  ];
  for (const [method, pathKey, operation] of noDecryptOperations) {
    if (!gatherText(operation).join(" ").toLowerCase().includes("no vault decrypt")) fail(`${method} ${pathKey} must explicitly document no vault decrypt`);
  }

  const examples = collectExamples(doc);
  examples.forEach((item) => scanExampleForSecrets(item.value, item.label));
  const validatedExamples = validateExamplesWithPython(doc, examples);

  const componentExamples = doc.components?.examples || {};
  for (const [name, code] of Object.entries(REQUIRED_ERROR_EXAMPLES)) {
    const example = componentExamples[name]?.value;
    if (!example) fail(`missing required error example ${name}`);
    else if (example.code !== code) fail(`${name}.code must be ${code}`);
  }
  const notFound = componentExamples.ErrorResourceNotFound?.value || {};
  if ("resource_reference" in notFound || JSON.stringify(notFound).includes("provider_account_id")) fail(`resource_not_found example must not carry a resource reference`);

  const corpus = gatherText(doc).join(" ").toLowerCase();
  for (const phrase of [
    "retention hold may retain unusable encrypted evidence but never restores",
    "cheapest same-tenant auth/capability-proving path",
    "health success cannot bypass",
    "fallback defaults false",
    "observed model slugs",
  ]) {
    if (!corpus.includes(phrase)) fail(`contract descriptions must include invariant phrase: ${phrase}`);
  }

  if (failures.length) {
    console.error(`FAIL: ${failures.length} management contract validation error(s)`);
    failures.forEach((message) => console.error(`- ${message}`));
    process.exit(1);
  }

  console.log(`PASS: management OpenAPI prototype validated (${REQUIRED_OPERATIONS.length} operations, ${examples.length} examples collected, ${validatedExamples} schema-validated)`);
}

main();
