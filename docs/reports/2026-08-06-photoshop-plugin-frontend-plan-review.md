# Báo cáo đánh giá kế hoạch frontend Photoshop Plugin

- Ngày đánh giá: 2026-08-06
- Phạm vi: `apps/photoshop-plugin`, issue #95 và các ticket #98-#110
- Nguồn chuẩn liên quan: `CONTEXT.md`, ADR 0002, ADR 0003, `NOTES.md`
- Loại tài liệu: báo cáo kết luận, không thay thế spec hoặc acceptance criteria
- Intake: #33, lane `tiny`, documentation-only

## 1. Kết luận điều hành

Kế hoạch hiện tại **đúng hướng nhưng chưa tối ưu hoàn toàn để giao thẳng cho agent triển khai**.

Phần mạnh nhất là kiến trúc hai Execution Path, chuỗi geometry, Mask Convention và place-back verification. Phần yếu nhất là cách chia ticket, kiến trúc frontend/state chưa được khóa, một số dependency chưa đúng thực tế và chiến lược test còn quá hẹp.

Đánh giá tổng thể: **7/10**.

| Hạng mục | Điểm |
| --- | ---: |
| Kiến trúc hai Execution Path | 9/10 |
| Geometry, mask và place-back | 9/10 |
| Chia ticket và dependency graph | 6/10 |
| Kiến trúc frontend và state | 5.5/10 |
| Chiến lược kiểm thử | 6/10 |
| Khả năng giao thẳng cho agent | 6/10 |

Mục tiêu sau khi chỉnh kế hoạch là đưa mỗi ticket về đúng một risk domain, một proof seam và không để implementation agent phải tự phát minh quyết định sản phẩm.

## 2. Những phần đã được tối ưu tốt

### 2.1 Hai Execution Path được tách đúng

- Direct Path giữ API key trên thiết bị và gọi Provider trực tiếp.
- Gateway Path dùng Client API Key, Provider Account, Credential Vault và durable Render Job.
- Không có fallback tự động giữa hai Path.
- Recent Runs phải phản ánh durability thật của từng Path.
- Model bị ràng buộc vào Connection đã chọn; Connection lỗi thì dừng thay vì âm thầm đổi nơi tính phí.

Đây là quyết định kiến trúc đúng và phải tiếp tục được giữ xuyên qua UI, application state và provider clients.

### 2.2 Geometry được coi là domain logic

Các khái niệm Target Rect, Context Rect, export space, imported bounds và place-back transform đã được tách rõ. Prototype đã bắt được lỗi `100% transform`, trong đó kết quả chỉ đúng khi Provider tình cờ trả cùng kích thước đã gửi.

Chuỗi đúng cần giữ:

1. Đọc Target Rect.
2. Mở rộng thành Context Rect.
3. Export Context Rect.
4. Tạo mask trong pixel space của ảnh export.
5. Gọi Provider qua đúng Execution Path.
6. Đo bounds thật của layer được import.
7. Scale và offset để phủ Context Rect.
8. Mask/crop vùng nhìn thấy về Target Rect và verify trong sai số 1 px.

### 2.3 Mask Convention được đặt đúng boundary

Canonical mask của Plugin là luminance, trắng = sửa, alpha = 255. Phép chuyển convention chỉ thuộc về thành phần trực tiếp nói chuyện với upstream:

- Direct Path: provider client trong Plugin.
- Gateway Path: Provider Adapter trong Gateway.

UI, application orchestration, storage và transport trung gian không được đảo mask. Mỗi phép đảo cần test byte-exact với một canonical fixture cố định.

### 2.4 Direct Path là vertical slice hợp lý để làm trước

Bảy trong tám bước của editing chain không phụ thuộc Gateway. Có thể chứng minh phần lớn giá trị người dùng bằng một PNG local, sau đó nối một Direct Provider cụ thể.

Milestone đầu tiên nên là:

```text
Selection
→ export Context Rect + canonical mask
→ dùng PNG local làm model response
→ place result chính xác thành layer mới
```

Milestone tiếp theo:

```text
Selection + prompt + Direct API key
→ một Provider cụ thể
→ một operation/model cụ thể
→ result layer chính xác, original không đổi
```

## 3. Các thay đổi P0 trước khi implementation

### 3.1 Thêm ticket `P00 - Plugin foundation`

`apps/photoshop-plugin` hiện chỉ có README, nhưng P01 đã yêu cầu panel chạy trong Photoshop. Nếu không có P00, agent làm P01 sẽ phải tự chọn framework, build system, state model, folder structure, OpenAPI generator, packaging và CI.

P00 cần khóa tối thiểu:

```text
apps/photoshop-plugin/
├── src/core/              # domain types, errors, run state
├── src/image/             # geometry, mapping, PNG, masks
├── src/photoshop/         # UXP host boundary
├── src/application/       # use-case orchestration
├── src/connections/       # Connection và Image Engine binding
├── src/clients/direct/    # Direct Provider clients
├── src/clients/gateway/   # stable Public API client
├── src/storage/           # secure và non-secret local data
├── src/ui/                # panel surfaces
└── generated/             # OpenAPI transport types
```

Các lệnh canonical cần được định nghĩa ngay trong P00:

```text
npm run dev | typecheck | test | build | package
npm run generate:api | check:api-drift
```

### 3.2 Sửa acceptance criterion của P01 về snap

Không được snap hoặc thay đổi Target Rect. Target Rect phải giữ nguyên vùng user chọn. Chỉ Context Rect hoặc export dimensions được mở rộng để đáp ứng alignment constraint.

Thứ tự cần khóa rõ:

```text
Target Rect bất biến
→ expand thành Context Rect
→ clamp vào document
→ snap Context Rect outward
→ clamp lại nếu snap vượt document
```

Cũng cần định nghĩa cách phân phối số pixel mở rộng ở hai cạnh để tránh agent tự chọn rounding khác nhau.

### 3.3 Chia P03 thành ba ticket

P03 hiện trộn image bytes, Photoshop host interaction và placement transaction. Nên tách:

1. **P02A - Canonical masks and PNG codecs**
   - Mapping pixel space, 1-bit PNG, RGBA PNG, inversion tests, size ceiling.
2. **P02B - Export Context Rect and capture selection**
   - Selection capture, fallback, resource disposal, CMYK duplicate/convert.
3. **P02C - Place, mask and verify result**
   - Import, measured-bounds transform, corner anchor, offset, layer mask, undo và rollback.

Mỗi ticket khi đó chỉ còn một nhóm lỗi chính và một cách chứng minh rõ ràng.

### 3.4 Làm rõ hai loại bounds trong place-back

P03 đang nói cả “result phủ Context Rect” và “final bounds bằng Target Rect”, dễ khiến agent áp hai assertion lên cùng một trạng thái.

Cần tách invariant:

```text
rawImportedLayerBounds ≈ Context Rect
visibleEditBounds ≈ Target Rect hoặc selection shape thực
```

Luồng đúng là transform ảnh model để phủ Context Rect trước, sau đó dùng layer mask để chỉ hiện phần selection. Không dùng một biến `finalBounds` cho cả hai nghĩa.

### 3.5 Bổ sung blocker thực tế cho Gateway Path

P07 hiện mới phụ thuộc P03 và ticket mask contract #98. Điều này chưa đủ.

Tại thời điểm báo cáo, branch `feature/issue-62-gateway-t19-chatgpt-codex-oauth-adapter` đã có ChatGPT Codex Adapter cho chat, streaming, probe và capability, nhưng `internal/composition/gated.go` ghi rõ chưa có render helper và render candidate vẫn bị chặn fail-closed.

Gateway Path E2E chỉ sẵn sàng khi chứng minh đủ:

- Client API Key được provision.
- OAuth exchange hoạt động.
- Provider Account probe pass và đạt `active`.
- Capability Snapshot fresh, operation/model offerable.
- Render Adapter được compose với credential injection.
- Render Job hoàn tất và output Asset tải được.

