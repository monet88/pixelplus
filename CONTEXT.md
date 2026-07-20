# InpaintKit Provider Gateway — Domain Glossary

## Tenant

Chủ thể sở hữu Client API Keys và Provider Accounts trong gateway. Dữ liệu và quyền sử dụng của một Tenant không được chia sẻ với Tenant khác.

## Provider

Một dịch vụ AI upstream mà gateway kết nối. Các Provider ban đầu là ChatGPT, Gemini và Grok.

## Provider Account

Một kết nối do đúng một Tenant sở hữu tới một Provider, đại diện cho danh tính và hạn mức sử dụng của người dùng tại Provider đó. Mỗi Provider Account gắn đúng một Auth Mode bất biến và mang lifecycle state quan sát được (`draft`, `pending_validation`, `pending_probe`, `active`, `reauth_required`, `disabled`, `revoked`, `deleted`). Account chỉ **usable** cho routing/execution khi `active`, đã qua required validation + required probe cho `credential_version` hiện tại, Auth Mode còn execution-enabled theo risk envelope, credential chưa bị vault-revoke, và không bị operational health hard-block. Chi tiết journey (create, submit, validate, probe, activation, refresh, reauthentication, disable, revoke, delete), usability gate và remediation nằm tại `docs/spec/provider-account-connection-and-credential-lifecycle.md`.

## Provider Account Health

Đánh giá vận hành của một Provider Account, độc lập với lifecycle state và được biểu diễn bằng hai chiều: **Health State** (`unknown`, `healthy`, `degraded`, `cooling_down`, `challenged`, `expired`, `blocked`) mô tả mức sẵn sàng hiện tại; **Health Reason** mô tả nguyên nhân canonical quan sát được từ account/upstream như chưa probe, probe thành công, tỷ lệ lỗi tăng, Provider rate limit/quota exhaustion, challenge, credential hết hạn/bị từ chối, protocol drift hoặc Provider ban. Đây là vocabulary canonical cho persistence, routing, capability và error mapping; các nhãn single-token cũ từ #9 như `auth_expired`, `rate_limited`, `quota_exhausted` và `provider_banned` chỉ là compatibility aliases phải được normalize theo #17. Tách state khỏi reason cho phép giữ đồng thời recovery semantics và nguyên nhân quan sát được; Tenant/operator lifecycle controls và Auth Mode risk/execution gates giữ authority riêng, không ghi đè health. Health không tự kích hoạt account hoặc vượt qua lifecycle, risk, capability hay Tenant routing policy.

## Scoped Health Condition

Một health state/reason đang có hiệu lực cho scope `account`, `operation` hoặc `model` của đúng một Provider Account. Routing tính effective health theo operation/model của request thay vì lấy một account-wide flag; management summary có thể chiếu condition nghiêm trọng nhất nhưng phải giữ affected scope. Concurrent observations gắn với credential version và condition revision; success cũ hoặc success ngoài scope không được xóa failure mới.

## Provider Account Cooldown

Hạn chế định tuyến tạm thời do Provider rate limit hoặc quota signal, gắn với đúng một Provider Account và có scope `account`, `operation` hoặc `model`. Gateway dùng scope hẹp nhất được upstream evidence xác nhận; nếu không xác định được bucket/phạm vi thì mặc định account-wide để tránh hammering upstream. Cooldown là trạng thái bền vững qua process restart; hết thời hạn chỉ mở một **half-open recovery permit**, không tự chứng minh account khỏe. Thành công chỉ xóa đúng scope đã phục hồi; thất bại gia hạn/escalate hoặc chuyển sang `expired`, `challenged`, `blocked` theo nguyên nhân mới. Cooldown không vượt Tenant boundary, không cho phép fallback ngoài Routing Policy và không thay thế trạng thái hard-block cho lỗi auth/challenge/ban.

## Recovery Probe

Kiểm tra upstream có giới hạn, single-flight và không tạo side effect, dùng để xác nhận một Provider Account hoặc cooldown scope có thể phục hồi. Recovery Probe dùng request Auth-Mode-defined rẻ nhất có thể kiểm chứng identity/entitlement/protocol; mặc định không tạo ảnh, Render Job, prompt người dùng hay Asset. Probe success không tự vượt lifecycle/risk/capability gates và chỉ phục hồi phạm vi mà nó thực sự kiểm chứng.

## Account Drain

