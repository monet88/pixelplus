import crypto from "node:crypto";
import { access, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const defaultManifest =
  "docs/spec/provider-gateway-implementation-ready-manifest.json";

const productionContract = {
  issue: 22,
  implementationIssue: 42,
  specificationPath:
    "docs/spec/provider-gateway-implementation-ready-specification.md",
  semanticHashes: {
    decisions:
      "c7c43b9ec2cf69927ae6b8c198fa768abfb946425489f3dbc1560be5d7e8cc24",
    implementationSlices:
      "2866718ff1f53c87cf4c458e72c353e5b7e9bfe48b8b366caa2547ee3e641189",
    deferredItems:
      "b442e659717679cd29a4b40d21b938f43898d92fb750800d55aec528df30041c",
    specification:
      "eb1293b2e57b79568c74a38f569fefeddbdfd42197b9cf3d879883b317def23d",
  },
  canonicalStatuses: [
    "verified",
    "conditionally_supported",
    "unsupported",
    "unverified",
  ],
  authority: {
    glossary: "CONTEXT.md",
    stablePublicApi: "contracts/openapi/pixelplus-public-api-v1.yaml",
    architectureDecisions: [
      "docs/decisions/0008-stable-public-api-contract-policy.md",
      "docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md",
    ],
    normativeSpecs: [
      "docs/spec/tenant-ownership-authorization-invariants.md",
      "docs/spec/auth-mode-risk-envelope-and-kill-criteria.md",
      "docs/spec/client-api-key-lifecycle-and-admission-controls.md",
      "docs/spec/provider-account-connection-and-credential-lifecycle.md",
      "docs/spec/capability-snapshot-and-model-availability-semantics.md",
      "docs/spec/tenant-scoped-routing-fallback-affinity-leases.md",
      "docs/spec/chat-execution-and-streaming-lifecycle.md",
      "docs/spec/asset-exchange-authorization-and-retention-lifecycle.md",
      "docs/spec/durable-render-job-and-output-retry-lifecycle.md",
      "docs/spec/credential-vault-and-sensitive-data-lifecycle.md",
      "docs/spec/canonical-errors-and-retry-ownership.md",
      "docs/spec/provider-account-health-cooldown-and-operator-controls.md",
      "docs/spec/api-versioning-compatibility-idempotency-contract-testing-policy.md",
    ],
    evidenceSources: [
      "docs/spec/research/web-to-api-compliance-risk-evidence.md",
      "docs/spec/research/chatgpt-auth-mode-capability-evidence.md",
      "docs/spec/research/gemini-auth-mode-capability-evidence.md",
      "docs/spec/research/grok-auth-mode-capability-evidence.md",
      "docs/spec/openai-compatible-inference-contract.md",
      "docs/spec/provider-account-and-capability-management-contract.md",
      "contracts/openapi/pixelplus-public-api-v0alpha.yaml",
      "contracts/openapi/pixelplus-management-api-v0alpha.yaml",
      "contracts/openapi/baselines/pixelplus-public-api-v1.0.0.yaml",
    ],
  },
  authModes: [
    {
      id: "chatgpt_web_access",
      label: "ChatGPT Web Access",
      riskStatus: "experimental",
      evidence: "docs/spec/research/chatgpt-auth-mode-capability-evidence.md",
      claims: {
        chat: "conditionally_supported",
        chat_streaming: "conditionally_supported",
        image_generation: "conditionally_supported",
        image_edit: "conditionally_supported",
        inpaint: "conditionally_supported",
      },
    },
    {
      id: "chatgpt_codex_oauth",
      label: "ChatGPT Codex OAuth",
      riskStatus: "gated",
      evidence: "docs/spec/research/chatgpt-auth-mode-capability-evidence.md",
      claims: {
        chat: "conditionally_supported",
        chat_streaming: "conditionally_supported",
        image_generation: "conditionally_supported",
        image_edit: "conditionally_supported",
        inpaint: "conditionally_supported",
      },
    },
    {
      id: "gemini_web_cookie",
      label: "Gemini Web Cookie",
      riskStatus: "experimental",
      evidence: "docs/spec/research/gemini-auth-mode-capability-evidence.md",
      claims: {
        chat: "conditionally_supported",
        chat_streaming: "conditionally_supported",
        image_generation: "conditionally_supported",
        image_edit: "conditionally_supported",
        inpaint: "unsupported",
      },
    },
    {
      id: "gemini_antigravity_oauth",
      label: "Gemini Antigravity OAuth",
      riskStatus: "gated",
      evidence: "docs/spec/research/gemini-auth-mode-capability-evidence.md",
      claims: {
        chat: "conditionally_supported",
        chat_streaming: "conditionally_supported",
        image_generation: "unverified",
        image_edit: "unverified",
        inpaint: "unsupported",
      },
    },
    {
      id: "grok_web_sso",
      label: "Grok Web SSO",
      riskStatus: "prohibited",
      evidence: "docs/spec/research/grok-auth-mode-capability-evidence.md",
      claims: {
        chat: "conditionally_supported",
        chat_streaming: "conditionally_supported",
        image_generation: "conditionally_supported",
        image_edit: "conditionally_supported",
        inpaint: "unsupported",
      },
    },
    {
      id: "grok_xai_oauth",
      label: "Grok xAI OAuth",
      riskStatus: "gated",
      evidence: "docs/spec/research/grok-auth-mode-capability-evidence.md",
      claims: {
        chat: "conditionally_supported",
        chat_streaming: "conditionally_supported",
        image_generation: "conditionally_supported",
        image_edit: "conditionally_supported",
        inpaint: "unsupported",
      },
    },
  ],
  decisionIds: [
    "tenant_ownership_and_authorization",
    "auth_mode_risk_envelope",
    "client_api_key_and_admission",
    "provider_account_and_credential_lifecycle",
    "capability_snapshot_and_model_availability",
    "tenant_routing_fallback_affinity_and_leases",
    "chat_execution_and_streaming",
    "asset_exchange_and_retention",
    "durable_render_jobs_and_output_retry",
    "credential_vault_and_sensitive_data",
    "canonical_errors_idempotency_and_retry_ownership",
    "health_cooldown_circuit_and_operator_controls",
    "stable_public_api_and_compatibility",
    "pure_go_module_seams_and_dependency_budget",
  ],
  implementationSliceIds: [
    "foundation_and_composition",
    "principal_admission_audit_and_replay",
    "provider_account_and_vault",
    "capability_health_and_routing",
    "asset_and_render_execution",
    "chat_execution",
    "provider_adapters_and_full_conformance",
  ],
  deferredItemIds: [
    "D-PERSISTENCE-DRIVER",
    "D-VAULT-CRYPTO-VENDOR",
    "D-JOB-RUNTIME",
    "D-DEPLOYMENT-TOPOLOGY",
    "D-SLO-CANARY-LAUNCH",
    "D-LEGACY-MIGRATION",
    "D-NUMERIC-TUNE",
    "D-PROBE-RATE",
    "D-SNAPSHOT-GRACE",
    "D-ROTATE-GRACE",
    "D-REAUTH-GRACE",
    "D-CODEX-APIKEY-MODE",
    "D-MULTI-ACCT",
    "D-ROUTE-AUTOFALLBACK",
    "D-ROUTE-XMODE",
    "D-CHAT-TOOLS",
    "D-CHAT-RESUME",
    "D-RENDER-RESUME",
    "D-RENDER-UPSTREAM-IDEMPOTENCY",
    "D-RENDER-OUTPUT-RECREATE",
    "D-ASSET-CHUNK",
    "D-ASSET-DEDUPE",
    "D-CHAT-HISTORY",
    "D-BREAK-GLASS",
    "D-TENANT-DELETE-EXPORT",
    "D-JURISDICTION-RETENTION",
    "D-INCIDENT-UPSTREAM-EVIDENCE",
    "D-BATCH-RETRY",
    "D-BILLING",
    "D-NEW-PROVIDERS-OFFICIAL-ADAPTERS",
    "D-COUNSEL-AGENT",
    "D-COUNSEL-RE",
    "D-OAI-TOKEN",
    "D-ANTIGRAVITY-TERMS",
    "D-GROK-ISSUER",
    "D-XAI-COMPETE",
    "D-COMM",
    "D-REGION",
    "D-ASSET-CAP-TUNE",
    "D-ERROR-WIRE",
    "D-RETRY-AT-LEAST-ONCE",
    "D-VAULT-KEY-TOPOLOGY",
    "D-LEGAL-HOLD-CREDENTIAL",
  ],
  requiredSections: [
    "Authority and conflict resolution",
    "Capability evidence ledger",
    "Decision ledger",
    "Implementation work breakdown",
    "Deferred item register",
    "Completion gate",
  ],
};

function sortedUnique(values) {
  return [...new Set(values)].sort();
}

function normalizedText(value) {
  return value.replace(/\r\n/g, "\n").trimEnd() + "\n";
}

function semanticHash(value) {
  const serialized =
    typeof value === "string" ? normalizedText(value) : JSON.stringify(value);
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
      evidence: mode.evidence ?? Object.values(mode.claims)[0]?.evidence,
      claims: Object.fromEntries(
        Object.entries(mode.claims).map(([operation, claim]) => [
          operation,
          typeof claim === "string" ? claim : claim.status,
        ]),
      ),
      claimEvidence: Object.fromEntries(
        Object.entries(mode.claims).map(([operation, claim]) => [
          operation,
          typeof claim === "string" ? mode.evidence : claim.evidence,
        ]),
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
  if (
    JSON.stringify(requiredRiskStatuses) !== JSON.stringify(expectedRiskStatuses)
  ) {
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
      const expectedStatus = expectedMode.claims[claim.operation];
      if (claim.status !== expectedStatus) {
        throw new Error(
          `capability ${mode.auth_mode}/${claim.operation} status must be ${expectedStatus}`,
        );
      }
      const expectedEvidence =
        expectedMode.claimEvidence[claim.operation] ?? expectedMode.evidence;
      if (claim.evidence !== expectedEvidence) {
        throw new Error(
          `capability ${mode.auth_mode}/${claim.operation} evidence must be ${expectedEvidence}`,
        );
      }
      claims += 1;
    }
  }

  return claims;
}

function declaredAuthoritySet(manifest) {
  return new Set(authorityPaths(manifest.authority));
}

function validateDecisions(manifest, contract) {
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
  assertExactStringSet(
    requiredDecisionIds,
    contract.decisionIds,
    "completion_gate.required_decision_ids",
  );
  assertExactStringSet(
    [...decisionIds],
    contract.decisionIds,
    "decision ids",
  );
  assertSemanticHash(
    manifest.decisions,
    contract.semanticHashes?.decisions,
    "decision ledger does not match validator-owned semantic contract",
  );
  for (const decisionId of requiredDecisionIds) {
    if (!decisionIds.has(decisionId)) {
      throw new Error(`missing required decision: ${decisionId}`);
    }
  }

  return manifest.decisions.length;
}

function validateImplementationSlices(manifest, contract) {
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
  assertExactStringSet(
    requiredSliceIds,
    contract.implementationSliceIds,
    "completion_gate.required_implementation_slice_ids",
  );
  assertExactStringSet(
    [...knownIds],
    contract.implementationSliceIds,
    "implementation slice ids",
  );
  assertSemanticHash(
    manifest.implementation_slices,
    contract.semanticHashes?.implementationSlices,
    "implementation slices do not match validator-owned semantic contract",
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

function validateDeferredItems(manifest, contract) {
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
  assertExactStringSet(
    requiredDeferredIds,
    contract.deferredItemIds,
    "completion_gate.required_deferred_item_ids",
  );
  assertExactStringSet(
    [...deferredIds],
    contract.deferredItemIds,
    "deferred item ids",
  );
  assertSemanticHash(
    manifest.deferred_items,
    contract.semanticHashes?.deferredItems,
    "deferred register does not match validator-owned semantic contract",
  );
  for (const deferredId of requiredDeferredIds) {
    if (!deferredIds.has(deferredId)) {
      throw new Error(`missing required deferred item: ${deferredId}`);
    }
  }

  return manifest.deferred_items.length;
}

function parseMarkdownTable(sectionText) {
  return sectionText
    .split(/\r?\n/)
    .filter((line) => line.trim().startsWith("|"))
    .map((line) =>
      line
        .trim()
        .slice(1, -1)
        .split("|")
        .map((cell) => cell.trim()),
    );
}

function stripCode(value) {
  const codeToken = value.match(/^`([^`]+)`/);
  return codeToken ? codeToken[1] : value.replace(/ \([^)]*\)$/, "");
}

function validateHumanCapabilityLedger(specification, contract) {
  const start = specification.indexOf("## Capability evidence ledger");
  const end = specification.indexOf("\n## ", start + 1);
  const section = specification.slice(start, end === -1 ? undefined : end);
  const rows = parseMarkdownTable(section);
  const dataRows = rows.slice(2);
  const rowsByLabel = new Map(dataRows.map((row) => [row[0], row]));
  const columns = [
    ["chat", 2],
    ["chat_streaming", 3],
    ["image_generation", 4],
    ["image_edit", 5],
    ["inpaint", 6],
  ];

  for (const expectedMode of contract.authModes) {
    const row = rowsByLabel.get(expectedMode.label);
    if (!row) {
      throw new Error(`human capability ledger is missing ${expectedMode.label}`);
    }
    if (stripCode(row[1]) !== expectedMode.riskStatus) {
      throw new Error(
        `human capability ${expectedMode.label} risk status must be ${expectedMode.riskStatus}`,
      );
    }
    for (const [operation, column] of columns) {
      if (!(operation in expectedMode.claims)) {
        continue;
      }
      const expectedStatus = expectedMode.claims[operation];
      if (stripCode(row[column]) !== expectedStatus) {
        throw new Error(
          `human capability ${expectedMode.label}/${operation} status must be ${expectedStatus}`,
        );
      }
    }
    if (stripCode(row[7]) !== expectedMode.evidence) {
      throw new Error(
        `human capability ${expectedMode.label} evidence must be ${expectedMode.evidence}`,
      );
    }
  }
  if (dataRows.length !== contract.authModes.length) {
    throw new Error("human capability ledger has unexpected rows");
  }
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

  for (const section of requiredSections) {
    const escapedSection = section.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    const heading = new RegExp(`^## ${escapedSection}\\s*$`, "m");
    if (!heading.test(specification)) {
      throw new Error(`specification is missing required section: ${section}`);
    }
  }
  validateHumanCapabilityLedger(specification, contract);
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
  const capabilityClaims = validateCapabilities(manifest, contract);
  const decisions = validateDecisions(manifest, contract);
  validateImplementationSlices(manifest, contract);
  const deferredItems = validateDeferredItems(manifest, contract);
  await validateSpecificationSections(root, manifest, contract);

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