P07 nên phụ thuộc một Gateway Image Readiness Gate chứa toàn bộ chuỗi trên.

## 4. Các thay đổi P1 để giảm critical path

### 4.1 Chỉ định một Direct Provider cho MVP

P05 hiện nói chung chung về “Provider client”, khiến agent phải tự chọn Provider, endpoint, model, operation, mask wire format và cancel semantics.

P05 phải khóa:

- Một Provider cụ thể.
- Một credential class cụ thể.
- Một operation cụ thể: image edit hoặc inpaint.
- Một model/model family cụ thể.
- Input/output format và mask conversion.
- Cancel có hỗ trợ hay không.

Chỉ trích xuất abstraction đa Provider sau khi vertical đầu tiên chạy thật. Không xây framework đa Provider trước bằng giả định.

### 4.2 Cho P01 và P04 chạy song song

P04 secure Connection storage không phụ thuộc geometry của P01. Sau P00, nên có ba lane song song:

```text
Geometry lane
Image codec lane
Secure Connection storage lane
```

Điều này rút ngắn đường tới Direct MVP và tránh dùng geometry như blocker giả cho storage/UI settings.

### 4.3 Secure storage phải chịu được secret bị mất

Connection metadata có thể còn trong ordinary local storage trong khi secret trong secure storage không còn. UI và state model cần biểu diễn rõ:

```ts
type ConnectionCredentialState =
  | "present"
  | "missing"
  | "needs_reentry"
  | "invalid";
```

Không được biến tình huống này thành `unknown_error`. Remediation đúng là yêu cầu user nhập lại key, đồng thời giữ các non-secret settings của Connection.

### 4.4 Spike native `FormData` trước manual multipart

Kế hoạch không nên mặc định viết multipart encoder thủ công chỉ vì UXP thiếu `TextEncoder`. Trước tiên cần spike native `FormData`/XHR trên min Photoshop version và bản mới nhất.

Chỉ dùng manual multipart khi native path không đáp ứng file bytes, boundary, cancellation hoặc upload progress. Manual encoder phải là quyết định có bằng chứng, không phải mặc định.

### 4.5 Hoãn ETA, giữ phase và elapsed time

P06 yêu cầu ETA “realistic” quá sớm. Chưa có đủ dữ liệu theo Provider, model, dimensions, queue time và Execution Path.

MVP nên hiển thị:

- Phase hiện tại.
- Elapsed time.
- Provider/model.
- Cancel state.

ETA chỉ mở lại sau khi có telemetry đủ mẫu. ETA giả thường gây mất niềm tin hơn không có ETA.

### 4.6 Không dùng masked-edit probe để chặn toàn bộ OAuth UI

Hai câu hỏi độc lập:

1. User có sign in và tạo Provider Account được không?
2. Auth Mode đó có hỗ trợ masked image edit/inpaint không?

Có thể hoàn thiện browser open, poll authorization, account lifecycle, sign-out và reauthentication UI trước khi inpaint được xác nhận.

Probe mask chỉ nên gate:

```text
Connection offerable for operation=inpaint
```

Nó không nên gate toàn bộ connection journey.

### 4.7 Mở rộng chiến lược test thành ba seam

1. **Pure image seam**
   - Geometry, pixel mapping, PNG, mask conversion, place-back plan.
2. **Application/run state seam**
   - State transition, cancel race, panel close, Direct interruption, Gateway resume, duplicate terminal event.
3. **Client/contract seam**
   - Generated type drift, runtime decoding, error mapping, polling, timeout, abort và multipart.

Photoshop host integration vẫn được kiểm chứng trên Photoshop thật bằng checklist. Không cần dựng fake Photoshop API lớn, nhưng không nên để toàn bộ orchestration chỉ dựa vào manual testing.

### 4.8 Khóa OpenAPI code generation trong P00

Câu “types generated from stable contract” chưa đủ. P00 cần ghi rõ:

- Generator và version.
- Input artifact và output path.
- Types-only hay generated client.
- Generated files có commit hay không.
- CI phát hiện drift thế nào.
- Runtime decoder xử lý malformed/unknown data ra sao.

Boundary khuyến nghị:

```text
OpenAPI artifact
→ generated transport DTOs
→ runtime parser/decoder do Plugin sở hữu
→ application/domain types
```

Generated DTO không được lan trực tiếp vào UI state hoặc image domain.

### 4.9 Thêm compatibility matrix bắt buộc

Tối thiểu phải kiểm:

- Windows và macOS.
- Min supported Photoshop và latest stable.
- Panel docked/floating, light/dark, scale thường/HiDPI.
- RGB và CMYK document.
- Rect, ellipse, lasso và feathered selection.
- Result exact size, 1024² và 2048².
- Network failure, Provider failure, cancellation và cleanup failure.

`minVersion` phải được chọn theo API thực tế đã dùng và proof trên host thật, không theo scaffold.

## 5. Các thay đổi P2

### 5.1 Tách P11 thành ba ticket

P11 hiện gom ba feature không cùng dependency:

- Variants: phụ thuộc run orchestration và place-back.
- Reference images: phụ thuộc Provider capability, request contract và asset strategy.
- Local data management: phụ thuộc storage inventory và retention policy.

Nên tách để từng enhancement có thể được ưu tiên, kiểm thử và ship độc lập.

### 5.2 Tách Product License khỏi Plugin MVP

Issue #95 coi licence server/device activation là out of scope nhưng user stories và một số acceptance criterion vẫn nhắc tới licence.

Cần chọn một trong hai:

1. Bỏ mọi acceptance criterion về licence khỏi P01-P11.
2. Tạo epic riêng cho Product License và device activation.

Không để agent tự tạo placeholder licensing storage rồi phải migrate khi contract thật xuất hiện.

### 5.3 Giữ các feature sau ngoài critical path

- Chat độc lập.
- Outpainting, upscaling, background removal.
- Assistant/structured edit planning.
- Real-time preview.
- Reference-image support đa Provider.
- Commercial licensing và device slots.

## 6. Dependency graph đề xuất

```text
P00 - Foundation, architecture, state types, build/package
│
├── A1 - Target/Context geometry
├── A2 - Mask mapping + PNG codecs
├── B1 - Secure Connection storage
└── C1 - Run state-machine skeleton

A1 + A2
    ↓
A3 - Photoshop export + selection capture
    ↓
A4 - Import/place/mask/undo/cleanup
    ↓
Local-fixture vertical proven

B1 + A4 + one named Direct Provider
    ↓
D1 - Complete Direct Path edit
    ↓
D2 - Phase, elapsed time, cancel, recent runs
```

Gateway lane chạy song song:

```text
#98 canonical mask contract
#99 permitted Auth Modes
OAuth/probe/render implementation + live capability probe
        ↓
Generated Gateway client
        ↓
Gateway Path E2E
        ↓
Resume after panel reopen
```

## 7. Thứ tự triển khai khuyến nghị

### Giai đoạn 1 - Foundation

1. P00 scaffold và architecture boundary.
2. Core types: `Rect`, `Connection`, `ImageEngineBinding`, `Run`, `ExecutionPath`, `PluginError`.
3. Build, test, package và API-generation pipeline.
4. Compatibility matrix và real-host checklist.

### Giai đoạn 2 - Không mạng

1. Geometry.
2. Mask codecs.
3. Export selection/context.
4. Place local fixture result.
5. Undo, cleanup và CMYK.

Milestone: chọn vùng, chọn PNG local và có layer mới nằm đúng vị trí.

### Giai đoạn 3 - Direct MVP

1. Secure API key.
2. Một Provider, một operation và một model được chỉ định.
3. End-to-end Direct edit.
4. Basic cancel và Recent Runs.

Milestone: user thật hoàn thành một edit mà không cần Gateway.

### Giai đoạn 4 - Gateway MVP

