# InpaintKit Provider Gateway — Domain Glossary

## Tenant

Chủ thể sở hữu Client API Keys và Provider Accounts trong gateway. Dữ liệu và quyền sử dụng của một Tenant không được chia sẻ với Tenant khác.

## Provider

Một dịch vụ AI upstream mà gateway kết nối. Các Provider ban đầu là ChatGPT, Gemini và Grok.

## Provider Account

Một kết nối do đúng một Tenant sở hữu tới một Provider, đại diện cho danh tính và hạn mức sử dụng của người dùng tại Provider đó.

## Provider Credential

Dữ liệu bí mật chứng minh gateway được phép hành động qua một Provider Account. Provider Credential không đồng nghĩa với Provider Account và không được dùng chéo Tenant.

## Client API Key

Credential do gateway cấp cho phần mềm gọi Public API thay mặt một Tenant. Client API Key không phải Provider Credential.

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

## Capability Snapshot

Kết quả kiểm chứng capability của một Provider Account tại một thời điểm, bao gồm các thao tác và model account thực sự được phép sử dụng. Capability Snapshot không phải tuyên bố capability tĩnh của Provider hoặc Adapter.
