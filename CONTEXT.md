# InpaintKit Provider Gateway — Domain Glossary

## Tenant

Chủ thể sở hữu Client API Keys và Provider Accounts trong gateway. Dữ liệu và quyền sử dụng của một Tenant không được chia sẻ với Tenant khác.

## Provider

Một dịch vụ AI upstream mà gateway kết nối. Các Provider ban đầu là ChatGPT, Gemini và Grok.

## Provider Account

Một kết nối do đúng một Tenant sở hữu tới một Provider, đại diện cho danh tính và hạn mức sử dụng của người dùng tại Provider đó. Mỗi Provider Account gắn đúng một Auth Mode bất biến và mang lifecycle state quan sát được (`draft`, `pending_validation`, `pending_probe`, `active`, `reauth_required`, `disabled`, `revoked`, `deleted`). Account chỉ **usable** cho routing/execution khi `active`, đã qua required validation + required probe cho `credential_version` hiện tại, Auth Mode còn execution-enabled theo risk envelope, credential chưa bị vault-revoke, và không bị operational health hard-block. Chi tiết journey (create, submit, validate, probe, activation, refresh, reauthentication, disable, revoke, delete), usability gate và remediation nằm tại `docs/spec/provider-account-connection-and-credential-lifecycle.md`.

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

Interface OpenAI-compatible mà client bên ngoài, bao gồm Photoshop Plugin, dùng để gọi khả năng chat và ảnh của gateway.

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

## Normative canonical errors and retry ownership spec

Stable Provider-independent error codes, status/remediation/retryability classes, safe `request_id`/`correlation_id` diagnostics, admission-versus-runtime distinction, shared commit certainty, scoped idempotency outcomes, and the rule that each non-idempotent operation has exactly one retry owner nằm tại `docs/spec/canonical-errors-and-retry-ownership.md`. Retryability signals never bypass ownership, lifecycle, capability, vault, commit, or accounting gates; render retry and output-delivery retry remain separate.