1. Generated client.
2. Asset upload, Render Job create/poll/output.
3. Một render adapter đã được chứng minh offerable.
4. Resume after reopen và honest cancellation semantics.

### Giai đoạn 5 - Connection UX

1. Permitted Auth Modes từ Gateway.
2. OAuth sign-in và polling.
3. Reauthentication và sign-out.
4. Model-to-Connection binding.
5. Pause khi Connection unavailable, không silent fallback.

### Giai đoạn 6 - Enhancements

- Variants.
- Reference images.
- Local data management.
- Licensing.
- Chat và các image operations bổ sung.

## 8. Quyết định thực thi

Có thể bắt đầu Plugin ngay, nhưng không nên chạy nguyên dependency graph P01-P11 hiện tại.

Nên bắt đầu ngay:

```text
P00 foundation
A1 geometry
A2 image codecs
B1 secure storage
P02 live OAuth capability probe
```

Nên tạm dừng hoặc viết lại trước khi giao agent:

```text
P03 monolithic
P05 generic unnamed Provider
P06 ETA requirement
P07 Gateway E2E thiếu readiness gate
P10 OAuth bị block sai bởi masked-edit capability
P11 combined enhancements
```

Sau các chỉnh sửa trên, kế hoạch có thể đạt khoảng **9/10 về khả năng giao cho agent**: mỗi ticket có phạm vi hẹp, dependency thật, proof seam rõ và không còn quyết định sản phẩm ẩn.

## 9. Căn cứ chính trong repository

- `CONTEXT.md`: Execution Path, Connection, Target Rect, Context Rect, Mask Convention, Image Engine.
- `docs/adr/0002-two-execution-paths-and-uxp-network-allowlist.md`.
- `docs/adr/0003-canonical-mask-convention.md`.
- `NOTES.md`: research log và các correction ngày 2026-08-06.
- GitHub issue #95: plugin umbrella/spec.
- GitHub issues #98-#110: Gateway dependencies và Plugin tickets P01-P11.
- `apps/gateway/internal/composition/gated.go`: trạng thái composition của gated ChatGPT Codex Adapter tại thời điểm đánh giá.
- `apps/photoshop-plugin/README.md`: Plugin chưa có implementation.

## 10. Giới hạn của báo cáo

- Đây là snapshot tại ngày 2026-08-06.
- Báo cáo không sửa issue, acceptance criteria, ADR, OpenAPI hoặc implementation.
- Các blocker Gateway phải được kiểm lại sau khi branch hiện tại merge hoặc composition thay đổi.
- Khi chuyển kết luận thành thay đổi ticket/spec, cần tạo một change request riêng và chạy lại Harness intake theo phạm vi mới.

## 11. Audit issue hiện tại và đề xuất sửa

- Ngày kiểm tra: 2026-08-06
- Harness intake cho lần cập nhật này: #34, lane `tiny`
- Issue đã đối chiếu: #95, #98-#110, cùng các dependency #47, #62 và #111
- Branch được quan sát: `feature/issue-62-gateway-t19-chatgpt-codex-oauth-adapter`
- Commit HEAD khi kiểm tra: `08589d2`

Toàn bộ #95 và #98-#110 đang mở và mang nhãn `ready-for-agent`. Tuy nhiên, sau khi so body issue với repository hiện tại, chỉ #98 đủ hẹp và đủ quyết định để giữ nguyên trạng thái đó. Nhiều issue còn bắt agent tự chọn kiến trúc, wire contract, Provider hoặc dependency chưa tồn tại.

Các dòng `Blocked by` hiện chỉ là nội dung Markdown. GitHub API báo `blocked_by=0` và `blocking=0` cho toàn bộ #100-#110, nên dependency chưa được biểu diễn bằng quan hệ issue native và có thể không được scheduler tự động tôn trọng.

### 11.1 Ma trận kết luận

| Nhóm | Issue |
| --- | --- |
| Cần viết lại hoặc tách trước khi triển khai | #95, #99, #100, #101, #102, #104, #106, #109, #110 |
| Cần chỉnh có mục tiêu | #103, #105, #107, #108 |
| Có thể giữ nguyên | #98 |
| Dependency Gateway cần đồng bộ | #62, #111 |
| Generic OAuth journey đã có nhưng chưa đủ cho Provider thật | #47 |

Đề xuất bỏ tạm nhãn `ready-for-agent` khỏi nhóm cần viết lại hoặc tách. Chỉ gắn lại sau khi quyết định sản phẩm, dependency và proof seam đã khóa.
### 11.2 Issue #95 - umbrella Plugin

**Kết luận: cần sửa lớn.**

Các thay đổi nên áp dụng:

1. Thêm module và ticket `P00 - Plugin foundation` trước P01.
2. Đổi câu "Selection bounds are snapped" thành quy tắc bất biến: Target Rect giữ nguyên; chỉ Context Rect hoặc export dimensions được snap.
3. Ghi rõ hai trạng thái place-back: raw imported layer phải phủ Context Rect, còn visible edit sau layer mask phải khớp Target Rect hoặc selection shape.
4. Thay "one automated test seam" bằng ba seam: pure image, application/run state và client/contract.
5. Thay yêu cầu ETA ngay từ MVP bằng phase + elapsed time; ETA chỉ mở khi có đủ telemetry.
6. Ghi SecureStorage là cache có thể mất, không phải nguồn bền vững duy nhất; Connection phải có trạng thái `needs_reentry`.
7. Tách licensing/device activation hoàn toàn khỏi scope Plugin MVP hoặc tạo epic riêng.
8. Chốt OpenAPI generator, output path, drift check và runtime decoding boundary.
9. Bổ sung compatibility matrix cho Windows/macOS, Photoshop min/latest, UI backend mới, theme, panel mode, RGB/CMYK và selection types.
10. Thay phần Ordering bằng dependency graph mới, trong đó Direct Path chạy trước và Gateway/OAuth chạy theo readiness gate riêng.

Ngoài ra, mục `Unresolved` về wildcard UXP nên được thu hẹp thành việc xác minh hostname Vertex thực tế có khớp wildcard subdomain an toàn hay không, thay vì giữ câu hỏi chung rằng UXP có hỗ trợ wildcard hay không.
### 11.3 Issue #98 - canonical Mask Convention

**Kết luận: giữ nguyên.**

Issue đã có phạm vi hẹp, authority rõ, proof seam đúng và acceptance criteria đủ cụ thể. Nó không nên hấp thụ việc từ chối mask non-PNG vì đó là thay đổi validation behavior khác với khóa contract fact.

Tuy nhiên, audit không tìm thấy issue Gateway riêng cho việc `kind=mask` từ chối JPEG/WebP dù #98 và #95 đều nói công việc này được theo dõi riêng. Nên tạo một ticket hardening mới với contract test `mask non-PNG -> invalid_mask`, trong khi `kind=input` vẫn giữ PNG/JPEG/WebP.

### 11.4 Issue #99 - permitted Auth Modes

**Kết luận: cần viết lại trước khi giao agent.**

Issue yêu cầu thêm một Tenant-scoped read nhưng chưa khóa:

- HTTP method và path chính xác.
- Response schema và closed/open enum policy.
- Reason/remediation khi Tenant chưa có Provider Account.
- Scope bắt buộc và non-enumeration behavior.
- Cache/freshness semantics phía client.
- Runtime decoder và unknown-value behavior của Plugin.

Đây là public contract change, nên agent không được tự phát minh wire shape. Hoặc #99 phải khóa representation trước, hoặc tách thành một contract-design ticket rồi mới có implementation ticket. Nhãn `ready-for-agent` nên được bỏ cho tới khi endpoint và schema đã cụ thể.
### 11.5 Issue #100 - P01 geometry

**Kết luận: cần viết lại và thêm blocker P00.**

Các sửa đổi cụ thể:

