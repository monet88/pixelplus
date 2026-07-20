import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test, { afterEach } from "node:test";
import { fileURLToPath } from "node:url";

import { validateImplementationSpecification } from "./validate-provider-gateway-implementation-spec.mjs";

const canonicalStatuses = [
  "verified",
  "conditionally_supported",
  "unsupported",
  "unverified",
];
const fixtureRoots = new Set();
const repositoryRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);

afterEach(async () => {
  await Promise.all(
    [...fixtureRoots].map((root) => rm(root, { recursive: true, force: true })),
  );
  fixtureRoots.clear();
});

async function createFixture(mutator = () => {}) {
  const root = await mkdtemp(path.join(os.tmpdir(), "pixelplus-us022-"));
  fixtureRoots.add(root);
  const files = {
    "CONTEXT.md": "# Domain glossary\n",
    "contracts/openapi/pixelplus-public-api-v1.yaml": "openapi: 3.1.1\n",
    "docs/decisions/0008.md": "# Public API decision\n",
    "docs/decisions/0009.md": "# Architecture decision\n",
    "docs/spec/domain.md": "# Domain specification\n",
    "docs/spec/evidence.md": [
      "# Evidence",
      "",
      "## Capability matrices",
      "",
      "### 1.1 Example Mode",
      "",
      "| Capability | Status | Evidence |",
      "| --- | --- | --- |",
      "| chat (non-streaming) | `conditionally supported` | fixture |",
      "",
    ].join("\n"),
    "docs/spec/provider-gateway-implementation-ready-specification.md": [
      "# Provider Gateway Implementation-Ready Specification",
      "",
      "## Authority and conflict resolution",
      "## Capability evidence ledger",
      "",
      "| Auth Mode | Risk state | Chat | Streaming | Image generation | Image edit | Inpaint | Evidence |",
      "| --- | --- | --- | --- | --- | --- | --- | --- |",
      "| Example Mode | `gated` | `conditionally_supported` | `unsupported` | `unsupported` | `unsupported` | `unsupported` | `docs/spec/evidence.md` |",
      "## Decision ledger",
      "## Planning closure ledger",
      "",
      "| Domain | Disposition | Decision IDs |",
      "| --- | --- | --- |",
      "| `product` | `locked` | `tenant_authority` |",
      "## Implementation work breakdown",
      "## Deferred item register",
      "## Completion gate",
      "",
    ].join("\n"),
  };

  const manifest = {
    schema_version: 1,
    issue: 22,
    implementation_issue: 42,
    status: "implementation_ready",
    specification: "docs/spec/provider-gateway-implementation-ready-specification.md",
    canonical_capability_statuses: canonicalStatuses,
    authority: {
      glossary: "CONTEXT.md",
      stable_public_api: "contracts/openapi/pixelplus-public-api-v1.yaml",
      architecture_decisions: [
        "docs/decisions/0008.md",
        "docs/decisions/0009.md",
      ],
      normative_specs: ["docs/spec/domain.md"],
      evidence_sources: ["docs/spec/evidence.md"],
    },
    capabilities: [
      {
        auth_mode: "example_mode",
        risk_status: "gated",
        claims: [
          {
            operation: "chat",
            status: "conditionally_supported",
            evidence: "docs/spec/evidence.md",
          },
        ],
      },
    ],
    decisions: [
      {
        id: "tenant_authority",
        observable_behavior: "Tenant authority is derived server-side.",
        failure_semantics: "Foreign resources are non-enumerating.",
        security_impact: "Prevents cross-Tenant access.",
        dependencies: ["docs/spec/domain.md"],
      },
    ],
    planning_closure: [
      {
        domain: "product",
        disposition: "locked",
        decision_ids: ["tenant_authority"],
      },
    ],
    implementation_slices: [
      {
        id: "foundation",
        depends_on: [],
        authority: ["docs/decisions/0009.md"],
        proof_seam: "public HTTP composition",
      },
    ],
    deferred_items: [
      {
        id: "deployment_topology",
        reason: "Needs runtime load evidence.",
        dependencies: ["implementation issue #42"],
        reopen_trigger: "The single-region implementation reaches its measured limit.",
      },
    ],
    completion_gate: {
      required_auth_modes: ["example_mode"],
      required_auth_mode_risk_statuses: {
        example_mode: "gated",
      },
      required_capability_operations: ["chat"],
      required_decision_ids: ["tenant_authority"],
      required_planning_domains: ["product"],
      required_implementation_slice_ids: ["foundation"],
      required_deferred_item_ids: ["deployment_topology"],
      required_sections: [
        "Authority and conflict resolution",
        "Capability evidence ledger",
        "Decision ledger",
        "Planning closure ledger",
        "Implementation work breakdown",
        "Deferred item register",
        "Completion gate",
      ],
      implementation_issue_must_differ: true,
    },
  };

  const expectedContract = {
    issue: 22,
    implementationIssue: 42,
    canonicalStatuses,
    authModes: [
      {
        id: "example_mode",
        label: "Example Mode",
        riskStatus: "gated",
        claims: {
          chat: {
            status: "conditionally_supported",
            evidence: "docs/spec/evidence.md",
          },
        },
      },
    ],
    decisionIds: ["tenant_authority"],
    planningDomains: [
      {
        id: "product",
        decisionIds: ["tenant_authority"],
      },
    ],
    implementationSliceIds: ["foundation"],
    deferredItemIds: ["deployment_topology"],
    requiredSections: [
      "Authority and conflict resolution",
      "Capability evidence ledger",
      "Decision ledger",
      "Planning closure ledger",
      "Implementation work breakdown",
      "Deferred item register",
      "Completion gate",
    ],
  };

  mutator({ files, manifest });

  for (const [relativePath, contents] of Object.entries(files)) {
    const absolutePath = path.join(root, relativePath);
    await mkdir(path.dirname(absolutePath), { recursive: true });
    await writeFile(absolutePath, contents, "utf8");
  }

  const manifestPath = path.join(
    root,
    "docs/spec/provider-gateway-implementation-ready-manifest.json",
  );
  await mkdir(path.dirname(manifestPath), { recursive: true });
  await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`, "utf8");

  return { expectedContract, manifestPath, root };
}

async function createProductionFixture(mutator = () => {}) {
  const manifestPath = path.join(
    repositoryRoot,
    "docs/spec/provider-gateway-implementation-ready-manifest.json",
  );
  const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
  const state = {
    manifest,
    specification: await readFile(
      path.join(repositoryRoot, manifest.specification),
      "utf8",
    ),
  };
  const authorityPaths = [
    state.manifest.authority.glossary,
    state.manifest.authority.stable_public_api,
    ...state.manifest.authority.architecture_decisions,
    ...state.manifest.authority.normative_specs,
    ...state.manifest.authority.evidence_sources,
  ];
  state.authorityContents = Object.fromEntries(
    await Promise.all(
      authorityPaths.map(async (relativePath) => [
        relativePath,
        await readFile(path.join(repositoryRoot, relativePath), "utf8"),
      ]),
    ),
  );
  mutator(state);

  const root = await mkdtemp(path.join(os.tmpdir(), "pixelplus-us022-real-"));
  fixtureRoots.add(root);
  for (const relativePath of authorityPaths) {
    const absolutePath = path.join(root, relativePath);
    await mkdir(path.dirname(absolutePath), { recursive: true });
    await writeFile(absolutePath, state.authorityContents[relativePath], "utf8");
  }

  const specificationPath = path.join(root, state.manifest.specification);
  await mkdir(path.dirname(specificationPath), { recursive: true });
  await writeFile(specificationPath, state.specification, "utf8");

  const fixtureManifestPath = path.join(
    root,
    "docs/spec/provider-gateway-implementation-ready-manifest.json",
  );
  await writeFile(
    fixtureManifestPath,
    `${JSON.stringify(state.manifest, null, 2)}\n`,
    "utf8",
  );

  return { manifestPath: fixtureManifestPath, root };
}

test("accepts a complete implementation-ready specification package", async () => {
  const fixture = await createFixture();
  const result = await validateImplementationSpecification(fixture);

  assert.equal(result.issue, 22);
  assert.equal(result.implementationIssue, 42);
  assert.equal(result.capabilityClaims, 1);
  assert.equal(result.decisions, 1);
  assert.equal(result.deferredItems, 1);
});

test("rejects missing authority artifacts", async () => {
  const fixture = await createFixture(({ files }) => {
    delete files["docs/spec/domain.md"];
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /authority file does not exist: docs\/spec\/domain\.md/,
  );
});

test("rejects authority paths that escape the repository root", async () => {
  const fixture = await createFixture(({ manifest }) => {
    manifest.authority.normative_specs = ["../outside.md"];
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /repository path escapes root: \.\.\/outside\.md/,
  );
});

test("rejects capability claims outside the canonical vocabulary", async () => {
  const fixture = await createFixture(({ manifest }) => {
    manifest.capabilities[0].claims[0].status = "supported";
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /non-canonical capability status: supported/,
  );
});

test("rejects a missing required Auth Mode or capability operation", async () => {
  const missingMode = await createFixture(({ manifest }) => {
    manifest.completion_gate.required_auth_modes.push("second_mode");
  });
  await assert.rejects(
    validateImplementationSpecification(missingMode),
    /completion_gate\.required_auth_modes does not match validator-owned contract/,
  );

  const missingOperation = await createFixture(({ manifest }) => {
    manifest.completion_gate.required_capability_operations.push("inpaint");
  });
  await assert.rejects(
    validateImplementationSpecification(missingOperation),
    /completion_gate\.required_capability_operations does not match validator-owned contract/,
  );
});

test("rejects extra, duplicate, or risk-mismatched capability rows", async () => {
  const extraMode = await createFixture(({ manifest }) => {
    manifest.capabilities.push(structuredClone(manifest.capabilities[0]));
    manifest.capabilities[1].auth_mode = "extra_mode";
  });
  await assert.rejects(
    validateImplementationSpecification(extraMode),
    /unexpected Auth Mode: extra_mode/,
  );

  const duplicateOperation = await createFixture(({ manifest }) => {
    manifest.capabilities[0].claims.push(
      structuredClone(manifest.capabilities[0].claims[0]),
    );
  });
  await assert.rejects(
    validateImplementationSpecification(duplicateOperation),
    /duplicate capability operation: example_mode\/chat/,
  );

  const riskMismatch = await createFixture(({ manifest }) => {
    manifest.capabilities[0].risk_status = "experimental";
  });
  await assert.rejects(
    validateImplementationSpecification(riskMismatch),
    /capability example_mode risk status must be gated/,
  );
});

test("rejects capability evidence outside the declared evidence authority", async () => {
  const fixture = await createFixture(({ manifest }) => {
    manifest.capabilities[0].claims[0].evidence = "docs/spec/undeclared.md";
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /capability example_mode\/chat evidence is not declared authority/,
  );
});

test("rejects capability drift in the declared evidence matrix", async () => {
  const fixture = await createFixture(({ files }) => {
    files["docs/spec/evidence.md"] = files["docs/spec/evidence.md"].replace(
      "`conditionally supported`",
      "`unsupported`",
    );
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /evidence matrix example_mode\/chat status must be conditionally_supported/,
  );
});

test("rejects decisions that omit required implementation dimensions", async () => {
  const fixture = await createFixture(({ manifest }) => {
    delete manifest.decisions[0].failure_semantics;
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /decision tenant_authority is missing failure_semantics/,
  );
});

test("rejects decision and slice authority outside the declared source set", async () => {
  const decisionAuthority = await createFixture(({ manifest }) => {
    manifest.decisions[0].dependencies = ["docs/spec/undeclared.md"];
  });
  await assert.rejects(
    validateImplementationSpecification(decisionAuthority),
    /decision tenant_authority dependency is not declared authority/,
  );

  const sliceAuthority = await createFixture(({ manifest }) => {
    manifest.implementation_slices[0].authority = ["docs/spec/undeclared.md"];
  });
  await assert.rejects(
    validateImplementationSpecification(sliceAuthority),
    /implementation slice foundation authority is not declared/,
  );
});

test("rejects missing required decisions and implementation slices", async () => {
  const missingDecision = await createFixture(({ manifest }) => {
    manifest.completion_gate.required_decision_ids.push("retry_ownership");
  });
  await assert.rejects(
    validateImplementationSpecification(missingDecision),
    /completion_gate\.required_decision_ids does not match validator-owned contract/,
  );

  const missingSlice = await createFixture(({ manifest }) => {
    manifest.completion_gate.required_implementation_slice_ids.push("runtime");
  });
  await assert.rejects(
    validateImplementationSpecification(missingSlice),
    /completion_gate\.required_implementation_slice_ids does not match validator-owned contract/,
  );
});

test("rejects incomplete planning closure for mandatory decision domains", async () => {
  const missingDomain = await createFixture(({ manifest }) => {
    manifest.completion_gate.required_planning_domains = [];
  });
  await assert.rejects(
    validateImplementationSpecification(missingDomain),
    /completion_gate\.required_planning_domains must be a non-empty string array/,
  );

  const missingDecision = await createFixture(({ manifest }) => {
    manifest.planning_closure[0].decision_ids = [];
  });
  await assert.rejects(
    validateImplementationSpecification(missingDecision),
    /planning domain product decision ids must be a non-empty string array/,
  );
});

test("rejects a planning domain that is not locked", async () => {
  const fixture = await createFixture(({ manifest }) => {
    manifest.planning_closure[0].disposition = "deferred";
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /planning domain product must be locked/,
  );
});

test("rejects duplicate IDs and cyclic implementation slices", async () => {
  const duplicateDecision = await createFixture(({ manifest }) => {
    manifest.decisions.push(structuredClone(manifest.decisions[0]));
  });
  await assert.rejects(
    validateImplementationSpecification(duplicateDecision),
    /duplicate decision id: tenant_authority/,
  );

  const cyclicSlices = await createFixture(({ manifest }) => {
    manifest.implementation_slices[0].depends_on = ["foundation"];
  });
  await assert.rejects(
    validateImplementationSpecification(cyclicSlices),
    /implementation slice dependency cycle:/,
  );

  const duplicateDeferred = await createFixture(({ manifest }) => {
    manifest.deferred_items.push(structuredClone(manifest.deferred_items[0]));
  });
  await assert.rejects(
    validateImplementationSpecification(duplicateDeferred),
    /duplicate deferred item id: deployment_topology/,
  );
});

test("rejects deferred items without a reason, dependency, and reopen trigger", async () => {
  const fixture = await createFixture(({ manifest }) => {
    manifest.deferred_items[0].reopen_trigger = "";
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /deferred item deployment_topology is missing reopen_trigger/,
  );
});

test("rejects a missing source-owned deferred item", async () => {
  const fixture = await createFixture(({ manifest }) => {
    manifest.completion_gate.required_deferred_item_ids.push("risk_terms");
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /completion_gate\.required_deferred_item_ids does not match validator-owned contract/,
  );
});

test("rejects a package that starts implementation in the gate issue", async () => {
  const fixture = await createFixture(({ manifest }) => {
    manifest.implementation_issue = 22;
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /implementation issue must be 42/,
  );
});

test("rejects a package that shrinks its manifest and completion gate together", async () => {
  const fixture = await createProductionFixture(({ manifest }) => {
    const retained = manifest.capabilities[0];
    manifest.capabilities = [retained];
    manifest.completion_gate.required_auth_modes = [retained.auth_mode];
    manifest.completion_gate.required_auth_mode_risk_statuses = {
      [retained.auth_mode]: retained.risk_status,
    };
    manifest.completion_gate.required_capability_operations = [
      retained.claims[0].operation,
    ];
    retained.claims = [retained.claims[0]];
    manifest.decisions = [manifest.decisions[0]];
    manifest.completion_gate.required_decision_ids = [manifest.decisions[0].id];
    manifest.implementation_slices = [manifest.implementation_slices[0]];
    manifest.completion_gate.required_implementation_slice_ids = [
      manifest.implementation_slices[0].id,
    ];
    manifest.deferred_items = [manifest.deferred_items[0]];
    manifest.completion_gate.required_deferred_item_ids = [
      manifest.deferred_items[0].id,
    ];
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /completion_gate\.required_auth_modes does not match validator-owned contract/,
  );
});

test("rejects authority content drift even when every path still exists", async () => {
  const fixture = await createProductionFixture((state) => {
    const authorityPath = state.manifest.authority.normative_specs[0];
    state.authorityContents[authorityPath] = "# Hollow authority\n";
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /authority content does not match validator-owned fingerprint:/,
  );
});

test("accepts semantic JSON with reordered object keys", async () => {
  const fixture = await createProductionFixture(({ manifest }) => {
    manifest.decisions[0] = Object.fromEntries(
      Object.entries(manifest.decisions[0]).reverse(),
    );
    manifest.completion_gate.required_auth_mode_risk_statuses =
      Object.fromEntries(
        Object.entries(
          manifest.completion_gate.required_auth_mode_risk_statuses,
        ).reverse(),
      );
  });

  const result = await validateImplementationSpecification(fixture);
  assert.equal(result.decisions, 15);
});

test("rejects provider policy drift", async () => {
  const fixture = await createProductionFixture(({ manifest }) => {
    manifest.provider_policies.grok_xai_oauth.operation_surfaces.image_edit =
      "cli_chat_proxy";
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /provider policies do not match validator-owned contract/,
  );
});

test("rejects capability drift from the accepted evidence baseline", async () => {
  const fixture = await createProductionFixture(({ manifest }) => {
    manifest.capabilities[0].claims[0].status = "unsupported";
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /capability chatgpt_web_access\/chat status must be conditionally_supported/,
  );
});

test("rejects capability drift in the human evidence ledger", async () => {
  const fixture = await createProductionFixture((state) => {
    state.specification = state.specification.replace(
      "| ChatGPT Web Access | `experimental` | `conditionally_supported` |",
      "| ChatGPT Web Access | `experimental` | `unsupported` |",
    );
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /human capability ChatGPT Web Access\/chat status must be conditionally_supported/,
  );
});

test("rejects planning closure drift in the human handoff", async () => {
  const fixture = await createProductionFixture((state) => {
    state.specification = state.specification.replace(
      "| `product` | `locked` |",
      "| `product` | `deferred` |",
    );
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /human planning domain product must be locked/,
  );
});

test("rejects altered gate and implementation issue identities unconditionally", async () => {
  const wrongGate = await createProductionFixture(({ manifest }) => {
    manifest.issue = 999;
    manifest.implementation_issue = 1000;
  });
  await assert.rejects(
    validateImplementationSpecification(wrongGate),
    /gate issue must be 22/,
  );

  const reusedGate = await createProductionFixture(({ manifest }) => {
    manifest.implementation_issue = 22;
    manifest.completion_gate.implementation_issue_must_differ = false;
  });
  await assert.rejects(
    validateImplementationSpecification(reusedGate),
    /implementation issue must be 42/,
  );
});

test("rejects placeholder decision semantics despite complete IDs", async () => {
  const fixture = await createProductionFixture(({ manifest }) => {
    for (const decision of manifest.decisions) {
      decision.observable_behavior = "TBD";
      decision.failure_semantics = "TBD";
      decision.security_impact = "TBD";
      decision.dependencies = [manifest.authority.normative_specs[0]];
    }
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /decision ledger does not match validator-owned semantic contract/,
  );
});

test("rejects changed slice order, authority, or proof semantics", async () => {
  const fixture = await createProductionFixture(({ manifest }) => {
    for (const slice of manifest.implementation_slices) {
      slice.depends_on = [];
      slice.authority = [manifest.authority.architecture_decisions[1]];
      slice.proof_seam = "TBD";
    }
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /implementation slices do not match validator-owned semantic contract/,
  );
});

test("rejects placeholder deferred reasons, dependencies, and triggers", async () => {
  const fixture = await createProductionFixture(({ manifest }) => {
    for (const item of manifest.deferred_items) {
      item.reason = "TBD";
      item.dependencies = ["TBD"];
      item.reopen_trigger = "TBD";
    }
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /deferred register does not match validator-owned semantic contract/,
  );
});

test("rejects a hollow or relocated human implementation handoff", async () => {
  const hollow = await createProductionFixture((state) => {
    const capabilityStart = state.specification.indexOf(
      "## Capability evidence ledger",
    );
    const decisionStart = state.specification.indexOf("## Decision ledger");
    const planningStart = state.specification.indexOf(
      "## Planning closure ledger",
    );
    const implementationStart = state.specification.indexOf(
      "## Implementation work breakdown",
    );
    const capabilitySection = state.specification.slice(
      capabilityStart,
      decisionStart,
    );
    const planningSection = state.specification.slice(
      planningStart,
      implementationStart,
    );
    state.specification = [
      "# Provider Gateway Implementation-Ready Specification",
      "",
      "## Authority and conflict resolution",
      "TBD",
      capabilitySection.trim(),
      "## Decision ledger",
      "TBD",
      planningSection.trim(),
      "## Implementation work breakdown",
      "TBD",
      "## Deferred item register",
      "TBD",
      "## Completion gate",
      "TBD",
      "",
    ].join("\n");
  });
  await assert.rejects(
    validateImplementationSpecification(hollow),
    /human specification does not match validator-owned semantic contract/,
  );

  const relocated = await createProductionFixture(({ manifest }) => {
    manifest.specification = "docs/spec/alternate-implementation-specification.md";
  });
  await assert.rejects(
    validateImplementationSpecification(relocated),
    /specification path does not match validator-owned contract/,
  );
});

test("reports malformed human capability rows as validation errors", async () => {
  const fixture = await createProductionFixture((state) => {
    state.specification = state.specification.replace(
      /\| ChatGPT Web Access \|[^\n]+/,
      "| ChatGPT Web Access | `experimental` |",
    );
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /human capability ChatGPT Web Access\/chat status must be conditionally_supported/,
  );
});