Administrative control tạm thời chặn Provider Account hoặc Provider surface nhận selection/new lease step mới nhưng cho work đã bắt đầu đi hết execution, cancellation, residual tracking và accounting contract hiện hành trong một drain window có giới hạn. Drain không phải health failure; hết deadline không tự chứng minh upstream attempt chưa commit, và mở lại phải qua Recovery Probe phù hợp.

## Account Quarantine

Platform/operator hard control cô lập ngay một Provider Account do security incident, suspected credential compromise, Provider ban hoặc protocol corruption. Quarantine độc lập với upstream Health State, chặn new work, có authority/reason/review condition được audit và chỉ privileged operator được release. Nếu credential có thể bị compromise, rotation/revocation phải hoàn tất trước Recovery Probe; release không trực tiếp đánh dấu account khỏe hoặc active.

## Provider Surface Circuit

Operational gate tạm thời ở scope deployment/region + Provider/Auth Mode/surface, được mở khi có correlated failure evidence từ đủ nguồn độc lập để bảo vệ upstream khỏi hammering. Circuit không ghi đè health của mọi Provider Account và không phải Auth Mode risk kill; nó chặn execution mới cho matching surface, chuyển half-open bằng bounded recovery permits và vẫn tuân thủ Tenant Routing Policy cùng retry/commit-safety contracts.

## Provider Credential

Dữ liệu bí mật chứng minh gateway được phép hành động qua một Provider Account. Provider Credential không đồng nghĩa với Provider Account và không được dùng chéo Tenant. Material được vault tách khỏi metadata account; Public API response, log, metric label, trace, audit và operator metadata không được chứa plaintext credential hoặc envelope/bearer material. Credential chỉ được giải mã trên purpose allowlist của cùng-Tenant path (`provider_execution`, `provider_probe`, `provider_refresh` hoặc lifecycle purpose tương ứng) sau khi ownership, lifecycle, version và audit gates pass; `credential_handle` không tự cấp quyền decrypt. `credential_version` (business lifecycle), `crypto_key_version` (envelope protection) và `hash_version` (Client API Key verifier) là ba version độc lập. Rotation/revocation, AAD binding theo Tenant/resource, retention, logical deletion, cryptographic purge, redaction và audit semantics nằm tại `docs/spec/credential-vault-and-sensitive-data-lifecycle.md`; connection journey và Auth Mode class vẫn nằm tại `docs/spec/provider-account-connection-and-credential-lifecycle.md`.

## Credential Vault

Logical boundary lưu encrypted Provider Credential envelope và sensitive-data object theo Tenant/resource binding. Vault không phải shared secret store: ciphertext, wrapped key, handle hay worker identity tự thân không cấp quyền decrypt; audit intent và purpose-bound authorization là điều kiện bắt buộc. Credential Vault cũng định nghĩa boundary mã hóa, key/envelope rotation, fail-closed behavior, redaction, retention và deletion cho prompt, request metadata, Asset bytes, Render Job staging và audit-safe records.

## Sensitive Data Lifecycle

Chính sách phân loại, truy cập, redaction, audit, retention, logical deletion và cryptographic purge cho Secret và Tenant-confidential data. Prompt, request/replay payload, Asset, Render Job staging/result handle và metadata có lifecycle riêng; retention hold có thể trì hoãn physical purge của encrypted evidence nhưng không được khôi phục Public API retrieval, Provider Credential decrypt hoặc execution.

## Retention Hold

Policy record do platform kiểm soát, gắn với `(tenant_id, resource kind/id, data class)` và authority/reason/review window, có thể trì hoãn physical hoặc cryptographic purge của encrypted evidence. Retention Hold không khôi phục Public API retrieval, Asset download, Provider Credential decrypt, execution hoặc storage accounting; Provider Credential hold không bao giờ giữ material ở trạng thái decryptable.

## Client API Key

Credential do gateway cấp cho phần mềm gọi Public API thay mặt một Tenant. Client API Key không phải Provider Credential.

Material dạng bearer `sk-pxp_<public_locator>_<secret>`: secret chỉ hiển thị một lần khi create/rotate, chỉ lưu secret hash (HMAC-SHA-256 với server pepper theo mặc định), không lưu plaintext. `client_api_key_id` là định danh không bí mật dùng cho log/audit. Scope chỉ được thu hẹp quyền trong Tenant sở hữu; revoke có hiệu lực tại Public API boundary với ngân sách cache dương bị chặn (`R-REVOKE-PROP`). Chi tiết lifecycle, hashing, scope, rotation, revocation, rate/concurrency/quota/request-size và abuse controls nằm tại `docs/spec/client-api-key-lifecycle-and-admission-controls.md`.