- Tách việc dựng panel/build/manifest/network allowlist sang P00.
- Giữ P01 chỉ cho Target Rect, Context Rect và pure geometry.
- Thay acceptance criterion "Selection bounds are snapped" bằng "Target Rect không đổi; Context Rect được snap outward".
- Khóa thứ tự `expand -> clamp -> snap -> clamp lại` và quy tắc phân phối pixel dư.
- Bổ sung test chứng minh snap không làm đổi Target Rect.
- Bổ sung edge/corner cases sau cả hai lần clamp.
- Đổi `Blocked by: None` thành `Blocked by: P00`.

Network allowlist không nên nằm trong geometry ticket vì nó là foundation/security packaging concern.

### 11.6 Issue #101 - P02 live OAuth capability probe

**Kết luận: nội dung nghiên cứu hợp lý nhưng issue cần đổi ownership và dependency.**

Nên sửa:

- Đổi từ `Plugin P02` thành research/capability ticket của Auth Mode cụ thể.
- Ghi chính xác surface và Auth Mode được probe, không dùng cụm "first chosen OAuth surface".
- Giữ yêu cầu phân biệt mask được tôn trọng với mask bị bỏ qua.
- Kết quả chỉ gate offerability của `inpaint`/`image_edit`, không gate OAuth sign-in nói chung.
- Không để #101 tiếp tục block #109.
- Chỉ giữ `ready-for-agent` khi account được ủy quyền, surface cụ thể và protocol probe procedure đã được xác định.

Nếu mục tiêu là ChatGPT Codex OAuth, ticket phải nói rõ token/surface nào được dùng và nơi cập nhật Capability Baseline sau probe.
### 11.7 Issue #102 - P03 export và place-back

**Kết luận: phải tách trước khi triển khai.**

Issue hiện chứa ba risk domain lớn:

1. Pure bytes/geometry: mask mapping, 1-bit PNG, RGBA PNG, compression và inversion.
2. Photoshop capture: export Context Rect, đọc selection, fallback, dispose và CMYK duplicate/convert.
3. Photoshop placement: import, transform, layer mask, verify, undo và rollback.

Đề xuất thay #102 bằng ba ticket:

- `P02A - Encode and transform canonical masks`.
- `P02B - Export Context Rect and capture selection`.
- `P02C - Place, mask and verify result`.

Các acceptance criteria cần sửa thêm:

- `rawImportedLayerBounds` phải khớp Context Rect.
- `visibleEditBounds` sau layer mask mới khớp Target Rect/selection shape.
- Mọi `PhotoshopImageData` phải được `dispose()` trong `finally`.
- Cleanup phải chạy cả khi transform, mask apply hoặc verification thất bại.
- Việc ghi file debug phải là dev-only và không mặc định lưu ảnh người dùng.
- P02C chỉ bắt đầu sau P00, geometry và codec ticket.

Nhãn `ready-for-agent` nên được bỏ khỏi #102 và gắn vào ba ticket nhỏ sau khi tách.
### 11.8 Issue #103 - P04 Direct API key storage

**Kết luận: cần chỉnh dependency và lifecycle.**

Các sửa đổi:

- Bỏ blocker #100; geometry không chặn secure storage.
- Thay blocker bằng P00 foundation.
- Ghi rõ SecureStorage là cache có thể mất khi uninstall hoặc hỏng metadata.
- Thêm state `present | missing | needs_reentry | invalid` cho local credential.
- Khi Connection metadata còn nhưng secret mất, UI phải yêu cầu nhập lại thay vì báo lỗi chung.
- Tách secret deletion khỏi xóa non-secret settings và khỏi Product License.
- Kiểm tra host allowlist display từ cùng nguồn cấu hình tạo manifest, tránh hardcode lần hai trong UI.

Sau các sửa này, #103 có thể chạy song song với geometry và image codec.

### 11.9 Issue #104 - P05 Direct Path vertical slice

**Kết luận: cần viết lại để khóa một Provider cụ thể.**

Issue hiện chưa xác định Provider, model, operation, endpoint hoặc cancel semantics. Agent sẽ phải tự chọn sản phẩm và wire contract.

Nên khóa tối thiểu:

- Một Provider/Auth surface cụ thể.
- Một model hoặc model family cụ thể.
- Một operation cụ thể: image edit hoặc inpaint.
- Input/output wire shape và target mask convention.
- Error classes, timeout và cancel behavior.
- Kích thước/format output dự kiến.

Không nên bắt buộc manual multipart ngay. Trước hết tạo spike native `FormData`/XHR trên Photoshop min và latest; chỉ dùng handwritten multipart nếu native path không đạt. Nếu phải viết tay, test boundary, UTF-8, binary bytes, abort và memory copies.

Nhãn `ready-for-agent` chỉ nên gắn lại khi Provider đầu tiên được nêu tên rõ ràng.
### 11.10 Issue #105 - P06 progress, cancel và recent runs

**Kết luận: cần chỉnh có mục tiêu.**

Sửa yêu cầu MVP từ "realistic time estimate" thành:

- Phase hiện tại.
- Elapsed time.
- Provider/model và Execution Path.
- Cancel state và terminal honesty.

ETA chỉ được mở khi có đủ observed samples theo Provider, model, operation, image dimensions và Path. Không dùng fixed guess.

Bổ sung automated application/run-state seam với fake clock và fake transport cho các transition:

```text
idle -> preparing -> sending -> waiting -> retrieving -> placing -> terminal
```

Test phải bao phủ cancel race, duplicate terminal, panel close, Direct interrupted và error remediation. Recent prompts có thể giữ trong ticket này, nhưng không được kéo theo reference-image hoặc local-data scope.

### 11.11 Issue #106 - P07 Gateway Path E2E

**Kết luận: cần viết lại blocker và completion gate.**

Blocker hiện tại chỉ có #102 và #98, trong khi production composition vẫn fail-closed ở nhiều lớp. P07 chỉ được coi là runnable khi một Gateway Image Readiness Gate chứng minh đầy đủ:

1. Có PrincipalStore/Client API Key provisioning thật.
2. Có Provider-specific OAuth exchange hoặc credential path thật.
3. Probe và Capability được bind với đúng credential của account; #111 phải được giải quyết hoặc chứng minh không áp dụng.
4. Provider Account đạt `active` và Capability Snapshot còn fresh cho đúng operation + model.
5. Có real `RenderAdapter` cho Auth Mode đã chọn và render candidate gate được mở có chủ ý.
6. Render credential authorizer, prompt store, durable jobs/staging và digester đạt readiness.
7. Asset upload, Render Job poll và output content chạy qua production composition.
8. #98 đã khóa Mask Convention và ticket riêng đã siết mask PNG nếu hardening được yêu cầu trước E2E.

P07 cũng phải khóa generator cụ thể, generated output path, drift check và runtime response decoder. Nhãn `ready-for-agent` nên bỏ cho tới khi readiness gate có issue dependency cụ thể.
### 11.12 Issue #107 - P08 resume Gateway run

**Kết luận: giữ hướng, bổ sung contract local state.**

Nên thêm:

- Danh sách metadata non-secret được lưu local: run id, job id, Path, created time, document fingerprint và placement state.
- Không lưu prompt hoặc image bytes mặc định nếu retention policy chưa cho phép.
- Quy tắc khi job unknown, expired, deleted hoặc output đã hết retention.
- Placement idempotency để crash sau khi đặt layer nhưng trước khi ghi local state không tạo layer trùng khi mở lại.
- Trạng thái `retrieved_not_placed`, `placed`, `placement_failed` tách khỏi Render Job terminal state.
- Retry placement không được tạo Render Job mới.

Issue có thể giữ nguyên dependency #105/#106 sau khi hai issue đó được sửa.

### 11.13 Issue #108 - P09 permitted Connections và Image Engine binding

**Kết luận: cần chỉnh dependency và chia proof seam.**

