import crypto from "node:crypto";
import { access, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import {
  validateEvidenceCapabilityMatrices,
  validateHumanSpecification,
} from "./lib/provider-gateway-spec-markdown.mjs";

const defaultManifest =
  "docs/spec/provider-gateway-implementation-ready-manifest.json";

const contractPath = fileURLToPath(
  new URL("./provider-gateway-implementation-spec-contract.json", import.meta.url),
);
const productionContract = JSON.parse(await readFile(contractPath, "utf8"));
function sortedUnique(values) {
  return [...new Set(values)].sort();
}

function normalizedText(value) {
  return value.replace(/\r\n/g, "\n").trimEnd() + "\n";
}

function canonicalize(value) {
  if (Array.isArray(value)) {
    return value.map(canonicalize);
  }
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(
      Object.keys(value)
        .sort()
        .map((key) => [key, canonicalize(value[key])]),
    );
  }
  return value;
}

function semanticHash(value) {
  const serialized =
    typeof value === "string"
      ? normalizedText(value)
      : JSON.stringify(canonicalize(value));
  return crypto.createHash("sha256").update(serialized).digest("hex");
}

function assertSemanticHash(value, expected, message) {
  if (expected && semanticHash(value) !== expected) {
    throw new Error(message);
  }
}

function assertExactStringSet(actual, expected, label) {
  requireStringArray(actual, `${label} must be a non-empty string array`);
  const actualSorted = sortedUnique(actual);
  const expectedSorted = sortedUnique(expected);
  if (
    actual.length !== actualSorted.length ||
    JSON.stringify(actualSorted) !== JSON.stringify(expectedSorted)
  ) {
    throw new Error(`${label} does not match validator-owned contract`);
  }
}

function normalizeExpectedContract(contract) {
  return {
    ...contract,
    authority: contract.authority ?? null,
    authModes: contract.authModes.map((mode) => ({
      ...mode,
      claims: Object.fromEntries(
        Object.entries(mode.claims).map(([operation, claim]) => {
          requireText(
            claim?.status,
            `validator-owned capability ${mode.id}/${operation} status is required`,
          );
          requireText(
            claim?.evidence,
            `validator-owned capability ${mode.id}/${operation} evidence is required`,
          );
          return [operation, { ...claim }];
        }),
      ),
    })),
  };
}

function requireText(value, message) {
  if (typeof value !== "string" || value.trim() === "") {
    throw new Error(message);
  }
}

function requireStringArray(value, message) {
  if (
    !Array.isArray(value) ||
    value.length === 0 ||
    value.some((item) => typeof item !== "string" || item.trim() === "")
  ) {
    throw new Error(message);
  }
}

function authorityPaths(authority) {
  requireText(authority?.glossary, "authority.glossary is required");
  requireText(
    authority?.stable_public_api,
    "authority.stable_public_api is required",
  );
  requireStringArray(
    authority?.architecture_decisions,
    "authority.architecture_decisions must be a non-empty string array",
  );
  requireStringArray(
    authority?.normative_specs,
    "authority.normative_specs must be a non-empty string array",
  );
  requireStringArray(
    authority?.evidence_sources,
    "authority.evidence_sources must be a non-empty string array",
  );

  return [
    authority.glossary,
    authority.stable_public_api,
    ...authority.architecture_decisions,
    ...authority.normative_specs,
    ...authority.evidence_sources,
  ];
}

async function assertFilesExist(root, relativePaths) {
  const resolvedRoot = path.resolve(root);
  for (const relativePath of relativePaths) {
    const resolvedPath = path.resolve(resolvedRoot, relativePath);
    const relativeToRoot = path.relative(resolvedRoot, resolvedPath);
    if (
      path.isAbsolute(relativePath) ||
      relativeToRoot === ".." ||
      relativeToRoot.startsWith(`..${path.sep}`)
    ) {
      throw new Error(`repository path escapes root: ${relativePath}`);
    }
    try {
      await access(resolvedPath);
    } catch {
      throw new Error(`authority file does not exist: ${relativePath}`);
    }
  }
}

