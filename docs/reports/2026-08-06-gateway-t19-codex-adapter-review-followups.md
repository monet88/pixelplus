# Báo cáo follow-up — Review Gateway T19 Codex Adapter (#62)

- Ngày: 2026-08-06
- Phạm vi: `apps/gateway`, PR cho issue #62 (gated ChatGPT Codex OAuth Adapter), nhánh `feature/issue-62-gateway-t19-chatgpt-codex-oauth-adapter`
- Loại tài liệu: báo cáo kết luận các finding **chưa xử lý trong turn fix** — là các mục lớn / đã-track sẵn, cần quyết định kiến trúc hoặc issue follow-up riêng
- Nguồn: review Standards/Spec trên PR #62 (11 findings); fix đã land 2 commit `89eb983`, `c60ab52`

## 1. Kết luận điều hành

Review đưa ra **11 findings**. Trong lượt fix đã xử lý và commit **5 mục** (F1 commit certainty, F3 chat operator-gate trước Vault, F4 diff hygiene, F5 duplicated code, F8 render gate trước Vault) — đều có mutation-test chứng minh. Còn lại **6 mục** không được xử lý trong cùng lượt vì một trong hai lý do:

1. Là quyết định **kiến trúc lớn** (đổi Vault/lifecycle boundary, thêm seam test scaffold vào `composition`, hoặc nới ADR 0009) — không nên khoét vào một "fix" một cách liều.
2. Là scope đã **chủ động defer** bởi chính ADR/story packet của nhánh (render adapter = story riêng; probe binding = #111; kill/reopen = risk-envelope machinery ngoài T19).

Cả 6 mục đều là gap thật (đã xác minh trên code), cần mở issue follow-up (theo pattern #96/#97/#111 đã dùng).

## 2. Các finding đã xác minh và chưa xử lý

### 2.1 F2 + F10 (HIGH / P1) — Silent refresh không tuân thủ credential lifecycle

**Hiện trạng (xác minh):**
- `apps/gateway/internal/adapters/chatgptcodex/chat.go:417-492` — `refreshAccessToken` chỉ parse và dùng tạm `access_token`, **bỏ rotated `refresh_token`**.
- Không persist material mới, không tăng `credential_version`, không cutover/probe inheritance, không singleflight theo (tenant_id, provider_account_id), không audit refresh.
- Sau 401, `withResponses` **gửi lại toàn bộ POST** /responses (`chat.go:417-430`) — dùng token mới để re-send cùng exchange.

**Hậu quả:** Provider vô hiệu refresh token cũ sau rotation → Gateway vẫn lưu token cũ → lần refresh kế tiếp thất bại → account bị đẩy sang reauthentication không cần thiết; refresh đồng thời có thể tái sử dụng cùng token.

**Cần cho fix (đổi Vault/lifecycle boundary):** refresh phải là capability do Vault/lifecycle boundary sở hữu, với atomic rotate, versioning, singleflight, audit.

**Cạm D2 (trùng F10):** `chat.go:457-492` chỉ trả access_token, không persist rotated credential — mâu thuẫn lifecycle spec §4.8.

---

### 2.2 F6 (P1) — Chưa có proof qua public seam cho probe/chat/stream của Codex Adapter

**Hiện trạng (xác minh):**
- `apps/gateway/internal/contracttest/fixture.go` — `contracttest.Options` **KHÔNG expose `GatedChatGPTCodexTransport`**, nên production composition không đăng ký Codex Adapter trong contracttest.
- `gated_codex_test.go:48-161` chỉ chứng minh create account + credential submission, **không** chứng minh probe/chat/stream chạy qua HTTP seam.
- Các test tại package `chatgptcodex` là unit test — không phải completion evidence theo issue #62.

**Blocker kiến trúc:** đã thử wire `GatedChatGPTCodexTransport` vào `contracttest.Options` + fixture, nhưng **ADR 0009 cấm `contracttest → adapters`** (có `TestGatewayImportsRespectDependencyDirection` trong `composition` chặn). Cần một trong hai:
- Đặt seam proof ở package **`composition`** (được phép import adapter, vốn là composition root mà issue nêu), hoặc
- **Relax ADR 0009** để contracttest được import adapter transport.

→ Quyết định kiến trúc, cần chủ trương trước khi làm.

---

### 2.3 F7 (P1) — Render + image-edit bị loại khỏi scope trái issue gốc

**Hiện trạng (xác minh):**
- `apps/gateway/internal/composition/gated.go:48-55` **không có RenderAdapter**; render candidate gate để render chạm fail-closed foundation.
- Chưa có request builder + mask conversion + render execution proof; 2 fixture image chỉ được kiểm tra "file tồn tại" tại `boundary_test.go:51-99`.

**Yêu cầu issue #62 (kèm evidence comment về image-edit):**
- `/responses` với tool `image_generation`.
- Mask tại `input_image_mask.image_url`; chuyển mask bằng `alpha_out = 255 - luminance_in` (transparent = edit region, RGBA encoder, không phải in-place transform).
- Kiểm tra pixel ngoài mask không đổi (không chỉ kiểm tra response đến).

**Ghi chú:** ADR 0014 chủ động defer implement RenderAdapter sang story riêng (accept-then-fail cho enabled gated mode). Issue chính không cho phép hạ acceptance về follow-up, nên cần tách story mới cho render/image-edit.

---

### 2.4 F9 (P1) — Probe không bind exact account evidence

**Hiện trạng (xác minh):**
- `chatgptcodex/adapter.go:78-85` — Probe chỉ kiểm tra Auth Mode rồi gọi ambient `/backend-api/me`; không dùng Tenant, account, credential version để bind exchange.
- `chatgptcodex/transport.go:83-112` — transport deployment-wide có thể dùng session account A để chứng minh account B; nil transport chỉ khiến lỗ hổng "chưa live", không làm AC5 hoàn thành.
- **Đã track sẵn trong chính transport.go/ADR 0014/0013 là follow-up #111** (ports change để probe/observe carry per-account credential binding).

---

### 2.5 F11 (P2) — Kill/reopen evidence chưa có proof thực thi

**Hiện trạng (xác minh):**
- AC6 yêu cầu kill recovery dựa trên documented evidence. Nhánh có config bật mode + test từ chối Codex→Web fallback, nhưng chưa có test/enforcement chứng minh một mode đã bị kill chỉ được reopen sau checklist R0–R4/audit evidence.
- Theo `validation.md` AC6: "Kill recovery via documented evidence is the existing §3.5.5 reopen checklist; T19 ships no silent cross-mode fallback path." và `TestCrossModeFallbackIntoTheExperimentalModeStaysRefused`.

**Nhận định:** reopen checklist R0–R4 thuộc risk-envelope machinery (ngoài scope T19) → cần issue follow-up để thêm enforcement test riêng cho gated mode, không phải sửa trong T19.

## 3. Việc đã làm trong lượt fix (để tham chiếu)

| ID | Mức | Fix | Commit |
| --- | --- | --- | --- |
| F1 | HIGH | `classifyFailure` — UNKNOWN sau send boundary khi không có authoritative non-commit proof; NotCommitted chỉ cho nil-transport/pre-send/Provider-refusal; gộp luôn F5 | `89eb983` |
| F3 | HIGH | `candidateRejection` chat gọi `BlocksGated` trước Vault; seam test zero vault.Validate + zero adapter | `c60ab52` |
| F4 | LOW | xóa blank line cuối file, `git diff --check` clean | `c60ab52` |
| F5 | LOW | gộp classifier chat/stream thành `classifyFailure` | `89eb983` |
| F8 | P1 | render candidate gate + `RenderService.gatedProfile` gọi `BlocksGated` trước Vault | `c60ab52` |

Validation: `go build` / `go vet` / `go test ./...` / `go test -race` (4 package) đều PASST trên cả 2 commit; mutation-test cho F1 và F3 đều bắt đúng regression khi revert.

## 4. Kiến nghị hành động tiếp theo

1. Mở **issue follow-up F2/F10** — refresh lifecycle do Vault/lifecycle boundary sở hữu.
2. Quyết định **kiến trúc F6** (seam test ở `composition` hay relax ADR 0009), rồi mở issue.
3. Tách **story F7** render + image-edit (mask `alpha_out = 255 - luminance_in`, RGBA encoder, pixel-outside-mask check).
4. Xác nhận **F9** nằm trong #111 hiện hữu.
5. Mở **issue F11** — enforcement test cho kill/reopen R0–R4 của gated mode.

## 5. Trạng thái

- Đã push: `origin/feature/issue-62-gateway-t19-chatgpt-codex-oauth-adapter` (`08589d2..c60ab52`).
- Các issue follow-up trong §4 **chưa** được mở — cần người chủ trì xác nhận hoặc ủy quyền tạo.

## 6. Cập nhật 2026-08-06 (lượt fix thứ hai) — cả 6 mục đã xử lý trong phạm vi T19

Sau khi báo cáo trên được viết, cả 6 mục còn lại đã được xử lý trong 5 commit,
**trong phạm vi trách nhiệm của T19**. Bốn mục (F2/F10, F6, F9, F11) đóng trọn
vẹn; F7 đóng ở mức chứng minh posture accept-then-fail chứ không implement
render. Hai việc vẫn còn mở ngoài phạm vi T19 — story F7 và blocker #111 — xem
§“Còn lại” bên dưới.

| ID | Xử lý | Commit |
| --- | --- | --- |
| F2 + F10 | Rotation chuyển sang credential boundary qua port bổ sung `ports.CredentialRotation`; adapter chỉ chạy grant và trả **toàn bộ** rotated set, từ chối tự rotate khi không có boundary sở hữu | `a361780` |
| F6 | Thêm `composition.GatedCodexResponder` (seam không đặt tên `adapters`) để `contracttest` cấp egress mà **không** nới ADR 0009; 7 proof probe/chat/stream/rotation/nil-stream qua HTTP seam | `85ff0e7` |
| F9 | Xác nhận #111 có track thật (transport.go + ADR 0013/0014); thêm posture test chặn **cả hai** field egress trong production | `41f94e4` |
| F11 | Enforcement test: kill đóng mọi surface trước Vault/Adapter; reopen không tự động (retry / account recovery / routing policy đều bị từ chối), kèm control test chứng minh chỉ config change mới mở lại | `bb0c40a` |
| F7 | Render/image-edit **vẫn defer** theo ADR 0014 (đúng chủ trương), nhưng posture accept-then-fail lần đầu được chứng minh: job được accept rồi fail closed, `failed` với 0 output entry, không bao giờ `completed` | `a2f92f6` |

### Quyết định kiến trúc đã chốt

- **F6**: chọn phương án "giữ ADR 0009, thêm seam trung tính về adapter" thay vì hai lựa chọn ban đầu (đặt test ở `composition` hoặc nới ADR). Lý do: nới ADR sẽ cho mọi contracttest tương lai chạm vào adapter internals; seam chỉ mirror data shape và không thêm hành vi.
- **F2/F10**: chọn "adapter từ chối rotate khi không có boundary" thay vì để adapter tự rotate. Mất session đang sống, nhưng không bao giờ tiêu một single-use refresh token rồi bỏ.
- **F7**: giữ defer. Phần đóng được ngay là chứng minh posture, không phải implement render.

### Validation

- `go build ./...`, `go vet ./...`, `go test ./...` PASS trên mọi commit.
- `go test -race` PASS cho `contracttest`, `composition`, `chatgptcodex`, `vault`, `ports`.
- Mutation-test cho từng mục: revert hành vi cũ đều làm sập đúng test tương ứng (chi tiết trong commit message). Riêng một guard nil-stream em từng thêm bị mutation chứng minh là vô dụng (gán interface nil sang interface khác trong Go vẫn giữ nil) nên đã bỏ, thay bằng proof end-to-end.

### Còn lại

- **F7 story render + image-edit** vẫn cần một story riêng (RenderAdapter + gated render registry + nới render candidate gate + mask `alpha_out = 255 - luminance_in`). Đây là feature mới, không phải gap chưa xử lý.
- **#111** (probe/observe per-account credential binding) vẫn mở và vẫn là điều kiện chặn việc wire một Transport thật.