Phần domain `Connection` và `ImageEngineBinding` nên được định nghĩa từ P00, không chờ cả Direct và Gateway E2E hoàn thành.

Đề xuất tách logic thành hai lớp:

1. Catalog/onboarding: render permitted Auth Modes từ #99 và giải thích empty state.
2. Binding/runtime: bind model vào đúng Connection, pause khi unavailable và tuyệt đối không fallback.

Có thể phát triển UI/domain bằng controlled data sau P00 và #99; #104/#106 chỉ cần cho live E2E verification, không nên block toàn bộ implementation. Acceptance criteria phải phân biệt `configured`, `usable`, `offerable` và `temporarily unavailable`.

Nếu giữ một issue, cần ghi rõ server quyết định permitted Auth Modes, còn Plugin quyết định model-to-Connection binding. Không được suy model availability chỉ từ Auth Mode catalog.
### 11.14 Issue #109 - P10 OAuth sign-in

**Kết luận: cần sửa dependency lớn.**

#101 không được block sign-in. Masked-edit capability chỉ quyết định operation nào được offer sau khi account đã kết nối.

#62 cũng chưa đủ làm blocker duy nhất:

- Generic OAuth journey #47 đã đóng, nhưng production vẫn dùng `FailClosedOAuthExchangeAdapter` khi không inject Provider-specific exchange.
- Branch #62 hiện register ChatGPT Codex cho chat, stream, probe và capability; `gated.go` ghi rõ không có render helper.
- Probe/capability account binding còn issue #111.
- Production `ProductionDependencies()` không provision PrincipalStore, Vault, OAuth exchange hoặc Provider transport.

Blocker mới nên gồm:

1. P00 và phần Connection UI cần thiết, không nhất thiết toàn bộ #108 live E2E.
2. Provider-specific OAuth exchange implementation cho Auth Mode đầu tiên.
3. #111 hoặc resolution tương đương cho exact-account probe/capability.
4. Provider Account lifecycle đạt active qua protected probe.
5. #101 chỉ là blocker cho việc bật `inpaint`, không phải blocker cho browser sign-in.

Nên tách acceptance thành `connect`, `poll`, `reauthenticate`, `disconnect local` và `delete/revoke server-side`. Câu "signing out removes the Connection from this device" chưa nói rõ có disable/revoke Provider Account trên Gateway hay chỉ xóa local binding; quyết định này phải được khóa.
### 11.15 Issue #110 - P11 variants, references và local data

**Kết luận: phải tách thành ba issue.**

Ba phần không cùng dependency hoặc risk:

- `Variants`: phụ thuộc run orchestration và place-back.
- `Reference images`: phụ thuộc Provider capability, request contract, asset strategy và per-Path wire behavior.
- `Local data management`: phụ thuộc storage inventory, retention và deletion policy.

Đề xuất tạo:

1. `P11A - Request and place multiple variants`.
2. `P11B - Attach capability-gated reference images`.
3. `P11C - Inspect and clear local plugin data`.

`Reference images` phải có explicit capability fact; Provider không hỗ trợ phải trả unavailable trước send, không silent drop. `Local data management` không được hứa giữ Product License cho tới khi licensing storage contract tồn tại. Hoặc bỏ câu này, hoặc tạo epic Product License riêng.

Nhãn `ready-for-agent` nên bỏ khỏi #110 và gắn lại cho các issue con sau khi tách.

### 11.16 Issue #62 và #111 - dependency Gateway

Issue #62 hiện mô tả proof seam gồm connect, probe, chat, stream và render, đồng thời acceptance criteria nhắc image operations và render commit certainty. Tuy nhiên code trên branch hiện tại chỉ compose chat, stream, probe và capability; comment trong `gated.go` xác nhận chưa có `renderAdapter` helper và render vẫn bị từ chối.

Vì vậy cần chọn một trong hai:

- Hoàn thiện #62 đúng body hiện tại, gồm OAuth exchange/provider credential path, render Adapter, mask conversion và exact-account probe; hoặc
- Thu hẹp #62 thành chat/probe/capability và tạo các issue riêng cho OAuth exchange và render.

Issue #111 là blocker bảo mật cho real probe/capability egress. Cùng một deployment-level Transport không được chứng minh account-bound; probe account B có thể dùng session account A. Gateway Path Plugin không nên dựa vào offerability/account activation cho tới khi invariant exact-account credential được khóa.
### 11.17 Issue #47 - generic OAuth journey

#47 đã đóng và cung cấp application-level start/poll/account lifecycle qua stable HTTP với controlled ports. Nó không đồng nghĩa một Provider-specific OAuth exchange đã được compose trong production.

Khi `Dependencies.OAuth` nil, `runtime.go` vẫn thay bằng `NewFailClosedOAuthExchangeAdapter()`. Vì vậy #47 là nền tảng cần thiết nhưng không phải proof rằng #109 có thể sign in với ChatGPT/xAI thật.

### 11.18 Các issue còn thiếu

Audit phát hiện ít nhất bốn work item chưa được biểu diễn rõ:

1. **Plugin P00 foundation**: stack, folder boundaries, commands, manifest, packaging, state types, OpenAPI generation và compatibility matrix.
2. **Gateway mask ingest hardening**: `kind=mask` chỉ chấp nhận PNG; input giữ PNG/JPEG/WebP.
3. **Provider-specific OAuth exchange**: triển khai `OAuthExchangeAdapter` thật cho Auth Mode đầu tiên.
4. **Image-capable Gateway Adapter/readiness**: real RenderAdapter, credential injection, render gate và live operation/model evidence.

Ngoài ra cần xác định work item cho production Client API Key provisioning/PrincipalStore. `ProductionDependencies()` hiện chỉ cung cấp runtime, clock và ID generator; thiếu Principal sẽ rơi về fail-closed store nên Plugin không thể authenticate Gateway Path trong production.

### 11.19 Dependency graph sau khi sửa

```text
P00 Foundation
├── P01 Geometry
├── P02A Mask codecs
├── P04 Secure Connection storage
└── Run-state core

P01 + P02A
    -> P02B Photoshop export/capture
    -> P02C Place/mask/verify

P02C + P04 + named Direct Provider
    -> Direct Path E2E
    -> Progress/cancel/recent runs
```

Gateway lane chạy độc lập cho tới khi đạt readiness gate, sau đó mới mở Gateway E2E và resume-after-reopen.
### 11.20 Thao tác issue được khuyến nghị

| Issue | Hành động |
| --- | --- |
| #95 | Bỏ `ready-for-agent`, cập nhật umbrella/spec và dependency graph |
| #98 | Giữ nguyên và có thể triển khai |
| #99 | Bỏ `ready-for-agent`, khóa endpoint/response contract trước |
| #100 | Bỏ `ready-for-agent`, thêm P00 blocker và sửa snap semantics |
| #101 | Đổi thành capability research cho Auth Mode cụ thể; bỏ blocker khỏi #109 |
| #102 | Đóng/thay thế bằng ba issue nhỏ hoặc rewrite thành parent |
| #103 | Bỏ blocker #100, thêm P00 và secure-cache lifecycle |
| #104 | Bỏ `ready-for-agent`, chọn Provider/model/operation cụ thể |
| #105 | Sửa ETA thành phase + elapsed; thêm run-state tests |
| #106 | Bỏ `ready-for-agent`, thêm Gateway Image Readiness Gate |
| #107 | Bổ sung local resume/placement idempotency contract |
| #108 | Sửa dependency và tách catalog khỏi binding runtime |
| #109 | Bỏ `ready-for-agent`, sửa OAuth blockers và sign-out semantics |
| #110 | Tách thành variants, reference images và local data |
| #62 | Đồng bộ issue body với implementation thực tế hoặc tạo follow-up |
| #111 | Đặt làm blocker trước real probe/capability egress |

Sau khi sửa body, nên chuyển các dòng `Blocked by` thành GitHub issue dependencies thực hoặc đồng bộ chúng vào Harness story dependency graph. Chỉ văn bản Markdown không đủ bảo đảm scheduler không chạy issue sai thứ tự.

