import { access, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const defaultManifest =
  "docs/spec/provider-gateway-implementation-ready-manifest.json";

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

function validateCapabilities(manifest) {
  const canonical = manifest.canonical_capability_statuses;
  requireStringArray(
    canonical,
    "canonical_capability_statuses must be a non-empty string array",
  );
  const expected = new Set([
    "verified",
    "conditionally_supported",
    "unsupported",
    "unverified",
  ]);
  if (
    canonical.length !== expected.size ||
    canonical.some((item) => !expected.has(item))
  ) {
    throw new Error(
      "canonical_capability_statuses must contain exactly verified, conditionally_supported, unsupported, and unverified",
    );
  }

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
      claims += 1;
    }
  }

  return claims;
}

function declaredAuthoritySet(manifest) {
  return new Set(authorityPaths(manifest.authority));
}

function validateDecisions(manifest) {
  if (!Array.isArray(manifest.decisions) || manifest.decisions.length === 0) {
    throw new Error("decisions must be a non-empty array");
  }

  const dimensions = [
    "observable_behavior",
    "failure_semantics",
    "security_impact",
  ];
  const declaredAuthority = declaredAuthoritySet(manifest);
  const decisionIds = new Set();
  for (const decision of manifest.decisions) {
    requireText(decision?.id, "decision id is required");
    if (decisionIds.has(decision.id)) {
      throw new Error(`duplicate decision id: ${decision.id}`);
    }
    decisionIds.add(decision.id);
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
  }

  const requiredDecisionIds = manifest.completion_gate?.required_decision_ids;
  requireStringArray(
    requiredDecisionIds,
    "completion_gate.required_decision_ids must be a non-empty string array",
  );
  for (const decisionId of requiredDecisionIds) {
    if (!decisionIds.has(decisionId)) {
      throw new Error(`missing required decision: ${decisionId}`);
    }
  }

  return manifest.decisions.length;
}

function validateImplementationSlices(manifest) {
  if (
    !Array.isArray(manifest.implementation_slices) ||
    manifest.implementation_slices.length === 0
  ) {
    throw new Error("implementation_slices must be a non-empty array");
  }

  const declaredAuthority = declaredAuthoritySet(manifest);
  const knownIds = new Set();
  for (const slice of manifest.implementation_slices) {
    requireText(slice?.id, "implementation slice id is required");
    if (knownIds.has(slice.id)) {
      throw new Error(`duplicate implementation slice id: ${slice.id}`);
    }
    knownIds.add(slice.id);
  }
  for (const slice of manifest.implementation_slices) {
    if (!Array.isArray(slice.depends_on)) {
      throw new Error(
        `implementation slice ${slice.id} depends_on must be an array`,
      );
    }
    for (const dependency of slice.depends_on) {
      if (!knownIds.has(dependency)) {
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
  }

  const requiredSliceIds =
    manifest.completion_gate?.required_implementation_slice_ids;
  requireStringArray(
    requiredSliceIds,
    "completion_gate.required_implementation_slice_ids must be a non-empty string array",
  );
  for (const sliceId of requiredSliceIds) {
    if (!knownIds.has(sliceId)) {
      throw new Error(`missing required implementation slice: ${sliceId}`);
    }
  }

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

function validateDeferredItems(manifest) {
  if (
    !Array.isArray(manifest.deferred_items) ||
    manifest.deferred_items.length === 0
  ) {
    throw new Error("deferred_items must be a non-empty array");
  }

  const deferredIds = new Set();
  for (const item of manifest.deferred_items) {
    requireText(item?.id, "deferred item id is required");
    if (deferredIds.has(item.id)) {
      throw new Error(`duplicate deferred item id: ${item.id}`);
    }
    deferredIds.add(item.id);
    requireText(item.reason, `deferred item ${item.id} is missing reason`);
    requireStringArray(
      item.dependencies,
      `deferred item ${item.id} is missing dependencies`,
    );
    requireText(
      item.reopen_trigger,
      `deferred item ${item.id} is missing reopen_trigger`,
    );
  }

  const requiredDeferredIds =
    manifest.completion_gate?.required_deferred_item_ids;
  requireStringArray(
    requiredDeferredIds,
    "completion_gate.required_deferred_item_ids must be a non-empty string array",
  );
  for (const deferredId of requiredDeferredIds) {
    if (!deferredIds.has(deferredId)) {
      throw new Error(`missing required deferred item: ${deferredId}`);
    }
  }

  return manifest.deferred_items.length;
}

async function validateSpecificationSections(root, manifest) {
  requireText(manifest.specification, "specification path is required");
  const requiredSections = manifest.completion_gate?.required_sections;
  requireStringArray(
    requiredSections,
    "completion_gate.required_sections must be a non-empty string array",
  );

  const specPath = path.join(root, manifest.specification);
  let specification;
  try {
    specification = await readFile(specPath, "utf8");
  } catch {
    throw new Error(`specification file does not exist: ${manifest.specification}`);
  }

  for (const section of requiredSections) {
    const escapedSection = section.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    const heading = new RegExp(`^## ${escapedSection}\\s*$`, "m");
    if (!heading.test(specification)) {
      throw new Error(`specification is missing required section: ${section}`);
    }
  }
}

export async function validateImplementationSpecification({
  manifestPath = defaultManifest,
  root = process.cwd(),
} = {}) {
  const absoluteManifestPath = path.isAbsolute(manifestPath)
    ? manifestPath
    : path.join(root, manifestPath);
  const manifest = JSON.parse(await readFile(absoluteManifestPath, "utf8"));

  if (manifest.schema_version !== 1) {
    throw new Error("schema_version must be 1");
  }
  if (!Number.isInteger(manifest.issue) || manifest.issue <= 0) {
    throw new Error("issue must be a positive integer");
  }
  if (
    !Number.isInteger(manifest.implementation_issue) ||
    manifest.implementation_issue <= 0
  ) {
    throw new Error("implementation_issue must be a positive integer");
  }
  if (
    manifest.completion_gate?.implementation_issue_must_differ === true &&
    manifest.issue === manifest.implementation_issue
  ) {
    throw new Error("implementation issue must differ from gate issue");
  }
  if (manifest.status !== "implementation_ready") {
    throw new Error("status must be implementation_ready");
  }

  const referencedAuthority = authorityPaths(manifest.authority);
  await assertFilesExist(root, [manifest.specification, ...referencedAuthority]);
  const capabilityClaims = validateCapabilities(manifest);
  const decisions = validateDecisions(manifest);
  validateImplementationSlices(manifest);
  const deferredItems = validateDeferredItems(manifest);
  await validateSpecificationSections(root, manifest);

  return {
    issue: manifest.issue,
    implementationIssue: manifest.implementation_issue,
    capabilityClaims,
    decisions,
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
      `${result.implementationSlices} slices, ${result.deferredItems} deferred items, ` +
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
