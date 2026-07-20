import assert from "node:assert/strict";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test, { afterEach } from "node:test";

import { validateImplementationSpecification } from "./validate-provider-gateway-implementation-spec.mjs";

const canonicalStatuses = [
  "verified",
  "conditionally_supported",
  "unsupported",
  "unverified",
];
const fixtureRoots = new Set();

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
    "docs/spec/evidence.md": "# Evidence\n",
    "docs/spec/provider-gateway-implementation-ready-specification.md": [
      "# Provider Gateway Implementation-Ready Specification",
      "",
      "## Authority and conflict resolution",
      "## Capability evidence ledger",
      "## Decision ledger",
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
      required_implementation_slice_ids: ["foundation"],
      required_deferred_item_ids: ["deployment_topology"],
      required_sections: [
        "Authority and conflict resolution",
        "Capability evidence ledger",
        "Decision ledger",
        "Implementation work breakdown",
        "Deferred item register",
        "Completion gate",
      ],
      implementation_issue_must_differ: true,
    },
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

  return { manifestPath, root };
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
    /missing required Auth Mode: second_mode/,
  );

  const missingOperation = await createFixture(({ manifest }) => {
    manifest.completion_gate.required_capability_operations.push("inpaint");
  });
  await assert.rejects(
    validateImplementationSpecification(missingOperation),
    /capability example_mode is missing required operation: inpaint/,
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
    /missing required decision: retry_ownership/,
  );

  const missingSlice = await createFixture(({ manifest }) => {
    manifest.completion_gate.required_implementation_slice_ids.push("runtime");
  });
  await assert.rejects(
    validateImplementationSpecification(missingSlice),
    /missing required implementation slice: runtime/,
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
    manifest.implementation_slices[0].depends_on = ["runtime"];
    manifest.implementation_slices.push({
      id: "runtime",
      depends_on: ["foundation"],
      authority: ["docs/decisions/0009.md"],
      proof_seam: "public HTTP composition",
    });
    manifest.completion_gate.required_implementation_slice_ids.push("runtime");
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
    /missing required deferred item: risk_terms/,
  );
});

test("rejects a package that starts implementation in the gate issue", async () => {
  const fixture = await createFixture(({ manifest }) => {
    manifest.implementation_issue = 22;
  });

  await assert.rejects(
    validateImplementationSpecification(fixture),
    /implementation issue must differ from gate issue/,
  );
});