### 11.21 Phạm vi thay đổi của lần audit

Lần audit này chỉ cập nhật report. Không issue GitHub nào đã bị sửa, đổi nhãn, đóng hoặc tạo mới. Các thay đổi issue cần một change request riêng vì chúng làm thay đổi planning authority và dependency của Plugin/Gateway.

## 12. Repository Enforcement Layer Audit

- Ngày bổ sung: 2026-08-06
- Harness intake: #35, lane `tiny`, documentation-only
- Phạm vi: toàn repository, không chỉ Photoshop Plugin
- Mục tiêu: biến các invariant và quy tắc đã viết thành gate máy móc trước khi merge, release hoặc giao việc cho agent
- Bằng chứng được lấy từ một checkout sạch của local HEAD `08589d2`, GitHub metadata và các file hiện có trong repository

Phần này là phụ lục repo-wide. Nó không thay đổi authority của spec, ADR, issue hoặc OpenAPI; nó mô tả lớp enforcement còn thiếu để các authority đó được thực thi nhất quán.

### 12.1 Kết luận điều hành

PixelPlus không thiếu thiết kế. Khoảng trống chính là nhiều quy tắc quan trọng hiện chỉ tồn tại dưới dạng prose, checklist hoặc convention của từng agent.

Lớp enforcement mục tiêu phải bảo đảm:

```text
Source of Truth
    -> deterministic verify command
    -> required CI status checks
    -> protected merge/release rules
    -> reproducible toolchain
    -> auditable planning dependencies
```

Không một PR, agent hoặc local environment nào được phép bỏ qua chuỗi này chỉ vì đã chạy một phần test thủ công.

### 12.2 Evidence snapshot

| Check trên clean local HEAD | Kết quả |
| --- | --- |
| `go -C apps/gateway test ./...` | Pass |
| `go -C apps/gateway vet ./...` | Pass |
| `gofmt -l` trên toàn bộ Gateway | Fail, có 6 file tracked chưa format |
| Stable Public API validator | Pass, 26 operations và 205 examples |
| Public API mutation suite | Pass, khoảng 190.48 giây |
| Implementation-spec validator | Fail do fingerprint của `CONTEXT.md` không khớp |
| OpenAPI stable artifact so với baseline file | Cùng hash tại thời điểm kiểm tra |

Các bằng chứng trên phải được hiểu đúng:

- Clean HEAD build/test được, nhưng chưa đạt repository hygiene vì `gofmt` và authority validator vẫn fail.
- Working tree hiện có thay đổi đang được thực hiện cho Gateway; report không dùng các thay đổi chưa commit đó làm baseline chất lượng.
- GitHub API còn báo một workflow `Docker` ở `.github/workflows/docker.yml`, trong khi local `main` không có workflow tracked. Trạng thái này cần được reconcile thay vì coi như CI đang bảo vệ repository.
- Coverage package-level không nên trở thành global merge threshold ngay. Nhiều hành vi được chứng minh qua contract tests ở package khác, nên gate đúng là public-seam conformance và regression tests có chủ đích.

### 12.3 Enforcement priorities

| Mức | Enforcement |
| --- | --- |
| P0 | Required PR CI foundation |
| P0 | Reconcile implementation-spec authority fingerprint |
| P0 | Correct disposable sandbox state semantics |
| P1 | One canonical verify entrypoint |
| P1 | Pin complete validation toolchain |
| P1 | Track minimal agent authority entrypoint |
| P1 | Make PixelPlus architecture the default architecture authority |
| P1 | Optimize contract mutation execution without weakening mutations |
| P2 | Native issue dependency and readiness governance |
| P2 | Public-repository governance and dependency-update automation |

### 12.4 P0 - Required PR CI foundation

Basic PR CI phải được tách khỏi release ticket #70. Không nên chờ full runtime conformance #67 mới bắt đầu chặn code chưa format, spec drift hoặc test regression.

Workflow tối thiểu:

```text
.github/workflows/ci.yml
├── repository-hygiene
├── gateway-unit-and-contract
├── public-api-contract
├── authority-consistency
└── changed-surface checks
```

Required checks trên mọi pull request:

```bash
gofmt -l apps/gateway

go -C apps/gateway vet ./...
go -C apps/gateway test ./...

node scripts/validate-public-api-contract.mjs
node scripts/validate-provider-gateway-implementation-spec.mjs

git diff --check
```

`gofmt -l` phải fail job khi có output, không chỉ in danh sách. CI không được tự sửa format rồi tiếp tục vì như vậy commit được review khác artifact đã verify.

Workflow phải dùng least-privilege permissions, không có quyền publish trên PR hoặc fork, và third-party Actions phải được pin bằng full commit SHA.Branch protection nên khóa tối thiểu:

- Mọi thay đổi vào `main` đi qua pull request.
- Required status checks phải pass trên commit head mới nhất.
- Approval cũ bị dismiss khi có commit mới làm thay đổi code đã review.
- Không cho force-push hoặc xóa `main`.
- Conversation quan trọng phải được resolve trước merge.
- Admin bypass chỉ dùng cho incident có audit note, không dùng như đường merge thường xuyên.

Issue #70 vẫn sở hữu release/changelog/GHCR/SBOM/provenance. CI foundation nên là ticket riêng, runnable ngay.

### 12.5 P0 - Authority and fingerprint gate

`node scripts/validate-provider-gateway-implementation-spec.mjs` đang fail vì `CONTEXT.md` đã thay đổi sau fingerprint được chấp nhận.

Quy trình reconciliation bắt buộc:

1. Diff `CONTEXT.md` với revision mà fingerprint hiện tại đại diện.
2. Phân loại từng thay đổi là Gateway authority, Plugin vocabulary hoặc editorial-only.
3. Review xem thay đổi có làm lệch implementation-ready handoff hay không.
4. Chỉ sau review mới chạy một lần:

```bash
node scripts/refresh-provider-gateway-implementation-spec-contract.mjs
```

5. Commit source change và refreshed contract trong cùng một reviewed change.
6. Chạy validator lần nữa mà không refresh.
7. CI chỉ validate; CI tuyệt đối không tự refresh fingerprint.

Gate này ngăn việc một agent vô tình “chấp nhận” authority mới chỉ bằng cách regenerate artifact cho xanh.Các authority/drift gate khác:

- Stable OpenAPI compatibility phải dùng immutable base SHA trong CI, không dùng mutable `HEAD` hoặc worktree artifact làm baseline.
- OpenAPI candidate và baseline không được cùng bị sửa để che breaking change.
- Generated Plugin API types sau này phải có `generate:api` và `check:api-drift`; CI regenerate vào temp và fail nếu diff với file committed.
- ADR/spec reference paths trong issue phải tồn tại và không được trỏ tới file historical đã supersede.
- Mọi claim `ready-for-agent` phải có authority, dependency và proof seam có thể máy kiểm tra hoặc review độc lập.

### 12.6 P0 - Sandbox lifecycle enforcement

Sandbox hiện dùng named volume `pixelplus-gateway-state`. `sandbox.sh stop` xóa container nhưng không xóa volume, dù log nói “no state retained”.

Semantics phải được tách rõ:

```text
ephemeral mode    -> stop xóa container + named volume
persistent mode   -> stop giữ volume để test restart/recovery
```

Mặc định phải là ephemeral. Persistent là opt-in có tên rõ, ví dụ:

```bash
./sandbox.sh stop
./sandbox.sh stop --keep-state
```

hoặc:

```bash
./sandbox.sh ephemeral-smoke
./sandbox.sh persistent-restart-test
```

`smoke` phải cleanup volume kể cả khi probe fail. `docker compose down -v` phải là cleanup chuẩn cho disposable profile.Sandbox verification phải chứng minh:

