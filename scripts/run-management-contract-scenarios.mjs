#!/usr/bin/env node

import assert from "node:assert/strict";

const OPERATIONS = ["chat", "chat_streaming", "image_generation", "image_edit", "inpaint"];
const OFFERABLE = new Set(["verified", "conditionally_supported"]);
const CREDENTIAL_CLASSES = new Set(["web_session", "oauth_token_import"]);
const NEW_CONNECTION_BLOCKED_AUTH_MODES = new Set(["grok_web_sso", "chatgpt_web_access"]);
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

const clock = (() => {
  let tick = 0;
  const base = Date.parse("2026-07-18T10:00:00Z");
  return () => new Date(base + tick++ * 1000).toISOString();
})();

let idSequence = 1;
const nextId = (prefix) => `${prefix}_${String(idSequence++).padStart(3, "0")}`;
const clone = (value) => JSON.parse(JSON.stringify(value));

const admin = {
  key: "cak_admin",
  tenantId: "tenant-a",
  authenticated: true,
  scopes: new Set(["chat.completions", "images.generate", "images.edit", "accounts.read", "accounts.manage", "capabilities.read", "routing.read", "routing.manage"]),
};
const reader = {
  key: "cak_reader",
  tenantId: "tenant-a",
  authenticated: true,
  scopes: new Set(["accounts.read", "capabilities.read", "routing.read"]),
};
const invalidKey = { key: "unknown", authenticated: false, scopes: new Set() };
const noScope = {
  key: "cak_no_scope",
  tenantId: "tenant-a",
  authenticated: true,
  scopes: new Set(),
};

const state = {
  accounts: {
    pa_foreign: {
      tenantId: "tenant-b",
      providerAccountId: "pa_foreign",
      provider: "chatgpt",
      authMode: "chatgpt_web_access",
      lifecycleState: "active",
      credential: { currentVersion: 8, pendingVersion: null, highestVersion: 8, refreshSupported: false, decryptable: true, disabledIntent: false },
      health: condition("account", "healthy", "probe_succeeded", 8, "none"),
      controls: { drain: "off", quarantine: "off", authModeExecutionEnabled: true },
      deleted: false,
      createdAt: clock(),
      updatedAt: clock(),
    },
  },
  oauth: {},
  snapshots: {},
  policies: {
    "tenant-a": {
      candidateAccounts: [],
      selectionOrder: [],
      fallbackEnabled: false,
      fallbackChain: [],
      fallbackAuthModes: [],
      affinity: { enabled: false, windowClass: "AFFINITY-WINDOW-CLASS" },
      leasePolicy: { enabled: false, eligibleUnits: [] },
      updatedAt: clock(),
      updatedBy: "system_default",
    },
  },
  vault: {},
  retentionEvidence: {},
  retentionHolds: {},
  effects: {
    vaultWrites: 0,
    vaultDecrypts: 0,
    vaultRevokes: 0,
    vaultDeletes: 0,
    adapterCalls: 0,
    providerProbeCalls: 0,
    oauthExchanges: 0,
    policyWrites: 0,
  },
};

function condition(kind, healthState, reason, credentialVersion, remediation, extra = {}) {
  return {
    summaryState: healthState,
    conditions: [
      {
        scope: { kind, ...(extra.operation ? { operation: extra.operation } : {}), ...(extra.modelSlug ? { modelSlug: extra.modelSlug } : {}) },
        state: healthState,
        reason,
        credentialVersion,
        observedAt: clock(),
        remediation,
        ...(extra.retryAfterSeconds ? { retryAfterSeconds: extra.retryAfterSeconds } : {}),
      },
    ],
  };
}

function canonical(status, code, category, statusClass, remediation, extra = {}) {
  return {
    status,
    body: {
      code,
      category,
      status_class: statusClass,
      retryability: extra.retryability || "not_retryable",
      remediation,
      request_id: nextId("req"),
      ...(extra.retryAfterClass ? { retry_after_class: extra.retryAfterClass } : {}),
      ...(extra.retryAfterSeconds ? { retry_after_seconds: extra.retryAfterSeconds } : {}),
      ...(extra.safeContext ? { safe_context: extra.safeContext } : {}),
    },
  };
}

const errors = {
  authentication: () => canonical(401, "authentication_failed", "authentication", "unauthorized", "replace_client_api_key"),
  forbidden: () => canonical(403, "forbidden", "authorization", "forbidden", "request_permission"),
  notFound: () => canonical(404, "resource_not_found", "authorization", "not_found", "none"),
  invalid: (cause) => canonical(400, "invalid_request", "validation", "invalid_request", "fix_request", { safeContext: { cause_class: cause } }),
  accountNotUsable: (cause) => canonical(409, "account_not_usable", "routing", "account_policy", "account_remediation", { safeContext: { cause_class: cause } }),
  authModeUnavailable: () => canonical(409, "auth_mode_unavailable", "routing", "account_policy", "auth_mode_unavailable", { retryability: "operator_action_required" }),
  snapshotStale: () => canonical(409, "snapshot_stale", "capability", "capability", "snapshot_stale", { retryability: "retry_after", retryAfterClass: "capability_reprobe" }),
  capabilityUnverified: () => canonical(409, "capability_unverified", "capability", "capability", "capability_unverified", { retryability: "retry_after", retryAfterClass: "capability_reprobe" }),
};

function authenticateAndAuthorize(principal, scope) {
  if (!principal.authenticated) return errors.authentication();
  if (!principal.scopes.has(scope)) return errors.forbidden();
  return null;
}

function ownAccount(principal, providerAccountId) {
  const account = state.accounts[providerAccountId];
  if (!account || account.deleted || account.tenantId !== principal.tenantId) return null;
  return account;
}

function accessAccount(principal, providerAccountId, scope) {
  if (!principal.authenticated) return { denied: errors.authentication() };
  const account = ownAccount(principal, providerAccountId);
  if (!account) return { denied: errors.notFound() };
  if (!principal.scopes.has(scope)) return { denied: errors.forbidden() };
  return { account };
}

function hasPendingOAuthAuthorization(providerAccountId) {
  return Object.values(state.oauth).some((authorization) => (
    authorization.providerAccountId === providerAccountId
    && authorization.status === "authorization_pending"
  ));
}

function validateCredential(account, credential) {
  if (!credential || !CREDENTIAL_CLASSES.has(credential.credential_class)) return "credential_class_invalid";
  const expectedClass = account.authMode.includes("oauth") ? "oauth_token_import" : "web_session";
  if (credential.credential_class !== expectedClass) return "credential_class_mismatch";
  const material = credential.material || "";
  if (material.length < 8 || /[\u0000-\u001f]/.test(material)) return "credential_malformed";
  return null;
}

function allocateCredentialVersion(account) {
  const highestVersion = Math.max(
    account.credential.highestVersion || 0,
    account.credential.currentVersion || 0,
    account.credential.pendingVersion || 0,
  );
  account.credential.highestVersion = highestVersion + 1;
  return account.credential.highestVersion;
}

function executionScope(operation) {
  if (["chat", "chat_streaming"].includes(operation)) return "chat.completions";
  if (operation === "image_generation") return "images.generate";
  return "images.edit";
}

function probeControlDenial(account) {
  if (account.controls.quarantine === "quarantined" || !account.controls.authModeExecutionEnabled) {
    return errors.accountNotUsable("administrative_control");
  }
  return null;
}

function executionControlsBlocked(account) {
  return account.controls.drain === "draining"
    || account.controls.quarantine === "quarantined"
    || !account.controls.authModeExecutionEnabled;
}

function healthBlocksOperation(account, request) {
  const blockingStates = new Set(["cooling_down", "challenged", "expired", "blocked"]);
  return account.health.conditions.some((entry) => {
    if (!blockingStates.has(entry.state)) return false;
    if (entry.scope.kind === "account") return true;
    if (entry.scope.kind === "operation") return entry.scope.operation === request.operation;
    if (entry.scope.kind === "model") return entry.scope.modelSlug === request.model_slug;
    return false;
  });
}

function publicHealth(health) {
  return {
    summary_state: health.summaryState,
    conditions: health.conditions.map((entry) => ({
      scope: {
        kind: entry.scope.kind,
        ...(entry.scope.operation ? { operation: entry.scope.operation } : {}),
        ...(entry.scope.modelSlug ? { model_slug: entry.scope.modelSlug } : {}),
      },
      state: entry.state,
      reason: entry.reason,
      credential_version: entry.credentialVersion,
      observed_at: entry.observedAt,
      remediation: entry.remediation,
      ...(entry.retryAfterSeconds ? { retry_after_seconds: entry.retryAfterSeconds } : {}),
    })),
  };
}

function publicRouting(policy) {
  if (!policy) return null;
  return {
    candidate_accounts: clone(policy.candidateAccounts),
    selection_order: clone(policy.selectionOrder),
    fallback_enabled: policy.fallbackEnabled,
    fallback_chain: clone(policy.fallbackChain),
    fallback_auth_modes: clone(policy.fallbackAuthModes),
    affinity: { enabled: policy.affinity.enabled, window_class: policy.affinity.windowClass },
    lease_policy: { enabled: policy.leasePolicy.enabled, eligible_units: clone(policy.leasePolicy.eligibleUnits) },
    updated_at: policy.updatedAt,
    updated_by: policy.updatedBy,
  };
}

function publicAccount(account) {
  return {
    provider_account_id: account.providerAccountId,
    provider: account.provider,
    auth_mode: account.authMode,
    label: account.label,
    lifecycle_state: account.lifecycleState,
    credential: {
      ...(account.credential.currentVersion ? { version: account.credential.currentVersion } : {}),
      refresh_supported: account.credential.refreshSupported,
      ...(account.credential.lastValidatedAt ? { last_validated_at: account.credential.lastValidatedAt } : {}),
      ...(account.credential.lastProbedAt ? { last_probed_at: account.credential.lastProbedAt } : {}),
    },
    health: publicHealth(account.health),
    administrative_controls: {
      drain_state: account.controls.drain,
      quarantine_state: account.controls.quarantine,
      auth_mode_execution_enabled: account.controls.authModeExecutionEnabled,
    },
    created_at: account.createdAt,
    updated_at: account.updatedAt,
  };
}

function accountResponse(account, status = 200, trace = []) {
  return {
    status,
    body: { account: publicAccount(account), request_id: nextId("req") },
    ...(trace.length ? { prototypeTrace: trace } : {}),
  };
}

function redact(value) {
  if (Array.isArray(value)) return value.map(redact);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, nested]) => [key, isSecretName(key) ? "<redacted>" : redact(nested)]));
  }
  if (typeof value === "string" && SECRET_VALUE_PATTERNS.some((pattern) => pattern.test(value))) return "<redacted>";
  return value;
}

function effectDelta(before, after) {
  return Object.fromEntries(Object.keys(after).map((key) => [key, after[key] - before[key]]));
}

