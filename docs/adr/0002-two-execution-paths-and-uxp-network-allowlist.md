# ADR 0002: Hai Execution Path và UXP network allowlist

- Status: Accepted
- Date: 2026-08-05

## Context

Plugin phục vụ hai loại credential mà user đã có: API key của chính họ, và
account OAuth/Web có sẵn. Hai loại này không thể đi cùng một đường: API key
thuộc về thiết bị của user, còn credential OAuth/Web phải nằm trong Credential
Vault phía server vì lifecycle của nó (refresh, reauthentication, probe, revoke)
là công việc của Gateway.

UXP ràng buộc thêm một điều kiện không có ở web: `requiredPermissions.network.domains`
trong `manifest.json` là allowlist **tĩnh**, khai báo lúc đóng gói
(`.ref/bolt-uxp/uxp.config.ts:146-158`). Plugin không thể mở kết nối tới host
chưa khai báo, nên "user nhập endpoint bất kỳ" không biểu diễn được trừ khi mở
`domains: "all"`.

Ranh giới giữa hai loại credential không phải lựa chọn sản phẩm mà là ràng buộc
kỹ thuật. OAuth authorization-code cần một `redirect_uri` công khai để Provider
redirect tới, và cần giữ `client_secret` ngoài tay người dùng. Panel UXP không
có URL công khai, nên Adobe's `oauth-workflow-sample` giải bằng cách dựng một
server riêng: plugin lấy `requestId`, gọi `uxp.shell.openExternal` để mở browser
ngoài, Provider redirect về `${publicUrl}/callback` của **server**, server đổi
code lấy token, rồi plugin poll `getCredentials`
(`oauth-workflow-sample/index.js:1-19`, `server/index.js:38,48`). Gateway chính
là server mà cơ chế này đòi, và `POST /provider-accounts/{id}/oauth-authorizations`
cộng endpoint poll tương ứng đã có đúng hình dạng đó. Vì vậy mọi Connection dạng
OAuth tất yếu thuộc Gateway Path; "OAuth chạy hoàn toàn trên máy user" là bất khả
trong UXP mà không tự dựng HTTP server cục bộ.

## Decision

Plugin có đúng hai Execution Path, phân nhánh theo loại credential và do user
chọn tường minh:

- **Direct Path** — API key trên thiết bị, Plugin gọi thẳng Provider. Không có
  Tenant, Asset, Render Job hay Client API Key.
- **Gateway Path** — OAuth/Web account, Plugin gọi Public API bằng Client API
  Key, Gateway thực thi trên server.

Hai Path tách bạch tới tầng UI: Compose/Chat biết mình đang đi đường nào và
không giả lập ngữ nghĩa của đường kia. Không có fallback tự động giữa hai Path.

`manifest.json` khai báo allowlist tường minh gồm host của các Provider được hỗ
trợ, host Gateway, cộng `localhost` cho Custom endpoint. Không dùng
`domains: "all"`.

## Consequences

- NOTES.md Bước 6 ("xoá 11 domain provider, chỉ để domain gateway") không còn
  đúng: Direct Path cần chính những domain đó.
- NOTES.md kết luận "`src/providers/*` — **Bỏ** — gateway lo" không còn đúng:
  Direct Path cần lớp provider client trong plugin.
- Custom endpoint chỉ trỏ được tới máy cục bộ. Người cần proxy từ xa phải dùng
  Gateway Path.
- Thêm một Provider mới cho Direct Path là thay đổi `manifest.json`, nên cần
  bản cài mới — không cấu hình nóng được.
- Vertex AI đặt host theo region. Nếu UXP không nhận wildcard thì phải khai báo
  từng region được hỗ trợ, hoặc giới hạn Vertex vào Gateway Path.
- Plugin giữ API key của user trên thiết bị, nên allowlist hẹp là biện pháp
  giảm bề mặt exfiltration chính; đây là lý do từ chối `domains: "all"`.