- Ephemeral run không thấy state marker từ run trước.
- Persistent restart vẫn thấy marker và không tạo duplicate work.
- Port chỉ bind loopback.
- Container chạy non-root, read-only rootfs, drop all capabilities và `no-new-privileges`.
- Không mount Docker socket, home, `.ref`, repository root hoặc credential files.
- Provider credential không đi qua CLI args, environment, Compose, image layer hoặc logs.
- Cleanup vẫn chạy khi readiness/probe thất bại.

Docker base images phải pin cả version và digest:

```dockerfile
FROM golang:<version>@sha256:...
FROM alpine:<version>@sha256:...
FROM gcr.io/distroless/static:nonroot@sha256:...
```

`docker build --pull` với tag mutable không đủ để gọi build là reproducible. Digest update nên đi qua review bot hoặc dependency-update PR.

### 12.7 P1 - Canonical verify interface

Repository cần một entrypoint duy nhất thay vì yêu cầu người và agent nhớ nhiều lệnh rải trong docs.

Giao diện đề xuất:

```bash
node scripts/verify-repository.mjs --fast
node scripts/verify-repository.mjs --full
node scripts/verify-repository.mjs --release
```

Có thể expose thêm qua `package.json`, nhưng Node entrypoint phải là implementation duy nhất để Windows, macOS, Linux và CI chạy cùng orchestration.`--fast`:

```text
gofmt gate
go vet
go test
stable Public API validation
implementation-spec authority validation
git diff --check
```

`--full`:

```text
all fast checks
go test -race
Public API mutation suite
retained historical contract validators
architecture/dependency checks
Docker sandbox smoke when Docker is available
```

`--release`:

```text
all full checks
container build and configuration inspection
vulnerability/SBOM/provenance checks
version, changelog and image-tag consistency
```

Mỗi mode phải fail fast nhưng vẫn in summary cuối gồm command, duration, exit code và artifact bị lỗi. Không được silently skip required check chỉ vì dependency không được cài; thiếu tool là failure có remediation rõ.

### 12.8 P1 - Reproducible toolchain

Repo hiện pin Redocly nhưng chưa pin toàn bộ environment mà validator cần. Cần khóa:

- Go version dùng local, CI và Docker.
- Node version và npm version.
- Python version.
- Python `jsonschema` version.
- Docker/BuildKit minimum version nếu release phụ thuộc feature cụ thể.Một phương án cross-platform:

```text
mise.toml hoặc .tool-versions
package.json: packageManager + engines
requirements-validation.txt hoặc uv.lock
```

Install path chuẩn:

```bash
npm ci
python -m pip install -r requirements-validation.txt
go version
node --version
python --version
```

CI phải in version thực tế trước khi verify. Fresh clone proof phải chạy từ tracked files, không dựa vào package được cài sẵn trên máy agent.

`@ai-hero/sandcastle` hiện không được source/script tracked tham chiếu. Nếu không có work item dùng nó ngay, nên xóa khỏi `package.json` và lockfile để giảm supply-chain surface và optional peer warnings.

### 12.9 P1 - Contract validator performance enforcement

Public API mutation suite mất khoảng 190.48 giây vì nhiều case spawn lại validator process và Redocly.

Mục tiêu refactor:

```text
validator library
    -> pure validateDocument(document, options)
CLI wrapper
    -> parse args, load files, print output, exit code
```

Mutation suite import library trực tiếp. Chỉ giữ một số ít black-box CLI cases để chứng minh process boundary.

Không được giảm số mutation hoặc nới assertion chỉ để nhanh hơn. Tối ưu phải giữ nguyên khả năng bắt breaking changes, baseline spoofing và policy drift.CI scheduling đề xuất:

- Fast validator chạy trên mọi PR.
- Full mutation suite chỉ chạy khi đổi OpenAPI, validator, Redocly plugin/config hoặc baseline policy.
- Full mutation suite vẫn chạy định kỳ trên `main` để phát hiện environment drift.
- Race tests chạy trên Gateway code changes và scheduled main verification.
- Docker smoke chỉ chạy khi đổi Gateway runtime, Dockerfile, sandbox controller hoặc Compose profile.

### 12.10 P1 - Agent bootstrap and planning governance

`AGENTS.md` và `CLAUDE.md` đang bị ignore. Fresh clone vì vậy không chắc nhận cùng authority entrypoint và workflow rules.

Mô hình đề xuất:

```text
AGENTS.md          tracked, tối thiểu và ổn định
AGENTS.local.md    ignored, machine-specific
CLAUDE.local.md    ignored, tool-specific override
```

`AGENTS.md` tracked nên chỉ chứa:

- Read-only request và change request distinction.
- Bootstrap/intake requirement.
- Source hierarchy.
- CodeGraph/GitNexus usage rule nếu đó là repository policy bắt buộc.
- Link tới `docs/CONTEXT_RULES.md`, không copy toàn bộ tài liệu dài.

CI có thể kiểm tra tracked block không lệch khỏi canonical Harness template. Local connector IDs, device paths hoặc secrets tuyệt đối không được đưa vào tracked agent file.Planning enforcement:

- `Blocked by` trong Markdown không được coi là dependency executable.
- Dùng GitHub native issue dependencies khi khả dụng; nếu connector không hỗ trợ mutation, mirror dependency vào Harness DB và kiểm tra consistency.
- `ready-for-agent` chỉ hợp lệ khi mọi blocker đóng, authority cụ thể, acceptance criteria testable và proof seam đã khóa.
- Một bot/check định kỳ nên báo issue có `ready-for-agent` nhưng native/Harness blocker còn mở.
- Issue body, Harness story và GitHub state không được cùng là ba nguồn viết tay độc lập cho cùng một trạng thái.

Suggested readiness predicate:

```text
ready_for_agent =
    issue_open
    AND all_blockers_closed
    AND normative_authority_resolves
    AND proof_seam_declared
    AND acceptance_criteria_nonempty
    AND no_unresolved_product_decision
```

### 12.11 P1 - Documentation authority hierarchy

`docs/ARCHITECTURE.md` hiện thiên về generic Harness template, trong khi agent được yêu cầu đọc nó cho structural/high-risk work.

Nên đổi thành:

```text
docs/ARCHITECTURE.md
    -> PixelPlus system architecture thực tế

docs/harness/ARCHITECTURE.md
    -> architecture của generic Harness nếu vẫn cần giữ
```

PixelPlus architecture authority phải mô tả Gateway layers, dependency direction, composition root, Public API seam, two Execution Paths, credential boundaries, durable jobs, persistence status và Plugin boundary.`NOTES.md` nên được chuyển thành historical research artifact và ghi rõ:

```yaml
status: historical-research
canonical: false
superseded_by:
  - CONTEXT.md
  - docs/adr/0002-two-execution-paths-and-uxp-network-allowlist.md
  - docs/adr/0003-canonical-mask-convention.md
  - GitHub issue #95
```

`TODOS.md` không nên tiếp tục mô tả live frontier nếu GitHub/Harness mới là nguồn trạng thái. Chọn một trong hai:

- Generate `TODOS.md` từ Harness/GitHub.
- Đổi thành historical execution playbook và bỏ mọi câu “current state”.

Documentation checks nên bao gồm:

- Broken relative links.
- Missing authority paths được issue/story tham chiếu.
- Duplicate IDs hoặc decision numbers.
- Historical document tự nhận là canonical.
- Closed issue vẫn được ghi như blocker hiện tại.
- File nói một artifact chưa tồn tại dù artifact đã được thêm, hoặc ngược lại.

### 12.12 CI execution matrix