function tenantView(principal, request) {
  if (!principal.authenticated) return { principal: "unauthenticated", accounts: [], routing_policy: null };
  const visibleAccounts = Object.values(state.accounts)
    .filter((account) => account.tenantId === principal.tenantId && !account.deleted)
    .map(publicAccount)
    .sort((a, b) => a.provider_account_id.localeCompare(b.provider_account_id));
  const target = request.provider_account_id ? ownAccount(principal, request.provider_account_id) : null;
  return {
    principal: { key_id: principal.key, tenant: principal.tenantId, scopes: [...principal.scopes].sort() },
    target: target ? publicAccount(target) : "not_visible_in_principal_universe",
    accounts: visibleAccounts,
    routing_policy: publicRouting(state.policies[principal.tenantId] || null),
    snapshot: target ? clone(state.snapshots[target.providerAccountId] || null) : null,
  };
}

const scenarioResults = [];

function action(title, principal, request, handler, assertions) {
  const beforeState = tenantView(principal, request);
  const beforeEffects = clone(state.effects);
  const response = handler(principal, request);
  const afterState = tenantView(principal, request);
  const delta = effectDelta(beforeEffects, state.effects);

  assertions({ response, delta, beforeState, afterState });
  scenarioResults.push(title);

  console.log(`\n=== ${title} ===`);
  console.log(JSON.stringify({
    principal: principal.authenticated
      ? { key_id: principal.key, tenant: principal.tenantId, scopes: [...principal.scopes].sort() }
      : { authenticated: false },
    request: redact(request),
    before: beforeState,
    response: redact({ status: response.status, body: response.body }),
    ...(response.prototypeTrace ? { prototype_trace: response.prototypeTrace } : {}),
    after: afterState,
    side_effects: delta,
    assertion: "PASS",
  }, null, 2));
  return response;
}

function createAccount(principal, request) {
  const denied = authenticateAndAuthorize(principal, "accounts.manage");
  if (denied) return denied;
  if (NEW_CONNECTION_BLOCKED_AUTH_MODES.has(request.auth_mode)) return errors.authModeUnavailable();

  const providerAccountId = nextId("pa");
  const account = {
    tenantId: principal.tenantId,
    providerAccountId,
    provider: request.provider,
    authMode: request.auth_mode,
    label: request.label,
    lifecycleState: "draft",
    credential: { currentVersion: null, pendingVersion: null, highestVersion: 0, refreshSupported: request.auth_mode.includes("oauth"), decryptable: false, disabledIntent: false },
    health: condition("account", "unknown", "initial_unprobed", 0, "submit_credential"),
    controls: { drain: "off", quarantine: "off", authModeExecutionEnabled: true },
    deleted: false,
    createdAt: clock(),
    updatedAt: clock(),
  };
  state.accounts[providerAccountId] = account;
  return accountResponse(account, 201);
}

function listAccounts(principal) {
  const denied = authenticateAndAuthorize(principal, "accounts.read");
  if (denied) return denied;
  return {
    status: 200,
    body: {
      data: Object.values(state.accounts)
        .filter((account) => account.tenantId === principal.tenantId && !account.deleted)
        .map(publicAccount),
    },
  };
}

function getAccount(principal, request) {
  const { account, denied } = accessAccount(principal, request.provider_account_id, "accounts.read");
  if (denied) return denied;
  return { status: 200, body: publicAccount(account) };
}

function submitCredential(principal, request) {
  const { account, denied } = accessAccount(principal, request.provider_account_id, "accounts.manage");
  if (denied) return denied;
  if (account.lifecycleState !== "draft") return errors.accountNotUsable("credential_intake_requires_draft");

  const validationError = validateCredential(account, request.credential);
  if (validationError) return errors.invalid(validationError);

  const nextVersion = allocateCredentialVersion(account);
  state.effects.vaultWrites += 1;
  state.vault[account.providerAccountId] = { version: nextVersion, revoked: false, deleted: false };
  account.credential.currentVersion = nextVersion;
  account.credential.validationTarget = "current";
  account.credential.decryptable = false;
  account.lifecycleState = "pending_validation";
  account.health = condition("account", "unknown", "initial_unprobed", nextVersion, "none");
  account.updatedAt = clock();
  return accountResponse(account, 202, ["credential_stored", "pending_validation"]);
}

function completeCredentialValidation(principal, request) {
  const { account, denied } = accessAccount(principal, request.provider_account_id, "accounts.manage");
  if (denied) return denied;

  const target = account.credential.validationTarget;
  const version = target === "pending"
    ? account.credential.pendingVersion
    : target === "current"
      ? account.credential.currentVersion
      : null;
  const validationStateAllowed = account.lifecycleState === "pending_validation"
    || (account.lifecycleState === "disabled" && account.credential.disabledIntent);
  if (!target || !version || !validationStateAllowed) {
    return errors.accountNotUsable("credential_validation_not_pending");
  }

  if (request.simulated_result === "success") {
    account.credential.validationTarget = null;
    account.credential.lastValidatedAt = clock();
    const preserveDisabled = account.credential.disabledIntent
      || account.lifecycleState === "disabled"
      || account.credential.reauthReturnState === "disabled";
    if (target === "current") {
      account.credential.probeReturnState = preserveDisabled ? "disabled" : "active";
    }
    account.lifecycleState = preserveDisabled ? "disabled" : "pending_probe";
    account.updatedAt = clock();
    return accountResponse(account, 200, ["validation_success", account.lifecycleState, "provider_probe_required"]);
  }

  if (request.simulated_result !== "failure") return errors.invalid("validation_result_invalid");

  state.effects.vaultRevokes += 1;
  account.credential.validationTarget = null;
  account.credential.decryptable = false;
  if (target === "pending") {
    delete state.vault[`${account.providerAccountId}:pending`];
    account.credential.pendingVersion = null;
    account.credential.priorVersionForInFlight = null;
    account.credential.retiredForNewAdmissions = account.credential.currentVersion;
    const preserveDisabled = account.credential.disabledIntent
      || account.lifecycleState === "disabled"
      || account.credential.reauthReturnState === "disabled";
    account.credential.reauthReturnState = null;
    account.lifecycleState = preserveDisabled ? "disabled" : "reauth_required";
    account.health = condition("account", "expired", "credential_rejected", version, "reauthenticate");
  } else {
    delete state.vault[account.providerAccountId];
    account.credential.currentVersion = null;
    account.credential.probeReturnState = null;
    account.credential.disabledIntent = false;
    account.lifecycleState = "draft";
    account.health = condition("account", "unknown", "credential_rejected", version, "submit_credential");
  }
  invalidateSnapshot(account.providerAccountId);
  account.updatedAt = clock();
  return accountResponse(account, 200, ["validation_failed", "staged_version_revoked", account.lifecycleState]);
}

function startOAuth(principal, request) {
  const { account, denied } = accessAccount(principal, request.provider_account_id, "accounts.manage");
  if (denied) return denied;
  if (!account.authMode.includes("oauth")) return errors.invalid("oauth_not_supported_for_auth_mode");
  if (!["connect", "reauthenticate"].includes(request.purpose)) return errors.invalid("oauth_purpose_invalid");

  if (request.purpose === "connect") {
    if (account.lifecycleState !== "draft" || account.credential.currentVersion) {
      return errors.accountNotUsable("oauth_connect_requires_draft");
    }
  } else {
    if (!account.credential.currentVersion || !["active", "reauth_required", "revoked", "disabled"].includes(account.lifecycleState)) {
      return errors.accountNotUsable("oauth_reauthentication_not_allowed");
    }
    if (
      account.credential.pendingVersion
      || account.credential.validationTarget
      || account.credential.reauthReturnState
      || account.credential.probeReturnState
      || hasPendingOAuthAuthorization(account.providerAccountId)
    ) {
      return errors.accountNotUsable("reauthentication_in_progress");
    }
    account.credential.priorVersionForInFlight = account.credential.currentVersion;
    account.credential.retiredForNewAdmissions = account.credential.currentVersion;
    account.credential.reauthReturnState = account.lifecycleState === "disabled" ? "disabled" : "active";
    account.credential.decryptable = false;
    invalidateSnapshot(account.providerAccountId);
  }

  const authorizationId = nextId("oauth");
  if (account.lifecycleState !== "disabled") account.lifecycleState = "pending_validation";
  account.updatedAt = clock();
  state.oauth[authorizationId] = {
    tenantId: principal.tenantId,
    providerAccountId: account.providerAccountId,
    purpose: request.purpose,
    flow: request.flow_preference,
    status: "authorization_pending",
    expiresAt: clock(),
  };
  return {
    status: 202,
    body: {
      authorization_id: authorizationId,
      provider_account_id: account.providerAccountId,
      purpose: request.purpose,
      flow: request.flow_preference,
      status: "authorization_pending",
      verification_uri: "https://provider.example/device",
      user_code: "ABCD-EFGH",
      expires_at: state.oauth[authorizationId].expiresAt,
      remediation: "complete_oauth",
    },
  };
}

function pollOAuth(principal, request) {
  const { account, denied } = accessAccount(principal, request.provider_account_id, "accounts.manage");
  if (denied) return denied;
  const authorization = state.oauth[request.authorization_id];
  if (!authorization || authorization.tenantId !== principal.tenantId || authorization.providerAccountId !== account.providerAccountId) {
    return errors.notFound();
  }

  if (authorization.status === "authorization_pending" && request.simulated_result === "succeeded") {
    authorization.status = "succeeded";
    authorization.remediation = "none";
    state.effects.oauthExchanges += 1;
    state.effects.vaultWrites += 1;
    const nextVersion = allocateCredentialVersion(account);
    if (authorization.purpose === "reauthenticate") {
      state.vault[`${account.providerAccountId}:pending`] = { version: nextVersion, revoked: false, deleted: false };
      account.credential.pendingVersion = nextVersion;
    } else {
      state.vault[account.providerAccountId] = { version: nextVersion, revoked: false, deleted: false };
      account.credential.currentVersion = nextVersion;
      account.credential.probeReturnState = account.credential.disabledIntent || account.lifecycleState === "disabled"
        ? "disabled"
        : "active";
    }
    account.credential.decryptable = false;
    account.credential.lastValidatedAt = clock();
    const preserveDisabled = account.credential.disabledIntent
      || account.lifecycleState === "disabled"
      || account.credential.reauthReturnState === "disabled";
    account.lifecycleState = preserveDisabled ? "disabled" : "pending_probe";
    account.updatedAt = clock();
  } else if (authorization.status === "authorization_pending" && request.simulated_result === "failed") {
    authorization.status = "failed";
    authorization.remediation = "complete_oauth";
    state.effects.oauthExchanges += 1;
    account.credential.decryptable = false;
    if (authorization.purpose === "reauthenticate") {
      const preserveDisabled = account.credential.disabledIntent
        || account.lifecycleState === "disabled"
        || account.credential.reauthReturnState === "disabled";
      account.credential.priorVersionForInFlight = null;
      account.credential.reauthReturnState = null;
      account.lifecycleState = preserveDisabled ? "disabled" : "reauth_required";
    } else {
      account.credential.disabledIntent = false;
      account.lifecycleState = "draft";
    }
    account.updatedAt = clock();
  }
  return {
    status: 200,
    body: {
      authorization_id: request.authorization_id,
      provider_account_id: account.providerAccountId,
      purpose: authorization.purpose,
      flow: authorization.flow,
      status: authorization.status,
      expires_at: authorization.expiresAt,
      remediation: authorization.remediation
        || (authorization.status === "authorization_pending" ? "complete_oauth" : "none"),
    },
  };
}