async function validateAuthorityFingerprints(root, contract) {
  const fingerprints = contract.authorityFingerprints;
  if (fingerprints === undefined) {
    return;
  }
  if (fingerprints === null || typeof fingerprints !== "object" || Array.isArray(fingerprints)) {
    throw new Error("validator-owned authority fingerprints must be an object");
  }

  const expectedPaths = authorityPaths({
    glossary: contract.authority.glossary,
    stable_public_api: contract.authority.stablePublicApi,
    architecture_decisions: contract.authority.architectureDecisions,
    normative_specs: contract.authority.normativeSpecs,
    evidence_sources: contract.authority.evidenceSources,
  });
  assertExactStringSet(
    Object.keys(fingerprints),
    expectedPaths,
    "validator-owned authority fingerprint paths",
  );

  for (const relativePath of expectedPaths) {
    const contents = await readFile(path.join(root, relativePath), "utf8");
    assertSemanticHash(
      contents,
      fingerprints[relativePath],
      `authority content does not match validator-owned fingerprint: ${relativePath}`,
    );
  }
}

function validateAuthority(manifest, contract) {
  if (!contract.authority) {
    return;
  }
  if (manifest.authority.glossary !== contract.authority.glossary) {
    throw new Error("authority.glossary does not match validator-owned contract");
  }
  if (
    manifest.authority.stable_public_api !== contract.authority.stablePublicApi
  ) {
    throw new Error(
      "authority.stable_public_api does not match validator-owned contract",
    );
  }
  assertExactStringSet(
    manifest.authority.architecture_decisions,
    contract.authority.architectureDecisions,
    "authority.architecture_decisions",
  );
  assertExactStringSet(
    manifest.authority.normative_specs,
    contract.authority.normativeSpecs,
    "authority.normative_specs",
  );
  assertExactStringSet(
    manifest.authority.evidence_sources,
    contract.authority.evidenceSources,
    "authority.evidence_sources",
  );
}