## BYOA

Bring Your Own Account. Mỗi request chỉ được sử dụng Provider Account thuộc cùng Tenant với Client API Key đã xác thực request đó.

## Public API

Stable client interface hợp nhất inference và management dưới server base `/v1`, được phát hành bằng OpenAPI 3.1.1 `info.version=1.0.0`. Surface gồm models/chat, Assets, durable Render Jobs/image operations, Provider Account lifecycle/credential/OAuth/probe/controls, Capability Snapshot và Routing Policy. Mọi operation dùng Client API Key để derive Security Principal/Tenant server-side; client không gửi `tenant_id`. Tương thích trong `/v1`, deprecation tối thiểu 180 ngày, operation-specific `Idempotency-Key`, replay/commit safety và yêu cầu future contract tests qua real Gateway composition được khóa tại `docs/spec/api-versioning-compatibility-idempotency-contract-testing-policy.md`; artifact stable duy nhất là `contracts/openapi/pixelplus-public-api-v1.yaml`.

## Web Adapter

Module kết nối một Provider qua consumer web surface của Provider và chuyển hành vi đó sang domain chung của gateway.

## Web Access

Cách truy cập Provider qua consumer web surface bằng credential của phiên Web. Web Access là nhóm khái niệm chung; loại credential cụ thể vẫn do Auth Mode xác định, chẳng hạn Web access token, Web cookie hoặc Web SSO token.

## OAuth/CLI Access

Cách truy cập Provider bằng OAuth flow và upstream surface dành cho CLI hoặc ứng dụng được Provider cấp quyền. OAuth/CLI Access có credential lifecycle, quota và capability độc lập với Web Access, kể cả khi hai Provider Account thuộc cùng một danh tính bên ngoài.

## Auth Mode

Phân loại chính xác cách một Provider Account xác thực và truy cập Provider. Auth Mode quyết định loại Provider Credential, credential lifecycle và Adapter có thể xử lý account; nó không đồng nghĩa với Provider.

Các Auth Mode ban đầu là:

- **ChatGPT Web Access**: truy cập ChatGPT consumer web bằng Web access credential.
- **ChatGPT Codex OAuth**: truy cập ChatGPT/Codex surface bằng Codex OAuth credential.
- **Gemini Web Cookie**: truy cập Gemini consumer web bằng bộ Web cookie.
- **Gemini Antigravity OAuth**: truy cập Gemini/Antigravity surface bằng Google OAuth credential.
- **Grok Web SSO**: truy cập Grok consumer web bằng Web SSO credential.
- **Grok xAI OAuth**: truy cập xAI surface bằng xAI OAuth credential.

Trạng thái risk envelope (`allowed` / `prohibited` / `experimental` / `gated`), kill criteria và điều kiện phục hồi của từng Auth Mode nằm tại `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md`. Risk status độc lập với Capability Snapshot.

## Capability Snapshot