function buildSnapshot(account, freshness = "fresh", evidenceClass = "live_probe") {
  const observedStatuses = account.authMode.startsWith("gemini")
    ? { chat: "verified", chat_streaming: "conditionally_supported", image_generation: "conditionally_supported", image_edit: "conditionally_supported", inpaint: "unsupported" }
    : { chat: "verified", chat_streaming: "conditionally_supported", image_generation: "conditionally_supported", image_edit: "conditionally_supported", inpaint: "conditionally_supported" };
  const statuses = evidenceClass === "reference_learned"
    ? Object.fromEntries(OPERATIONS.map((operation) => [operation, "unverified"]))
    : observedStatuses;
  const operations = Object.fromEntries(OPERATIONS.map((operation) => [operation, {
    status: statuses[operation],
    offerable: freshness === "fresh" && evidenceClass !== "reference_learned" && OFFERABLE.has(statuses[operation]),
    evidence_class: evidenceClass,
    ...(operation === "chat_streaming" ? { streaming_class: account.authMode === "gemini_web_cookie" ? "synthetic" : "real" } : {}),
  }]));
  return {
    provider_account_id: account.providerAccountId,
    credential_version: account.credential.currentVersion,
    verified_at: clock(),
    freshness,
    ttl_class: evidenceClass === "reference_learned" ? "TTL-REFERENCE" : "TTL-PROBE-LIVE",
    provenance: [{
      evidence_class: evidenceClass,
      probe_surface: evidenceClass === "reference_learned" ? "provider-reference-metadata" : "safe-account-probe",
      observed_at: clock(),
    }],
    operations,
    models: [{
      model_slug: account.provider === "gemini" ? "gemini-observed-1" : "gpt-observed-1",
      operations: clone(statuses),
      surface_binding: account.authMode,
      observed_at: clock(),
    }],
  };
}

function invalidateSnapshot(providerAccountId) {
  const snapshot = state.snapshots[providerAccountId];
  if (!snapshot) return;
  snapshot.freshness = "invalid";
  for (const fact of Object.values(snapshot.operations)) fact.offerable = false;
}

function completeProbe(principal, request) {
  const { account, denied } = accessAccount(principal, request.provider_account_id, "accounts.manage");
  if (denied) return denied;
  if (account.credential.validationTarget) {
    return errors.accountNotUsable("credential_validation_pending");
  }
  const controlDenial = probeControlDenial(account);
  if (controlDenial) return controlDenial;

  const pendingVersion = account.credential.pendingVersion;
  const probeVersion = pendingVersion || account.credential.currentVersion;
  const disabledPendingProbe = Boolean(
    pendingVersion
    && account.lifecycleState === "disabled"
    && account.credential.reauthReturnState === "disabled",
  );
  const disabledCurrentProbe = Boolean(
    !pendingVersion
    && account.lifecycleState === "disabled"
    && account.credential.disabledIntent
    && account.credential.probeReturnState === "disabled",
  );
  if (
    !probeVersion
    || (
      account.lifecycleState !== "pending_probe"
      && !disabledPendingProbe
      && !disabledCurrentProbe
    )
  ) {
    return errors.accountNotUsable("probe_not_allowed");
  }

  state.effects.vaultDecrypts += 1;
  state.effects.adapterCalls += 1;
  state.effects.providerProbeCalls += 1;
  account.credential.lastProbedAt = clock();

  if (request.simulated_result === "success") {
    const trace = ["current_version_probe_success"];
    if (pendingVersion) {
      const oldVersion = account.credential.currentVersion;
      account.credential.currentVersion = pendingVersion;
      account.credential.pendingVersion = null;
      account.credential.retiredForNewAdmissions = oldVersion;
      account.credential.priorVersionForInFlight = null;
      if (oldVersion) state.effects.vaultRevokes += 1;
      state.vault[account.providerAccountId] = { version: pendingVersion, revoked: false, deleted: false };
      delete state.vault[`${account.providerAccountId}:pending`];
      trace.splice(0, 1, "new_version_probe_success", "cutover", "prior_version_inflight_only");
    }

    const stagedReturnState = pendingVersion
      ? account.credential.reauthReturnState || "active"
      : account.credential.probeReturnState || "active";
    const returnState = account.credential.disabledIntent
      || account.lifecycleState === "disabled"
      || account.credential.reauthReturnState === "disabled"
      ? "disabled"
      : stagedReturnState;
    account.credential.reauthReturnState = null;
    account.credential.probeReturnState = null;
    account.lifecycleState = returnState;
    account.credential.decryptable = returnState === "active";
    account.health = condition("account", "healthy", "probe_succeeded", account.credential.currentVersion, "none");
    state.snapshots[account.providerAccountId] = buildSnapshot(account);
    if (returnState !== "active") invalidateSnapshot(account.providerAccountId);
    account.updatedAt = clock();
    return accountResponse(account, 200, trace);
  }

  if (request.simulated_result === "auth_failure") {
    const preserveDisabled = account.credential.disabledIntent
      || account.lifecycleState === "disabled"
      || account.credential.reauthReturnState === "disabled";
    if (pendingVersion) {
      state.effects.vaultRevokes += 1;
      delete state.vault[`${account.providerAccountId}:pending`];
      account.credential.pendingVersion = null;
      account.credential.retiredForNewAdmissions = account.credential.currentVersion;
      account.credential.priorVersionForInFlight = null;
    }
    account.credential.reauthReturnState = null;
    account.credential.probeReturnState = null;
    account.lifecycleState = preserveDisabled ? "disabled" : "reauth_required";
    account.credential.decryptable = false;
    account.health = condition("account", "expired", "credential_rejected", probeVersion, "reauthenticate");
    invalidateSnapshot(account.providerAccountId);
  } else {
    const preserveDisabled = account.credential.disabledIntent
      || account.lifecycleState === "disabled"
      || account.credential.reauthReturnState === "disabled"
      || account.credential.probeReturnState === "disabled";
    account.lifecycleState = preserveDisabled ? "disabled" : "pending_probe";
    account.credential.decryptable = false;
    account.health = condition("account", "degraded", "upstream_unavailable", probeVersion, "wait_provider_cooldown");
  }
  account.updatedAt = clock();
  return accountResponse(account, 200);
}

function disableAccount(principal, request) {
  const { account, denied } = accessAccount(principal, request.provider_account_id, "accounts.manage");
  if (denied) return denied;
  if (account.lifecycleState === "draft") return errors.accountNotUsable("draft_cannot_disable");
  account.credential.disabledIntent = true;
  if (account.lifecycleState !== "disabled") {
    if (account.credential.pendingVersion || account.credential.reauthReturnState) {
      account.credential.reauthReturnState = "disabled";
    }
    if (account.credential.probeReturnState) {
      account.credential.probeReturnState = "disabled";
    }
    account.lifecycleState = "disabled";
    account.credential.decryptable = false;
    account.updatedAt = clock();
    invalidateSnapshot(account.providerAccountId);
  }
  return accountResponse(account);
}

function enableAccount(principal, request) {
  const { account, denied } = accessAccount(principal, request.provider_account_id, "accounts.manage");
  if (denied) return denied;
  const journeyInProgress = account.credential.validationTarget
    || account.credential.reauthReturnState
    || account.credential.probeReturnState
    || hasPendingOAuthAuthorization(account.providerAccountId);
  if (
    account.lifecycleState !== "disabled"
    || !account.credential.currentVersion
    || account.credential.pendingVersion
    || journeyInProgress
  ) {
    return errors.accountNotUsable("enable_requires_idle_disabled_credentialed_account");
  }
  account.credential.disabledIntent = false;
  account.lifecycleState = "pending_probe";
  account.credential.decryptable = false;
  account.credential.probeReturnState = "active";
  account.updatedAt = clock();
  return accountResponse(account, 202, ["disabled", "pending_probe", "current_version_probe_required"]);
}

function reauthenticate(principal, request) {
  const { account, denied } = accessAccount(principal, request.provider_account_id, "accounts.manage");
  if (denied) return denied;
  if (!account.credential.currentVersion || !["active", "reauth_required", "revoked", "disabled"].includes(account.lifecycleState)) {
    return errors.accountNotUsable("reauthentication_not_allowed");
  }
  if (
    account.credential.pendingVersion
    || account.credential.validationTarget
    || account.credential.reauthReturnState
    || account.credential.probeReturnState
    || hasPendingOAuthAuthorization(account.providerAccountId)
  ) {
    return errors.accountNotUsable("reauthentication_in_progress");
  }
  const validationError = validateCredential(account, request.credential);
  if (validationError) return errors.invalid(validationError);

  const priorVersion = account.credential.currentVersion;
  const pendingVersion = allocateCredentialVersion(account);
  state.effects.vaultWrites += 1;
  state.vault[`${account.providerAccountId}:pending`] = { version: pendingVersion, revoked: false, deleted: false };
  account.credential.pendingVersion = pendingVersion;
  account.credential.validationTarget = "pending";
  account.credential.priorVersionForInFlight = priorVersion;
  account.credential.retiredForNewAdmissions = priorVersion;
  account.credential.reauthReturnState = account.lifecycleState === "disabled" ? "disabled" : "active";
  account.credential.decryptable = false;
  invalidateSnapshot(account.providerAccountId);
  if (account.lifecycleState !== "disabled") account.lifecycleState = "pending_validation";
  account.updatedAt = clock();
  return accountResponse(account, 202, [
    "same_provider_account_id",
    "pending_version_stored",
    account.lifecycleState,
    "old_version_new_admission_blocked",
  ]);
}



function deleteAccount(principal, request) {
  const { account, denied } = accessAccount(principal, request.provider_account_id, "accounts.manage");
  if (denied) return denied;
  account.credential.decryptable = false;
  const vaultKeys = [account.providerAccountId, `${account.providerAccountId}:pending`]
    .filter((key) => state.vault[key]);
  for (const key of vaultKeys) {
    state.vault[key].revoked = true;
    state.vault[key].deleted = true;
    state.effects.vaultRevokes += 1;
    state.effects.vaultDeletes += 1;
  }
  delete state.vault[`${account.providerAccountId}:pending`];
  if (state.retentionHolds[account.providerAccountId]) {
    state.retentionEvidence[account.providerAccountId] = { encryptedEvidenceRetained: true, decryptable: false, restorable: false };
  }
  account.lifecycleState = "deleted";
  account.deleted = true;
  account.updatedAt = clock();
  delete state.snapshots[account.providerAccountId];
  return { status: 204, body: null };
}