function validateCapabilities(manifest, contract) {
  const canonical = manifest.canonical_capability_statuses;
  assertExactStringSet(
    canonical,
    contract.canonicalStatuses,
    "canonical_capability_statuses",
  );
  const expected = new Set(contract.canonicalStatuses);

  if (!Array.isArray(manifest.capabilities) || manifest.capabilities.length === 0) {
    throw new Error("capabilities must be a non-empty array");
  }

  const requiredAuthModes = manifest.completion_gate?.required_auth_modes;
  const requiredRiskStatuses =
    manifest.completion_gate?.required_auth_mode_risk_statuses;
  const requiredOperations =
    manifest.completion_gate?.required_capability_operations;
  requireStringArray(
    requiredAuthModes,
    "completion_gate.required_auth_modes must be a non-empty string array",
  );
  requireStringArray(
    requiredOperations,
    "completion_gate.required_capability_operations must be a non-empty string array",
  );
  if (
    requiredRiskStatuses === null ||
    typeof requiredRiskStatuses !== "object" ||
    Array.isArray(requiredRiskStatuses)
  ) {
    throw new Error(
      "completion_gate.required_auth_mode_risk_statuses must be an object",
    );
  }
  const expectedAuthModeIds = contract.authModes.map((mode) => mode.id);
  const expectedOperations = Object.keys(contract.authModes[0].claims);
  assertExactStringSet(
    requiredAuthModes,
    expectedAuthModeIds,
    "completion_gate.required_auth_modes",
  );
  assertExactStringSet(
    requiredOperations,
    expectedOperations,
    "completion_gate.required_capability_operations",
  );
  const expectedRiskStatuses = Object.fromEntries(
    contract.authModes.map((mode) => [mode.id, mode.riskStatus]),
  );
  if (semanticHash(requiredRiskStatuses) !== semanticHash(expectedRiskStatuses)) {
    throw new Error(
      "completion_gate.required_auth_mode_risk_statuses does not match validator-owned contract",
    );
  }
  const declaredEvidence = new Set(manifest.authority.evidence_sources);
  const modesById = new Map();
  for (const mode of manifest.capabilities) {
    requireText(mode?.auth_mode, "capability auth_mode is required");
    if (modesById.has(mode.auth_mode)) {
      throw new Error(`duplicate Auth Mode: ${mode.auth_mode}`);
    }
    modesById.set(mode.auth_mode, mode);
  }
  for (const authMode of requiredAuthModes) {
    if (!modesById.has(authMode)) {
      throw new Error(`missing required Auth Mode: ${authMode}`);
    }
  }
  for (const authMode of modesById.keys()) {
    if (!requiredAuthModes.includes(authMode)) {
      throw new Error(`unexpected Auth Mode: ${authMode}`);
    }
  }

  let claims = 0;
  for (const mode of manifest.capabilities) {
    const expectedMode = contract.authModes.find(
      (candidate) => candidate.id === mode.auth_mode,
    );
    const requiredRiskStatus = requiredRiskStatuses[mode.auth_mode];
    requireText(
      requiredRiskStatus,
      `completion gate is missing risk status for ${mode.auth_mode}`,
    );
    if (mode.risk_status !== requiredRiskStatus) {
      throw new Error(
        `capability ${mode.auth_mode} risk status must be ${requiredRiskStatus}`,
      );
    }
    if (!Array.isArray(mode.claims) || mode.claims.length === 0) {
      throw new Error(`capability ${mode.auth_mode} must contain claims`);
    }

    const claimsByOperation = new Map();
    for (const claim of mode.claims) {
      requireText(
        claim?.operation,
        `capability ${mode.auth_mode} operation is required`,
      );
      if (claimsByOperation.has(claim.operation)) {
        throw new Error(
          `duplicate capability operation: ${mode.auth_mode}/${claim.operation}`,
        );
      }
      claimsByOperation.set(claim.operation, claim);
    }
    for (const operation of requiredOperations) {
      if (!claimsByOperation.has(operation)) {
        throw new Error(
          `capability ${mode.auth_mode} is missing required operation: ${operation}`,
        );
      }
    }
    for (const operation of claimsByOperation.keys()) {
      if (!requiredOperations.includes(operation)) {
        throw new Error(
          `unexpected capability operation: ${mode.auth_mode}/${operation}`,
        );
      }
    }

    for (const claim of mode.claims) {
      requireText(
        claim?.evidence,
        `capability ${mode.auth_mode}/${claim?.operation ?? "unknown"} evidence is required`,
      );
      if (!declaredEvidence.has(claim.evidence)) {
        throw new Error(
          `capability ${mode.auth_mode}/${claim.operation} evidence is not declared authority`,
        );
      }
      if (!expected.has(claim?.status)) {
        throw new Error(`non-canonical capability status: ${claim?.status}`);
      }
      const expectedClaim = expectedMode.claims[claim.operation];
      if (claim.status !== expectedClaim.status) {
        throw new Error(
          `capability ${mode.auth_mode}/${claim.operation} status must be ${expectedClaim.status}`,
        );
      }
      if (claim.evidence !== expectedClaim.evidence) {
        throw new Error(
          `capability ${mode.auth_mode}/${claim.operation} evidence must be ${expectedClaim.evidence}`,
        );
      }
      claims += 1;
    }
  }

  return claims;
}

function validateProviderPolicies(manifest, contract) {
  if (!contract.providerPolicies) {
    return;
  }
  if (semanticHash(manifest.provider_policies) !== semanticHash(contract.providerPolicies)) {
    throw new Error("provider policies do not match validator-owned contract");
  }
}

function declaredAuthoritySet(manifest) {
  return new Set(authorityPaths(manifest.authority));
}

