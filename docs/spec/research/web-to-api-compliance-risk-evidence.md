# SaaS Web-to-API Compliance and Risk Evidence Base

- **Issue:** [#2](https://github.com/monet88/pixelplus/issues/2) — research only; does **not** set product risk acceptance
- **Parent:** [#1](https://github.com/monet88/pixelplus/issues/1)
- **Downstream consumer:** [#7](https://github.com/monet88/pixelplus/issues/7) (Risk Envelope and Kill Criteria)
- **Check date:** 2026-07-14
- **Domain vocabulary:** `CONTEXT.md` (`Provider`, `Provider Account`, `Provider Credential`, `Auth Mode`, `Web Access`, `OAuth/CLI Access`, `Web Adapter`, `BYOA`, `Tenant`)

## 1. Purpose and non-goals

### Purpose

Build a **sourced** evidence profile of acceptable-use, ToS/account-ban, anti-automation, reverse-engineering, multi-account, commercial resale/proxying, challenge, and sensitive-credential risks for a SaaS **BYOA** Gateway that may use:

| Provider family | Web Access Auth Mode | OAuth/CLI Access Auth Mode |
|---|---|---|
| ChatGPT / OpenAI | ChatGPT Web Access | ChatGPT Codex OAuth |
| Gemini / Google | Gemini Web Cookie | Gemini Antigravity OAuth |
| Grok / xAI | Grok Web SSO | Grok xAI OAuth |

### Non-goals (explicit)

- **Not** deciding which risks PixelPlus accepts (reserved for #7).
- **Not** a capability matrix for chat/image/inpaint (reserved for #3–#5).
- **Not** legal advice, counsel opinion, or jurisdiction-specific enforceability analysis.
- **Not** treating `.ref/*` reverse-engineering projects as compliance proof.

---

## 2. Method, source ranking, confidence rubric

### 2.1 Source ranking (highest → lowest)

| Rank | Class | Use for |
|---|---|---|
| A | Official Terms of Use / Terms of Service / Services Agreement / Enterprise ToS | Binding use restrictions, account sharing, resale, reverse engineering |
| B | Official Acceptable Use / Usage / Prohibited Use policies | Content and safety abuse; some automation language |
| C | Official developer / API / service-specific terms and auth docs | Distinction between consumer web and OAuth/API/CLI surfaces |
| D | Official Help Center / Privacy Hub product pages | Operational clarification of which terms apply; not a substitute for A/B |
| E | Public developer forums / community reports | Existence of enforcement signals only; not policy text |
| F | Local reference repos under `.ref/` | **Technical challenge surfaces only** (PoW, cookies, Cloudflare, SSO shapes). Never used as legal permission. |

### 2.2 Confidence rubric

| Level | Meaning |
|---|---|
| **high** | Direct quote/paraphrase from Rank A/B primary page retrieved on check date; applicability to named surface is clear |
| **medium** | Rank C/D official page, or Rank A/B text that requires interpretation for SaaS BYOA / Web-to-API |
| **low** | Rank E enforcement anecdote, or inference from adjacent product surface |
| **unverified** | No primary evidence found; must not become a product promise |

### 2.3 Evidence record schema

Every conclusion below uses:

- **Claim**
- **Sources** (URL)
- **Checked** (2026-07-14 unless noted)
- **Confidence**
- **Applicability limits**
- **Implication for PixelPlus risk lens** (fact → risk signal only; not acceptance)

### 2.4 Interpretation rules used in this document

1. **Auth Mode is the unit of risk**, not the Provider brand name (`CONTEXT.md`).
2. **Web Access ≠ OAuth/CLI Access** even when both map to one external human identity.
3. **Official OAuth/CLI path** reduces *some* reverse-engineering risk relative to private web backend reverse engineering, but does **not** auto-clear credential-sharing, resale, multi-tenant proxy, or AUP bot language.
4. **BYOA** means Tenant-owned Provider Accounts; it does **not** by itself legalize third-party automated use of consumer surfaces.
5. Gaps are labeled **`unverified`**, never filled with marketing claims.

---

## 3. Cross-cutting risk themes for a SaaS Web-to-API Gateway

| Theme | Why it matters for PixelPlus | Typical policy hooks (all families) |
|---|---|---|
| Programmatic / bot access of **consumer Web** | Core of Web Adapter design | “automated/programmatic extract”, “bots”, “automated means”, “non-human means” |
| Reverse engineering / protocol discovery | Web Adapter often mirrors private web APIs | reverse engineer, decompile, extract underlying components/models |
| Circumvent rate limits / safety / anti-bot | Challenge solvers, clearance refresh, multi-IP | bypass protective measures, circumvent rate limits/restrictions |
| Credential sharing / third-party custody | SaaS vault holds Provider Credential for Tenant | “do not share account credentials”, “make account available to anyone else” |
| Multi-account pooling / load balancing | Operator patterns in reference gateways | resell/lease/time-sharing; circumvention of account limits |
| Commercial resale / proxying of consumer entitlement | Public API monetization over consumer subscription | lease/sell/distribute Services; resell Outputs; time sharing |
| Official API/CLI alternatives | OAuth/CLI Adapter may still be lower reverse-eng risk | Business/Enterprise/API terms grant programmatic use under different contract |
| Sensitive data in vault | Cookies/SSO/OAuth refresh = account takeover | privacy + security obligations of both PixelPlus and Tenant |

---

## 4. Provider family: ChatGPT / OpenAI

### 4.1 Governing documents (primary)

| Document | URL | Surface relevance |
|---|---|---|
| Terms of Use (consumer / individuals) | https://openai.com/terms and https://openai.com/policies/row-terms-of-use/ | ChatGPT Web Access; Codex when signed in with ChatGPT consumer plan |
| Usage Policies | https://openai.com/policies/usage-policies/ | All OpenAI services named by the policy |
| Service Terms | https://openai.com/policies/service-terms/ | Service-specific features including Codex/code generation notes |
| Services Agreement / Business Terms | https://openai.com/policies/services-agreement/ | APIs, ChatGPT Business/Enterprise/Edu and other business/developer services |
| Codex authentication docs | https://developers.openai.com/codex/auth.md | Official ChatGPT sign-in vs API key for Codex CLI/IDE/app |
| Codex plan help | https://help.openai.com/en/articles/11369540 | Which terms apply when Codex uses ChatGPT plan |
| Account Sharing Policy (Help Center) | https://help.openai.com/en/articles/10471989-openai-account-sharing-policy | Operational account-sharing guidance (**full body partially unverified** this check — see gaps) |

### 4.2 ChatGPT Web Access (consumer web surface)

#### Risk table

| Risk area | Evidence-backed conclusion | Confidence | Applicability limits |
|---|---|---|---|
| **ToS / AUP boundary** | Consumer ToU prohibits illegal/harmful/abusive activity and requires compliance with Usage Policies. | **high** | Applies to individual ChatGPT/DALL·E and associated apps/websites under ToU, not automatically to Enterprise API contracts. |
| **Credential / account sharing** | “You may not share your account credentials or make your account available to anyone else and are responsible for all activities that occur under your account.” | **high** | Literal text targets credential sharing and making the account available to others. How a BYOA vault is characterized (agent of account owner vs third-party sharing) is **unverified** without counsel. |
| **Automation / scraping / programmatic extract** | Explicit prohibition: “Automatically or programmatically extract data or Output.” | **high** | Text does not carve out “self-hosted reverse proxy of my own account.” Web Adapter that programmatically extracts chat/image Output from consumer web is **directly implicated**. |
| **Reverse engineering** | Prohibits reverse engineer / decompile / discover source or underlying components (models, algorithms, systems), except where law forbids the restriction. | **high** | Applies to Services broadly; private web API reverse engineering is the classic risk pattern for Web Adapters. |
| **Protective measures / rate limits** | Prohibits interfering/disrupting Services, including circumventing rate limits/restrictions or bypassing protective measures or safety mitigations. | **high** | Challenge solvers, CF clearance automation, multi-egress rotation intended to evade limits are implicated. |
| **Commercial resale / redistribute Services** | Prohibits modify, copy, lease, sell or distribute any of the Services. | **high** | Reselling ChatGPT consumer access as a multi-tenant API is a high-tension reading; exact litigation outcome **unverified**. |
| **Multi-account** | No explicit “one natural person, one free account” clause located in the ToU body retrieved this check. Business Services Agreement separately forbids sharing individual logins across users and reselling/leasing Account access. | **medium** for consumer multi-account specifics | Consumer multi-account abuse often enforced operationally; formal multi-account rule text beyond credential-sharing is **partially unverified**. |
| **Challenge / ban signals (technical, not legal)** | Reference `chatgpt2api` documents Cloudflare interception, FlareSolverr clearance refresh, and implements PoW / Turnstile / Sentinel utilities — evidence that OpenAI/ChatGPT web edges present anti-bot challenges in the wild. | **medium** (existence of challenge surface) | Not proof of policy text; only operational risk that Web Adapter will hit challenges and that “solving” them may itself be circumvention. |

**Sources (checked 2026-07-14):**

- https://openai.com/terms
- https://openai.com/policies/row-terms-of-use/
- https://openai.com/policies/usage-policies/
- Local technical only: `.ref/chatgpt2api/README.md`, `.ref/chatgpt2api/utils/{pow,turnstile,sentinel}.py`

**Implication (risk signal only):** ChatGPT Web Access as a SaaS Web Adapter carries **stacked high-tension clauses**: programmatic extract + reverse engineering + protective-measure bypass + credential non-sharing + no lease/sell/distribute Services.

### 4.3 ChatGPT Codex OAuth (OAuth/CLI Access)

#### Risk table

| Risk area | Evidence-backed conclusion | Confidence | Applicability limits |
|---|---|---|---|
| **Official surface exists** | OpenAI documents Codex CLI / IDE / desktop app authentication via **Sign in with ChatGPT** (subscription) or **API key** (usage-based). This is an official OAuth/CLI Access path, not a pure reverse of chatgpt.com chat UI. | **high** | Official docs authorize use of those clients; they do **not** authorize arbitrary third-party multi-tenant resale of subscription entitlements. |
| **Which contract applies** | Help Center: when signing into Codex with a ChatGPT account, ChatGPT Terms of Use + Privacy Policy apply; Business/Enterprise/API users may fall under the online services agreement instead. | **high** | Plan type changes the contract. Consumer Plus/Pro ≠ Enterprise Services Agreement. |
| **Business/API restrictions (if that contract applies)** | Services Agreement restrictions include: no Reverse Engineer; no extract data other than as permitted through the Services; **no buy/sell/transfer API keys**; no interfere/disrupt including circumvent rate limits or bypass protective measures; no violate/circumvent Usage Limits; Customer must not share Account access credentials or individual login credentials between multiple users; **may not resell or lease access** to its Account or any End User Account. | **high** for business/API customers | Does not automatically rewrite consumer ToU, but shows OpenAI’s consistent posture against credential markets and account leasing. |
| **Automation** | Enterprise Codex docs describe **Codex access tokens** for trusted non-interactive local workflows under admin permission — an official automation path inside Enterprise controls. API key path is documented for programmatic CLI (e.g. CI) with warnings not to expose in untrusted/public environments. | **high** | Official automation ≠ permission for PixelPlus to pool many consumer ChatGPT subscriptions behind a public SaaS API. |
| **Credential sharing** | Consumer ToU non-sharing + Business “no share individual login credentials between multiple users” both pressure multi-user reuse of one ChatGPT login through a gateway. | **high** | BYOA single-Tenant / single-owner use is lower tension than shared pool; still not a counsel-cleared design. |
| **Commercial proxying** | Business terms: no resell/lease Account access. Consumer ToU: no lease/sell/distribute Services. | **high** (text) / **medium** (application to BYOA SaaS fees) | Charging for Gateway software/ops vs charging for OpenAI entitlement is a product/legal characterization left to #7 + counsel. |
| **Reverse engineering residual** | Using official Codex endpoints with official OAuth reduces need to reverse private chatgpt.com web backends, but any undocumented protocol extension or token abuse remains reverse-eng risk. | **medium** | Depends on whether Adapter stays within documented Codex surfaces. |
| **Challenge / ban** | Official OAuth clients still subject to rate limits, plan quotas, workspace controls, and suspension for ToU/Usage Policy breach. | **high** | Suspension grounds appear in consumer ToU (breach, legal requirement, risk/harm). |

**Sources (checked 2026-07-14):**

- https://developers.openai.com/codex/auth.md
- https://help.openai.com/en/articles/11369540
- https://openai.com/policies/services-agreement/
- https://openai.com/policies/service-terms/
- https://openai.com/terms
- Technical prior art only: `.ref/CLIProxyAPI/` (Codex OAuth executor patterns)

**Implication (risk signal only):** ChatGPT Codex OAuth is **structurally lower reverse-engineering risk** than ChatGPT Web Access because OpenAI publishes auth and product surfaces; **credential-sharing, resale/lease, and multi-account pooling risks remain material**.

### 4.4 ChatGPT family — Web vs OAuth/CLI contrast

| Dimension | ChatGPT Web Access | ChatGPT Codex OAuth |
|---|---|---|
| Primary contract (typical) | Consumer Terms of Use | Consumer ToU (ChatGPT sign-in) **or** Services Agreement (Business/Enterprise/API key) |
| Official programmatic surface? | No public “ChatGPT Web API” for reverse-proxy use found | Yes — Codex CLI/IDE/app + documented auth |
| Explicit anti-programmatic-extract clause | Yes (consumer ToU) | Business: extract only as permitted through Services; consumer ToU still applies on ChatGPT sign-in |
| Typical Provider Credential | Web access token / session material | OAuth tokens from ChatGPT sign-in or API key / enterprise access token |
| Dominant residual risks | Programmatic extract, reverse eng, anti-bot bypass, CF challenges | Credential sharing, resale/lease, quota circumvention, plan-contract mismatch |
| Kill-priority if SaaS | High for public multi-tenant Web Adapter | Medium–High depending on whether product is BYOA-self-use vs entitlement resale |

---

## 5. Provider family: Gemini / Google

### 5.1 Governing documents (primary)

| Document | URL | Surface relevance |
|---|---|---|
| Google Terms of Service | https://policies.google.com/terms | Gemini Apps / consumer Google services |
| Generative AI Prohibited Use Policy | https://policies.google.com/terms/generative-ai/use-policy | Gemini Apps and other gen-AI products that reference it |
| Gemini Apps Privacy Hub (terms pointer) | https://support.google.com/gemini/answer/13594961 | States Google ToS + Prohibited Use Policy apply to Gemini Apps |
| Google APIs Terms of Service | https://developers.google.com/terms | Developer API clients |
| Gemini API Additional Terms of Service | https://ai.google.dev/gemini-api/terms | Gemini API / AI Studio developer services |

### 5.2 Gemini Web Cookie (consumer web surface)

#### Risk table

| Risk area | Evidence-backed conclusion | Confidence | Applicability limits |
|---|---|---|---|
| **ToS / AUP boundary** | Gemini Apps are under Google Terms of Service and Generative AI Prohibited Use Policy (Privacy Hub). | **high** | Workspace / business Gemini may use different agreements; consumer gemini.google.com is the Web Cookie surface. |
| **Automation / scraping** | Google ToS: must not use automated means to access content from Google services **in violation of machine-readable instructions** on web pages (e.g. robots.txt disallow for crawling/training/other activities). | **high** for the robots.txt-conditioned rule; **medium** for whether specific Gemini chat XHR endpoints are “content access via automated means” under that clause | Exact robots.txt / machine-readable policy for gemini.google.com app endpoints was **not fully re-audited endpoint-by-endpoint** this check → partial **unverified** mapping. |
| **Bypass / disruption** | Google ToS prohibits abusing/harming/interfering/disrupting services, including spamming, hacking, or **bypassing systems or protective measures**; also jailbreaking/adversarial prompting/prompt injection outside official safety/bug programs. | **high** | Anti-bot bypass and safety-filter circumvention are in-scope. |
| **Reverse engineering** | Google ToS prohibits reverse engineering services or underlying technology (e.g. ML models) to extract trade secrets or other proprietary information, except as allowed by law. | **high** | Web-cookie reverse of Gemini internal APIs is the standard Web Adapter pattern and sits in this risk zone. |
| **GenAI prohibited use** | Prohibited Use Policy forbids abuse of / interference with Google infrastructure/services and **circumvention of abuse protections or safety filters**. | **high** | Applies to interactions with generative AI in Google products that reference the policy. |
| **Credential nature** | Reference implementations use browser cookies such as `__Secure-1PSID` and `__Secure-1PSIDTS`. These are full Google account session cookies, not narrow API keys. | **high** (technical fact) | Holding them in a SaaS vault is high-impact security risk (account takeover across Google properties possible depending on cookie scope). |
| **Commercial / multi-account** | Google ToS does not, in the retrieved sections, grant a right to run a multi-tenant commercial proxy of consumer Gemini. “Hiding or misrepresenting who you are in order to violate these terms” is prohibited. | **medium** | Explicit “no resell Gemini consumer” clause not located as a single sentence; residual commercial risk remains. |
| **Challenge / ban signals** | Public Google AI Developers Forum thread describes Gemini web app restricted after an “automated activity” / bot flag while other Google AI surfaces still worked — evidence of **account-level bot enforcement** on consumer Gemini. | **low–medium** | Anecdotal; proves enforcement exists, not thresholds. |
| **Reference project disclaimer** | `.ref/gemini-web-to-api` explicitly warns reverse-engineered web cookies may not comply with Google ToS and is research/educational only. | **medium** as industry self-assessment | Not an official Google statement. |

**Sources (checked 2026-07-14):**

- https://policies.google.com/terms
- https://policies.google.com/terms/generative-ai/use-policy
- https://support.google.com/gemini/answer/13594961?hl=en
- https://discuss.ai.google.dev/t/gemini-web-app-restricted-after-false-bot-flag-appeal-ai-studio-still-works/112633
- Technical only: `.ref/gemini-web-to-api/README.md`

**Implication (risk signal only):** Gemini Web Cookie combines **session-cookie custody risk** with Google’s **anti-automation / reverse-engineering / protective-measure** rules. Account bot flags are an observed operational kill signal.

### 5.3 Gemini Antigravity OAuth (OAuth/CLI Access)

#### Risk table

| Risk area | Evidence-backed conclusion | Confidence | Applicability limits |
|---|---|---|---|
| **Developer/API contract family** | Gemini API Additional Terms require accepting Google APIs Terms + Additional Terms; use is described as for developers building with Google AI models for professional/business purposes (not consumer use of AI Studio/API). Must follow Prohibited Use Policy; may not reverse engineer/extract/replicate components including models; may not bypass safety features. | **high** for Gemini API / AI Studio | **Unverified:** whether “Antigravity” branded product has a separately named ToS page beyond Google OAuth + Gemini/AI developer terms. Treat Antigravity OAuth as **Google developer OAuth/CLI surface family** pending product-specific confirmation in #4. |
| **API prohibitions (Google APIs ToS)** | Google APIs Terms include prohibitions such as reverse engineering/extracting source from APIs, interfering with APIs, scraping/creating permanent copies of content beyond cache headers, and not circumventing documented API limits. | **high** | Applies when the integration is actually using Google APIs under those terms. |
| **Official programmatic posture** | Unlike consumer Gemini web cookies, Google publishes first-party Gemini API / AI Studio developer access. OAuth/CLI Access that stays on documented Google developer endpoints is aligned with an official integration model. | **medium–high** | PixelPlus MVP explicitly excludes Official API Adapter as a product promise in #1 Out of Scope; Antigravity OAuth is still a distinct Auth Mode that may ride Google OAuth developer surfaces — capability details are #4. |
| **Credential sharing / multi-account** | API clients still must respect account/project quotas and not circumvent limitations. Multi-project abuse / credential markets not expressly blessed. | **medium** | Exact Google Cloud / AI Studio seat-sharing rules not fully enumerated here. |
| **Commercial resale** | Google API / Gemini API terms regulate how developer services may be used in applications; they are not a license to resell consumer Gemini. Grounding-specific terms even forbid reselling certain grounded results. | **medium** | Depends on which Google products the OAuth token actually unlocks. |
| **Bypass / safety** | Additional Terms: may not attempt to bypass protective measures. | **high** | Safety-filter jailbreak automation remains prohibited. |
| **Challenge / ban** | Developer projects can be limited/suspended for ToS/AUP violations; rate limits must not be circumvented. | **high** | Different enforcement plane than consumer bot flags, but still real. |

**Sources (checked 2026-07-14):**

- https://ai.google.dev/gemini-api/terms
- https://developers.google.com/terms
- https://policies.google.com/terms/generative-ai/use-policy
- Technical prior art only: `.ref/CLIProxyAPI/` Antigravity translators/executors

**Implication (risk signal only):** Gemini Antigravity OAuth is **lower reverse-eng / cookie-theft risk** than Gemini Web Cookie **if** confined to documented Google OAuth/developer surfaces; **product-specific Antigravity terms remain partially unverified**.

### 5.4 Gemini family — Web vs OAuth/CLI contrast

| Dimension | Gemini Web Cookie | Gemini Antigravity OAuth |
|---|---|---|
| Primary contract | Google ToS + GenAI Prohibited Use | Google APIs ToS + Gemini API Additional Terms (+ OAuth client policies) |
| Credential type | Browser session cookies (`__Secure-1PSID*`) | Google OAuth tokens for developer/CLI surface |
| Official programmatic path? | No | Yes (Gemini API / AI Studio family); Antigravity naming detail **unverified** |
| Dominant residual risks | Automation vs robots/protective measures; reverse eng; cookie ATO; bot flags | Quota circumvention; AUP; uncertain product-specific Antigravity terms; multi-project pooling |
| Sensitive-data severity | Extremely high (broad Google session) | High (OAuth refresh) but usually narrower than full browser session |

---

## 6. Provider family: Grok / xAI

### 6.1 Governing documents (primary)

| Document | URL | Surface relevance |
|---|---|---|
| Terms of Service — Consumer | https://x.ai/legal/terms-of-service | Grok consumer apps/websites (Grok Web SSO) |
| Acceptable Use Policy | https://x.ai/legal/acceptable-use-policy | Consumers, developers, and businesses |
| Terms of Service — Enterprise | https://x.ai/legal/terms-of-service-enterprise | xAI APIs, Grok Business, developer/business services |
| Legal index | https://x.ai/legal | Navigation hub |
| Consumer FAQs | https://x.ai/legal/faq | Clarifications (non-ToS) |

**Note:** Consumer ToS states use of Grok **on the X platform** is governed by X Terms, not these xAI consumer Terms. Grok Web SSO implementations that authenticate via X may inherit **dual-regime** risk (xAI + X). Exact split for each SSO token type is **partially unverified** without mapping token issuer → surface in #5.

### 6.2 Grok Web SSO (consumer web surface)

#### Risk table

| Risk area | Evidence-backed conclusion | Confidence | Applicability limits |
|---|---|---|---|
| **ToS / AUP boundary** | Consumer ToS requires compliance with AUP; access/use must not engage in illegal/harmful/abusive activities or other AUP violations. xAI may suspend/discontinue access for violations, abuse prevention, law, or security. | **high** | Consumer Service definition covers Grok and associated apps/websites for individuals. |
| **Credential sharing** | “You may not share your account credentials or make your account available to anyone else.” | **high** | Same BYOA characterization gap as other families. |
| **Automation / bots (explicit)** | AUP prohibits detrimentally impacting the Service by “**using bots to access**” and, separately, “**Accessing the Services through automated or non-human means, whether through a bot, script, or otherwise**.” | **high** | This is among the **most explicit** anti-bot clauses of the three families. A Web Adapter that scripts Grok web is directly in tension. |
| **Reverse engineering** | AUP: modifying/copying/translating/leasing/selling/reselling/…/**reverse engineer**/decompile/disassemble or seek source of Service/systems/models/algorithms (except where law prohibits). | **high** | |
| **Scrape / resell outputs** | AUP: scraping, harvesting or reselling any Input or Output, or distilling model data or Outputs. | **high** | Affects both data harvesting and commercial resale of outputs obtained via prohibited access. |
| **Protective measures / rate limits** | AUP: disrupting/interfering/unauthorized access including circumventing rate limits/restrictions or protective measures and safety mitigations; bypassing systems or protective measures. | **high** | |
| **Commercial facilitation of violations** | AUP: “Providing services that encourage others to violate these Terms, including by operating websites offering violative outputs from our Services in exchange for payment.” | **high** | Directly relevant to a paid SaaS that industrially produces outputs via non-compliant access. |
| **Enterprise-style resale language (consumer still under AUP)** | Enterprise ToS (for API/business) separately forbids sell/rent/lease/time sharing — shows commercial posture; consumer path is already constrained by AUP bots/resell language. | **medium** for cross-reading | Do not apply Enterprise ToS wholesale to pure consumer accounts. |
| **Challenge / ban (technical)** | `.ref/grok2api` models independent Web SSO vs Grok xAI OAuth pools, cooldown/fault switching, SSO non-refresh expiry — operational evidence of credential fragility and health states. | **medium** technical | Not legal permission. |

**Sources (checked 2026-07-14):**

- https://x.ai/legal/terms-of-service
- https://x.ai/legal/acceptable-use-policy
- Technical only: `.ref/grok2api/README.md`, provider web/cli packages

**Implication (risk signal only):** Grok Web SSO is **high-tension** for any automated Web Adapter because AUP text targets bots/scripts and non-human access **by name**.

### 6.3 Grok xAI OAuth (OAuth/CLI Access)

#### Risk table

| Risk area | Evidence-backed conclusion | Confidence | Applicability limits |
|---|---|---|---|
| **Official business/API contract** | Enterprise ToS governs business customers of xAI business services including **xAI API and Grok Business**. Access is non-exclusive, non-transferable (with stated exceptions), for business purposes and documentation. | **high** | Applies when Customer actually contracts for those services — not when only a consumer Grok login is used. |
| **Permitted integration** | Enterprise ToS grants limited right to use xAI APIs to develop integration between Services and Customer products (“Bundled Services”) and make them available to End-Users under Customer responsibility. | **high** | This is the legitimate commercial integration path — distinct from scraping grok.com consumer web. |
| **Password / credential confidentiality** | If passwords are issued, Customer must require Permitted Users keep user ID/password confidential and **not share with any unauthorized person**; notify on compromise. | **high** | |
| **Hard restrictions** | Customer shall not (and shall not allow third parties to): (a) sell, rent, lease or use any Service for **time sharing**; (b) help develop/provide competitive similar service; (c) reverse engineer; (e) scrape User Content / distill model behavior; (g) access/use to **circumvent or exceed service account limitations**; (j) probe/scan/penetrate/benchmark; plus AUP compliance. xAI may monitor and terminate. | **high** | Time-sharing / sell-rent-lease language is especially relevant to multi-tenant gateways that rent capacity. |
| **AUP still applies to developers/businesses** | AUP header: applies to anyone using the Service, including consumers, developers and businesses. Bot/script and reverse-eng clauses therefore still matter for how OAuth clients behave. | **high** | Official API use is expected to be non-human; the AUP’s “bots” language is best read together with Enterprise permission to use APIs — **medium** interpretive residual for edge cases. |
| **Competitive product clause** | Enterprise forbids using Service to help develop or provide any product/service similar to or competitive with any Service. | **medium–high** | A multi-provider AI gateway could be argued as adjacent; enforceability and scope **unverified** without counsel. |
| **Challenge / ban** | Corrective actions include immediate account termination at xAI discretion for AUP/restriction breaches. | **high** | |

**Sources (checked 2026-07-14):**

- https://x.ai/legal/terms-of-service-enterprise
- https://x.ai/legal/acceptable-use-policy
- https://x.ai/legal/terms-of-service
- Technical prior art only: `.ref/CLIProxyAPI/`, `.ref/grok2api/` CLI OAuth

**Implication (risk signal only):** Grok xAI OAuth / API is the **intended** programmatic and commercial integration path, but Enterprise restrictions still forbid time-sharing/resale patterns and competitive misuse; AUP remains in force.

### 6.4 Grok family — Web vs OAuth/CLI contrast

| Dimension | Grok Web SSO | Grok xAI OAuth |
|---|---|---|
| Primary contract | Consumer ToS + AUP (+ possible X ToS if on X) | Enterprise ToS + AUP + purchase terms |
| Explicit anti-bot/script language | **Yes — very explicit** | AUP still present; Enterprise grants API integration rights |
| Official programmatic path? | No | Yes — xAI API / Grok Business |
| Dominant residual risks | Bots/scripts, scrape/resell, reverse eng, credential sharing | Time-sharing/lease, competitive clause, account limit circumvention, End-User compliance |
| Credential lifecycle (technical) | SSO often non-refresh; reauth required (ref) | OAuth refresh patterns (ref) |

---

## 7. Sensitive-data and credential-handling risks (BYOA SaaS vault)

This section is **security/compliance-adjacent evidence**, not the full vault design (#15).

| Credential class | Auth Modes | Sensitivity | Policy hooks | Vault risk notes |
|---|---|---|---|---|
| Web access tokens / session cookies | ChatGPT Web Access; Gemini Web Cookie | **Critical** — often equivalent to full consumer session | OpenAI/Google/xAI “do not share credentials”; Google cookie can span account services | Encrypt at rest; never log raw values; Tenant isolation; short display; reauth UX; treat leak as account takeover |
| Web SSO tokens | Grok Web SSO | **Critical** | xAI credential non-sharing; possible X-platform dual terms | Same as cookies; document issuer (xAI vs X) when known |
| OAuth refresh/access tokens | ChatGPT Codex OAuth; Gemini Antigravity OAuth; Grok xAI OAuth | **Critical** | Business terms on credential confidentiality; API key non-transfer (OpenAI) | Support rotation/revocation; bind to Tenant + Provider Account; no cross-Tenant decrypt |
| API keys (if ever stored) | Codex API-key path; official APIs (out of MVP scope per #1) | **Critical** | OpenAI: no buy/sell/transfer API keys | Prefer not to broker third-party key marketplaces |

### Cross-cutting vault conclusions

1. **Provider Credential ≠ Provider Account** (`CONTEXT.md`): secrets need separate lifecycle, audit, and deletion.
2. **BYOA does not remove third-party processor risk:** PixelPlus becomes custodian of Tenant secrets capable of acting as the user at the Provider.
3. **Logging/metrics must redaction-default** Provider Credential, cookies, Authorization headers, and raw SSO payloads (aligns with parent #1 security stories).
4. **Tenant copy / informed connection:** connection UX should disclose that credentials may violate Provider non-sharing rules if misused, and that Web Access modes carry higher ban/ToS tension — exact product copy is #7/#9.
5. **Unverified:** whether acting as a technical agent solely for the credential owner is a viable legal theory in target jurisdictions.

---

## 8. Kill-switch, feature-gate, and re-research triggers

These are **observable triggers for operators and for #7 decisioning**. They are **not** themselves product decisions.

### 8.1 Kill-switch triggers (disable an Auth Mode / Web Adapter immediately)

| ID | Trigger | Why | Typical scope |
|---|---|---|---|
| KS-1 | Provider publishes or clarifies a ban on automated/bot/script access that **unambiguously** covers the Adapter’s method | Direct ToS/AUP collision | Auth Mode or entire Web Access class |
| KS-2 | Sustained challenge storm (Cloudflare/captcha/PoW/bot interstitial) making successful completion **require** bypass tooling | Circumvention risk + unreliability | Web Adapter |
| KS-3 | Clustered Provider Account bans/disables correlated with Gateway traffic patterns | Account-harm + legal exposure | Auth Mode, region, or egress class |
| KS-4 | Formal legal notice / cease-and-desist / ToS enforcement communication naming the product or method | Legal process | Global or named surface |
| KS-5 | Critical protocol break where continued operation requires new reverse engineering of anti-bot or private APIs | Forces higher reverse-eng risk | Web Adapter |
| KS-6 | Credential class invalidated (e.g. cookie family revoked; OAuth client disallowed) | Cannot operate without unsafe workarounds | Auth Mode |

### 8.2 Feature-gate triggers (keep code path but block default enablement)

| ID | Trigger | Gate behavior |
|---|---|---|
| FG-1 | Auth Mode remains high-tension (programmatic extract / bots / reverse eng) but owner wants lab access | Default **off**; enable only via explicit operator + Tenant acknowledgements (details #7) |
| FG-2 | Only OAuth/CLI Access is within provisional envelope; Web Access still research-grade | Ship OAuth Adapter gated separately from Web Adapter |
| FG-3 | Commercial plan of Tenant (consumer vs business Provider contract) unknown | Gate features that assume Enterprise/API rights |
| FG-4 | Dual-regime identity (e.g. Grok via X) incomplete | Gate until issuer/surface mapping verified |
| FG-5 | Challenge rate above internal SLO but below kill threshold | Auto-cooldown, reduce concurrency, disable new connections |

### 8.3 Re-research triggers (refresh this evidence base)

| ID | Trigger | Action |
|---|---|---|
| RR-1 | Any Rank A/B policy effective-date change for OpenAI, Google, or xAI | Re-read changed sections; diff automation/credential/resale clauses |
| RR-2 | New official CLI/OAuth product terms for Codex, Antigravity, or Grok xAI | Add product-specific annex; possibly lower/higher risk than parent family |
| RR-3 | Provider launches first-party API that cannibalizes a Web Access capability | Reassess necessity of Web Adapter |
| RR-4 | Material enforcement campaign in industry (mass bans) | Update challenge/ban annex with operational signals (still not legal proof) |
| RR-5 | Counsel opinion or insurer questionnaire requires jurisdiction-specific analysis | Escalate beyond this research note |
| RR-6 | `#3`–`#5` discover that an Auth Mode depends on a different upstream host/contract than assumed here | Patch family section |

**Suggested review cadence:** at least on every major Provider ToS revision, and no less than quarterly while any Web Access Auth Mode is enabled in any environment.

---

## 9. Explicit non-decisions left for #7

Issue #2 **must not** decide the following (owned by #7 after #2–#5):

1. Whether each of the six Auth Modes is `allowed` / `prohibited` / `experimental` / `gated`.
2. Whether PixelPlus accepts residual ToS/account-ban risk for any Web Access mode.
3. Operator obligations, Tenant acknowledgements, and in-product risk disclosures.
4. Security impact acceptance for holding browser cookies vs OAuth refresh tokens.
5. Kill criteria thresholds (numeric challenge rates, ban cluster definitions) as binding product policy.
6. Whether commercial Gateway fees are structured to avoid “resale/lease/time-sharing” characterizations.
7. Whether multi-account-per-Tenant is permitted operationally.
8. Regional launch restrictions based on enforceability.

This document supplies **evidence and triggers only**.

---

## 10. Unverified gaps (do not invent product promises)

| ID | Gap | Why it matters | Suggested follow-up |
|---|---|---|---|
| G-1 | Full text of OpenAI Help Center “Account Sharing Policy” not reliably extracted this check (page heavily gated by cookie UI in fetch tooling) | May add operational detail beyond ToU non-sharing sentence | Manual browser capture; attach annex |
| G-2 | Product-specific **Antigravity** terms page (if any) distinct from Gemini API / Google APIs ToS | Auth Mode naming in `CONTEXT.md` may not map 1:1 to a public legal doc title | #4 research + Google product docs |
| G-3 | Endpoint-level robots.txt / machine-readable rules for `gemini.google.com` app APIs | Google automation clause is conditioned on those instructions | Fetch robots/meta for relevant hosts during #4 |
| G-4 | Whether BYOA “agent of the account owner” theory mitigates credential-sharing clauses | Core SaaS legal theory | Counsel |
| G-5 | Enforceability of reverse-engineering prohibitions by jurisdiction | Risk residual differs by launch country | Counsel |
| G-6 | Exact ban thresholds / anti-bot scoring (Cloudflare rules, device fingerprint weights) | Operational planning only | Empirical #3–#5; never treat as stable |
| G-7 | Grok SSO token issuer matrix (xAI vs X) and which ToS applies per token type | Dual-regime compliance | #5 |
| G-8 | Whether OpenAI permits third-party SaaS to store ChatGPT OAuth tokens for Codex on behalf of end users outside official client models | Codex OAuth Adapter design | OpenAI developer/app policies + counsel |
| G-9 | Google Workspace / business Gemini terms when cookie belongs to a managed account | Different data and admin regime | Privacy Hub Workspace pointers + Workspace agreement |
| G-10 | Quantitative frequency of consumer account termination for automation | Residual risk sizing | Not available from public primary sources |

---

## 11. Consolidated risk heat map (evidence-based, not acceptance)

Legend: tension relative to a **public multi-tenant SaaS BYOA Gateway** that automates the named Auth Mode. **Not** a ship/no-ship decision.

| Auth Mode | Programmatic/bot tension | Reverse-eng tension | Credential-share tension | Resale/lease/time-share tension | Challenge/ban operational tension | Overall evidence tension |
|---|---|---|---|---|---|---|
| ChatGPT Web Access | **High** (programmatic extract) | **High** | **High** | **High** | **High** (CF/PoW/Turnstile patterns) | **Critical** |
| ChatGPT Codex OAuth | **Medium** (official clients; still rate-limit rules) | **Low–Medium** | **High** | **High** | **Medium** | **High** |
| Gemini Web Cookie | **High** (automated means + protective measures) | **High** | **High** | **Medium–High** | **High** (bot flags) | **Critical** |
| Gemini Antigravity OAuth | **Low–Medium** if documented API/OAuth only | **Low–Medium** | **Medium** | **Medium** | **Medium** | **Medium–High** (with G-2 gap) |
| Grok Web SSO | **Critical** (bots/scripts/non-human access named) | **High** | **High** | **High** (scrape/resell + paid violative services) | **High** | **Critical** |
| Grok xAI OAuth | **Low–Medium** on official API | **Low–Medium** | **Medium–High** | **High** (time-sharing/lease) | **Medium** | **Medium–High** |

---

## 12. Technical challenge surfaces from reference repos (non-legal)

Use only to design health/cooldown/kill signals; **not** compliance permission.

| Reference | Relevant Auth Modes | Observed challenge / fragility signals |
|---|---|---|
| `.ref/chatgpt2api` | ChatGPT Web Access; some Codex image paths | Cloudflare interception; FlareSolverr clearance; PoW, Turnstile, Sentinel utilities; account token invalidation; rate-limit account cooling; disclaimer of ban risk |
| `.ref/CLIProxyAPI` | ChatGPT Codex OAuth; Gemini Antigravity OAuth; Grok xAI OAuth | Multi-account load balancing patterns; OAuth login flows; protocol translators — shows industry operates OAuth/CLI bridges, not legal cover |
| `.ref/gemini-web-to-api` | Gemini Web Cookie | Cookie pair `__Secure-1PSID` / `__Secure-1PSIDTS`; refresh interval; explicit ToS non-affiliation warning |
| `.ref/grok2api` | Grok Web SSO; Grok xAI OAuth | Split pools; SSO non-auto-refresh; cooldown and fault switching; credential encryption at rest |

---

## 13. Acceptance criteria checklist (issue #2)

| Criterion | Status | Where satisfied |
|---|---|---|
| Evidence covers each Provider family separately and distinguishes Web Access from OAuth/CLI Access | **Met** | §§4–6 |
| Each conclusion records source, check date, confidence, applicability limits | **Met** | Evidence tables + §2 schema |
| Gaps are `unverified`, not product promises | **Met** | §10 |
| Profile identifies triggers needing feature-gate, kill switch, or re-research | **Met** | §8 |
| Does not decide PixelPlus risk acceptance | **Met** | §1, §9 |
| Does not expand into #3–#5 capability matrices | **Met** | Non-goals |

---

## 14. Source index (checked 2026-07-14)

### OpenAI / ChatGPT

- https://openai.com/terms
- https://openai.com/policies/row-terms-of-use/
- https://openai.com/policies/usage-policies/
- https://openai.com/policies/service-terms/
- https://openai.com/policies/services-agreement/
- https://developers.openai.com/codex/auth.md
- https://help.openai.com/en/articles/11369540
- https://help.openai.com/en/articles/10471989-openai-account-sharing-policy (partial fetch)

### Google / Gemini

- https://policies.google.com/terms
- https://policies.google.com/terms/generative-ai/use-policy
- https://support.google.com/gemini/answer/13594961?hl=en
- https://developers.google.com/terms
- https://ai.google.dev/gemini-api/terms
- https://discuss.ai.google.dev/t/gemini-web-app-restricted-after-false-bot-flag-appeal-ai-studio-still-works/112633 (enforcement anecdote)

### xAI / Grok

- https://x.ai/legal/terms-of-service
- https://x.ai/legal/acceptable-use-policy
- https://x.ai/legal/terms-of-service-enterprise
- https://x.ai/legal
- https://x.ai/legal/faq

### Local technical references (non-legal)

- `.ref/chatgpt2api/`
- `.ref/CLIProxyAPI/`
- `.ref/gemini-web-to-api/`
- `.ref/grok2api/`

---

## 15. Document control

| Field | Value |
|---|---|
| Status | Evidence complete for #2 |
| Authors | Spec research agent for issue #2 |
| Check date | 2026-07-14 |
| Next mandatory refresh | On RR-* trigger or before enabling any Web Access Auth Mode in a shared environment |
| Supersedes | n/a (initial) |
