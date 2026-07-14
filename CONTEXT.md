# InpaintKit Provider Gateway — Domain Glossary

## Tenant

Chủ thể sở hữu Client API Keys và Provider Accounts trong gateway. Dữ liệu và quyền sử dụng của một Tenant không được chia sẻ với Tenant khác.

## Provider

Một dịch vụ AI upstream mà gateway kết nối. Các Provider ban đầu là ChatGPT, Gemini và Grok.

## Provider Account

Một kết nối do đúng một Tenant sở hữu tới một Provider, đại diện cho danh tính và hạn mức sử dụng của người dùng tại Provider đó. Mỗi Provider Account gắn đúng một Auth Mode bất biến và mang lifecycle state quan sát được (`draft`, `pending_validation`, `pending_probe`, `active`, `reauth_required`, `disabled`, `revoked`, `deleted`). Account chỉ **usable** cho routing/execution khi `active`, đã qua required validation + required probe cho `credential_version` hiện tại, Auth Mode còn execution-enabled theo risk envelope, credential chưa bị vault-revoke, và không bị operational health hard-block. Chi tiết journey (create, submit, validate, probe, activation, refresh, reauthentication, disable, revoke, delete), usability gate và remediation nằm tại `docs/spec/provider-account-connection-and-credential-lifecycle.md`.

## Provider Credential

Dữ liệu bí mật chứng minh gateway được phép hành động qua một Provider Account. Provider Credential không đồng nghĩa với Provider Account và không được dùng chéo Tenant. Material được vault tách khỏi metadata account; Public API response, log, metric label và operator metadata không được chứa plaintext credential. Silent refresh chỉ khi Auth Mode/credential class hỗ trợ; Web Access và OAuth/CLI Access không trộn lifecycle trên cùng một account. Chi tiết class theo sáu Auth Mode, dual-version reauth cutover và redaction nằm tại `docs/spec/provider-account-connection-and-credential-lifecycle.md`.

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

Kết quả kiểm chứng capability của một Provider Account tại một thời điểm, bao gồm các thao tác và model account thực sự được phép sử dụng. Capability Snapshot không phải tuyên bố capability tĩnh của Provider hoặc Adapter. Capability Snapshot thuộc Tenant của Provider Account tương ứng và không được dùng ngoài Tenant đó.

## Security Principal

Danh tính bảo mật của một request Public API đã xác thực, gồm `tenant_id` của Tenant sở hữu Client API Key và `client_api_key_id` của Client API Key đó. Mọi quyết định authorization của request phải dựa trên Security Principal này; client không được tự chỉ định Tenant khác.

## Admission Control

Tập kiểm tra có thứ tự tại Public API boundary sau khi xác thực Client API Key và trước khi request được chấp nhận để thực thi (gọi Adapter, enqueue Render Job, hoặc side effect tương đương). Thứ tự chuẩn: scope → request-size → rate → concurrency → quota → accept. Admission rejection (401/403/413/429-class) khác execution/runtime failure từ Provider hoặc worker. Giới hạn theo hierarchy `min(platform, tenant, key_override?)` và cô lập theo Tenant.

## Asset

Đối tượng dữ liệu ảnh (input, mask hoặc output) do đúng một Tenant sở hữu trong gateway. Asset không được đọc, ghi hoặc tham chiếu chéo Tenant.

## Render Job

Đơn vị công việc bền vững cho image generation, edit hoặc inpaint, do đúng một Tenant sở hữu. Worker chỉ được thực thi Render Job bằng Provider Account, Provider Credential và Asset cùng Tenant với job.

## Routing Policy

Cấu hình do Tenant khai báo để chọn, ưu tiên hoặc fallback giữa các Provider Account thuộc chính Tenant đó. Routing Policy không được đưa Provider Account của Tenant khác vào candidate set.

## Normative ownership spec

Các invariant sở hữu và authorization chuẩn nằm tại `docs/spec/tenant-ownership-authorization-invariants.md`.

## Normative risk envelope spec

Quyết định risk envelope, acceptable-use boundary, operator obligation và kill criteria cho sáu Auth Mode nằm tại `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md`.

## Normative Client API Key and admission spec

Lifecycle Client API Key (create, one-time display, authenticate, scope, rotate, revoke), hashing/storage, admission controls (rate, concurrency, quota, request-size), abuse controls và ranh giới admission vs execution nằm tại `docs/spec/client-api-key-lifecycle-and-admission-controls.md`.

## Normative Provider Account connection and Provider Credential lifecycle spec

Journey kết nối Provider Account (create, credential submission, validation, probe, activation, refresh, reauthentication, disable, revoke, delete), usability gate, khác biệt lifecycle sáu Auth Mode (Web vs OAuth/CLI không trộn), remediation class và redaction Provider Credential nằm tại `docs/spec/provider-account-connection-and-credential-lifecycle.md`.
