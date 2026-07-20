import crypto from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const contractPath = path.join(
  root,
  "scripts/provider-gateway-implementation-spec-contract.json",
);
const manifestPath = path.join(
  root,
  "docs/spec/provider-gateway-implementation-ready-manifest.json",
);

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

function authorityPaths(contract) {
  return [
    contract.authority.glossary,
    contract.authority.stablePublicApi,
    ...contract.authority.architectureDecisions,
    ...contract.authority.normativeSpecs,
    ...contract.authority.evidenceSources,
  ];
}

const contract = JSON.parse(await readFile(contractPath, "utf8"));
const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
const specification = await readFile(path.join(root, manifest.specification), "utf8");

contract.semanticHashes = {
  decisions: semanticHash(manifest.decisions),
  implementationSlices: semanticHash(manifest.implementation_slices),
  deferredItems: semanticHash(manifest.deferred_items),
  specification: semanticHash(specification),
};
contract.authorityFingerprints = Object.fromEntries(
  await Promise.all(
    authorityPaths(contract).map(async (relativePath) => [
      relativePath,
      semanticHash(await readFile(path.join(root, relativePath), "utf8")),
    ]),
  ),
);

await writeFile(contractPath, `${JSON.stringify(contract, null, 2)}\n`, "utf8");
console.log(
  `Refreshed Provider Gateway contract fingerprints for ${Object.keys(contract.authorityFingerprints).length} authority files.`,
);