function getSnapshot(principal, request) {
  if (!principal.authenticated) return errors.authentication();
  const account = ownAccount(principal, request.provider_account_id);
  if (!account) return errors.notFound();
  if (!principal.scopes.has("accounts.read") && !principal.scopes.has("capabilities.read")) return errors.forbidden();
  const snapshot = state.snapshots[account.providerAccountId];
  if (!snapshot) return errors.capabilityUnverified();
  return { status: 200, body: clone(snapshot) };
}

function authorizeCapability(principal, request) {
  if (!principal.authenticated) return errors.authentication();
  if (!OPERATIONS.includes(request.operation)) return errors.invalid("operation_invalid");
  const { account, denied } = accessAccount(principal, request.provider_account_id, executionScope(request.operation));
  if (denied) return denied;
  if (account.lifecycleState !== "active" || !account.credential.decryptable) return errors.accountNotUsable("lifecycle_or_credential_gate");
  if (executionControlsBlocked(account)) return errors.accountNotUsable("administrative_control");
  if (healthBlocksOperation(account, request)) return errors.accountNotUsable("health_gate");

  const snapshot = state.snapshots[account.providerAccountId];
  if (!snapshot) return errors.capabilityUnverified();
  if (snapshot.freshness !== "fresh") return errors.snapshotStale();
  const fact = snapshot.operations[request.operation];
  if (!fact || !fact.offerable) {
    return fact?.status === "unsupported"
      ? canonical(409, "capability_unsupported", "capability", "capability", "capability_unsupported")
      : errors.capabilityUnverified();
  }
  state.effects.adapterCalls += 1;
  state.effects.vaultDecrypts += 1;
  return { status: 200, body: { admitted: true, provider_account_id: account.providerAccountId, operation: request.operation } };
}

function getRoutingPolicy(principal) {
  const denied = authenticateAndAuthorize(principal, "routing.read");
  if (denied) return denied;
  return { status: 200, body: publicRouting(state.policies[principal.tenantId]) };
}

function replaceRoutingPolicy(principal, request) {
  const denied = authenticateAndAuthorize(principal, "routing.manage");
  if (denied) return denied;
  const referencedAccounts = new Set([
    ...(request.candidate_accounts || []),
    ...(request.selection_order || []),
    ...(request.fallback_chain || []),
  ]);
  for (const providerAccountId of referencedAccounts) {
    if (!ownAccount(principal, providerAccountId)) return errors.notFound();
  }
  state.policies[principal.tenantId] = {
    candidateAccounts: clone(request.candidate_accounts),
    selectionOrder: clone(request.selection_order),
    fallbackEnabled: request.fallback_enabled,
    fallbackChain: clone(request.fallback_chain),
    fallbackAuthModes: clone(request.fallback_auth_modes),
    affinity: { enabled: request.affinity?.enabled ?? false, windowClass: request.affinity?.window_class ?? "" },
    leasePolicy: { enabled: request.lease_policy?.enabled ?? false, eligibleUnits: clone(request.lease_policy?.eligible_units || []) },
    updatedAt: clock(),
    updatedBy: principal.key,
  };
  state.effects.policyWrites += 1;
  return { status: 200, body: publicRouting(state.policies[principal.tenantId]) };
}

function routeExplicitPin(principal, request) {
  if (!principal.authenticated) return errors.authentication();
  if (!OPERATIONS.includes(request.operation)) return errors.invalid("operation_invalid");
  const { account, denied } = accessAccount(principal, request.provider_account_id, executionScope(request.operation));
  if (denied) return denied;
  const controlsBlocked = executionControlsBlocked(account);
  const healthBlocked = healthBlocksOperation(account, request);
  const snapshot = state.snapshots[account.providerAccountId];
  const fact = snapshot?.operations?.[request.operation];
  const capabilityBlocked = !snapshot
    || snapshot.freshness !== "fresh"
    || !fact?.offerable;
  if (account.lifecycleState !== "active" || !account.credential.decryptable || controlsBlocked || healthBlocked || capabilityBlocked) {
    return canonical(409, "routing_fallback_not_allowed", "routing", "routing", "routing_remediation", { safeContext: { cause_class: "explicit_pin_no_fallback" } });
  }
  return { status: 200, body: { selected_provider_account_id: account.providerAccountId, fallback_used: false } };
}

function setScopedHealth(account, healthState, reason, operation, retryAfterSeconds, remediation) {
  account.health = condition("operation", healthState, reason, account.credential.currentVersion || 0, remediation, { operation, retryAfterSeconds });
  account.updatedAt = clock();
}

function noSecretBearingKeys(value, path = "$", found = []) {
  if (Array.isArray(value)) value.forEach((item, index) => noSecretBearingKeys(item, `${path}[${index}]`, found));
  else if (value && typeof value === "object") {
    for (const [key, nested] of Object.entries(value)) {
      if (isSecretName(key)) found.push(`${path}.${key}`);
      noSecretBearingKeys(nested, `${path}.${key}`, found);
    }
  } else if (typeof value === "string" && SECRET_VALUE_PATTERNS.some((pattern) => pattern.test(value))) {
    found.push(path);
  }
  return found;
}

assert.deepEqual(
  noSecretBearingKeys({ accessToken: "opaque", nested: { setCookie: "opaque" } }),
  ["$.accessToken", "$.nested.setCookie"],
);
assert.deepEqual(
  redact({ accessToken: "opaque", nested: { setCookie: "opaque" } }),
  { accessToken: "<redacted>", nested: { setCookie: "<redacted>" } },
);

console.log("PROTOTYPE — issue #19 management contract logic; throwaway executable evidence, not Gateway runtime.\n");

// Authentication and authorization precedence.
action("Invalid Client API Key is indistinguishable 401", invalidKey, {}, listAccounts, ({ response, delta }) => {
  assert.equal(response.status, 401);
  assert.equal(response.body.code, "authentication_failed");
  assert.deepEqual(delta, effectDelta(state.effects, state.effects));
});
action("Same-Tenant missing scope is 403", reader, { provider: "gemini", auth_mode: "gemini_web_cookie" }, createAccount, ({ response, delta }) => {
  assert.equal(response.status, 403);
  assert.equal(response.body.code, "forbidden");
  assert.equal(delta.vaultWrites, 0);
  assert.equal(delta.adapterCalls, 0);
});
action("Foreign account is 404 before scope evaluation", noScope, { provider_account_id: "pa_foreign" }, getAccount, ({ response, delta }) => {
  assert.equal(response.status, 404);
  assert.equal(response.body.code, "resource_not_found");
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.equal("resource_reference" in response.body, false);
});

// Create shell and Auth Mode gates.
action("Prohibited Auth Mode create fails closed", admin, { provider: "grok", auth_mode: "grok_web_sso" }, createAccount, ({ response, delta }) => {
  assert.equal(response.body.code, "auth_mode_unavailable");
  assert.equal(delta.vaultWrites, 0);
});
action("Gated-off valid Auth Mode create fails closed", admin, { provider: "chatgpt", auth_mode: "chatgpt_web_access" }, createAccount, ({ response, delta }) => {
  assert.equal(response.body.code, "auth_mode_unavailable");
  assert.equal(delta.adapterCalls, 0);
});
const created = action("Create shell starts draft without credential", admin, { provider: "gemini", auth_mode: "gemini_web_cookie", label: "Gemini web" }, createAccount, ({ response, delta }) => {
  assert.equal(response.status, 201);
  assert.equal(response.body.account.lifecycle_state, "draft");
  assert.equal("version" in response.body.account.credential, false);
  assert.equal(delta.vaultWrites, 0);
});
const directId = created.body.account.provider_account_id;
action("Same-Tenant resource without required scope is 403", noScope, { provider_account_id: directId }, getAccount, ({ response }) => {
  assert.equal(response.status, 403);
  assert.equal(response.body.code, "forbidden");
});

// Direct credential validation and probe outcomes.
action("Missing credential class is rejected before vault", admin, { provider_account_id: directId, credential: { material: "cookie-session-material" } }, submitCredential, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "credential_class_invalid");
  assert.equal(delta.vaultWrites, 0);
});
action("Mismatched credential class is rejected before vault", admin, { provider_account_id: directId, credential: { credential_class: "oauth_token_import", material: "cookie-session-material" } }, submitCredential, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "credential_class_mismatch");
  assert.equal(delta.vaultWrites, 0);
});
action("Control character credential is rejected before vault and probe", admin, { provider_account_id: directId, credential: { credential_class: "web_session", material: "bad\nmaterial" } }, submitCredential, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "credential_malformed");
  assert.equal(delta.vaultWrites, 0);
  assert.equal(delta.providerProbeCalls, 0);
});
action("Direct credential store lands in observable pending_validation", admin, { provider_account_id: directId, credential: { credential_class: "web_session", material: "cookie-session-material" } }, submitCredential, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "pending_validation");
  assert.equal(response.body.account.credential.version, 1);
  assert.equal("last_validated_at" in response.body.account.credential, false);
  assert.equal(delta.vaultWrites, 1);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
  assert.equal(noSecretBearingKeys(response).length, 0);
});
action("Credential intake cannot bypass lifecycle after draft", admin, { provider_account_id: directId, credential: { credential_class: "web_session", material: "second-session-material" } }, submitCredential, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "credential_intake_requires_draft");
  assert.equal(delta.vaultWrites, 0);
});
action("Server-owned validation success advances to pending_probe", admin, { provider_account_id: directId, simulated_result: "success" }, completeCredentialValidation, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "pending_probe");
  assert.equal(response.body.account.credential.version, 1);
  assert.equal("last_validated_at" in response.body.account.credential, true);
  assert.equal(delta.vaultWrites, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
});
const validationFailureAccount = action("Create shell for post-store validation failure", admin, { provider: "gemini", auth_mode: "gemini_web_cookie", label: "Validation failure" }, createAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "draft");
});
const validationFailureId = validationFailureAccount.body.account.provider_account_id;
action("Credential is stored before server-owned validation", admin, { provider_account_id: validationFailureId, credential: { credential_class: "web_session", material: "stored-invalid-session" } }, submitCredential, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "pending_validation");
  assert.equal(delta.vaultWrites, 1);
  assert.equal(delta.adapterCalls, 0);
});
action("Post-store validation failure revokes material with safe remediation", admin, { provider_account_id: validationFailureId, simulated_result: "failure" }, completeCredentialValidation, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "draft");
  assert.equal("version" in response.body.account.credential, false);
  assert.equal(response.body.account.health.conditions[0].remediation, "submit_credential");
  assert.equal(state.accounts[validationFailureId].credential.highestVersion, 1);
  assert.equal(state.vault[validationFailureId], undefined);
  assert.equal(delta.vaultRevokes, 1);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
});
action("Transient activation probe failure stays non-usable", admin, { provider_account_id: directId, simulated_result: "transient_failure" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "pending_probe");
  assert.equal(response.body.account.health.summary_state, "degraded");
  assert.equal(delta.providerProbeCalls, 1);
});
state.accounts[directId].controls.drain = "draining";
action("Drain does not block an authorized recovery probe", admin, { provider_account_id: directId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "active");
  assert.equal(response.body.account.health.summary_state, "healthy");
  assert.equal(response.body.account.administrative_controls.drain_state, "draining");
  assert.equal(delta.vaultDecrypts, 1);
  assert.equal(delta.adapterCalls, 1);
  assert.equal(delta.providerProbeCalls, 1);
  assert.equal(state.snapshots[directId].credential_version, 1);
});
state.accounts[directId].controls.drain = "off";