function validateRegister(
  items,
  {
    collectionName,
    itemName,
    requiredIds,
    requiredIdsLabel,
    expectedIds,
    semanticHash: expectedSemanticHash,
    semanticHashMessage,
    validateItem,
  },
) {
  if (!Array.isArray(items) || items.length === 0) {
    throw new Error(`${collectionName} must be a non-empty array`);
  }

  const ids = new Set();
  for (const item of items) {
    requireText(item?.id, `${itemName} id is required`);
    if (ids.has(item.id)) {
      throw new Error(`duplicate ${itemName} id: ${item.id}`);
    }
    ids.add(item.id);
  }
  for (const item of items) {
    validateItem(item, ids);
  }

  requireStringArray(
    requiredIds,
    `${requiredIdsLabel} must be a non-empty string array`,
  );
  assertExactStringSet(requiredIds, expectedIds, requiredIdsLabel);
  assertExactStringSet([...ids], expectedIds, `${itemName} ids`);
  assertSemanticHash(items, expectedSemanticHash, semanticHashMessage);
  for (const requiredId of requiredIds) {
    if (!ids.has(requiredId)) {
      throw new Error(`missing required ${itemName}: ${requiredId}`);
    }
  }

  return { count: items.length, ids };
}

function validateDecisions(manifest, contract) {
  const dimensions = [
    "observable_behavior",
    "failure_semantics",
    "security_impact",
  ];
  const declaredAuthority = declaredAuthoritySet(manifest);
  const requiredDecisionIds = manifest.completion_gate?.required_decision_ids;
  return validateRegister(manifest.decisions, {
    collectionName: "decisions",
    itemName: "decision",
    requiredIds: requiredDecisionIds,
    requiredIdsLabel: "completion_gate.required_decision_ids",
    expectedIds: contract.decisionIds,
    semanticHash: contract.semanticHashes?.decisions,
    semanticHashMessage:
      "decision ledger does not match validator-owned semantic contract",
    validateItem(decision) {
      for (const dimension of dimensions) {
        requireText(
          decision[dimension],
          `decision ${decision.id} is missing ${dimension}`,
        );
      }
      requireStringArray(
        decision.dependencies,
        `decision ${decision.id} is missing dependencies`,
      );
      for (const dependency of decision.dependencies) {
        if (!declaredAuthority.has(dependency)) {
          throw new Error(
            `decision ${decision.id} dependency is not declared authority: ${dependency}`,
          );
        }
      }
    },
  });
}

function validateImplementationSlices(manifest, contract) {
  const declaredAuthority = declaredAuthoritySet(manifest);
  const requiredSliceIds =
    manifest.completion_gate?.required_implementation_slice_ids;
  const { ids: knownIds } = validateRegister(manifest.implementation_slices, {
    collectionName: "implementation_slices",
    itemName: "implementation slice",
    requiredIds: requiredSliceIds,
    requiredIdsLabel: "completion_gate.required_implementation_slice_ids",
    expectedIds: contract.implementationSliceIds,
    semanticHash: contract.semanticHashes?.implementationSlices,
    semanticHashMessage:
      "implementation slices do not match validator-owned semantic contract",
    validateItem(slice, sliceIds) {
      if (!Array.isArray(slice.depends_on)) {
        throw new Error(
          `implementation slice ${slice.id} depends_on must be an array`,
        );
      }
      for (const dependency of slice.depends_on) {
        if (!sliceIds.has(dependency)) {
          throw new Error(
            `implementation slice ${slice.id} has unknown dependency: ${dependency}`,
          );
        }
      }
      requireStringArray(
        slice.authority,
        `implementation slice ${slice.id} is missing authority`,
      );
      for (const authority of slice.authority) {
        if (!declaredAuthority.has(authority)) {
          throw new Error(
            `implementation slice ${slice.id} authority is not declared: ${authority}`,
          );
        }
      }
      requireText(
        slice.proof_seam,
        `implementation slice ${slice.id} is missing proof_seam`,
      );
    },
  });

  const dependenciesById = new Map(
    manifest.implementation_slices.map((slice) => [slice.id, slice.depends_on]),
  );
  const visiting = new Set();
  const visited = new Set();
  const visit = (sliceId, stack) => {
    if (visiting.has(sliceId)) {
      throw new Error(
        `implementation slice dependency cycle: ${[...stack, sliceId].join(" -> ")}`,
      );
    }
    if (visited.has(sliceId)) {
      return;
    }
    visiting.add(sliceId);
    for (const dependency of dependenciesById.get(sliceId)) {
      visit(dependency, [...stack, sliceId]);
    }
    visiting.delete(sliceId);
    visited.add(sliceId);
  };
  for (const sliceId of knownIds) {
    visit(sliceId, []);
  }
}