| Job | Trigger | Gate |
| --- | --- | --- |
| Hygiene | Mọi PR | `gofmt`, `git diff --check`, generated drift |
| Gateway fast | Go/runtime changes | `go vet`, `go test` |
| Gateway race | Gateway changes + scheduled main | `go test -race` |
| Public API | Contract/validator changes, fast check mọi PR | stable validator |
| Public API mutations | Contract/validator-specific paths + scheduled main | mutation suite |
| Authority | Mọi PR thay docs/spec/context/manifests | implementation-spec validator |
| Docker sandbox | Docker/runtime/sandbox changes | build, inspect, smoke, cleanup |
| Plugin | Khi P00 tồn tại | typecheck, unit tests, package smoke |
| Release | Protected tag/manual only | full verify + publish controls |Path filters chỉ được dùng để tránh chạy suite đắt trên surface không liên quan. Chúng không được cho phép bỏ qua hygiene, authority hoặc generated-drift checks khi một shared file có thể ảnh hưởng nhiều surface.

### 12.13 Code-quality enforcement without metric gaming

Không đặt global coverage percentage làm merge gate ở giai đoạn này. Các package application/domain có coverage package-level thấp trong lệnh `go test ./... -cover`, nhưng phần lớn behavior được kiểm qua `internal/contracttest` và production composition.

Gate nên ưu tiên:

- Observable behavior qua public HTTP/worker seam.
- Negative tests chứng minh protected effect chưa chạy.
- Mutation tests cho absence/security assertions.
- Race tests cho lifecycle concurrent.
- Architecture tests cho dependency direction.
- Fixture hygiene và secret scans cho Provider adapters.

Coverage được dùng để tìm vùng mù, không để thưởng cho test gọi helper mà không chứng minh behavior.

Các file lớn như `application/render.go`, `credential.go`, `chat_stream.go` và `composition/runtime.go` là refactoring hotspot, nhưng chỉ tách sau khi lifecycle/conformance ổn định. Refactor phải giữ public seam và không đi cùng feature ticket không liên quan.

### 12.14 P2 - Public repository governance

Repository public hiện chưa có governance tối thiểu. Cần quyết định và track:

- `LICENSE` hoặc proprietary rights notice.
- `SECURITY.md` với đường báo credential leak, cross-Tenant issue và disclosure policy.
- `CONTRIBUTING.md` với verify commands và source hierarchy.
- `.github/CODEOWNERS` cho contracts, security/vault, adapters và release workflow.
- Pull request template buộc link issue, authority, proof và risk flags.
- Dependabot hoặc Renovate cho npm, Docker digest và GitHub Actions SHA updates.

Governance không được tự tuyên bố repo là open source nếu chiến lược thương mại chưa chọn license.

### 12.15 Recommended enforcement work items

| ID đề xuất | Work item | Lane | Blocker |
| --- | --- | --- | --- |
| ENF-01 | Add required PR CI foundation | normal | None |
| ENF-02 | Reconcile implementation-spec authority fingerprint | high-risk docs/authority | Human authority review |
| ENF-03 | Correct sandbox ephemeral/persistent lifecycle | normal | None; follow-up to #68 |
| ENF-04 | Add canonical verify entrypoint and pin toolchain | normal | ENF-01 có thể chạy song song |
| ENF-05 | Track minimal `AGENTS.md` and fix architecture authority | normal | Authority direction confirmed |
| ENF-06 | Refactor Public API validator into library + CLI | normal | Existing mutation suite green |
| ENF-07 | Add repository governance and dependency update automation | normal | License strategy decision |

Quan hệ với issue hiện tại:

- #70 giữ release/changelog/GHCR; phần basic PR CI phải được tách ra ENF-01.
- #68 đã đóng; ENF-03 là behavior-correction follow-up, không mở lại toàn bộ sandbox story nếu phần còn lại vẫn đúng.
- #67 không block ENF-01, ENF-02, ENF-03 hoặc ENF-04.
- Plugin P00 sau này phải consume verify/toolchain/CI conventions thay vì tạo pipeline riêng cạnh tranh.

### 12.16 Implementation order

```text
Wave 1
├── ENF-02 authority fingerprint reconciliation
├── gofmt cleanup
└── ENF-01 basic required CI

Wave 2
├── ENF-04 canonical verify + pinned toolchain
├── ENF-03 sandbox lifecycle correction
└── native/Harness dependency consistency check

Wave 3
├── ENF-05 architecture and agent authority cleanup
├── ENF-06 mutation-suite optimization
└── ENF-07 governance
```

P0 work phải hoàn tất trước khi mở rộng mạnh Plugin hoặc merge thêm Provider adapters, vì chúng tăng số surface mà enforcement phải bảo vệ.

### 12.17 Completion criteria

P0 hoàn tất khi:

- Clean clone chạy một lệnh fast verify và pass.
- `main` yêu cầu PR cùng required checks.
- Một intentional formatting mutation làm CI fail.
- Một intentional OpenAPI breaking mutation làm CI fail.
- Một intentional `CONTEXT.md` authority drift làm CI fail mà không tự refresh.
- Implementation-spec validator xanh sau reviewed reconciliation.
- Ephemeral sandbox test chứng minh state không sống qua cleanup.
- Persistent sandbox test chứng minh state chỉ sống khi opt-in.

P1 hoàn tất khi:

- Node, npm, Python, `jsonschema`, Go và Docker assumptions được pin/document.
- Fresh machine không cần dependency “tình cờ có sẵn” để verify.
- `AGENTS.md` tối thiểu được track; machine-specific override vẫn ignored.
- `docs/ARCHITECTURE.md` mô tả PixelPlus, không phải generic Harness.
- Generated API drift có automated check.
- Mutation suite dùng validator library, giữ nguyên mutation coverage và có performance baseline.
- Readiness check phát hiện `ready-for-agent` có blocker hoặc authority thiếu.

P2 hoàn tất khi:

- License strategy, security reporting và code ownership được track.
- Dependency-update automation tạo reviewed PR, không auto-merge security-sensitive changes.
- Release workflow của #70 chỉ publish từ protected, reviewed source.

### 12.18 Enforcement proof standard

Mỗi gate phải có một negative control hoặc mutation chứng minh gate thật sự quan sát đúng failure. Chỉ thấy job xanh không đủ bằng chứng.

Ví dụ:

```text
format gate       -> cố ý làm lệch gofmt và thấy fail
fingerprint gate  -> sửa authority không refresh và thấy fail
sandbox gate      -> giữ marker ngoài opt-in và thấy fail
secret gate       -> seed fixture marker và thấy scanner fail
readiness gate    -> gắn ready label khi blocker mở và thấy check fail
```

Mutation phải được revert sau proof và không được commit vào product branch.

### 12.19 Non-goals

Lớp enforcement này không yêu cầu:

- Thêm Kubernetes hoặc quyết định production topology.
- Chuyển Gateway sang framework hoặc ORM.
- Đặt global coverage threshold để tối ưu con số.
- Refactor mọi file lớn trước conformance.
- Tự động publish release từ pull request.
- Tự động refresh authority fingerprint hoặc compatibility baseline.
- Cho bot tự merge thay đổi security, contract, Provider adapter hoặc Docker digest.

### 12.20 Final enforcement conclusion

Ưu tiên đúng cho PixelPlus là:

```text
1. Authority nhất quán
2. Verify deterministic
3. Merge gate bắt buộc
4. Reproducible environment
5. Planning dependency có thể kiểm tra
6. Release automation sau conformance
```

Khi các P0/P1 gate trên tồn tại, những thiết kế mạnh sẵn có của Gateway và kế hoạch Plugin mới trở thành hành vi repository có thể dự đoán, thay vì phụ thuộc agent nhớ đúng checklist.

### 12.21 Phạm vi thay đổi của lần bổ sung enforcement

Lần bổ sung này chỉ sửa report. Không code, workflow, issue, label, dependency, contract, fingerprint hoặc sandbox behavior nào đã được thay đổi.

Các work item ENF-01 đến ENF-07 là đề xuất planning. Mỗi work item cần change request và Harness intake riêng trước implementation, với ENF-02 được coi là authority-sensitive và không được xử lý như regenerate artifact thông thường.
