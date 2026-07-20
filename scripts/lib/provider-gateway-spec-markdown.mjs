import { readFile } from "node:fs/promises";
import path from "node:path";

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
  if (typeof value !== "string") {
    return "";
  }
  const codeToken = value.match(/^`([^`]+)`/);
  return codeToken ? codeToken[1] : value.replace(/ \([^)]*\)$/, "");
}

function codeTokens(value) {
  if (typeof value !== "string") {
    return [];
  }
  return [...value.matchAll(/`([^`]+)`/g)].map((match) => match[1]);
}

function normalizeCapabilityLabel(value) {
  const token = stripCode(value)
    .toLowerCase()
    .replaceAll("/", " ")
    .replaceAll(/[^a-z0-9]+/g, "_")
    .replaceAll(/^_+|_+$/g, "");
  if (token === "chat_non_streaming") {
    return "chat";
  }
  if (token === "inpaint_mask_edit") {
    return "inpaint";
  }
  return token;
}

function sortedUnique(values) {
  return [...new Set(values)].sort();
}

function assertExactStringSet(actual, expected, label) {
  if (!Array.isArray(actual) || !Array.isArray(expected)) {
    throw new Error(`${label} must contain arrays`);
  }
  const actualSorted = sortedUnique(actual);
  const expectedSorted = sortedUnique(expected);
  if (
    actual.length !== actualSorted.length ||
    JSON.stringify(actualSorted) !== JSON.stringify(expectedSorted)
  ) {
    throw new Error(`${label} does not match validator-owned contract`);
  }
}

function sectionByHeading(markdown, heading) {
  const escapedHeading = heading.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`^## ${escapedHeading}\\s*$`, "m").exec(markdown);
  if (!match) {
    throw new Error(`specification is missing required section: ${heading}`);
  }
  const start = match.index;
  const nextHeading = markdown.indexOf("\n## ", start + match[0].length);
  return markdown.slice(start, nextHeading === -1 ? undefined : nextHeading);
}

function sectionByHeadingSuffix(markdown, level, suffix) {
  const marker = "#".repeat(level);
  const headingPattern = new RegExp(`^${marker}\\s+(.+?)\\s*$`, "gm");
  let match;
  while ((match = headingPattern.exec(markdown)) !== null) {
    const title = match[1];
    if (title === suffix || title.endsWith(` ${suffix}`)) {
      const start = match.index;
      const nextHeadingPattern = new RegExp(`^#{1,${level}}\\s+`, "gm");
      nextHeadingPattern.lastIndex = headingPattern.lastIndex;
      const nextHeading = nextHeadingPattern.exec(markdown);
      return markdown.slice(start, nextHeading?.index);
    }
  }
  throw new Error(`evidence is missing capability section: ${suffix}`);
}

function tableDataRows(markdown, heading) {
  const rows = parseMarkdownTable(sectionByHeading(markdown, heading));
  if (rows.length < 2) {
    throw new Error(`${heading} must contain a Markdown table`);
  }
  return rows.slice(2);
}

function validateHumanCapabilityLedger(specification, contract) {
  const dataRows = tableDataRows(specification, "Capability evidence ledger");
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
      const expectedClaim = expectedMode.claims[operation];
      if (!expectedClaim) {
        continue;
      }
      if (stripCode(row[column]) !== expectedClaim.status) {
        throw new Error(
          `human capability ${expectedMode.label}/${operation} status must be ${expectedClaim.status}`,
        );
      }
    }
    const expectedEvidence = sortedUnique(
      Object.values(expectedMode.claims).map((claim) => claim.evidence),
    );
    if (expectedEvidence.length !== 1 || stripCode(row[7]) !== expectedEvidence[0]) {
      throw new Error(
        `human capability ${expectedMode.label} evidence must be ${expectedEvidence.join(", ")}`,
      );
    }
  }
  if (dataRows.length !== contract.authModes.length) {
    throw new Error("human capability ledger has unexpected rows");
  }
}

function validateHumanPlanningClosure(specification, contract) {
  const dataRows = tableDataRows(specification, "Planning closure ledger");
  const rowsByDomain = new Map(dataRows.map((row) => [stripCode(row[0]), row]));

  for (const expectedDomain of contract.planningDomains) {
    const row = rowsByDomain.get(expectedDomain.id);
    if (!row) {
      throw new Error(`human planning closure is missing ${expectedDomain.id}`);
    }
    if (stripCode(row[1]) !== "locked") {
      throw new Error(`human planning domain ${expectedDomain.id} must be locked`);
    }
    assertExactStringSet(
      codeTokens(row[2]),
      expectedDomain.decisionIds,
      `human planning domain ${expectedDomain.id} decision ids`,
    );
  }
  if (dataRows.length !== contract.planningDomains.length) {
    throw new Error("human planning closure has unexpected rows");
  }
}

export function validateHumanSpecification({
  specification,
  requiredSections,
  contract,
}) {
  for (const section of requiredSections) {
    sectionByHeading(specification, section);
  }
  validateHumanCapabilityLedger(specification, contract);
  validateHumanPlanningClosure(specification, contract);
}

export async function validateEvidenceCapabilityMatrices({ root, contract }) {
  const expectedByEvidence = new Map();
  for (const mode of contract.authModes) {
    const evidencePaths = sortedUnique(
      Object.values(mode.claims).map((claim) => claim.evidence),
    );
    if (evidencePaths.length !== 1) {
      throw new Error(
        `validator-owned capability ${mode.id} must use one evidence matrix`,
      );
    }
    const modes = expectedByEvidence.get(evidencePaths[0]) ?? [];
    modes.push(mode);
    expectedByEvidence.set(evidencePaths[0], modes);
  }

  for (const [evidencePath, expectedModes] of expectedByEvidence) {
    const evidence = await readFile(path.join(root, evidencePath), "utf8");
    for (const mode of expectedModes) {
      const rows = parseMarkdownTable(
        sectionByHeadingSuffix(evidence, 3, mode.label),
      ).slice(2);
      const rowsByCapability = new Map();
      for (const row of rows) {
        const capability = normalizeCapabilityLabel(row[0]);
        if (rowsByCapability.has(capability)) {
          throw new Error(
            `duplicate evidence capability row: ${mode.id}/${capability}`,
          );
        }
        rowsByCapability.set(capability, stripCode(row[1]).replaceAll(" ", "_"));
      }

      for (const [operation, claim] of Object.entries(mode.claims)) {
        if (!rowsByCapability.has(operation)) {
          throw new Error(
            `evidence matrix ${evidencePath} is missing ${mode.id}/${operation}`,
          );
        }
        if (rowsByCapability.get(operation) !== claim.status) {
          throw new Error(
            `evidence matrix ${mode.id}/${operation} status must be ${claim.status}`,
          );
        }
      }
    }
  }
}