function validateDeferredItems(manifest, contract) {
  const requiredDeferredIds =
    manifest.completion_gate?.required_deferred_item_ids;
  return validateRegister(manifest.deferred_items, {
    collectionName: "deferred_items",
    itemName: "deferred item",
    requiredIds: requiredDeferredIds,
    requiredIdsLabel: "completion_gate.required_deferred_item_ids",
    expectedIds: contract.deferredItemIds,
    semanticHash: contract.semanticHashes?.deferredItems,
    semanticHashMessage:
      "deferred register does not match validator-owned semantic contract",
    validateItem(item) {
      requireText(item.reason, `deferred item ${item.id} is missing reason`);
      requireStringArray(
        item.dependencies,
        `deferred item ${item.id} is missing dependencies`,
      );
      requireText(
        item.reopen_trigger,
        `deferred item ${item.id} is missing reopen_trigger`,
      );
    },
  });
}

function validatePlanningClosure(manifest, contract) {
  if (!Array.isArray(contract.planningDomains) || contract.planningDomains.length === 0) {
    throw new Error("validator-owned planning domains must be a non-empty array");
  }
  if (!Array.isArray(manifest.planning_closure) || manifest.planning_closure.length === 0) {
    throw new Error("planning_closure must be a non-empty array");
  }

  const requiredDomains = manifest.completion_gate?.required_planning_domains;
  const expectedDomainIds = contract.planningDomains.map((domain) => domain.id);
  assertExactStringSet(
    requiredDomains,
    expectedDomainIds,
    "completion_gate.required_planning_domains",
  );

  const closuresByDomain = new Map();
  const assignedDecisions = new Set();
  const knownDecisions = new Set(manifest.decisions.map((decision) => decision.id));
  for (const closure of manifest.planning_closure) {
    requireText(closure?.domain, "planning closure domain is required");
    if (closuresByDomain.has(closure.domain)) {
      throw new Error(`duplicate planning closure domain: ${closure.domain}`);
    }
    closuresByDomain.set(closure.domain, closure);
  }
  assertExactStringSet(
    [...closuresByDomain.keys()],
    expectedDomainIds,
    "planning closure domains",
  );

  for (const expectedDomain of contract.planningDomains) {
    const closure = closuresByDomain.get(expectedDomain.id);
    if (closure.disposition !== "locked") {
      throw new Error(`planning domain ${expectedDomain.id} must be locked`);
    }
    requireStringArray(
      closure.decision_ids,
      `planning domain ${expectedDomain.id} decision ids must be a non-empty string array`,
    );
    assertExactStringSet(
      closure.decision_ids,
      expectedDomain.decisionIds,
      `planning domain ${expectedDomain.id} decision ids`,
    );
    for (const decisionId of closure.decision_ids) {
      if (!knownDecisions.has(decisionId)) {
        throw new Error(
          `planning domain ${expectedDomain.id} references unknown decision: ${decisionId}`,
        );
      }
      if (assignedDecisions.has(decisionId)) {
        throw new Error(`decision is assigned to multiple planning domains: ${decisionId}`);
      }
      assignedDecisions.add(decisionId);
    }
  }
  assertExactStringSet(
    [...assignedDecisions],
    [...knownDecisions],
    "planning closure decision coverage",
  );

  return manifest.planning_closure.length;
}