// Inference admission helpers prove the management state is fail-closed.
action("Capability authorization requires a valid Client API Key", invalidKey, { provider_account_id: directId, operation: "chat" }, authorizeCapability, ({ response, delta }) => {
  assert.equal(response.status, 401);
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
});
action("Foreign capability authorization is 404 before scope evaluation", noScope, { provider_account_id: "pa_foreign", operation: "chat" }, authorizeCapability, ({ response, delta }) => {
  assert.equal(response.status, 404);
  assert.equal(delta.vaultDecrypts, 0);
});
action("Same-Tenant capability authorization requires operation scope", reader, { provider_account_id: directId, operation: "chat" }, authorizeCapability, ({ response, delta }) => {
  assert.equal(response.status, 403);
  assert.equal(delta.adapterCalls, 0);
});
action("Fresh verified capability admits only after every gate", admin, { provider_account_id: directId, operation: "chat" }, authorizeCapability, ({ response, delta }) => {
  assert.equal(response.body.admitted, true);
  assert.equal(delta.adapterCalls, 1);
  assert.equal(delta.vaultDecrypts, 1);
});

// OAuth boundary on a second account.
const oauthAccount = action("Create OAuth account shell", admin, { provider: "chatgpt", auth_mode: "chatgpt_codex_oauth", label: "Codex OAuth" }, createAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "draft");
});
const oauthId = oauthAccount.body.account.provider_account_id;
const oauthStart = action("OAuth start returns safe server-owned authorization metadata", admin, { provider_account_id: oauthId, purpose: "connect", flow_preference: "device" }, startOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "authorization_pending");
  assert.equal(noSecretBearingKeys(response).length, 0);
  assert.equal(delta.vaultWrites, 0);
});
action("OAuth connect cannot restart outside pure draft", admin, { provider_account_id: oauthId, purpose: "connect", flow_preference: "device" }, startOAuth, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "oauth_connect_requires_draft");
  assert.equal(delta.oauthExchanges, 0);
});
action("OAuth poll success stores server-side material but returns no token", admin, { provider_account_id: oauthId, authorization_id: oauthStart.body.authorization_id, simulated_result: "succeeded" }, pollOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "succeeded");
  assert.equal(delta.oauthExchanges, 1);
  assert.equal(delta.vaultWrites, 1);
  assert.equal(noSecretBearingKeys(response).length, 0);
  assert.equal(state.accounts[oauthId].lifecycleState, "pending_probe");
});
action("OAuth activation auth failure requires reauthentication", admin, { provider_account_id: oauthId, simulated_result: "auth_failure" }, completeProbe, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "reauth_required");
  assert.equal(response.body.account.health.summary_state, "expired");
  assert.equal("retry_after_seconds" in response.body.account.health.conditions[0], false);
});
const oauthFailureAccount = action("Create OAuth account for failed authorization", admin, { provider: "chatgpt", auth_mode: "chatgpt_codex_oauth", label: "OAuth failure" }, createAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "draft");
});
const oauthFailureId = oauthFailureAccount.body.account.provider_account_id;
const oauthFailureStart = action("OAuth failure journey starts with safe pending metadata", admin, { provider_account_id: oauthFailureId, purpose: "connect", flow_preference: "device" }, startOAuth, ({ response }) => {
  assert.equal(response.body.status, "authorization_pending");
  assert.equal(response.body.remediation, "complete_oauth");
});
action("OAuth failed status returns remediation without storing credential", admin, { provider_account_id: oauthFailureId, authorization_id: oauthFailureStart.body.authorization_id, simulated_result: "failed" }, pollOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "failed");
  assert.equal(response.body.remediation, "complete_oauth");
  assert.equal(state.accounts[oauthFailureId].lifecycleState, "draft");
  assert.equal(delta.oauthExchanges, 1);
  assert.equal(delta.vaultWrites, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(noSecretBearingKeys(response).length, 0);
});
action("Failed OAuth authorization is terminal and idempotent", admin, { provider_account_id: oauthFailureId, authorization_id: oauthFailureStart.body.authorization_id, simulated_result: "succeeded" }, pollOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "failed");
  assert.equal(delta.oauthExchanges, 0);
  assert.equal(delta.vaultWrites, 0);
});

// Tenant list isolation and disable semantics.
action("List exposes only same-Tenant non-deleted accounts", reader, {}, listAccounts, ({ response, delta }) => {
  assert.equal(response.body.data.some((account) => account.provider_account_id === "pa_foreign"), false);
  assert.equal(response.body.data.some((account) => account.provider_account_id === directId), true);
  assert.equal(response.body.data.some((account) => account.provider_account_id === oauthId), true);
  assert.equal(delta.vaultDecrypts, 0);
});
action("Disable active account blocks new use and preserves health evidence", admin, { provider_account_id: directId }, disableAccount, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(response.body.account.health.summary_state, "healthy");
  assert.equal(delta.vaultDeletes, 0);
  assert.equal(state.snapshots[directId].freshness, "invalid");
});
action("Disable is idempotent", admin, { provider_account_id: directId }, disableAccount, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
});
action("Disable reauth-required account after credential intake is allowed", admin, { provider_account_id: oauthId }, disableAccount, ({ response, delta }) => {
  assert.equal(response.status, 200);
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(delta.vaultDeletes, 0);
});
const draftOnly = action("Create separate draft for disable rejection", admin, { provider: "gemini", auth_mode: "gemini_web_cookie" }, createAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "draft");
});
action("Pure draft disable is stable rejection", admin, { provider_account_id: draftOnly.body.account.provider_account_id }, disableAccount, ({ response, delta }) => {
  assert.equal(response.body.code, "account_not_usable");
  assert.equal(delta.vaultDecrypts, 0);
});

// OAuth reauthentication preserves disabled administration state.
const oauthReauthStart = action("Disabled OAuth account can start reauthentication without hiding disabled lifecycle", admin, { provider_account_id: oauthId, purpose: "reauthenticate", flow_preference: "device" }, startOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "authorization_pending");
  assert.equal(state.accounts[oauthId].lifecycleState, "disabled");
  assert.equal(state.accounts[oauthId].credential.decryptable, false);
  assert.equal(delta.vaultDecrypts, 0);
});
action("Second OAuth reauthentication is rejected while authorization is pending", admin, { provider_account_id: oauthId, purpose: "reauthenticate", flow_preference: "browser" }, startOAuth, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "reauthentication_in_progress");
  assert.equal(delta.oauthExchanges, 0);
  assert.equal(delta.vaultWrites, 0);
});
action("Direct replacement cannot race an OAuth reauthentication", admin, { provider_account_id: oauthId, credential: { credential_class: "oauth_token_import", material: "parallel-oauth-material" } }, reauthenticate, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "reauthentication_in_progress");
  assert.equal(state.accounts[oauthId].credential.pendingVersion, null);
  assert.equal(delta.vaultWrites, 0);
});
action("Enable is rejected while disabled OAuth reauthentication is pending", admin, { provider_account_id: oauthId }, enableAccount, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "enable_requires_idle_disabled_credentialed_account");
  assert.equal(state.accounts[oauthId].lifecycleState, "disabled");
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.providerProbeCalls, 0);
});
action("Failed disabled OAuth reauthentication preserves disabled intent without storing credential", admin, { provider_account_id: oauthId, authorization_id: oauthReauthStart.body.authorization_id, simulated_result: "failed" }, pollOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "failed");
  assert.equal(response.body.remediation, "complete_oauth");
  assert.equal(state.accounts[oauthId].lifecycleState, "disabled");
  assert.equal(state.accounts[oauthId].credential.disabledIntent, true);
  assert.equal(state.accounts[oauthId].credential.currentVersion, 1);
  assert.equal(state.accounts[oauthId].credential.pendingVersion, null);
  assert.equal(delta.oauthExchanges, 1);
  assert.equal(delta.vaultWrites, 0);
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
  assert.equal(noSecretBearingKeys(response).length, 0);
});
action("Failed disabled OAuth reauthentication authorization stays terminal", admin, { provider_account_id: oauthId, authorization_id: oauthReauthStart.body.authorization_id, simulated_result: "succeeded" }, pollOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "failed");
  assert.equal(state.accounts[oauthId].credential.pendingVersion, null);
  assert.equal(delta.oauthExchanges, 0);
  assert.equal(delta.vaultWrites, 0);
});
const oauthReauthRetry = action("Disabled OAuth account can restart reauthentication after terminal failure", admin, { provider_account_id: oauthId, purpose: "reauthenticate", flow_preference: "browser" }, startOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "authorization_pending");
  assert.equal(state.accounts[oauthId].lifecycleState, "disabled");
  assert.equal(delta.vaultWrites, 0);
});
action("OAuth reauthentication exchange stages a pending version while disabled", admin, { provider_account_id: oauthId, authorization_id: oauthReauthRetry.body.authorization_id, simulated_result: "succeeded" }, pollOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "succeeded");
  assert.equal(state.accounts[oauthId].lifecycleState, "disabled");
  assert.equal(state.accounts[oauthId].credential.currentVersion, 1);
  assert.equal(state.accounts[oauthId].credential.pendingVersion, 2);
  assert.equal(delta.vaultWrites, 1);
});
action("OAuth reauthentication probe returns disabled and non-decryptable", admin, { provider_account_id: oauthId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(response.body.account.credential.version, 2);
  assert.equal(state.accounts[oauthId].credential.decryptable, false);
  assert.equal(state.snapshots[oauthId].freshness, "invalid");
  assert.equal(delta.vaultRevokes, 1);
});
action("OAuth account enable still requires current-version probe", admin, { provider_account_id: oauthId }, enableAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "pending_probe");
});
action("OAuth current-version probe restores active use", admin, { provider_account_id: oauthId, simulated_result: "success" }, completeProbe, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "active");
  assert.equal(response.body.account.credential.version, 2);
});
const activeOAuthFailureStart = action("Active OAuth account starts reauthentication failure journey", admin, { provider_account_id: oauthId, purpose: "reauthenticate", flow_preference: "device" }, startOAuth, ({ response }) => {
  assert.equal(response.body.status, "authorization_pending");
  assert.equal(state.accounts[oauthId].credential.reauthReturnState, "active");
});
action("Failed active-origin OAuth reauthentication returns reauth_required without storing credential", admin, { provider_account_id: oauthId, authorization_id: activeOAuthFailureStart.body.authorization_id, simulated_result: "failed" }, pollOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "failed");
  assert.equal(response.body.remediation, "complete_oauth");
  assert.equal(state.accounts[oauthId].lifecycleState, "reauth_required");
  assert.equal(state.accounts[oauthId].credential.currentVersion, 2);
  assert.equal(state.accounts[oauthId].credential.pendingVersion, null);
  assert.equal(delta.oauthExchanges, 1);
  assert.equal(delta.vaultWrites, 0);
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
  assert.equal(noSecretBearingKeys(response).length, 0);
});
const activeOAuthReauth = action("Non-disabled OAuth account can restart replacement after terminal failure", admin, { provider_account_id: oauthId, purpose: "reauthenticate", flow_preference: "device" }, startOAuth, ({ response }) => {
  assert.equal(response.body.status, "authorization_pending");
  assert.equal(state.accounts[oauthId].credential.reauthReturnState, "active");
});
action("Disable before OAuth exchange overrides the staged return state", admin, { provider_account_id: oauthId }, disableAccount, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(state.accounts[oauthId].credential.reauthReturnState, "disabled");
  assert.equal(delta.vaultDecrypts, 0);
});
action("OAuth exchange after disable stages a new pending version", admin, { provider_account_id: oauthId, authorization_id: activeOAuthReauth.body.authorization_id, simulated_result: "succeeded" }, pollOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "succeeded");
  assert.equal(state.accounts[oauthId].credential.currentVersion, 2);
  assert.equal(state.accounts[oauthId].credential.pendingVersion, 3);
  assert.equal(state.accounts[oauthId].credential.reauthReturnState, "disabled");
  assert.equal(delta.vaultWrites, 1);
});
action("Post-disable OAuth cutover cannot reactivate the account", admin, { provider_account_id: oauthId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(response.body.account.credential.version, 3);
  assert.equal(state.accounts[oauthId].credential.decryptable, false);
  assert.equal(delta.vaultRevokes, 1);
});

