# Auth Mode Risk Envelope and Kill Criteria

- Status: Accepted for specification (issue #7)
- Date: 2026-07-14
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#7](https://github.com/monet88/pixelplus/issues/7)
- Vocabulary source: `CONTEXT.md`
- Evidence inputs (research only; not acceptance by themselves):
  - `docs/spec/research/web-to-api-compliance-risk-evidence.md` (#2)
  - `docs/spec/research/chatgpt-auth-mode-capability-evidence.md` (#3)
  - `docs/spec/research/gemini-auth-mode-capability-evidence.md` (#4)
  - `docs/spec/research/grok-auth-mode-capability-evidence.md` (#5)
- Related ownership invariants: `docs/spec/tenant-ownership-authorization-invariants.md` (#6)

## 1. Scope and non-goals

### 1.1 Scope

This specification converts compliance and capability **evidence** into **product decisions** for the six initial Auth Modes:

| Auth Mode | Access class |
|---|---|
| ChatGPT Web Access | Web Access |
| ChatGPT Codex OAuth | OAuth/CLI Access |
| Gemini Web Cookie | Web Access |
| Gemini Antigravity OAuth | OAuth/CLI Access |
| Grok Web SSO | Web Access |
| Grok xAI OAuth | OAuth/CLI Access |

For each Auth Mode it locks:

1. Product status: `allowed` | `prohibited` | `experimental` | `gated`
2. Acceptable-use boundary for PixelPlus product surfaces
3. Operator obligations
4. Security impact acceptance (what residual risk the product accepts)
5. Kill criteria that must disable the Auth Mode path (and therefore pause that Auth Mode’s Adapter executions and new connections)
6. Recovery / reopen conditions based on **observable** signals
7. Assumptions still in force and deferred decisions with reopen triggers
8. Per-mode **ToS / account-ban residual risk** (severity of policy collision and account-harm residual the product accepts or refuses)

### 1.2 Non-goals

This document does **not**:

- Implement Gateway, Adapter, vault, or UI code.
- Replace counsel advice, insurer review, or jurisdiction-specific enforceability analysis.
- Decide chat/image capability matrices (owned by #3–#5 evidence; later contract issues consume them).
- Design Client API Key abuse numbers (#8), connection UX copy beyond required disclosures (#9), Capability Snapshot schema (#10), routing numbers (#11), vault cryptography (#15), or full error taxonomy (#16).
- Approve Official API Adapters for OpenAI / Gemini Developer API / xAI API-key surfaces (parent #1 Out of Scope for MVP).
- Treat `.ref/*` reverse-engineering projects as legal permission.

Downstream issues **MUST** preserve every decision in this document. They may add stricter gates; they MUST NOT silently promote a status without meeting the reopen criteria in §8.

### 1.3 Normative language

- **MUST / MUST NOT / REQUIRED**: product policy. Violation is a product/security defect.
- **SHALL**: same force as MUST for operator and Public API behavior.
- **SHOULD**: strongly preferred default; deviation needs an operator-recorded exception.
- **MAY**: optional surface that cannot weaken MUST rules.

### 1.4 Decision unit

**Auth Mode is the unit of risk decision**, not Provider brand name.

Cause → effect:

1. One external human identity may hold both a Web Access Provider Account and an OAuth/CLI Access Provider Account.
2. Those accounts have different credentials, contracts, challenge surfaces, and quotas (#3–#5).
3. Therefore a kill or promotion of “ChatGPT” as a brand is invalid; decisions MUST name the Auth Mode.

---

## 2. Status vocabulary (normative)

Every Auth Mode MUST carry exactly one of the following product statuses.

| Status | Meaning | Default enablement | Who may use it |
|---|---|---|---|
| **`allowed`** | Within the product risk envelope for ordinary Tenant self-serve use under documented operator controls. | Default **on** when the Auth Mode is compiled/configured into the deployment, subject to Tenant ownership rules (#6). | Any Tenant that completes ordinary connection flow. |
| **`gated`** | Residual risk accepted only behind explicit gates (operator config and/or Tenant acknowledgement). Code path may ship. | Default **off** at deployment and Tenant levels until gates are satisfied. | Tenants only after required acknowledgements; operators must enable the Auth Mode feature flag first. |
| **`experimental`** | Research / lab grade. High ToS, ban, reverse-engineering, or sensitive-credential tension. Not a production product promise. | Default **off** everywhere, including single-Tenant deployments unless an operator deliberately enables a lab profile. | Operator-controlled lab environments only. MUST NOT appear as ordinary self-serve production connection UX. |
| **`prohibited`** | Outside the product risk envelope. PixelPlus MUST NOT offer the Auth Mode to Tenants or run it in shared product environments. | Hard **off**. Configuration that enables it in product deployments is a policy defect. | No product Tenant use. Research forks outside product policy are out of scope of this document. |

### 2.1 Promotion and demotion rules

| Transition | Allowed only when |
|---|---|
| `prohibited` → any other | Reopen trigger in the Auth Mode card fires **and** evidence base (#2 family section) is re-checked **and** a human product owner records acceptance. |
| `experimental` → `gated` | Residual reverse-eng / bot tension reduced by upstream official surface **or** counsel clears the residual theory **and** kill/observability controls are implemented. |
| `gated` → `allowed` | Counsel or product owner accepts residual credential-custody and commercial-characterization risk **and** no open blocking gap in §8 for that Auth Mode. |
| Any → lower status | Immediate when a kill criterion fires; status change MUST be recorded with the triggering signal. |

No Auth Mode may be treated as `allowed` solely because a reference repo implements it.

### 2.2 Independence from capability maturity

Capability status (`verified` / `conditionally supported` / `unsupported` / `unverified` from #3–#5) is **orthogonal** to risk status.

Example:

- An Auth Mode can be `gated` (risk) while image edit remains `conditionally supported` (capability).
- An Auth Mode can be `prohibited` (risk) even if chat is technically `conditionally supported` in a reference.

Gateway MUST NOT promote risk status because a probe succeeded.

---

## 3. Cross-cutting product posture

### 3.1 Locked product facts (from parent #1 and #6)

1. Access model is **BYOA**. Shared Provider Account pools across Tenants are out of scope.
2. Provider Credential is vaulted separately from Provider Account metadata and never crosses Tenants.
3. Web Access and OAuth/CLI Access are independent execution surfaces.
4. Reference repositories are research seams, not production permission.
5. Official API Adapters (first-party paid APIs as product Adapters) are out of MVP scope even when an OAuth/CLI Auth Mode is gated.

### 3.2 Acceptable-use boundary (product-wide)

PixelPlus product surfaces MUST be designed as:

- **Tenant-owned account automation for that Tenant’s own Provider Accounts**, not as a marketplace that rents third-party consumer entitlements.
- **Not** a multi-Tenant shared pool, account time-share, or credential resale service.
- **Not** a challenge-solver / anti-bot bypass product.
- **Not** a scraper that harvests Provider Outputs for resale independent of the Tenant’s own session.

Concrete cause → effect:

1. Tenant A connects only Tenant A’s ChatGPT Codex OAuth account.
2. Client API Keys of Tenant A may use only that account (#6).
3. Gateway fee, if any, is for software/ops of the Gateway, not for leasing OpenAI/Google/xAI consumer seats to strangers.
4. Commercial fee legal characterization remains **deferred** (§8.D-COMM); until cleared, marketing MUST NOT claim “resell ChatGPT/Gemini/Grok access”.

### 3.3 Global operator obligations

Operators of any deployment that includes one or more Auth Modes MUST:

| ID | Obligation |
|---|---|
| OP-G1 | Maintain a **per-Auth-Mode kill switch** that immediately stops new executions and new connections for that Auth Mode. |
| OP-G2 | Keep kill-switch state durable and visible in operator health surfaces (details #17). |
| OP-G3 | Never log, export, or put in metrics labels: Provider Credential material, raw cookies, SSO tokens, OAuth refresh/access tokens, or Client API Key secrets. |
| OP-G4 | On KS-* kill events, freeze new connections for the affected Auth Mode and page a human owner within the operator’s incident process. |
| OP-G5 | Re-run Rank A/B policy review on every RR-* trigger from #2, and at least **quarterly** while any `experimental` or `gated` Auth Mode is enabled in any shared environment. |
| OP-G6 | Refuse product features whose primary purpose is solving Provider anti-bot challenges (captcha farms, residential proxy rotation marketed as bypass, etc.). |
| OP-G7 | Preserve Tenant isolation invariants (#6) even during incident response; emergency “use any account” is forbidden. |

### 3.4 Global security impact acceptance

| Residual risk | Product stance |
|---|---|
| Vault holds secrets that can act as the user at the Provider | **Accepted** for BYOA, with encrypt-at-rest, Tenant isolation, redaction, rotation/revocation (#15). |
| Provider may ban or rate-limit a Tenant’s Provider Account | **Accepted** as operational risk of BYOA; Gateway MUST surface health and must not hide bans. |
| Consumer Web reverse engineering / bot clauses | **Not accepted** as ordinary production posture → drives `experimental` / `prohibited` for Web modes. |
| Credential non-sharing / resale-lease tension for OAuth/CLI BYOA | **Conditionally accepted** only under `gated` controls and deferred counsel items. |
| Challenge-solver as product capability | **Not accepted**. |

### 3.5 Global kill and feature-gate signal catalog

Signals below are **product-binding**. Thresholds for KS-2 and FG-5 are **product-chosen conservative defaults** (not measured ban rates from Providers; those remain G-10 / unverified in #2). Operators MAY tighten thresholds. Operators MUST NOT loosen them without closing **D-NUMERIC-TUNE** with a recorded product exception.

#### 3.5.1 Kill-switch triggers (disable Auth Mode)

| ID | Observable signal | Default action | Typical scope |
|---|---|---|---|
| **KS-1** | Provider publishes or clarifies policy that **unambiguously** bans the Adapter’s access method for that Auth Mode | Immediate Auth Mode kill; status demotion toward `prohibited` or keep `prohibited` | Named Auth Mode |
| **KS-2** | Challenge/bot-interstitial rate ≥ **70%** of attempts over a continuous **30-minute** window **and** absolute attempts ≥ **50** for that Auth Mode | Auto-disable Auth Mode; require human reopen | Auth Mode (often Web Adapter) |
| **KS-3** | ≥ **3** distinct Tenant Provider Accounts under the same Auth Mode enter permanent ban / credential-revoked-by-provider health within **24 hours**, and traffic is correlated with Gateway egress or protocol path | Suspend Auth Mode; incident review | Auth Mode, region, or egress class |
| **KS-4** | Formal legal notice, cease-and-desist, or ToS enforcement communication naming the product or method | Global or named-surface disable within incident SLA | Global or named Auth Mode |
| **KS-5** | Protocol break such that continued operation requires **new** reverse engineering of private APIs or anti-bot systems | Disable affected Web Auth Mode; reopen only after product re-decision | Web Auth Mode |
| **KS-6** | Credential class invalidated (cookie family revoked, OAuth client disallowed, issuer refuses tokens) with no documented safe replacement | Disable Auth Mode until replacement path is re-specified | Auth Mode |

#### 3.5.2 Feature-gate / degrade triggers (do not full-kill yet)

| ID | Observable signal | Default action |
|---|---|---|
| **FG-1** | Auth Mode is `experimental` or high-tension `gated` | Keep default **off**; require operator flag + Tenant ack before any execution |
| **FG-2** | Only OAuth/CLI sibling is inside envelope; Web sibling is experimental/prohibited | Ship and enable OAuth path independently; never silent-fallback to Web |
| **FG-3** | Tenant Provider contract class unknown (consumer vs business) when a feature assumes business/API rights | Deny the business-only feature; do not assume Enterprise rights |
| **FG-4** | Dual-regime identity incomplete (e.g. Grok SSO issuer xAI vs X still ambiguous for a credential type) | Gate that credential subtype until mapping verified |
| **FG-5** | Challenge rate ≥ **40%** over **15 minutes** with ≥ **20** attempts | Auto-cooldown: reduce concurrency, pause new connections for Auth Mode, alert operator |

#### 3.5.3 Measurement requirements

For KS-2 and FG-5 to be enforceable, Gateway health telemetry MUST be able to count, per Auth Mode:

- attempt starts
- challenge/bot-interstitial classifications
- permanent ban / provider-revoked outcomes
- distinct `provider_account_id` affected

Exact metric names are owned by later observability work; this document locks **what must be countable**.

#### 3.5.4 Auth Mode kill vs Adapter pause

For this product, **Auth Mode is the Adapter registration unit** (`CONTEXT.md`: Auth Mode decides which Adapter may handle the account).

Cause → effect:

1. Kill switch for Auth Mode M fires (KS-* or operator OP-G1).
2. Composition root MUST stop registering M as Tenant-connectable and MUST refuse new executions that would select Provider Accounts whose Auth Mode is M.
3. That is the required **Adapter path pause** for M. A separate “pause Adapter binary while Auth Mode stays allowed” control is optional later ops detail; it MUST NOT re-enable a killed or `prohibited` Auth Mode.

Per-account cooldown (one Provider Account unhealthy while siblings on the same Auth Mode still run) remains allowed and is owned by health/routing issues (#11/#17). That is not an Auth Mode status change.

---

## 4. Decision matrix (summary)

| Auth Mode | Status | Primary reason (one line) | Dominant residual risks |
|---|---|---|---|
| ChatGPT Web Access | **`experimental`** | Consumer ToU stacks programmatic extract + reverse eng + protective-measure rules; CF/PoW/Turnstile operational tension | ToS extract, reverse eng, challenges, session credential custody |
| ChatGPT Codex OAuth | **`gated`** | Official Codex OAuth/CLI surface exists; residual credential-share, resale/lease, plan-contract mismatch | Credential custody, commercial characterization, G-8 third-party token custody |
| Gemini Web Cookie | **`experimental`** | Full Google session cookies + reverse eng + protective-measure / bot-flag tension | Cookie ATO blast radius, reverse eng, bot flags |
| Gemini Antigravity OAuth | **`gated`** | Documented Google OAuth/developer-family path; product-specific Antigravity terms still gapped (G-2) | Quota circumvention, G-2 terms gap, OAuth custody |
| Grok Web SSO | **`prohibited`** | xAI AUP **explicitly** forbids bots/scripts/non-human access and targets paid violative output services | Direct AUP collision for any scripted Web Adapter |
| Grok xAI OAuth | **`gated`** | Official xAI API / Grok Build CLI integration path; residual time-sharing/lease and competitive-clause tension | Time-share/lease characterization, competitive clause, OAuth custody |

No Auth Mode is `allowed` in this revision. That is intentional: open counsel/product gaps in §8 block promotion to unrestricted self-serve.

---

## 5. Per-Auth-Mode decision cards

Each card uses the same structure so operators and later issues can apply policy mechanically.

### 5.1 ChatGPT Web Access — `experimental`

| Field | Decision |
|---|---|
| **Status** | `experimental` |
| **Evidence** | #2 §4.2, §4.4, heat map Critical; #3 Web Access surface (chatgpt.com backend, Sentinel/PoW/Turnstile/CF) |
| **Assumptions in force** | Consumer Terms of Use + Usage Policies govern typical consumer ChatGPT web use (checked 2026-07-14). No public “ChatGPT Web API” for reverse-proxy use. Reference `.ref/chatgpt2api` is research only. |
| **ToS / account-ban residual risk** | **High.** Programmatic extract + reverse eng + protective-measure clauses stack; CF/challenge storms and session invalidation are expected operational ban/harm signals. Product **refuses** this residual for ordinary production (`experimental` only). |
| **Acceptable-use boundary** | Lab-only automation of a Tenant-owned ChatGPT web session for product research. MUST NOT be marketed, default-enabled, or offered as ordinary production BYOA connection. MUST NOT include productized challenge solving. |
| **Operator obligations** | Kill switch default **off**. Enable only in named lab deployments. Record lab purpose. Monitor FG-5/KS-2 challenge rates. No multi-Tenant demo of this mode. |
| **Security impact** | Accepts temporary custody of web access/session material in lab vault profiles only. Leak = account takeover risk. Blast radius: ChatGPT consumer session. |
| **Kill criteria** | KS-1 (clarified ban on programmatic extract of this method), KS-2, KS-3, KS-4, KS-5, KS-6 all apply. Any need for new anti-bot reverse eng → KS-5. Adapter path pause follows §3.5.4. |
| **Recovery / reopen** | Human product owner + refreshed #2 OpenAI section. Reopen to `gated` only if OpenAI publishes an official programmatic consumer-web path **or** counsel clears residual extract/reverse-eng theory **and** challenge rates stay below FG-5 for a defined soak. Otherwise remain `experimental` or demote to `prohibited`. |
| **Deferred** | Counsel on “agent of account owner” vs credential-sharing (G-4); quantitative ban rates (G-10). |

**Cause → effect example.** Lab enables ChatGPT Web Access for one internal Tenant. Challenge rate hits 75% over 40 minutes with 80 attempts → KS-2 auto-disables the Auth Mode → new chat/image attempts return Auth Mode disabled → reopen requires human review, not automatic cool-down alone.

### 5.2 ChatGPT Codex OAuth — `gated`

| Field | Decision |
|---|---|
| **Status** | `gated` |
| **Evidence** | #2 §4.3–4.4 (official Codex auth docs; residual share/resale); #3 Codex surface (`auth.openai.com`, `/backend-api/codex`, OAuth bundle) |
| **Assumptions in force** | Codex CLI/IDE/app Sign-in with ChatGPT is an official OAuth/CLI Access path. Consumer ToU still applies when signing in with ChatGPT consumer plans; Business/Enterprise may use Services Agreement. Official surface does **not** auto-authorize multi-Tenant entitlement resale. |
| **ToS / account-ban residual risk** | **Medium–High.** Lower reverse-eng tension than Web Access; credential non-sharing, resale/lease, plan-contract mismatch, and suspension for Usage Policy breach remain material. Product **conditionally accepts** this residual only behind gates. |
| **Acceptable-use boundary** | Tenant connects **their own** Codex/ChatGPT OAuth (or documented API-key path if later specified) for that Tenant’s workloads only. One human login MUST NOT be shared across Tenants or unrelated end users. No silent fallback to ChatGPT Web Access (FG-2). |
| **Operator obligations** | Feature flag default off until deployment opts in. Require Tenant acknowledgement of residual ToS/ban risk at connection (#9). Enforce single-Tenant ownership (#6). Support reauth/refresh failure as first-class health. Do not run multi-account “pool rental” features. |
| **Security impact** | Accepts vault custody of OAuth refresh/access tokens and related account ids. Leak = Codex/ChatGPT-acting credential compromise. Narrower reverse-eng risk than Web Access if Adapter stays on documented Codex surfaces. |
| **Kill criteria** | KS-1 if OpenAI disallows third-party storage/use of Codex tokens for SaaS agents (ties to G-8). KS-3 ban clusters. KS-4 legal notice. KS-6 if OAuth client revoked. KS-2/KS-5 only if Adapter drifts into private web/anti-bot paths. Adapter path pause follows §3.5.4. |
| **Recovery / reopen** | After kill: fix root cause, re-probe, human enable. Promotion to `allowed` blocked until D-OAI-TOKEN (G-8) and D-COMM are resolved. |
| **Deferred** | G-8 third-party SaaS custody of Codex OAuth tokens; commercial fee characterization; multi-account-per-Tenant load balancing policy. |

**Cause → effect example.** Tenant B acknowledges gate and connects Codex OAuth. OpenAI revokes the OAuth client id used by the Adapter (KS-6) → Auth Mode kill → existing Provider Accounts marked reauth-required / unusable → no execution until replacement client path is specified and gates re-passed.

### 5.3 Gemini Web Cookie — `experimental`

| Field | Decision |
|---|---|
| **Status** | `experimental` |
| **Evidence** | #2 §5.2, §5.4 Critical heat; #4 Web Cookie (`__Secure-1PSID*`, bard form protocol, bot-flag anecdotes) |
| **Assumptions in force** | Gemini Apps under Google ToS + Generative AI Prohibited Use Policy. Cookies are full Google session material, not narrow API keys. Reference `.ref/gemini-web-to-api` disclaims ToS compliance. |
| **ToS / account-ban residual risk** | **High.** Automation/protective-measure/reverse-eng tension plus observed consumer bot flags. Account restriction on Gemini app is an expected ban-class signal. Product **refuses** ordinary production residual (`experimental` only). |
| **Acceptable-use boundary** | Lab-only. MUST NOT be ordinary production connection. MUST NOT productize cookie theft, cookie markets, or anti-bot bypass. |
| **Operator obligations** | Default off. Lab profile only. Treat cookie material as critical secrets. Prefer short-lived lab vault retention. Monitor bot-flag / account-restricted signals as ban-class health. |
| **Security impact** | **Highest** sensitive-data severity among the six modes: cookie often spans Google account services. Product accepts this **only** in experimental lab custody, not as production default. |
| **Kill criteria** | KS-1–KS-6. Account-level “automated activity” restrictions correlated with Gateway traffic → count toward KS-3. New reverse eng after HTML/protocol drift → KS-5. Adapter path pause follows §3.5.4. |
| **Recovery / reopen** | Same bar as §5.1. Promotion toward `gated` requires either an official narrower credential for the same surface or counsel + security review of cookie custody. |
| **Deferred** | Endpoint robots.txt mapping (G-3); Workspace managed-account regime (G-9). |

**Cause → effect example.** Lab stores `__Secure-1PSID` for a test account. A metrics redaction bug prints the cookie into logs → security incident under OP-G3 → operator applies OP-G1 kill switch for the Auth Mode and rotates/deletes vault entries per #15 obligations once designed.

### 5.4 Gemini Antigravity OAuth — `gated`

| Field | Decision |
|---|---|
| **Status** | `gated` |
| **Evidence** | #2 §5.3–5.4 Medium–High with G-2 gap; #4 Antigravity/Cloud Code PA OAuth surface (`cloudcode-pa.googleapis.com`, Google OAuth tokens) |
| **Assumptions in force** | Adapter stays on documented Google OAuth + Antigravity/Cloud Code developer surfaces, **not** public `generativelanguage.googleapis.com` Official API Adapter (out of MVP). Product-specific Antigravity legal title may still be incomplete (G-2). |
| **ToS / account-ban residual risk** | **Medium.** Official developer/OAuth posture lowers reverse-eng tension; project suspension for ToS/AUP and quota circumvention remain real. Residual uncertainty from G-2 keeps status below `allowed`. Product **conditionally accepts** under gates. |
| **Acceptable-use boundary** | Tenant’s own Google OAuth grant for Antigravity/CLI-class surface, used only inside that Tenant. No cross-Tenant token reuse. No silent fallback to Gemini Web Cookie. |
| **Operator obligations** | Feature flag + Tenant ack. Record OAuth client identity. On token refresh failure, mark account reauth-required. Re-check G-2 when Google publishes product-specific terms. |
| **Security impact** | Accepts OAuth refresh custody (high, usually narrower than full browser session). Accepts residual uncertainty of Antigravity-named terms until G-2 closed. |
| **Kill criteria** | KS-1 if Google disallows this OAuth client class for SaaS agents. KS-3/KS-4/KS-6 standard. KS-5 if implementation abandons documented surfaces for private web reverse eng. Adapter path pause follows §3.5.4. |
| **Recovery / reopen** | Human enable after root-cause fix. Promotion to `allowed` blocked on D-ANTIGRAVITY-TERMS (G-2) and D-COMM. |
| **Deferred** | G-2 product-specific Antigravity terms; multi-project pooling policy. |

**Cause → effect example.** Deployment enables Antigravity OAuth for paying Tenants under gate. Google changes OAuth client policy to disallow the registered client (KS-6) → kill switch → Tenants see reauth/disabled mode → operators must not “fix” by switching those accounts to Web Cookie automatically (FG-2 / Auth Mode independence).

### 5.5 Grok Web SSO — `prohibited`

| Field | Decision |
|---|---|
| **Status** | `prohibited` |
| **Evidence** | #2 §6.2–6.4 Critical: AUP prohibits accessing Services through bots/scripts/non-human means by name; also scrape/resell and paid violative services language. #5 documents scripted `grok.com` REST/WS reverse surface. |
| **Assumptions in force** | Consumer ToS + AUP apply to Grok consumer apps/websites. A scripted Web Adapter is non-human automated access of that surface. Dual-regime X vs xAI issuer mapping may add risk (G-7) but is **not required** to justify prohibition given AUP text. |
| **ToS / account-ban residual risk** | **Critical / refused.** Automated Web Adapter is in direct AUP tension; account suspension and enforcement language are explicit. Product **does not accept** this residual for any product environment. |
| **Acceptable-use boundary** | **None** for PixelPlus product. MUST NOT offer Grok Web SSO connection, execution, or capability advertising in product deployments. |
| **Operator obligations** | Hard disable. Configuration enabling Grok Web SSO in product environments is a policy defect. Do not implement product UX for this mode. If code skeletons exist for research parity, they MUST remain unreachable from product composition roots. |
| **Security impact** | Product **does not accept** residual AUP collision for automated Grok web access. Credential custody risk is moot while prohibited. |
| **Kill criteria** | Already at terminal product kill. Any accidental enablement MUST be treated as a policy defect under OP-G1 (hard off) and disabled immediately. Adapter path must remain unregistered (§3.5.4). |
| **Recovery / reopen** | Only if **all** hold: (1) xAI publishes policy or official API that clearly permits the intended access method, or AUP bot/script clauses are revised; (2) #2 Grok section re-researched; (3) human product owner + counsel note recorded; (4) new status chosen explicitly (`experimental` or `gated`, never silent). G-7 issuer mapping must be closed before any non-prohibited status if SSO still involves X-platform tokens. |
| **Deferred** | None that block the current `prohibited` decision. G-7 remains relevant only on reopen. |

**Cause → effect example.** An engineer ports `.ref/grok2api` web provider into `apps/gateway` and wires it into Tenant connection UX. Review against this spec fails: status is `prohibited` → path must be removed or compile-time excluded from product builds → Tenants never see Grok Web SSO as a connectable Auth Mode.

### 5.6 Grok xAI OAuth — `gated`

| Field | Decision |
|---|---|
| **Status** | `gated` |
| **Evidence** | #2 §6.3–6.4 Medium–High: Enterprise/API path is intended programmatic integration; still forbids sell/rent/lease/time-sharing and imposes competitive-use limits; AUP still applies. #5 OAuth/CLI surfaces (`auth.x.ai`, CLI chat-proxy, `api.x.ai` media paths). |
| **Assumptions in force** | Grok xAI OAuth / Grok Build CLI is distinct from consumer web SSO. Official docs describe programmatic chat/image paths for developer/business surfaces. Enterprise time-sharing language constrains multi-Tenant rental designs. |
| **ToS / account-ban residual risk** | **Medium–High.** Official programmatic path lowers bot/reverse-eng tension vs Web SSO; lease/time-share and competitive clauses plus account-limit circumvention remain material. Termination for AUP/restriction breach is explicit. Product **conditionally accepts** under gates. |
| **Acceptable-use boundary** | Tenant’s own xAI/Grok OAuth grant for that Tenant’s workloads. No account time-share across Tenants. No silent fallback to Grok Web SSO (forbidden sibling). Do not market as resale of Grok consumer SuperGrok seats. |
| **Operator obligations** | Feature flag + Tenant ack. Track OAuth refresh health. If implementation routes some calls to `api.x.ai`, still treat this Auth Mode as Grok xAI OAuth — not a separate Official API Adapter product promise unless a future issue expands scope. |
| **Security impact** | Accepts OAuth token custody. Accepts residual Enterprise competitive-clause and lease/time-share interpretation risk under gates. |
| **Kill criteria** | KS-1 if xAI disallows third-party SaaS use of the Grok CLI OAuth client. KS-3/KS-4/KS-6 standard. Using Web SSO as a “backup” path is forbidden, not a recovery method. Adapter path pause follows §3.5.4. |
| **Recovery / reopen** | Human enable after fix. Promotion to `allowed` blocked on D-COMM and D-XAI-COMPETE. |
| **Deferred** | Commercial fee characterization vs time-sharing; competitive-clause counsel; multi-account-per-Tenant balancing. |

**Cause → effect example.** Three Tenant accounts on Grok xAI OAuth are permanently banned within 12 hours after a bad retry storm (KS-3) → Auth Mode suspended → cooldown alone is insufficient; operator must fix retry ownership (#16/#17 dependencies) before reopen.

---

## 6. Connection UX and Tenant disclosure obligations (feeds #9)

Issue #9 owns full journey design. This section locks **minimum risk disclosures** that #9 MUST implement.

### 6.1 Status-driven UX rules

| Status | Connection UX rule |
|---|---|
| `allowed` | Ordinary self-serve connection with standard security notices. |
| `gated` | Connection flow MUST show residual-risk acknowledgement (ToS/ban/credential custody). MUST require explicit Tenant accept before credential is stored as usable. Operator feature flag MUST be on first. |
| `experimental` | MUST NOT appear in ordinary production Tenant self-serve catalogs. Lab consoles only, with stronger “research only / high ban and ToS tension” warning. |
| `prohibited` | MUST NOT appear in any product connection catalog. API that attempts to create such a Provider Account MUST fail closed with a stable non-enumerating error class consistent with #6/#16. |

### 6.2 Required acknowledgement themes for `gated` modes

Tenant acknowledgement MUST cover, in plain language:

1. Tenant confirms they are authorized to use the Provider Account.
2. Tenant understands Provider may suspend the account for ToS/AUP reasons.
3. Tenant understands PixelPlus will store Provider Credential material to act on their behalf inside their Tenant only.
4. Tenant understands Web Access sibling modes (if any) are separate and may be unavailable (`experimental`/`prohibited`).

Exact copy is #9; themes above are normative.

### 6.3 No silent cross-mode fallback

Routing and connection recovery MUST NOT:

- Replace a dead Codex OAuth account with ChatGPT Web Access without explicit Tenant policy naming that Auth Mode and status allowing it.
- Replace Grok xAI OAuth with Grok Web SSO (Web SSO is prohibited).
- Replace Antigravity OAuth with Gemini Web Cookie without explicit experimental lab policy.

This binds #11 routing policy design.

---

## 7. Adapter, routing, and implementation constraints

These constraints apply to all later implementation issues:

1. **Composition root** MUST consult Auth Mode risk status before registering an Adapter as Tenant-connectable.
2. **Capability Snapshot** publishing MUST NOT imply risk acceptance; a snapshot may exist in lab for experimental modes without making the mode `allowed`.
3. **Health states** for challenge, ban, auth expiry remain first-class even when an Auth Mode is gated.
4. **Multi-account load balancing inside one Tenant** is not authorized by this document; it is deferred and must re-check resale/lease/time-share tension before enablement.
5. **Official API Adapters** remain out of MVP even if gated OAuth modes touch related hosts.
6. **Tests** MUST include negative cases: prohibited Auth Mode rejected; gated mode rejected without flag/ack; experimental mode absent from production catalog fixtures.

---

## 8. Deferred decisions and reopen triggers

| ID | Deferred decision | Blocks | Reopen trigger |
|---|---|---|---|
| **D-COUNSEL-AGENT** | Whether “technical agent of the account owner” mitigates credential-sharing clauses (G-4) | Promotion of any mode to `allowed`; possibly experimental→gated for Web modes | Written counsel note for target launch jurisdictions |
| **D-COUNSEL-RE** | Enforceability of reverse-engineering prohibitions by jurisdiction (G-5) | Regional launch of experimental Web modes outside pure lab | Counsel + regional launch checklist |
| **D-OAI-TOKEN** | Whether OpenAI permits third-party SaaS custody of Codex OAuth tokens outside official clients (G-8) | ChatGPT Codex OAuth → `allowed` | OpenAI developer/app policy update or counsel |
| **D-ANTIGRAVITY-TERMS** | Product-specific Antigravity terms beyond Google APIs / Gemini API family (G-2) | Gemini Antigravity OAuth → `allowed` | Primary-source terms page retrieved and reviewed into #2 annex |
| **D-GROK-ISSUER** | Grok SSO token issuer matrix xAI vs X (G-7) | Any future non-prohibited Grok Web status | #5/#2 update with issuer→ToS map |
| **D-XAI-COMPETE** | Scope and residual risk of xAI Enterprise competitive-product clause for a multi-provider Gateway | Grok xAI OAuth → `allowed` | Counsel read of Enterprise ToS competitive clause against PixelPlus product shape |
| **D-COMM** | Commercial Gateway fee structure vs “resale/lease/time-sharing” characterization | Marketing claims; possibly `gated`→`allowed` | Product + counsel characterization recorded |
| **D-MULTI-ACCT** | Multi-account-per-Tenant pooling / load balancing | Routing features that shard load across many consumer accounts | Risk review against resale/lease language + #11 design |
| **D-REGION** | Regional launch restrictions | Production launch geography | Counsel + enforcement landscape review |
| **D-NUMERIC-TUNE** | Loosening KS-2/FG-5 numeric thresholds | More permissive auto-kill behavior | Incident data from production-like traffic + product owner sign-off |

Deferred items MUST NOT be silently closed by implementers. Closing requires updating this document (or a superseding ADR) with evidence links.

---

## 9. Relationship to evidence documents

| Research doc | Role relative to this decision |
|---|---|
| #2 compliance risk evidence | Supplies policy text, heat map, KS/FG/RR catalog. This doc **binds** selected triggers into product policy and **decides** statuses. |
| #3 ChatGPT capability | Supplies surface separation and challenge technical signals; does not set status. |
| #4 Gemini capability | Same for Gemini; cookie sensitivity informs experimental stance. |
| #5 Grok capability | Same for Grok; scripted web surface + AUP text → prohibited. |
| #6 ownership invariants | Orthogonal security boundary; risk status cannot weaken Tenant isolation. |

When #2 is refreshed (RR-*), owners MUST diff automation/credential/resale clauses and update §4–§5 if tension materially changes.

---

## 10. Acceptance criteria checklist (issue #7)

| Criterion | Status | Where satisfied |
|---|---|---|
| All six Auth Modes have individual status and rationale (not Provider-level only) | **Met** | §4 matrix + §5 cards |
| Each status links to evidence and states assumptions still in force | **Met** | Each §5 card Evidence + Assumptions |
| Kill criteria and recovery described with observable signals | **Met** | §3.5 + per-card Kill/Recovery |
| Deferred decisions have dependency and reopen triggers | **Met** | §8 |
| Specification only; no Gateway implementation | **Met** | §1.2 |
| Downstream constraints for UX/routing/adapters recorded | **Met** | §6–§7 |

---

## 11. Downstream consumers

| Issue / area | What it must consume from this doc |
|---|---|
| #8 Client API Key / abuse | Abuse controls cannot re-enable prohibited modes or bypass gates. |
| #9 Provider Account connection | Status-driven UX rules §6; acknowledgements for gated modes. |
| #10 Capability Snapshot | Snapshots do not override risk status. |
| #11 Routing / fallback | No silent cross-Auth-Mode fallback into experimental/prohibited modes. |
| #15 Vault | Critical custody assumptions for cookies vs OAuth; experimental retention preferences. |
| #16 Errors | Fail-closed errors when Auth Mode disabled/prohibited/gated-without-ack. |
| #17 Health / operator | Kill switch visibility; KS/FG counters; cooldown interaction. |
| Implementation planning | Composition root registration gated by status table. |

---

## 12. Document control

| Field | Value |
|---|---|
| Status | Accepted for specification (issue #7) |
| Check date of evidence inputs | 2026-07-14 |
| Supersedes | n/a (initial product risk envelope) |
| Next review | On any RR-* from #2, any KS-1/KS-4 event, or before promoting any Auth Mode status |
| Authors | Spec decision agent for issue #7 |