Kết quả kiểm chứng capability của một Provider Account tại một thời điểm, bao gồm các thao tác và model account thực sự được phép sử dụng. Capability Snapshot không phải tuyên bố capability tĩnh của Provider hoặc Adapter. Capability Snapshot thuộc Tenant của Provider Account tương ứng và không được dùng ngoài Tenant đó. Snapshot phân loại năm operation chính (`chat`, `chat_streaming`, `image_generation`, `image_edit`, `inpaint`) theo capability status (`verified`/`conditionally_supported`/`unsupported`/`unverified` từ #3–#5), liệt kê model theo slug **quan sát được** (không phải catalog tĩnh), và mang `verified_at`, freshness (`fresh`/`stale`/`invalid`), TTL class cùng evidence/probe provenance. Snapshot gắn với `credential_version` hiện tại; operation `unsupported`/`unverified` hoặc snapshot không `fresh` bị từ chối trước upstream execution (là item 7 của `I-USABLE-GATE`). Snapshot không bao giờ nâng risk status của Auth Mode và không chứa plaintext credential. Chi tiết taxonomy, model availability, freshness, invalidation và enforcement nằm tại `docs/spec/capability-snapshot-and-model-availability-semantics.md`.

## Security Principal

Danh tính bảo mật của một request Public API đã xác thực, gồm `tenant_id` của Tenant sở hữu Client API Key và `client_api_key_id` của Client API Key đó. Mọi quyết định authorization của request phải dựa trên Security Principal này; client không được tự chỉ định Tenant khác.

## Admission Control

Tập kiểm tra có thứ tự tại Public API boundary sau khi xác thực Client API Key và trước khi request được chấp nhận để thực thi (gọi Adapter, enqueue Render Job, hoặc side effect tương đương). Thứ tự chuẩn: scope → request-size → rate → concurrency → quota → accept. Admission rejection (401/403/413/429-class) khác execution/runtime failure từ Provider hoặc worker. Giới hạn theo hierarchy `min(platform, tenant, key_override?)` và cô lập theo Tenant.

## Asset

Đối tượng dữ liệu ảnh bất biến (kind `input`, `mask` hoặc `output`) do đúng một Tenant sở hữu trong gateway. `tenant_id`, `asset_id` và `kind` bất biến; content bytes không đổi sau create (edit/inpaint tạo `output` Asset mới, không mutate input). Asset chỉ được đọc, liệt kê, tải, tham chiếu và xóa **trong** Tenant sở hữu; output không bao giờ vượt Tenant. Cross-Tenant hoặc unknown `asset_id` trả 404-class non-enumerating (không xác nhận tồn tại, không lộ dimension/relationship/tombstone của Asset lạ); job tham chiếu Asset lạ fail trước khi enqueue. Upload validate canonical (format/decodability, pixel dimensions, quan hệ image↔mask) trước upstream; mask không bao giờ bị âm thầm drop để hạ inpaint→image_edit. Mỗi Asset có retention class có giới hạn (`RETAIN-OUTPUT`/`RETAIN-INPUT`/`RETAIN-EPHEMERAL`); sau `expires_at` hoặc delete thì không còn tải được. Delete same-Tenant idempotent trong `ASSET-TOMBSTONE-TTL-CLASS`, sau khi tombstone bị purge thì id trở lại unknown/404. Storage cap theo Tenant dùng `committed + reserved` và atomic reservation cho `L-TENANT-ASSET-BYTES`/`L-TENANT-ASSET-COUNT`; create đồng thời không được vượt cap, delete/expiry giải phóng usage đúng một lần. Chi tiết validation, ownership, retention/expiry/deletion, storage cap và non-enumeration nằm tại `docs/spec/asset-exchange-authorization-and-retention-lifecycle.md`.

## Render Job

Đơn vị công việc bền vững cho image generation, edit hoặc inpaint, do đúng một Tenant sở hữu. Worker chỉ được thực thi Render Job bằng Provider Account, Provider Credential và Asset cùng Tenant với job. Job có state durable `queued` → `running` → `cancel_requested` → terminal `canceled`/`failed`/`completed`; worker claim dùng lease + fencing token, một accepted/idempotently identified job có tối đa một committed/uncertain upstream attempt, và `unknown` commit không bao giờ được coi là non-commit để re-render. `completed` chỉ được công bố sau khi result manifest bất biến đã durable; retrieval/staging/output Asset placement retry dùng manifest + placement key, không chạy lại generation/edit/inpaint. Progress phải phân biệt reported/estimated/unknown; cancellation giữ accounting Tenant+Client API Key đến khi upstream dừng hoặc conservative settlement. Chi tiết state machine, recovery, cancellation, progress, result manifest và output-only retry nằm tại `docs/spec/durable-render-job-and-output-retry-lifecycle.md`.

## Routing Policy

Cấu hình do Tenant khai báo để chọn, ưu tiên hoặc fallback giữa các Provider Account thuộc chính Tenant đó. Routing Policy không được đưa Provider Account của Tenant khác vào candidate set. Candidate set được dựng theo thứ tự lọc ownership → key allowlist → usability (#9 `I-USABLE-GATE`) → risk (#7) → capability (#10 offerable) → routable health, và precedence giải quyết là explicit selection → lease → affinity → policy routing → fallback. Fallback là opt-in fail-closed: chỉ chạy khi Tenant policy khai báo chuỗi fallback có thứ tự, chỉ giữa các account/Auth Mode cùng Tenant được policy cho phép và có capability khớp đúng `op`+model; fallback sau một upstream attempt còn phải thỏa retry-safety contract của operation, và chat cần authoritative proof-of-non-commit thay vì chỉ dựa vào `rate_limited`/`quota_exhausted`. Các trường hợp tuyệt đối không fallback (cross-Tenant, explicit pin, không policy, cross-mode không khai báo, prohibited/experimental/gated-chưa-ack, capability unsupported/model-unavailable/stale, candidate set rỗng, post-attempt không đủ proof) phải fail closed và không enumerate account của Tenant khác. Account lease gắn một đơn vị công việc vào đúng một account trong suốt vòng đời của nó; affinity là preference mềm; cả hai không bao giờ vượt qua usability/capability và bị void ngay khi durable #9 §5.1 items 1–5 của account fail. Chi tiết candidate construction, precedence ladder, affinity/lease, fallback và no-fallback semantics nằm tại `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md`.

## Normative ownership spec

Các invariant sở hữu và authorization chuẩn nằm tại `docs/spec/tenant-ownership-authorization-invariants.md`.

## Normative risk envelope spec

Quyết định risk envelope, acceptable-use boundary, operator obligation và kill criteria cho sáu Auth Mode nằm tại `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md`.

## Normative Client API Key and admission spec

Lifecycle Client API Key (create, one-time display, authenticate, scope, rotate, revoke), hashing/storage, admission controls (rate, concurrency, quota, request-size), abuse controls và ranh giới admission vs execution nằm tại `docs/spec/client-api-key-lifecycle-and-admission-controls.md`.

## Normative Provider Account connection and Provider Credential lifecycle spec

Journey kết nối Provider Account (create, credential submission, validation, probe, activation, refresh, reauthentication, disable, revoke, delete), usability gate, khác biệt lifecycle sáu Auth Mode (Web vs OAuth/CLI không trộn), remediation class và redaction Provider Credential nằm tại `docs/spec/provider-account-connection-and-credential-lifecycle.md`.

## Normative Credential Vault and Sensitive-Data Lifecycle spec

Data classification, storage/encryption boundaries, Tenant/resource-bound envelope semantics, purpose-bound decrypt rights, independent credential/crypto/hash versioning, redaction, retention, logical deletion, cryptographic purge, retention holds, audit semantics và fail-closed behavior nằm tại `docs/spec/credential-vault-and-sensitive-data-lifecycle.md`.

## Normative Capability Snapshot and model availability spec

Capability taxonomy (chat, streaming, image generation, image edit, inpaint), capability status, model availability theo observed slug, cấu trúc Capability Snapshot (provenance, `verified_at`, freshness), TTL/invalidation/refresh triggers (entitlement, credential, protocol drift) và enforcement từ chối operation unsupported/stale trước upstream execution nằm tại `docs/spec/capability-snapshot-and-model-availability-semantics.md`.

## Normative Tenant-scoped routing, fallback, affinity and lease spec

Candidate set construction (ownership → key allowlist → usability → risk → capability → routable health), selection precedence ladder (explicit selection → lease → affinity → policy routing → fallback), account lease và affinity semantics, điều kiện fallback (same-Tenant, Auth Mode được policy cho phép, capability khớp `op`+model, post-attempt thỏa retry safety; chat cần proof-of-non-commit), tập tuyệt đối không fallback cùng failure semantics fail-closed non-enumerating, và Routing Policy logical fields nằm tại `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md`.

## Normative Chat execution and streaming lifecycle spec

Vòng đời chat non-streaming và streaming từ admitted request qua X1 route/select → X2 selected-account gate → X3 credential decrypt → X4 upstream execution → X5 client terminal → X6 accounting terminal. Contract khóa `finish_class`, thứ tự `open`→`delta`*→một terminal duy nhất, heartbeat và honesty real/synthetic streaming; lease là hard, affinity là soft. Cancel/disconnect/timeout phải abort khi có thể; upstream còn chạy có thể chuyển nguyên tử sang residual tracking same-Tenant có giới hạn nhưng vẫn giữ occupancy Tenant+Client API Key gốc và token reservation đến accounting terminal, rồi settle bảo thủ nếu thiếu final usage. Gateway retry và routing fallback cùng yêu cầu proof-of-non-commit; idempotency dùng atomic scoped claim trước upstream để concurrent duplicate chỉ có một executor. Chi tiết nằm tại `docs/spec/chat-execution-and-streaming-lifecycle.md`.

## Normative Asset exchange, authorization and retention lifecycle spec

Canonical validation input image/mask (format/decodability, dimensions, quan hệ image↔mask, không drop mask để hạ inpaint→image_edit), enforcement Tenant ownership cho create/reference/retrieve/list/delete, retention class và expiry/deletion (output ngừng tải đúng lifecycle boundary; delete idempotent trong `ASSET-TOMBSTONE-TTL-CLASS`), atomic per-Tenant storage reservation trên committed+reserved bytes/count (`L-TENANT-ASSET-BYTES`/`L-TENANT-ASSET-COUNT`) tách khỏi per-request upload cap và admission quota, và cross-Tenant/unknown identifier trả 404-class non-enumerating nằm tại `docs/spec/asset-exchange-authorization-and-retention-lifecycle.md`.

## Normative Durable Render Job and output-retry lifecycle spec

Render Job state machine (`queued`, `running`, `cancel_requested`, terminal `canceled`/`failed`/`completed`), atomic idempotent creation, worker lease + fencing/recovery, same-Tenant Provider Account lease, authoritative image-attempt commit boundary (`not_started`/`not_committed`/`committed`/`unknown`), honest progress/cancellation/accounting, immutable result manifest before `completed`, và retrieval/staging/output Asset placement retry dùng stable placement key mà không re-render nằm tại `docs/spec/durable-render-job-and-output-retry-lifecycle.md`.

## Normative Provider Account health, cooldown, and operator controls spec

Provider Account Health State/Reason, Scoped Health Condition, account/operation/model cooldown scope, durable half-open recovery, Recovery Probe, degraded/circuit behavior, Retry-After semantics, Tenant disable, Account Drain, Account Quarantine, Auth Mode kill interaction, audit và recovery conditions được khóa bởi issue #17; normative specification nằm tại `docs/spec/provider-account-health-cooldown-and-operator-controls.md`.

## Normative canonical errors and retry ownership spec

Stable Provider-independent error codes, status/remediation/retryability classes, safe `request_id`/`correlation_id` diagnostics, admission-versus-runtime distinction, shared commit certainty, scoped idempotency outcomes, and the rule that each non-idempotent operation has exactly one retry owner nằm tại `docs/spec/canonical-errors-and-retry-ownership.md`. Retryability signals never bypass ownership, lifecycle, capability, vault, commit, or accounting gates; render retry and output-delivery retry remain separate.

## Stable API versioning, compatibility, idempotency and contract-testing policy (#20)

Stable Public API OpenAPI 3.1.1 package dùng `/v1`, `info.version=1.0.0` và `x-pixelplus-artifact-status: stable` tại `contracts/openapi/pixelplus-public-api-v1.yaml`; normative policy nằm tại `docs/spec/api-versioning-compatibility-idempotency-contract-testing-policy.md`. Contract hợp nhất toàn bộ inference #18 và management #19 thành 26 operation dùng chung `ClientApiKey`, `CanonicalError` và `Remediation`. Trong `/v1`, additive change chỉ compatible tại declared extension points; endpoint/field removal, requiredness/auth/status change, closed-enum expansion hoặc đổi idempotency header requirement đều cần major mới. Deprecation giữ behavior, notice tối thiểu 180 ngày, dùng RFC 9745 `Deprecation`, RFC 8594 `Sunset` và migration `Link`, chỉ remove khi successor đã generally available và major mới phát hành. HTTP replay scope là authenticated Tenant + Client API Key + key, fingerprint gồm operation identity, retention 24 giờ; chat key optional, Asset/Render Job/Provider Account/secret-ingress/OAuth create key required, output retrieval chỉ đọc durable resource và output delivery retry chỉ reuse manifest/placement identity. Future runtime contract tests phải đi qua public HTTP surface + real Gateway composition, chỉ thay controlled implementation tại Adapter, Credential Vault, persistence, job-runtime, clock và ID ports; concrete interfaces/package layout thuộc #21. Validate representation/policy bằng `node scripts/validate-public-api-contract.mjs` và mutation suite `node scripts/test-public-api-contract-validator.mjs`.

## Implementation-ready Provider Gateway specification (#22)

Handoff thống nhất cho implementation nằm tại `docs/spec/provider-gateway-implementation-ready-specification.md`, với manifest machine-readable tại `docs/spec/provider-gateway-implementation-ready-manifest.json`. Package này không thay thế normative specs: stable wire vẫn do `contracts/openapi/pixelplus-public-api-v1.yaml` sở hữu, Pure-Go seams/dependency budget do decisions 0008/0009 sở hữu, còn semantics domain/security/execution do các spec #6-#17 và #20 sở hữu. Manifest khóa sáu Auth Mode, năm capability operation chính, bốn status canonical, decision ledger, bảy vertical implementation slices, deferred item reason/dependency/reopen trigger và yêu cầu implementation nằm ở issue riêng #42. Chạy `node scripts/validate-provider-gateway-implementation-spec.mjs` để kiểm tra completion gate; issue #22 không triển khai Gateway runtime.

## Prototype OpenAI-compatible inference contract (#18)

Non-final Public API inference tracer (issue #18, base `d1c2830`) nằm tại `docs/spec/openai-compatible-inference-contract.md` với OpenAPI 3.1.1 `0.0.0-prototype` (`x-pixelplus-artifact-status: prototype`) tại `contracts/openapi/pixelplus-public-api-v0alpha.yaml` và validator `node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml`. Representation decisions: server base `/v1`; baseline OpenAI paths `/v1/models`, `/v1/chat/completions`, `/v1/images/generations`, `/v1/images/edits` plus PixelPlus `/v1/images/inpaints`, durable `/v1/render-jobs*`, `/v1/assets*`, chat cancel, capability `x_pixelplus.offers`, and output-delivery retry; shared `ClientApiKey` bearer `sk-pxp_<public_locator>_<secret>` on every operation; `/models` exposes only fresh, currently offerable owner-Tenant pairs; chat OpenAI-like request/response plus optional `x_pixelplus` routing (no client `tenant_id`); stream `open` includes actionable server-owned `execution_id`, then `delta*` (heartbeat allowed), then exactly one `completed|failed|canceled`, with no post-terminal data or `[DONE]` second sentinel; disconnect = implicit cancel, cancel not proof upstream stopped, and `commit_status=unknown` forbids fallback/retry; image ops return `202` durable RenderJob (poll/cancel; completed may keep pending outputs; output retry never re-renders), with #13 Asset metadata and conditional output `asset_id`; canonical errors preserve admission-versus-Provider-runtime distinctions and #17 finite retry timing. Đây là retained prototype evidence; stable clients dùng artifact #20. Domain semantics remain owned by #6–#17.

## Prototype Provider Account and Capability management contract (#19)

Non-final management tracer (issue #19, base `8447494`) nằm tại `docs/spec/provider-account-and-capability-management-contract.md` với OpenAPI 3.1.1 `0.0.0-prototype` tại `contracts/openapi/pixelplus-management-api-v0alpha.yaml`; chạy `node scripts/prototype-management-contract.mjs` để validate representation rồi thực thi deterministic cause→effect scenarios. Contract khóa representation cho Provider Account create/list/get/delete, direct credential và direct reauthentication secret boundaries, OAuth start/poll server-side exchange, safe probe, disable/enable, per-account Capability Snapshot read, và Tenant singleton Routing Policy. Mọi operation dùng `ClientApiKey`; Tenant authority được derive server-side; missing scope cùng Tenant là `forbidden`, còn foreign/unknown/deleted id là non-enumerating `resource_not_found` trước vault/Adapter. Store credential chỉ tới `pending_probe`; credential version tăng đơn điệu kể cả khi pending version thất bại; mọi enable theo #17 `I-ACCOUNT-ENABLE-PROBED` phải `disabled → pending_probe → current-version provider_probe → active|reauth_required|non-usable`. Reauthentication chỉ cut over sau pending-version `provider_probe`; disable intent sống qua OAuth authorization/exchange, staging, success, auth failure và retry, nên replacement không thể tự kích hoạt account. Lifecycle, scoped Health và administrative controls là ba chiều độc lập: drain chặn admission/selection mới, còn operation-scoped cooldown chỉ chặn operation tương ứng. Snapshot gắn current `credential_version`, provenance/freshness và đủ `chat|chat_streaming|image_generation|image_edit|inpaint`; existing stale/invalid snapshot vẫn đọc được để inspect nhưng không authorize execution, snapshot chưa tồn tại trả `capability_unverified`, và unsupported inpaint không downgrade thành edit. Routing fallback mặc định off; foreign candidate làm atomic policy rejection; explicit pin không silent fallback hoặc bypass lifecycle/control/health/capability gates. Secret material không xuất hiện trong response/error/example/trace/health/snapshot/routing; delete revoke cả current và pending credential trước khi xóa, còn internal retention hold không restore use/decrypt. Đây là retained prototype evidence; stable clients dùng artifact #20.