const disabledConnectFailureAccount = action("Create OAuth connect failure race account", admin, { provider: "chatgpt", auth_mode: "chatgpt_codex_oauth", label: "OAuth disabled connect failure" }, createAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "draft");
});
const disabledConnectFailureId = disabledConnectFailureAccount.body.account.provider_account_id;
const disabledConnectFailureStart = action("OAuth connect failure race starts before credential exchange", admin, { provider_account_id: disabledConnectFailureId, purpose: "connect", flow_preference: "device" }, startOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "authorization_pending");
  assert.equal(state.accounts[disabledConnectFailureId].lifecycleState, "pending_validation");
  assert.equal(delta.vaultWrites, 0);
});
action("Disable during failed OAuth connect records temporary intent", admin, { provider_account_id: disabledConnectFailureId }, disableAccount, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(state.accounts[disabledConnectFailureId].credential.disabledIntent, true);
  assert.equal(delta.vaultDecrypts, 0);
});
action("Failed disabled OAuth connect returns credentialless account to draft", admin, { provider_account_id: disabledConnectFailureId, authorization_id: disabledConnectFailureStart.body.authorization_id, simulated_result: "failed" }, pollOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "failed");
  assert.equal(response.body.remediation, "complete_oauth");
  assert.equal(state.accounts[disabledConnectFailureId].lifecycleState, "draft");
  assert.equal(state.accounts[disabledConnectFailureId].credential.disabledIntent, false);
  assert.equal(state.accounts[disabledConnectFailureId].credential.currentVersion, null);
  assert.equal(delta.oauthExchanges, 1);
  assert.equal(delta.vaultWrites, 0);
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
  assert.equal(noSecretBearingKeys(response).length, 0);
});
const disabledConnectRetry = action("Failed disabled OAuth connect can restart from draft", admin, { provider_account_id: disabledConnectFailureId, purpose: "connect", flow_preference: "browser" }, startOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "authorization_pending");
  assert.equal(state.accounts[disabledConnectFailureId].lifecycleState, "pending_validation");
  assert.equal(delta.oauthExchanges, 0);
  assert.equal(delta.vaultWrites, 0);
});
action("Retried OAuth connect failure remains terminal and credential-free", admin, { provider_account_id: disabledConnectFailureId, authorization_id: disabledConnectRetry.body.authorization_id, simulated_result: "failed" }, pollOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "failed");
  assert.equal(state.accounts[disabledConnectFailureId].lifecycleState, "draft");
  assert.equal(state.accounts[disabledConnectFailureId].credential.currentVersion, null);
  assert.equal(delta.oauthExchanges, 1);
  assert.equal(delta.vaultWrites, 0);
});

// Disable intent also survives a successful OAuth connect authorization/exchange window.
const oauthConnectRaceAccount = action("Create OAuth connect race account", admin, { provider: "chatgpt", auth_mode: "chatgpt_codex_oauth", label: "OAuth connect race" }, createAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "draft");
});
const oauthConnectRaceId = oauthConnectRaceAccount.body.account.provider_account_id;
const oauthConnectRaceStart = action("OAuth connect starts before credential exchange", admin, { provider_account_id: oauthConnectRaceId, purpose: "connect", flow_preference: "device" }, startOAuth, ({ response }) => {
  assert.equal(response.body.status, "authorization_pending");
  assert.equal(state.accounts[oauthConnectRaceId].lifecycleState, "pending_validation");
});
action("Disable during OAuth connect records sticky administrative intent", admin, { provider_account_id: oauthConnectRaceId }, disableAccount, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(state.accounts[oauthConnectRaceId].credential.disabledIntent, true);
  assert.equal(delta.vaultDecrypts, 0);
});
action("OAuth connect exchange cannot erase disable intent", admin, { provider_account_id: oauthConnectRaceId, authorization_id: oauthConnectRaceStart.body.authorization_id, simulated_result: "succeeded" }, pollOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "succeeded");
  assert.equal(state.accounts[oauthConnectRaceId].lifecycleState, "disabled");
  assert.equal(state.accounts[oauthConnectRaceId].credential.currentVersion, 1);
  assert.equal(state.accounts[oauthConnectRaceId].credential.probeReturnState, "disabled");
  assert.equal(delta.vaultWrites, 1);
});
action("Direct replacement is rejected while current-version probe marker is open", admin, { provider_account_id: oauthConnectRaceId, credential: { credential_class: "oauth_token_import", material: "blocked-probe-marker-material" } }, reauthenticate, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "reauthentication_in_progress");
  assert.equal(state.accounts[oauthConnectRaceId].credential.pendingVersion, null);
  assert.equal(state.accounts[oauthConnectRaceId].credential.probeReturnState, "disabled");
  assert.equal(delta.vaultWrites, 0);
});
action("OAuth replacement is rejected while current-version probe marker is open", admin, { provider_account_id: oauthConnectRaceId, purpose: "reauthenticate", flow_preference: "browser" }, startOAuth, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "reauthentication_in_progress");
  assert.equal(state.accounts[oauthConnectRaceId].credential.pendingVersion, null);
  assert.equal(state.accounts[oauthConnectRaceId].credential.probeReturnState, "disabled");
  assert.equal(delta.oauthExchanges, 0);
  assert.equal(delta.vaultWrites, 0);
});
action("Sticky-disable transient probe remains disabled and retryable", admin, { provider_account_id: oauthConnectRaceId, simulated_result: "transient_failure" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(response.body.account.health.summary_state, "degraded");
  assert.equal(state.accounts[oauthConnectRaceId].credential.disabledIntent, true);
  assert.equal(state.accounts[oauthConnectRaceId].credential.decryptable, false);
  assert.equal(delta.providerProbeCalls, 1);
});
action("Repeated disable preserves a retryable current-version probe marker", admin, { provider_account_id: oauthConnectRaceId }, disableAccount, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(state.accounts[oauthConnectRaceId].credential.probeReturnState, "disabled");
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
});
action("OAuth connect probe retry success remains disabled and non-decryptable", admin, { provider_account_id: oauthConnectRaceId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(response.body.account.credential.version, 1);
  assert.equal(state.accounts[oauthConnectRaceId].credential.decryptable, false);
  assert.equal(state.snapshots[oauthConnectRaceId].freshness, "invalid");
  assert.equal(delta.providerProbeCalls, 1);
});
action("Explicit enable clears OAuth connect disable intent", admin, { provider_account_id: oauthConnectRaceId }, enableAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "pending_probe");
  assert.equal(state.accounts[oauthConnectRaceId].credential.disabledIntent, false);
});
action("Enabled OAuth connect account activates only after current-version probe", admin, { provider_account_id: oauthConnectRaceId, simulated_result: "success" }, completeProbe, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "active");
  assert.equal(state.accounts[oauthConnectRaceId].credential.decryptable, true);
});

const postExchangeDisableAccount = action("Create OAuth post-exchange disable account", admin, { provider: "chatgpt", auth_mode: "chatgpt_codex_oauth", label: "OAuth post-exchange disable" }, createAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "draft");
});
const postExchangeDisableId = postExchangeDisableAccount.body.account.provider_account_id;
const postExchangeAuthorization = action("OAuth connect exchange window starts", admin, { provider_account_id: postExchangeDisableId, purpose: "connect", flow_preference: "device" }, startOAuth, ({ response }) => {
  assert.equal(response.body.status, "authorization_pending");
});
action("OAuth connect exchange stores current version pending probe", admin, { provider_account_id: postExchangeDisableId, authorization_id: postExchangeAuthorization.body.authorization_id, simulated_result: "succeeded" }, pollOAuth, ({ response, delta }) => {
  assert.equal(response.body.status, "succeeded");
  assert.equal(state.accounts[postExchangeDisableId].lifecycleState, "pending_probe");
  assert.equal(state.accounts[postExchangeDisableId].credential.probeReturnState, "active");
  assert.equal(delta.vaultWrites, 1);
});
action("Disable after OAuth exchange preserves current-version probe completion", admin, { provider_account_id: postExchangeDisableId }, disableAccount, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(state.accounts[postExchangeDisableId].credential.disabledIntent, true);
  assert.equal(state.accounts[postExchangeDisableId].credential.probeReturnState, "disabled");
  assert.equal(delta.vaultDecrypts, 0);
});
action("Post-exchange disabled probe completes without reactivation", admin, { provider_account_id: postExchangeDisableId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(response.body.account.credential.version, 1);
  assert.equal(state.accounts[postExchangeDisableId].credential.decryptable, false);
  assert.equal(state.snapshots[postExchangeDisableId].freshness, "invalid");
  assert.equal(delta.providerProbeCalls, 1);
});
action("Completed disabled journey does not open generic disabled probes", admin, { provider_account_id: postExchangeDisableId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "probe_not_allowed");
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
});