async function validateSpecificationSections(root, manifest, contract) {
  requireText(manifest.specification, "specification path is required");
  if (
    contract.specificationPath &&
    manifest.specification !== contract.specificationPath
  ) {
    throw new Error("specification path does not match validator-owned contract");
  }
  const requiredSections = manifest.completion_gate?.required_sections;
  assertExactStringSet(
    requiredSections,
    contract.requiredSections,
    "completion_gate.required_sections",
  );

  const specPath = path.join(root, manifest.specification);
  let specification;
  try {
    specification = await readFile(specPath, "utf8");
  } catch {
    throw new Error(`specification file does not exist: ${manifest.specification}`);
  }

  validateHumanSpecification({ specification, requiredSections, contract });
  assertSemanticHash(
    specification,
    contract.semanticHashes?.specification,
    "human specification does not match validator-owned semantic contract",
  );
}

export async function validateImplementationSpecification({
  manifestPath = defaultManifest,
  root = process.cwd(),
  expectedContract = productionContract,
} = {}) {
  const contract = normalizeExpectedContract(expectedContract);
  const absoluteManifestPath = path.isAbsolute(manifestPath)
    ? manifestPath
    : path.join(root, manifestPath);
  const manifest = JSON.parse(await readFile(absoluteManifestPath, "utf8"));

  if (manifest.schema_version !== 1) {
    throw new Error("schema_version must be 1");
  }
  if (manifest.issue !== contract.issue) {
    throw new Error(`gate issue must be ${contract.issue}`);
  }
  if (manifest.implementation_issue !== contract.implementationIssue) {
    throw new Error(`implementation issue must be ${contract.implementationIssue}`);
  }
  if (manifest.completion_gate?.implementation_issue_must_differ !== true) {
    throw new Error("implementation issue separation must be enforced");
  }
  if (manifest.status !== "implementation_ready") {
    throw new Error("status must be implementation_ready");
  }

  const referencedAuthority = authorityPaths(manifest.authority);
  validateAuthority(manifest, contract);
  await assertFilesExist(root, [manifest.specification, ...referencedAuthority]);
  await validateAuthorityFingerprints(root, contract);
  const capabilityClaims = validateCapabilities(manifest, contract);
  await validateEvidenceCapabilityMatrices({ root, contract });
  validateProviderPolicies(manifest, contract);
  const { count: decisions } = validateDecisions(manifest, contract);
  const planningDomains = validatePlanningClosure(manifest, contract);
  validateImplementationSlices(manifest, contract);
  const { count: deferredItems } = validateDeferredItems(manifest, contract);
  await validateSpecificationSections(root, manifest, contract);

  return {
    issue: manifest.issue,
    implementationIssue: manifest.implementation_issue,
    capabilityClaims,
    decisions,
    planningDomains,
    deferredItems,
    authorityFiles: referencedAuthority.length,
    implementationSlices: manifest.implementation_slices.length,
  };
}

async function main() {
  const manifestPath = process.argv[2] ?? defaultManifest;
  const result = await validateImplementationSpecification({ manifestPath });
  console.log(
    `PASS: implementation-ready Provider Gateway specification ` +
      `(issue #${result.issue}, implementation #${result.implementationIssue}, ` +
      `${result.capabilityClaims} capability claims, ${result.decisions} decisions, ` +
      `${result.planningDomains} planning domains, ${result.implementationSlices} slices, ` +
      `${result.deferredItems} deferred items, ` +
      `${result.authorityFiles} authority files)`,
  );
}

const invokedPath = process.argv[1] ? path.resolve(process.argv[1]) : "";
if (invokedPath === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(`FAIL: ${error.message}`);
    process.exitCode = 1;
  });
}