const terminalMarkerAccount = action("Create terminal marker account", admin, { provider: "chatgpt", auth_mode: "chatgpt_codex_oauth", label: "Terminal marker" }, createAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "draft");
});
const terminalMarkerId = terminalMarkerAccount.body.account.provider_account_id;
const terminalMarkerAuthorization = action("Terminal marker OAuth connect starts", admin, { provider_account_id: terminalMarkerId, purpose: "connect", flow_preference: "device" }, startOAuth, ({ response }) => {
  assert.equal(response.body.status, "authorization_pending");
});
action("Terminal marker exchange reaches current-version probe", admin, { provider_account_id: terminalMarkerId, authorization_id: terminalMarkerAuthorization.body.authorization_id, simulated_result: "succeeded" }, pollOAuth, ({ response }) => {
  assert.equal(response.body.status, "succeeded");
  assert.equal(state.accounts[terminalMarkerId].credential.probeReturnState, "active");
});
action("Disable converts terminal marker probe target to disabled", admin, { provider_account_id: terminalMarkerId }, disableAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(state.accounts[terminalMarkerId].credential.probeReturnState, "disabled");
});
action("Sticky current-version auth failure consumes the probe marker", admin, { provider_account_id: terminalMarkerId, simulated_result: "auth_failure" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(state.accounts[terminalMarkerId].credential.probeReturnState, null);
  assert.equal(delta.vaultDecrypts, 1);
  assert.equal(delta.providerProbeCalls, 1);
});
action("Consumed sticky marker rejects repeated auth-failure probe", admin, { provider_account_id: terminalMarkerId, simulated_result: "auth_failure" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "probe_not_allowed");
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
});
action("Enable creates a fresh one-shot current-version probe marker", admin, { provider_account_id: terminalMarkerId }, enableAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "pending_probe");
  assert.equal(state.accounts[terminalMarkerId].credential.probeReturnState, "active");
});
action("Enable auth failure also consumes its current-version marker", admin, { provider_account_id: terminalMarkerId, simulated_result: "auth_failure" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "reauth_required");
  assert.equal(state.accounts[terminalMarkerId].credential.probeReturnState, null);
  assert.equal(delta.providerProbeCalls, 1);
});
action("Disable after enable auth failure cannot recreate a consumed marker", admin, { provider_account_id: terminalMarkerId }, disableAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(state.accounts[terminalMarkerId].credential.probeReturnState, null);
});
action("Consumed enable marker rejects disabled probe without re-enable", admin, { provider_account_id: terminalMarkerId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "probe_not_allowed");
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
});

// Enable precedence from #17 and direct reauthentication cutover.
action("Enable always enters pending_probe before active", admin, { provider_account_id: directId }, enableAccount, ({ response, delta }) => {
  assert.equal(response.status, 202);
  assert.equal(response.body.account.lifecycle_state, "pending_probe");
  assert.equal(delta.vaultDecrypts, 0);
});
action("Enable current-version probe auth failure stays non-usable", admin, { provider_account_id: directId, simulated_result: "auth_failure" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "reauth_required");
  assert.equal(delta.providerProbeCalls, 1);
});
action("Malformed direct reauthentication is rejected before vault", admin, { provider_account_id: directId, credential: { credential_class: "web_session", material: "replacement\nmaterial" } }, reauthenticate, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "credential_malformed");
  assert.equal(delta.vaultWrites, 0);
});
action("Direct reauthentication keeps logical account and lands pending_validation", admin, { provider_account_id: directId, credential: { credential_class: "web_session", material: "replacement-session-material" } }, reauthenticate, ({ response, delta }) => {
  assert.equal(response.body.account.provider_account_id, directId);
  assert.equal(response.body.account.lifecycle_state, "pending_validation");
  assert.equal(state.accounts[directId].credential.pendingVersion, 2);
  assert.equal(state.accounts[directId].credential.currentVersion, 1);
  assert.equal(state.accounts[directId].credential.decryptable, false);
  assert.equal(state.snapshots[directId].freshness, "invalid");
  assert.equal(delta.vaultWrites, 1);
  assert.equal(delta.adapterCalls, 0);
});
action("Pending-validation reauthentication blocks new admissions", admin, { provider_account_id: directId, operation: "chat" }, authorizeCapability, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "lifecycle_or_credential_gate");
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
});
action("Replacement validation success advances to pending_probe", admin, { provider_account_id: directId, simulated_result: "success" }, completeCredentialValidation, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "pending_probe");
  assert.equal(response.body.account.credential.version, 1);
  assert.equal(state.accounts[directId].credential.pendingVersion, 2);
  assert.equal(delta.vaultWrites, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
});
state.accounts[directId].controls.quarantine = "quarantined";
action("Quarantined reauthentication probe has zero decrypt and Adapter calls", admin, { provider_account_id: directId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "administrative_control");
  assert.equal(state.accounts[directId].credential.pendingVersion, 2);
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
});
state.accounts[directId].controls.quarantine = "off";
state.accounts[directId].controls.authModeExecutionEnabled = false;
action("Auth Mode execution kill rejects probe before sensitive boundaries", admin, { provider_account_id: directId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "administrative_control");
  assert.equal(state.accounts[directId].credential.pendingVersion, 2);
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
});
state.accounts[directId].controls.authModeExecutionEnabled = true;
action("Reauthentication probe auth failure never promotes pending version", admin, { provider_account_id: directId, simulated_result: "auth_failure" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "reauth_required");
  assert.equal(state.accounts[directId].credential.currentVersion, 1);
  assert.equal(state.accounts[directId].credential.pendingVersion, null);
  assert.equal(state.accounts[directId].credential.highestVersion, 2);
  assert.equal(state.accounts[directId].credential.decryptable, false);
  assert.equal(delta.vaultRevokes, 1);
});
action("Replacement version is monotonic after failed pending version", admin, { provider_account_id: directId, credential: { credential_class: "web_session", material: "replacement-session-material-2" } }, reauthenticate, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "pending_validation");
  assert.equal(state.accounts[directId].credential.pendingVersion, 3);
  assert.equal(response.body.account.credential.version, 1);
  assert.equal(delta.vaultWrites, 1);
});
action("Monotonic replacement validates before its public probe", admin, { provider_account_id: directId, simulated_result: "success" }, completeCredentialValidation, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "pending_probe");
  assert.equal(state.accounts[directId].credential.pendingVersion, 3);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
});
action("Public probe cuts over replacement and retires old admissions", admin, { provider_account_id: directId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "active");
  assert.equal(response.body.account.credential.version, 3);
  assert.equal(state.accounts[directId].credential.retiredForNewAdmissions, 1);
  assert.equal(delta.providerProbeCalls, 1);
  assert.equal(delta.vaultRevokes, 1);
  assert.equal(state.snapshots[directId].credential_version, 3);
});

// Administrative disable intent survives replacement races and failures.
action("Active reauthentication stages another monotonic version for validation", admin, { provider_account_id: directId, credential: { credential_class: "web_session", material: "midflight-replacement-material" } }, reauthenticate, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "pending_validation");
  assert.equal(response.body.account.credential.version, 3);
  assert.equal(state.accounts[directId].credential.pendingVersion, 4);
  assert.equal(delta.vaultWrites, 1);
});
action("Disable during pending validation preserves administrative intent", admin, { provider_account_id: directId }, disableAccount, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(state.accounts[directId].credential.pendingVersion, 4);
  assert.equal(state.accounts[directId].credential.reauthReturnState, "disabled");
  assert.equal(delta.vaultDecrypts, 0);
});
action("Validation success remains disabled while opening one pending-version probe", admin, { provider_account_id: directId, simulated_result: "success" }, completeCredentialValidation, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(state.accounts[directId].credential.pendingVersion, 4);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
});
action("Mid-flight replacement success cannot reactivate disabled account", admin, { provider_account_id: directId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(response.body.account.credential.version, 4);
  assert.equal(state.accounts[directId].credential.decryptable, false);
  assert.equal(state.snapshots[directId].freshness, "invalid");
  assert.equal(delta.vaultRevokes, 1);
});
action("Enable after mid-flight replacement still requires current-version probe", admin, { provider_account_id: directId }, enableAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "pending_probe");
});
action("Enable probe activates the cut-over current version", admin, { provider_account_id: directId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "active");
  assert.equal(response.body.account.credential.version, 4);
  assert.equal(state.accounts[directId].credential.decryptable, true);
  assert.equal(delta.providerProbeCalls, 1);
});
action("Disable before replacement failure keeps administrative intent", admin, { provider_account_id: directId }, disableAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
});
action("Disabled reauthentication stages validation without hiding disabled lifecycle", admin, { provider_account_id: directId, credential: { credential_class: "web_session", material: "disabled-failing-replacement" } }, reauthenticate, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(state.accounts[directId].credential.pendingVersion, 5);
  assert.equal(state.accounts[directId].credential.decryptable, false);
  assert.equal(delta.vaultWrites, 1);
});
action("Disabled pending validation rejects probe before sensitive boundaries", admin, { provider_account_id: directId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "credential_validation_pending");
  assert.equal(state.accounts[directId].credential.pendingVersion, 5);
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
});
action("Disabled replacement validation success preserves disabled lifecycle", admin, { provider_account_id: directId, simulated_result: "success" }, completeCredentialValidation, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(state.accounts[directId].credential.pendingVersion, 5);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
});
action("Disabled-origin auth failure remains disabled", admin, { provider_account_id: directId, simulated_result: "auth_failure" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(response.body.account.credential.version, 4);
  assert.equal(state.accounts[directId].credential.pendingVersion, null);
  assert.equal(state.accounts[directId].credential.highestVersion, 5);
  assert.equal(state.accounts[directId].credential.decryptable, false);
  assert.equal(delta.vaultRevokes, 1);
});
action("Disabled reauthentication after failure allocates a new version", admin, { provider_account_id: directId, credential: { credential_class: "web_session", material: "disabled-success-replacement" } }, reauthenticate, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(response.body.account.credential.version, 4);
  assert.equal(state.accounts[directId].credential.pendingVersion, 6);
  assert.equal(delta.vaultWrites, 1);
});
action("Disabled replacement validates without becoming active", admin, { provider_account_id: directId, simulated_result: "success" }, completeCredentialValidation, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(state.accounts[directId].credential.pendingVersion, 6);
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.providerProbeCalls, 0);
});
action("Disabled replacement success stays disabled and non-decryptable", admin, { provider_account_id: directId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "disabled");
  assert.equal(response.body.account.credential.version, 6);
  assert.equal(state.accounts[directId].credential.decryptable, false);
  assert.equal(state.snapshots[directId].freshness, "invalid");
  assert.equal(Object.values(state.snapshots[directId].operations).some((fact) => fact.offerable), false);
  assert.equal(delta.vaultRevokes, 1);
});
action("Enable after disabled replacement still requires current-version probe", admin, { provider_account_id: directId }, enableAccount, ({ response }) => {
  assert.equal(response.body.account.lifecycle_state, "pending_probe");
});
action("Enable probe success is the only path back to active", admin, { provider_account_id: directId, simulated_result: "success" }, completeProbe, ({ response, delta }) => {
  assert.equal(response.body.account.lifecycle_state, "active");
  assert.equal(state.accounts[directId].credential.decryptable, true);
  assert.equal(state.snapshots[directId].credential_version, 6);
  assert.equal(delta.providerProbeCalls, 1);
});

// Snapshot classifications, missing evidence, freshness, and model observations.
delete state.snapshots[directId];
action("Missing Capability Snapshot read is capability_unverified", reader, { provider_account_id: directId }, getSnapshot, ({ response, delta }) => {
  assert.equal(response.body.code, "capability_unverified");
  assert.equal(delta.vaultDecrypts, 0);
});
action("Missing Capability Snapshot cannot authorize execution", admin, { provider_account_id: directId, operation: "chat" }, authorizeCapability, ({ response, delta }) => {
  assert.equal(response.body.code, "capability_unverified");
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.vaultDecrypts, 0);
});
state.snapshots[directId] = buildSnapshot(state.accounts[directId]);
action("Capability Snapshot classifies five operations for current credential", reader, { provider_account_id: directId }, getSnapshot, ({ response, delta }) => {
  assert.deepEqual(Object.keys(response.body.operations).sort(), [...OPERATIONS].sort());
  assert.equal(response.body.credential_version, 6);
  assert.equal(response.body.provenance[0].evidence_class, "live_probe");
  assert.equal(response.body.models[0].model_slug, "gemini-observed-1");
  assert.equal(delta.vaultDecrypts, 0);
});
state.snapshots[directId].freshness = "stale";
for (const fact of Object.values(state.snapshots[directId].operations)) fact.offerable = false;
action("Stale snapshot remains readable but visibly non-authorizing", reader, { provider_account_id: directId }, getSnapshot, ({ response, delta }) => {
  assert.equal(response.status, 200);
  assert.equal(response.body.freshness, "stale");
  assert.equal(Object.values(response.body.operations).some((fact) => fact.offerable), false);
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
});
action("Stale snapshot rejects before Adapter and vault", admin, { provider_account_id: directId, operation: "chat" }, authorizeCapability, ({ response, delta }) => {
  assert.equal(response.body.code, "snapshot_stale");
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.vaultDecrypts, 0);
});
state.snapshots[directId].freshness = "invalid";
action("Invalid snapshot remains readable for operator inspection", reader, { provider_account_id: directId }, getSnapshot, ({ response, delta }) => {
  assert.equal(response.status, 200);
  assert.equal(response.body.freshness, "invalid");
  assert.equal(Object.values(response.body.operations).some((fact) => fact.offerable), false);
  assert.equal(delta.vaultDecrypts, 0);
});
action("Invalid snapshot rejects execution before sensitive boundaries", admin, { provider_account_id: directId, operation: "chat" }, authorizeCapability, ({ response, delta }) => {
  assert.equal(response.body.code, "snapshot_stale");
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.vaultDecrypts, 0);
});
state.snapshots[directId] = buildSnapshot(state.accounts[directId], "fresh", "reference_learned");
action("Reference-only evidence remains unverified and non-offerable", reader, { provider_account_id: directId }, getSnapshot, ({ response, delta }) => {
  assert.equal(response.body.provenance[0].evidence_class, "reference_learned");
  assert.equal(Object.values(response.body.operations).every((fact) => fact.status === "unverified"), true);
  assert.equal(Object.values(response.body.operations).some((fact) => fact.offerable), false);
  assert.equal(delta.vaultDecrypts, 0);
});
action("Reference-only evidence cannot authorize execution", admin, { provider_account_id: directId, operation: "chat" }, authorizeCapability, ({ response, delta }) => {
  assert.equal(response.body.code, "capability_unverified");
  assert.equal(delta.adapterCalls, 0);
  assert.equal(delta.vaultDecrypts, 0);
});
state.snapshots[directId] = buildSnapshot(state.accounts[directId]);
action("Unsupported inpaint rejects without downgrade", admin, { provider_account_id: directId, operation: "inpaint" }, authorizeCapability, ({ response, delta }) => {
  assert.equal(response.body.code, "capability_unsupported");
  assert.equal(delta.adapterCalls, 0);
});

// Routing policy defaults, atomic writes, and explicit-pin behavior.
action("Routing Policy defaults fallback off", reader, {}, getRoutingPolicy, ({ response, delta }) => {
  assert.equal(response.body.fallback_enabled, false);
  assert.deepEqual(response.body.fallback_chain, []);
  assert.equal(delta.vaultDecrypts, 0);
});
action("Foreign candidate policy write is 404, atomic, and side-effect free", admin, {
  candidate_accounts: [directId, "pa_foreign"], selection_order: [directId, "pa_foreign"], fallback_enabled: true,
  fallback_chain: ["pa_foreign"], fallback_auth_modes: ["chatgpt_web_access"],
  affinity: { enabled: true, window_class: "AFFINITY-WINDOW-CLASS" }, lease_policy: { enabled: false, eligible_units: [] },
}, replaceRoutingPolicy, ({ response, delta, beforeState, afterState }) => {
  assert.equal(response.body.code, "resource_not_found");
  assert.equal(delta.policyWrites, 0);
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
  assert.deepEqual(afterState.routing_policy, beforeState.routing_policy);
});
action("Same-Tenant Routing Policy replacement is atomic and successful", admin, {
  candidate_accounts: [directId], selection_order: [directId], fallback_enabled: false,
  fallback_chain: [], fallback_auth_modes: [],
  affinity: { enabled: true, window_class: "AFFINITY-WINDOW-CLASS" }, lease_policy: { enabled: false, eligible_units: [] },
}, replaceRoutingPolicy, ({ response, delta }) => {
  assert.deepEqual(response.body.candidate_accounts, [directId]);
  assert.equal(response.body.fallback_enabled, false);
  assert.equal(delta.policyWrites, 1);
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
});
action("Explicit pin requires authentication", invalidKey, { provider_account_id: directId, operation: "chat", allow_fallback: false }, routeExplicitPin, ({ response, delta }) => {
  assert.equal(response.status, 401);
  assert.equal(delta.adapterCalls, 0);
});
action("Explicit pin requires operation scope", reader, { provider_account_id: directId, operation: "chat", allow_fallback: false }, routeExplicitPin, ({ response, delta }) => {
  assert.equal(response.status, 403);
  assert.equal(delta.adapterCalls, 0);
});
action("Healthy explicit pin selects only requested account", admin, { provider_account_id: directId, operation: "chat", allow_fallback: false }, routeExplicitPin, ({ response, delta }) => {
  assert.equal(response.body.selected_provider_account_id, directId);
  assert.equal(response.body.fallback_used, false);
  assert.equal(delta.adapterCalls, 0);
});
state.accounts[directId].controls.drain = "draining";
action("Drain blocks new capability admission before sensitive boundaries", admin, { provider_account_id: directId, operation: "chat" }, authorizeCapability, ({ response, delta }) => {
  assert.equal(response.body.safe_context.cause_class, "administrative_control");
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
});
action("Drain blocks explicit-pin selection without fallback", admin, { provider_account_id: directId, operation: "chat", allow_fallback: false }, routeExplicitPin, ({ response, delta }) => {
  assert.equal(response.body.code, "routing_fallback_not_allowed");
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
});
state.accounts[directId].controls.drain = "off";
setScopedHealth(state.accounts[directId], "cooling_down", "provider_rate_limited", "chat", 30, "wait_provider_cooldown");
action("Explicit pin never silently falls back", admin, { provider_account_id: directId, operation: "chat", allow_fallback: false }, routeExplicitPin, ({ response, delta }) => {
  assert.equal(response.body.code, "routing_fallback_not_allowed");
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
});
action("Chat cooldown does not block image generation", admin, { provider_account_id: directId, operation: "image_generation" }, authorizeCapability, ({ response, delta }) => {
  assert.equal(response.body.admitted, true);
  assert.equal(response.body.operation, "image_generation");
  assert.equal(delta.vaultDecrypts, 1);
  assert.equal(delta.adapterCalls, 1);
});

// Safe scoped health and truthful finite retry metadata.
action("Scoped finite cooldown exposes truthful retry time", reader, { provider_account_id: directId }, getAccount, ({ response }) => {
  const health = response.body.health;
  assert.equal(health.conditions[0].scope.kind, "operation");
  assert.equal(health.conditions[0].scope.operation, "chat");
  assert.equal(health.conditions[0].retry_after_seconds, 30);
  assert.equal(noSecretBearingKeys(response).length, 0);
});
setScopedHealth(state.accounts[directId], "challenged", "challenge_detected", "chat", null, "contact_operator");
action("Non-time health gate suppresses retry timing", reader, { provider_account_id: directId }, getAccount, ({ response }) => {
  assert.equal(response.body.health.summary_state, "challenged");
  assert.equal("retry_after_seconds" in response.body.health.conditions[0], false);
});

// Delete and internal retention-hold semantics.
action("Replacement can remain pending until account deletion", admin, { provider_account_id: directId, credential: { credential_class: "web_session", material: "delete-pending-replacement" } }, reauthenticate, ({ response, delta }) => {
  assert.equal(response.body.account.credential.version, 6);
  assert.equal(state.accounts[directId].credential.pendingVersion, 7);
  assert.equal(delta.vaultWrites, 1);
});
state.retentionHolds[directId] = true;
action("Delete revokes current and pending credentials before retention", admin, { provider_account_id: directId }, deleteAccount, ({ response, delta }) => {
  assert.equal(response.status, 204);
  assert.equal(delta.vaultRevokes, 2);
  assert.equal(delta.vaultDeletes, 2);
  assert.equal(delta.vaultDecrypts, 0);
  assert.deepEqual(state.retentionEvidence[directId], { encryptedEvidenceRetained: true, decryptable: false, restorable: false });
});
action("Deleted account ordinary get is resource_not_found", reader, { provider_account_id: directId }, getAccount, ({ response, delta }) => {
  assert.equal(response.body.code, "resource_not_found");
  assert.equal(delta.vaultDecrypts, 0);
  assert.equal(delta.adapterCalls, 0);
});

console.log(`\nPASS: ${scenarioResults.length} management contract actions validated.`);
console.log(JSON.stringify({ total_side_effects: state.effects, retained_evidence: state.retentionEvidence }, null, 2));
