# NOTES — Photoshop UXP Plugin Research (2026-08-05)

Ghi chép phiên nghiên cứu chọn hướng xây `apps/photoshop-plugin` cho PixelPlus
Gateway. Tài liệu này là **research log**, không phải spec. Mọi kết luận ở đây
cần được xác nhận lại bằng code thật trước khi đưa vào `docs/spec/`.

Nhánh khi ghi: `feature/issue-60-gateway-t17-cancel-residual`.

---

> ## Đã sửa 2026-08-06 — đọc mục này trước
>
> Phiên `/grill-with-docs` ngày 2026-08-06 phát hiện tài liệu này sai ở bốn chỗ,
> tất cả bắt nguồn từ **một giả định**: rằng gateway là đường duy nhất. Thực tế
> plugin có **hai Execution Path** (ADR 0002).
>
> | Chỗ sai | Mục | Sửa ở |
> |---|---|---|
> | `src/providers/*` "**Bỏ** — gateway lo" | 3 | ghi chú trong mục 3 |
> | `chatgpt2api` "nhận grayscale là chạy đúng" | 4.4 lý do 3 | **4.4b** |
> | "Plugin chỉ biết một convention duy nhất" | 4.4 | **4.4b** |
> | "Xoá 11 domain provider" | 13 Bước 6 | ghi chú trong Bước 6 |
>
> Đã đóng thêm: rủi ro #1 (convention chốt — ADR 0003, xem **4.4c**), rủi ro #3
> (place scale — prototype đo được, xem **14.3b**), rủi ro #4 (giữ fallback mask —
> xem ghi chú 12.2), rủi ro #6 (padding clamp — xem **14.6b**).
>
> Còn mở: rủi ro #8, #9, #10 mới thêm ở mục 14.
>
> Nguồn chuẩn giờ là `CONTEXT.md`, `docs/adr/0002`, `docs/adr/0003` và issue #95.
> Tài liệu này giữ lại làm **primary source** của quá trình nghiên cứu.

---

## 1. Bối cảnh và câu hỏi ban đầu

Câu hỏi mở đầu: nên học/lấy code từ repo mã nguồn mở nào để làm Photoshop plugin
cho backend đã có.

Điểm xuất phát thực tế trong repo:

- `apps/gateway/` — backend Go, đã chạy.
- `apps/photoshop-plugin/` — **chỉ có `README.md`**, nội dung ghi rõ sẽ viết lại
  sau khi Gateway và OpenAPI contract ổn định, và "chỉ port có chọn lọc
  Photoshop host interactions và pure image helpers đã được kiểm chứng từ repo
  legacy".
- `contracts/openapi/pixelplus-public-api-v1.yaml` — contract v1 (nội dung JSON
  dù đuôi `.yaml`).

## 2. Phát hiện quyết định: repo legacy là `monet88/layerflow`

Truy từ chữ "repo legacy" trong `apps/photoshop-plugin/README.md` và
`.wayfinder/issues/wf-0001-provider-gateway-spec-map.md` → repo
`monet88/layerflow`, tên plugin **InpaintKit**, thuộc chính team.

Repo này **đã có plugin UXP gần hoàn chỉnh**: `manifest.json` v5, host PS
`minVersion 24.0.0`, React + Spectrum Web Components + Vite, và thư mục
`src/photoshop/` gồm 5 file:

```
src/photoshop/place-result.ts        4556 B
src/photoshop/selection.ts           3565 B
src/photoshop/batch-play-helpers.ts  2666 B
src/photoshop/export-image.ts        2067 B
src/photoshop/document-utils.ts      1252 B
```

Kết luận: **không cần lấy code từ repo lạ**. Nguồn tốt nhất là repo của chính
team. Các repo public chỉ dùng để đối chiếu.

### Chất lượng code `layerflow` — các bẫy UXP đã xử lý đúng

| Bẫy | Cách xử lý |
|---|---|
| `imaging.getSelection()` rò rỉ bộ nhớ | `dispose()` trong `finally`, có comment cảnh báo |
| `batchPlay` options kiểu CEP | Comment rõ `synchronousExecution`/`modalBehavior` là legacy CEP, không hợp lệ trong UXP |
| Nhiều thao tác → nhiều undo step | `suspendHistory`/`resumeHistory`, rollback `accept=false` khi lỗi |
| Tài liệu CMYK | Duplicate document rồi mới convert, không đụng file gốc |
| `placeEvent` cần path | Dùng `createSessionToken` |
| Race sau `duplicate()` | Activate lại theo `id` thay vì tin `app.activeDocument` |

## 3. Khoảng cách kiến trúc: layerflow → pixelplus

**Cũ (layerflow):** plugin gọi thẳng provider (`api.openai.com`, `fal.run`,
`replicate.com`, `localhost:8000`), đồng bộ, OAuth device-code, token nằm trong
plugin.

**Mới (pixelplus gateway):** một `ClientApiKey`, mọi thứ async.

Endpoint liên quan (trích từ 26 endpoint của contract v1):

```
POST /assets                      multipart: kind + file  → 201 Asset
POST /images/inpaints             {model, prompt, input_asset_id, mask_asset_id} → 202 RenderJob
GET  /render-jobs/{job_id}        → lifecycle_state + execution_phase + progress
POST /render-jobs/{job_id}/cancel
GET  /assets/{asset_id}/content
```

`RenderJob.lifecycle_state`: `queued | running | cancel_requested | canceled | failed | completed`
`RenderJob.execution_phase`: `preflight | upstream | capturing_result | placing_output`

Hệ quả cho việc port:

| Phần trong layerflow | Xử lý |
|---|---|
| `src/photoshop/*` (5 file) | **Port gần như nguyên xi** — phần giá trị nhất |
| `src/providers/*` (~25 KB) | ~~**Bỏ** — gateway lo~~ → **GIỮ**, xem ghi chú dưới |
| `src/auth/*` (~13 KB) | ~~**Bỏ** — thay bằng một API key~~ → **Thu hẹp**, xem ghi chú dưới |
| `src/services/generation-service.ts` | **Viết lại** — đồng bộ → async job |
| `src/components/*` | Port được; progress map theo `execution_phase` |

> **Sửa 2026-08-06 (ADR 0002).** Hai dòng gạch ngang ở trên **sai**, vì bảng này
> giả định gateway là đường duy nhất. Thực tế có **hai Execution Path**:
>
> - **Direct Path** — user dán API key của chính họ, plugin gọi thẳng Provider từ
>   máy user. Chính là `src/providers/*`, nên **phải giữ**.
> - **Gateway Path** — user dùng OAuth/account có sẵn, plugin gọi Public API bằng
>   Client API Key, gateway thực thi trên server.
>
> Ranh giới này **không phải lựa chọn sản phẩm mà là ràng buộc kỹ thuật**: OAuth
> authorization-code cần `redirect_uri` công khai và phải giữ `client_secret`
> ngoài software phân phối. Panel UXP không có cả hai, nên mọi Connection dạng
> OAuth tất yếu thuộc Gateway Path. Bằng chứng: Adobe
> `oauth-workflow-sample/index.js:1-19` + `server/index.js:38,48` phải dựng một
> server riêng để nhận callback rồi cho plugin poll — gateway đóng đúng vai đó.
>
> Nên `src/auth/*` không bị bỏ hẳn: phần device-code bỏ được, nhưng vẫn cần
> `openExternal` + poll authorization. Đó là phần **dễ nhất**, ~19 dòng.

## 4. Convention mask — chỗ sai nhiều nhất trong phiên này

Đây là phần cần đọc kỹ nhất. Trong phiên đã **kết luận sai hai lần** trước khi
đọc đủ code.

### 4.1 Kết luận sai lần 1

Ban đầu kết luận "alpha=0 = sửa" là convention thống nhất, dựa trên ba nguồn:

- `layerflow/src/photoshop/selection.ts`:
  `// Alpha encoding follows the OpenAI convention: 0 = edit this pixel, 255 = preserve.`
- `.ref/chatgpt2api/services/protocol/openai_v1_image_edit.py`:
  `mask 的透明区域（低 alpha）= 需要编辑的区域`
- `docs/spec/research/chatgpt-auth-mode-capability-evidence.md:71`:
  `Mask transparency = edit region, opaque = preserve`

Từ đó đề xuất gateway **chặn mask không có kênh alpha** (JPEG) ở
`InspectImageContent`.

**Sai ở đâu:** đó là convention *nội bộ trong plugin*, không phải convention
*trên dây*. Chặn theo alpha sẽ từ chối chính mask mà plugin gửi lên.

### 4.2 Kết luận sai lần 2

Sau khi đọc `layerflow/src/services/image-processing.ts` và
`OpenLayer/src/photoshop/exactInpaintMask.ts`, cả hai đều ép `alpha = 255` và
mã hoá vùng sửa bằng độ sáng → kết luận "grayscale opaque, trắng = sửa" là
convention chung.

**Sai ở đâu:** vẫn giả định tồn tại **một** convention duy nhất.

### 4.3 Sự thật: có HAI convention, phân nhánh theo provider

Grep toàn bộ `layerflow` cho `invertMaskConvention`:

```
falai-provider.ts:174      const invertedMask = invertMaskConvention(options.maskImage);
falai-provider.ts:246      const invertedMask = invertMaskConvention(options.maskImage);
replicate-provider.ts:103  const invertedMask = invertMaskConvention(options.maskImage);
```

`backend-provider.ts` (đường ChatGPT) **không** gọi hàm này — gửi thẳng mask
alpha thô:

```ts
// backend-provider.ts:237
const maskPng = await rgbaToPngBytes(options.maskImage, options.maskWidth, options.maskHeight);
addFile('mask', 'mask.png', 'image/png', maskPng);
```

Bảng đối chiếu năm nguồn:

| Nguồn | Convention trên dây | Bằng chứng |
|---|---|---|
| `layerflow` → ChatGPT | **alpha**, alpha=0 = sửa | `rgbaToPngBytes` thẳng, không đảo |
| `layerflow` → fal/Replicate | **luminance**, trắng = sửa | `invertMaskConvention`, alpha=255 |
| `OpenAI-PS` | **alpha**, alpha=0 = sửa | `rgba[o+3] = 255 - selected` (app.js:8221) |
| `OpenLayer` | **luminance**, trắng = sửa | `maskValue = luminance`, alpha=255 |
| `Auto-Photoshop-SD` | **luminance**, *tự mâu thuẫn* | hai hàm ngược nhau, xem dưới |

`Auto-Photoshop-SD` (7.3k sao) có hai hàm ngược hẳn nhau trong cùng file
`utility/io.js`, code cũ bị comment ngay phía trên:

```js
transparentToMask:        alpha===0 → 0xffffffff  // trong suốt → TRẮNG (outpaint)
inpaintTransparentToMask: alpha===0 → 0x000000ff  // trong suốt → ĐEN   (inpaint)
```

Đây là bug đảo mask trong repo trưởng thành nhất mảng này. Bài học: convention
mask phải được chốt ở **một** chỗ và có test khoá lại.

### 4.4 Hướng đã chọn cho PixelPlus

Kiến trúc PixelPlus giải bài này tốt hơn mọi repo trên vì **có một tầng gateway
ở giữa** mà chúng không có:

> **Public API chốt MỘT convention. Adapter mỗi provider tự đảo nếu cần.**

Đề xuất: `POST /assets` với `kind=mask` nhận **luminance, trắng = sửa,
alpha=255**, vì:

1. Không mất thông tin — grayscale opaque sống sót qua flatten/xử lý ảnh; mask
   alpha dễ bị `applyAlpha` nhân vào RGB.
2. Kiểm chứng được bằng mắt — mở file thấy ngay vùng trắng. Mask alpha mở ra là
   ô trong suốt, gần như không debug được.
3. ~~`.ref/chatgpt2api` dùng `convert("L")` lấy luminance làm alpha → nhận
   grayscale là chạy đúng.~~ **SAI — xem 4.4b.**
4. Ba trong năm nguồn đã dùng, gồm cả hai provider hiện tại của `layerflow`.

Phần đảo cho ChatGPT — hiện nằm rải trong plugin — **chuyển xuống adapter
ChatGPT trong gateway**. Plugin chỉ biết một convention duy nhất.

~~**Chưa quyết định, cần xác nhận với backend trước khi implement.**~~
**Đã chốt 2026-08-06 — ADR 0003.**

### 4.4b Sửa: lý do 3 nói ngược, và "một convention" chỉ đúng nửa

**Lỗi thứ tư của mục 4** — đúng loại lỗi mục 15 cảnh báo.

`openai_v1_image_edit.py:36-44` thực tế làm:

```python
if mask_img.mode == "RGBA":  alpha = mask_img.split()[3]
elif mask_img.mode == "L":   alpha = mask_img          # luminance TRỞ THÀNH alpha
else:                        alpha = mask_img.convert("L")
img.putalpha(alpha)
```

Và dòng 26 ghi: `mask 的透明区域（低 alpha）= 需要编辑的区域` — **alpha thấp = vùng
sửa**.

Ghép lại: gửi mask grayscale trắng=sửa vào đây → trắng thành alpha **cao** → alpha
cao = **giữ lại**. Nó sẽ sửa đúng vùng **đen**, tức ngược hoàn toàn ý định. Nguồn
này **đòi phải đảo**, không hề chống lưng cho lựa chọn luminance. Bỏ lý do 3; bốn
lý do còn lại vẫn đủ.

**Chỗ sai thứ hai, căn bản hơn:** câu *"Plugin chỉ biết một convention duy nhất"*
chỉ đúng khi gateway là đường **duy nhất**. Trên Direct Path (mục 3, ADR 0002)
plugin nói thẳng với OpenAI (alpha), fal/Replicate (luminance) — nên plugin
**buộc phải** biết nhiều convention. Chính là `invertMaskConvention` mà mục 3 đề
xuất xoá cùng `src/providers/*`.

**Quyết định thật (ADR 0003):** canonical của plugin là **luminance, trắng = sửa,
alpha=255**. Phép đảo thuộc về **thành phần trực tiếp nói với upstream** — Direct
Path là provider client trong plugin, Gateway Path là Provider Adapter trong
gateway. Nguyên tắc: *ai nói chuyện với upstream thì người đó đảo.*

### 4.4c Convention đường ChatGPT — đã chốt là ALPHA, bốn nguồn

Codex OAuth **không** gọi `/images/edits`. Nó gọi `/responses` với tool
`image_generation`, mask là data URL base64 tại `input_image_mask.image_url`:

- `.ref/CLIProxyAPI/internal/runtime/executor/codex_openai_images.go:739,799`
- `.ref/CLIProxyAPI/sdk/api/handlers/openai/openai_images_handlers.go:861,998`
- `.ref/OpenAI-PS/src/app.js:3108`

Ngữ nghĩa của trường đó được `OpenAI-PS/src/app.js:3308` phát biểu tường minh
trong prompt gửi kèm:

> *"Use the `input_image_mask` exactly like ChatGPT image editing: **transparent
> pixels are the editable brush area; non-transparent pixels are protected
> context.**"*

Và bị **khoá bằng test**: `OpenAI-PS/scripts/smoke-plugin.js:625` assert đúng
chuỗi `transparent pixels` — nên đây không phải comment lỗi thời.

Cộng ba nguồn mục 4.3 đã ghi: `layerflow/backend-provider.ts:237` (gửi alpha thô,
không đảo), `OpenAI-PS/app.js:8221` (`rgba[o+3] = 255 - selected`), và
`chatgpt2api` nêu trên. **Bốn nguồn độc lập, cùng kết luận.**

Nên adapter ChatGPT phải làm `alpha_out = 255 - luminance_in`. Lưu ý đây **không
chỉ là nghịch đảo bit** mà còn đổi loại ảnh — PNG grayscale/palette opaque sang
PNG **RGBA** — nên adapter cần encoder RGBA.

Hệ quả kích thước: mask đi trong JSON dạng base64 nên phình ~33%. Mask 2048² dạng
RGBA thô sẽ vượt 20 MB sau base64 → encoder 1-bit palette (12.1) **càng** quan
trọng. Nhưng chỗ tiết kiệm là trên đường **plugin → gateway**, không phải
gateway → OpenAI.

### 4.5 Trạng thái validate hiện tại của gateway

`apps/gateway/internal/domain/asset.go:216`:

```go
func ValidateMaskRelationship(input, mask Asset) error {
	if mask.Kind != AssetKindMask {
		return ErrInvalidMask
	}
	if input.Width != mask.Width || input.Height != mask.Height {
		return ErrMaskDimensionMismatch
	}
	return nil
}
```

Chỉ kiểm `kind` và kích thước; comment ghi rõ *"performs no I/O and never
re-decodes content"*. `InspectImageContent` chấp nhận `image/png`,
`image/jpeg`, `image/webp` cho **mọi** asset kind.

Rủi ro còn mở: **mask JPEG được nhận**. JPEG nén mất mát làm nhoè biên vùng
chọn thành giá trị xám rác. Job vẫn trả `completed`, chỉ ảnh sai. Chú ý
`ErrInvalidMask` được mô tả là *"role **or encoding** cannot be interpreted"* —
chữ "encoding" cho thấy ý định ban đầu có kiểm, nhưng code chưa làm.

## 5. Encode PNG trong UXP — ba repo đều né canvas

Câu trả lời rõ ràng nhất từ đợt đọc này.

| Repo | Cách encode |
|---|---|
| `OpenLayer` | Encoder tự viết ~300 dòng, deflate **"stored"** (không nén) |
| `OpenAI-PS` | Encoder tự viết + `zlibDeflateRle` (nén thật) + biến thể **1-bit palette+tRNS** |
| `Auto-Photoshop-SD` | Bundle thư viện **Jimp** |
| `layerflow` | `canvas.toDataURL` — **repo duy nhất** làm vậy |

Bằng chứng UXP không phải trình duyệt đầy đủ, từ
`.ref/OpenLayer/src/utils/multipart.ts`:

> *"Photoshop's UXP environment does not expose TextEncoder, so multipart header
> text is UTF-8 encoded manually."*

`OpenAI-PS` chỉ dùng `toDataURL` ở nhánh chẩn đoán dự phòng (`app.js:9205`),
không phải đường chính.

**Rủi ro cho `layerflow`:** `rgbaToPngBytes` dùng `document.createElement('canvas')`
có thể đã chạy trên máy dev nhưng là chỗ dễ vỡ khi đổi phiên bản Photoshop.
Cần test thật.

**Đánh đổi kích thước:** encoder OpenLayer không nén → mask 2048×2048 ra ~16 MB.
`POST /assets` có `413 request_too_large`. Biến thể **1-bit palette+tRNS** của
`OpenAI-PS` (`app.js:9316`) là tối ưu cho mask nhị phân: cùng mask xuống vài
chục KB.

## 6. Kỹ thuật lấy mask — hai trường phái

**`layerflow`** — đọc trực tiếp qua `imaging.getSelection()`, có fallback mask
hình chữ nhật khi API vắng mặt (PS 24.x). Fallback này làm selection
tròn/feather blend sai.

**`OpenLayer`** (`photoshopAdapter.ts:1332`) — vẽ mask thành layer thật rồi chụp:

```ts
temporaryLayerId = await createTemporaryMaskLayer(photoshop);
await fillActiveSelection(photoshop, "white");   // vùng chọn → trắng
await invertActiveSelection(photoshop);
await fillActiveSelection(photoshop, "black");   // phần còn lại → đen
await invertActiveSelection(photoshop);
capturedMask = await captureMaskImage(...);      // getPixels layer đó
```

Vòng vo hơn nhưng **không phụ thuộc `imaging.getSelection`** → chạy trên mọi
phiên bản, mask chính xác từng pixel kể cả feather. Giá phải trả: chuỗi cleanup
dài (`saveSelectionSnapshot`, khôi phục selection, xoá layer tạm, khôi phục
layer active) cộng một vòng **retry cleanup** nếu vòng đầu lỗi.

Chi tiết `OpenLayer` có mà `layerflow` chưa có — ngưỡng tin cậy alpha:

```ts
const MASK_ALPHA_TRUST_THRESHOLD = 8;
const maskValue = alpha > MASK_ALPHA_TRUST_THRESHOLD ? luminance : 0;
```

Pixel gần trong suốt bị ép về 0 thay vì tin luminance rác của nó.

Comment đáng chú ý trong `photoshopAdapter.ts`, ghi cả ngày phát hiện:

> *"confirmed in Photoshop on 2026-07-16, where a visible layer above a
> non-topmost active layer replaced the saved mask with that layer's luminance"*

## 7. Padding vùng chọn

| Nguồn | Công thức |
|---|---|
| `OpenAI-PS` (gốc) | 18% ngang / 25% dọc, **clamp `[64, 384]` px** |
| `layerflow` | 18% / 25%, **thiếu clamp** |
| `OpenLayer` | 75% + tối thiểu 96 px + **snap bội số 8** |

`layerflow/src/photoshop/selection.ts` ghi `Pattern from OpenAI-PS` nhưng bỏ mất
`Math.max(64, Math.min(384, value * ratio))`. Hệ quả: vùng chọn 20×20 px chỉ
được padding 4 px (model gần như không có ngữ cảnh); vùng chọn 4000 px thì
padding 720 px mỗi bên (tốn băng thông vô ích).

**Snap bội số 8** — model diffusion cần kích thước chia hết cho 8. Cả
`layerflow` lẫn `OpenAI-PS` đều thiếu.

## 8. Undo history — `layerflow` làm đúng nhất

| Repo | Trạng thái |
|---|---|
| `layerflow` | `suspendHistory`/`resumeHistory`, rollback `accept=false` khi lỗi ✅ |
| `OpenLayer` | Có, kèm cleanup + retry ✅ |
| `Auto-Photoshop-SD` | Có code nhưng **bị comment out** |
| `OpenAI-PS` | **Không có** — mỗi batchPlay là một history step riêng |

Giữ nguyên cách của `layerflow`.

## 9. Polling job — mô hình đáng học từ OpenLayer

`comfyClient.ts` dùng WebSocket **chỉ để đánh thức** vòng poll sớm, kết quả vẫn
lấy từ `/history`:

> *"Fired when ComfyUI reports this prompt has finished executing. It is a hint
> to look at the history now, never a result in itself: the outputs still come
> from `/history`, because the socket does not carry them."*

Có `waitForWake?: (timeoutMs: number) => Promise<void>` để rút ngắn chu kỳ chờ,
và comment giải thích bằng số đo thật (224 ms lãng phí trong chu kỳ 653 ms).

Áp thẳng được vào `GET /render-jobs/{job_id}`: nếu sau này gateway có SSE thì
chỉ dùng nó để đánh thức, không đổi nguồn sự thật.

## 9b. `sd-ppp` — phần Photoshop-native là closed source

Đọc xong sau khi ghi bản đầu của tài liệu này. Kết luận quan trọng: **`sd-ppp`
không dùng được để học phần Photoshop-native.**

Code UXP thật sự nói chuyện với Photoshop nằm trong module `SDPPPInternal`, bị
`.gitignore` loại khỏi repo public:

```
typescripts/photoshop-internal
typescripts/modules/photoshop-internal
```

`esbuild.ts` khai báo nó là external global
(`external: ['uxp','photoshop',...,'SDPPPInternal']`), và nó chỉ tồn tại dưới
dạng bundle đã obfuscate trong `plugins/photoshop/dist/`. Grep hai bundle đó cho
`getPixels`, `imaging.`, `batchPlay`, `encodeImageData` → **0 kết quả**.

Cái còn đọc được chỉ là **wire contract**, không phải cách gọi Photoshop:

- `PhotoshopCalleeInterface.mts:33-46` — PS→server trả `jpegData` + `alphaData`
  riêng biệt, và dòng `// pngData: Uint8Array | null` bị comment cho thấy họ đã
  **bỏ PNG** chuyển sang JPEG + alpha tách rời.
- `PhotoshopCalleeInterface.mts:49-57` — server→PS nhận **hoặc** `pngData: Blob`
  **hoặc** `buffer + width + height` thô, để phía native tự quyết.
- Mask: `sdppp_python/comfy/nodes.py:398-422` đọc buffer single-channel `L`,
  chia 255 vào tensor MASK **không đảo** → trắng = 1.0. Không có comment nào nói
  rõ white/black semantics; đây là suy ra từ việc thiếu phép đảo.
- Upload: dùng Ant Design `<Upload>` (thư viện tự build multipart), không tự
  dựng body. PS↔Comfy đi qua socket.io với `Uint8Array` thô, không phải HTTP
  multipart. **Không** tìm thấy ghi chú về `TextEncoder`.
- Progress/cancel: socket.io + event bus của ComfyUI; cancel = `interrupt()` +
  `clearQueue()`.

**Hệ quả:** loại `sd-ppp` khỏi danh sách tham khảo cho phần Photoshop host
interaction. Mâu thuẫn license (mục 10) giờ thành vấn đề thứ yếu — kể cả được
phép copy thì cũng không có gì để copy. Giữ clone trong `.ref/` chỉ để tham khảo
kiến trúc multi-provider và UX, đúng như đánh giá ban đầu.

Điểm đáng suy nghĩ: họ chọn **JPEG cho ảnh màu + alpha buffer riêng** thay vì
một PNG. Với `POST /assets` của PixelPlus (mục 5, vấn đề mask 16 MB), đây là một
hướng khác đáng cân nhắc — nhưng contract hiện tại chỉ có một `mask_asset_id`,
nên không áp dụng trực tiếp.

## 10. Kiểm chứng license và quyền sở hữu

Đã verify qua GitHub API — **không** repo public nào thuộc `monet88` hoặc
`monet1992`; chúng thuộc bảy chủ sở hữu khác nhau.

| Repo | License | Ghi chú |
|---|---|---|
| `OpenLayer` | MIT | 2 sao, 0 fork, tạo 2026-06-21, tự ghi `v0.12.0-alpha` |
| `OpenAI-PS` | MIT | nhỏ, là nguồn gốc padding của `layerflow` |
| `Auto-Photoshop-SD` | MIT | 7.3k sao, push cuối 2024-04 |
| `uxp-photoshop-plugin-samples` | MIT | Adobe chính chủ |
| `sd-ppp` | ⚠️ **mâu thuẫn** | file `LICENSE` = BSD-3-Clause; README = GPL-3.0. Thứ yếu — xem mục 9b, phần native không có trong repo |
| `upscayl`, `realesrgan-gui`, `clarity-upscaler` | AGPL-3.0 | copyleft mạnh, ràng buộc cả SaaS |
| `robertsLando/upscaler`, `kenantang/ExpressEdit` | **không có LICENSE** | mặc định all rights reserved |

Lưu ý pháp lý: **đọc code để học thì tự do**, không license nào cấm. Cái bị ràng
buộc chỉ là copy-paste vào sản phẩm. Với `sd-ppp`, giả định xấu nhất (GPL) cho
tới khi tác giả làm rõ.

## 11. Reference repos đã clone

Ngày 2026-08-05, clone vào `.ref/` (đã gitignore, `git status` sạch):

```
.ref/OpenLayer                              26M   607178a   MIT
.ref/OpenAI-PS                             8.4M   5ddd44d   MIT
.ref/Auto-Photoshop-StableDiffusion-Plugin  40M   6f6d490   MIT
.ref/uxp-photoshop-plugin-samples           15M   1928d83   MIT (Adobe)
.ref/sd-ppp                                122M   d965457   ⚠️ license mâu thuẫn
```

Tổng `.ref/` sau khi thêm: 461 MB (trước 251 MB).

Repo gốc `monet88/layerflow` **chưa** clone — đọc qua `gh api`.

Đường dẫn hay dùng:

```
.ref/OpenLayer/src/utils/png.ts                    encoder PNG tự viết
.ref/OpenLayer/src/utils/multipart.ts              multipart + ghi chú thiếu TextEncoder
.ref/OpenLayer/src/photoshop/photoshopAdapter.ts   2490 dòng, mask "vẽ layer rồi chụp"
.ref/OpenAI-PS/src/app.js:9327                     encodePngRgba
.ref/OpenAI-PS/src/app.js:9690                     zlibDeflateRle — nén thật
.ref/OpenAI-PS/src/app.js:9316                     biến thể 1-bit palette+tRNS
.ref/OpenAI-PS/src/app.js:8151                     padding 18%/25% + clamp [64,384]
.ref/uxp-photoshop-plugin-samples/web-service-call-js-sample/
.ref/uxp-photoshop-plugin-samples/secure-storage-sample/
.ref/uxp-photoshop-plugin-samples/oauth-workflow-sample/
.ref/uxp-photoshop-plugin-samples/io-websocket-example/
.ref/uxp-photoshop-plugin-samples/layer-creation-js-sample/
```

## 12. Phương án tối ưu — chọn gì từ repo nào

Không repo nào tối ưu toàn bộ. Bảng dưới là lựa chọn từng hạng mục sau khi đọc
cả năm, kèm lý do và nguồn cụ thể.

| Hạng mục | Chọn theo | Lý do |
|---|---|---|
| Lớp `src/photoshop/*` | **`layerflow`** (giữ nguyên) | Đã kiểm chứng thực tế, xử lý đúng 6 bẫy UXP (mục 2). Không repo nào làm tốt hơn cho use-case này |
| Undo / rollback | **`layerflow`** (giữ nguyên) | Repo duy nhất `suspendHistory` + rollback `accept=false` còn hoạt động (mục 8) |
| Xử lý CMYK | **`layerflow`** (giữ nguyên) | Duplicate rồi convert, không đụng file gốc. Ba repo còn lại không có |
| Encode PNG mask | **`OpenAI-PS`** | 1-bit palette + `zlibDeflateRle`. Xem 12.1 |
| Encode PNG ảnh màu | **`OpenAI-PS`** | `encodePngRgba` + RLE deflate, có fallback `zlibStore` khi lỗi |
| Lấy mask từ selection | **`OpenLayer`** | Không phụ thuộc `imaging.getSelection` → bỏ được fallback mask chữ nhật. Xem 12.2 |
| Ngưỡng tin cậy alpha | **`OpenLayer`** | `MASK_ALPHA_TRUST_THRESHOLD = 8` — chặn luminance rác ở pixel gần trong suốt |
| Padding vùng chọn | **`OpenAI-PS`** + snap của `OpenLayer` | Xem 12.3 |
| Multipart body | **`OpenLayer`** | `POST /assets` là `multipart/form-data`; UXP thiếu `TextEncoder` nên phải tự encode UTF-8 |
| Poll job | **`OpenLayer`** | Poll là nguồn sự thật, socket chỉ để đánh thức (mục 9) |
| Lưu API key | **Adobe `secure-storage-sample`** | `layerflow` dùng OAuth device-code, không có sẵn đường API key |
| Validate kích thước | **`OpenLayer`** | `exactInpaintMask.ts` kiểm 3 lớp mask↔source↔result. Khớp `mask_dimension_mismatch` của gateway |
| ~~`sd-ppp`~~ | **không dùng** | Phần native là closed source (mục 9b) |

### 12.1 Encode PNG — vì sao `OpenAI-PS`

Ba lựa chọn đã đọc:

| Nguồn | Cách làm | Mask 2048×2048 |
|---|---|---|
| `layerflow` | `canvas.toDataURL` | nhỏ, nhưng **phụ thuộc DOM** |
| `OpenLayer` | tự viết, deflate "stored" | **~16 MB** — đụng `413` |
| `OpenAI-PS` | tự viết, RLE deflate + 1-bit palette | **vài chục KB** |

`OpenAI-PS` (`src/app.js:9316`) dùng `IHDR(bitDepth=1, colorType=3)` +
`PLTE(black, white)` cho mask nhị phân. Mask chỉ có hai giá trị nên 1 bit/pixel
là đủ — giảm 32× so với RGBA trước khi nén, rồi RLE nén tiếp vùng đồng màu.

`zlibDeflateRle` (`src/app.js:9690`) là fixed-Huffman + back-reference
distance=1, có `try/catch` fallback về `zlibStore`. Đủ cho mask, không cần
LZ77 đầy đủ.

**Lưu ý:** `OpenAI-PS` ghi `tRNS` chunk vào mask 1-bit của nó. PixelPlus **không
nên** copy phần đó — mask phải opaque hoàn toàn để luminance là thứ duy nhất
mang nghĩa (xem 4.4). Bỏ `tRNS`, giữ `PLTE`.

**Lý do không dùng `canvas.toDataURL` của `layerflow`:** `OpenLayer/src/utils/multipart.ts`
ghi rõ UXP không có `TextEncoder`; `OpenAI-PS` chỉ dùng `toDataURL` ở nhánh chẩn
đoán dự phòng. Ba repo độc lập đều né canvas — không phải trùng hợp. Cần test
`toDataURL` trên PS thật trước khi quyết bỏ hẳn hay giữ làm fallback.

### 12.2 Lấy mask — vì sao `OpenLayer`

`layerflow` gọi `imaging.getSelection()`, và tự có nhánh fallback mask hình chữ
nhật khi API vắng mặt (PS 24.x). Fallback đó làm selection tròn/feather blend
sai — chính là lỗi khó phát hiện.

`OpenLayer` (`photoshopAdapter.ts:1332`) vẽ mask thành layer thật rồi
`getPixels`: tạo layer tạm → tô trắng vùng chọn → đảo selection → tô đen → chụp
→ xoá layer. Không phụ thuộc `imaging.getSelection` nên chạy trên mọi phiên bản
và chính xác từng pixel kể cả feather.

**Giá phải trả — cần cân nhắc:** chuỗi cleanup rất dài (snapshot selection,
khôi phục selection, xoá layer tạm, khôi phục layer active) cộng một vòng retry
nếu vòng đầu lỗi. Đây là nơi dễ sinh bug nhất trong toàn bộ plugin.

**Phương án trung dung, nên chọn:** giữ `imaging.getSelection` làm đường chính
(đơn giản, đã chạy), nâng `manifest.json` lên `minVersion: 25.0.0` để ~~**xoá hẳn
nhánh fallback chữ nhật**~~, và chỉ port kỹ thuật của `OpenLayer` nếu gặp trường
hợp `getSelection` thất bại thật. OpenLayer chọn `25.0.0` chính vì lý do này.

> **Sửa 2026-08-06 — đã quyết GIỮ fallback.** `OpenAI-PS` cũng giữ, và đọc
> `createSelectionMaskBase64:8180-8187` mới thấy vì sao: **cả hai nhánh đều đi
> qua** `createRelativeRectMaskBase64` để map toạ độ về ảnh crop, nên fallback
> không phải một đường code riêng biệt — nó chỉ thay *hình dạng* mask, giữ nguyên
> phép quy đổi toạ độ. Rủi ro thấp hơn nhiều so với đánh giá ban đầu.
>
> Và `getSelection` có thể lỗi vì lý do **ngoài** phiên bản PS: tài liệu lạ,
> selection rỗng, memory. Khi đó mask xấp xỉ vẫn hơn tính năng dừng hẳn. Vẫn nâng
> `minVersion: 25.0.0`, nhưng giữ nhánh fallback.

### 12.3 Padding — ghép hai nguồn

```
padding = clamp(size × ratio, 64, 384)      ← OpenAI-PS, ratio 0.18 ngang / 0.25 dọc
bounds  = snapToMultipleOf8(paddedBounds)   ← OpenLayer
```

`layerflow` copy tỉ lệ 18%/25% từ `OpenAI-PS` nhưng **bỏ mất clamp**. Hệ quả:
vùng chọn 20×20 px chỉ được 4 px padding (model không có ngữ cảnh); vùng chọn
4000 px được 720 px mỗi bên (tốn băng thông).

`OpenLayer` dùng ratio 0.75 + min 96 px — rộng hơn nhiều. Chưa có bằng chứng
ratio nào cho chất lượng inpaint tốt hơn, nên **giữ 18%/25% của `OpenAI-PS`**
(đã là lựa chọn của `layerflow`, ít thay đổi nhất) và chỉ thêm clamp.

**Snap bội số 8** là thứ cả `layerflow` lẫn `OpenAI-PS` đều thiếu mà model
diffusion cần. Lấy `snapBoundsToMultiple` từ `OpenLayer/src/photoshop/selectionUtils.ts`.

## 13. Plan thực thi (chưa bắt đầu — chờ backend xong)

Thứ tự dưới đây tối thiểu hoá rủi ro: mỗi bước verify được độc lập, và những
bước phụ thuộc contract nằm sau.

**Bước 0 — Chốt convention mask trên OpenAPI.** *(chặn mọi bước sau)*
Thêm `description` cho `ImageInpaintRequest.mask_asset_id` (hiện chỉ có
`type: string`): luminance, trắng = sửa, đen = giữ, PNG opaque, cùng kích thước
input. Chạy `node scripts/validate-openapi-contract.mjs`. Quyết định nơi đặt
phép đảo cho ChatGPT — đề xuất: adapter trong gateway, không phải plugin (4.4).

**Bước 1 — Dựng khung build + `src/image/`, thuần logic, không cần Photoshop.**
`package.json` (Vite + `@bubblydoo/vite-uxp-plugin` như `layerflow`), rồi port
encoder PNG theo 12.1. Đây là phần **kiểm chứng được bằng test thuần Node** —
`node --test` + TypeScript chạy trực tiếp trên Node 24, không cần thêm
dependency. Test đáng viết: round-trip qua `zlib.inflateSync` của Node, padding
bit cuối hàng khi width không chia hết 8, và kích thước mask 2048² dưới 64 KB.

**Bước 2 — Port lớp `src/photoshop/*` nguyên trạng.**
5 file từ `layerflow`, đổi `InpaintKit` → `PixelPlus`. Thêm clamp padding (12.3)
và snap bội số 8. Nâng `minVersion` lên `25.0.0`, xoá nhánh fallback mask chữ
nhật (12.2). Verify bằng cách hardcode một PNG local rồi
`placeResultAsSmartObject` — chưa nối mạng.

**Bước 3 — `src/api/` bám contract.** *(cần backend chạy được)*
Sinh type từ `contracts/openapi/pixelplus-public-api-v1.yaml` (repo có
`@redocly/cli`). Port `multipart.ts` từ `OpenLayer`. Bốn hàm: `uploadAsset`,
`createInpaint`, `pollJob`, `fetchOutput`. Lưu API key theo Adobe
`secure-storage-sample`.

**Bước 4 — Nối hai đầu.**
`export → upload input + mask → inpaint → poll → download → place`. Thay
`generation-service.ts`: đồng bộ → async job. Poll theo mô hình mục 9.

**Bước 5 — UI + cancel.**
Map `execution_phase` (`preflight | upstream | capturing_result | placing_output`)
vào progress dialog. Nối `POST /render-jobs/{id}/cancel`.

**Bước 6 — Siết `manifest.json`.**
~~Xoá 11 domain provider cũ, chỉ để domain gateway.~~ Làm cuối để không cản
debug trong lúc phát triển.

> **Sửa 2026-08-06 (ADR 0002).** Xoá domain provider là **sai** — Direct Path cần
> đúng những domain đó (`api.openai.com`, `api.x.ai`,
> `generativelanguage.googleapis.com`, `*.aiplatform.googleapis.com`,
> `api.replicate.com`, `fal.run`).
>
> `requiredPermissions.network.domains` trong UXP là **allowlist tĩnh** khai báo
> lúc đóng gói (`.ref/bolt-uxp/uxp.config.ts:146-158`), nên không có cách nào cho
> "endpoint bất kỳ" ngoài việc mở `domains: "all"`. Đã chọn: **allowlist tường
> minh cho provider biết trước + host gateway + `localhost`** cho Custom endpoint.
> Từ chối `domains: "all"` vì plugin giữ credential tiêu tiền của user trên máy,
> nên allowlist hẹp là biện pháp giảm bề mặt exfiltration chính.
>
> Hệ quả: thêm Provider cho Direct Path là đổi `manifest.json` → cần bản cài mới;
> Custom endpoint chỉ trỏ được tới máy cục bộ; Vertex đặt host theo region nên có
> thể phải liệt kê từng region nếu UXP không nhận wildcard (**chưa kiểm**).

### Việc phía gateway, độc lập với plugin

- Từ chối mask JPEG ở `InspectImageContent` (4.5) — nén mất mát làm nhoè biên
  vùng chọn thành giá trị xám rác, job vẫn trả `completed` nhưng ảnh sai.
- Contract test khoá convention: mask JPEG → `invalid_mask`.
- Xác nhận `place-result.ts` scale đúng khi output khác kích thước `targetRect`
  (rủi ro #3 mục 14).

## 14. Rủi ro và việc còn mở

1. ~~**Convention mask trên dây chưa chốt** (mục 4.4)~~ — **đã đóng**, xem 4.4b và
   4.4c: canonical là luminance trắng=sửa (ADR 0003), đường ChatGPT là alpha với
   bốn nguồn xác nhận. Vẫn cần ghi vào OpenAPI `description` của `mask_asset_id`.
2. **Mask JPEG được gateway nhận** (mục 4.5) — nên từ chối, kèm contract test.
   Phạm vi đã hẹp lại: chỉ `kind=mask` siết PNG, `kind=input` **giữ nguyên**
   PNG/JPEG/WebP vì ảnh input bắt nguồn từ tài liệu của user. Cả ba repo tham
   khảo đều chỉ dùng PNG cho mask và không coi đó là một lựa chọn
   (`OpenLayer/exactInpaintMask.ts:48` hardcode `image/png`; `OpenAI-PS` dùng
   `decodePngRgbaBase64`, không có nhánh JPEG).
3. ~~**`place-result.ts` có thể không scale đúng**~~ — **đã đóng bằng prototype
   2026-08-06.** Rủi ro là **thật**, và cách sửa đã xác nhận. Xem 14.3b.
4. **`minVersion: 24.0.0` kéo theo fallback mask chữ nhật** — **đã quyết: GIỮ
   fallback.** `OpenAI-PS` cũng giữ (`createSelectionMaskBase64:8180-8187`): ưu
   tiên `imaging.getSelection`, fallback rect khi API vắng, **cả hai nhánh đều đi
   qua** `createRelativeRectMaskBase64` để map toạ độ. Giữ vì `getSelection` có
   thể lỗi vì lý do khác ngoài phiên bản PS (tài liệu lạ, selection rỗng,
   memory), và khi đó có kết quả xấp xỉ vẫn hơn dừng hẳn.
5. **`canvas.toDataURL` trong `layerflow` chưa được kiểm chứng trên nhiều bản PS**
   (mục 5). Không chặn — đã chọn encoder tự viết.
6. ~~**Padding thiếu clamp** (mục 7)~~ — **đã đo bằng prototype.** Xem 14.6b.
7. ~~`sd-ppp` chưa được đọc xong~~ — **đã đóng**, xem mục 9b: phần
   Photoshop-native là closed source, không dùng để tham khảo host interaction
   được.
8. **CÒN MỞ — token Codex OAuth có được `/responses` nhận cho tool
   `image_generation` không.** `.ref/CLIProxyAPI` là bằng chứng mạnh nhưng là
   implementation bên thứ ba, không phải tài liệu OpenAI. Rủi ro tệ nhất nếu sai:
   nhận request nhưng **bỏ qua mask** → sửa toàn ảnh, vẫn trả `completed`, không
   lỗi nào. Cần probe thật trước khi cam kết lịch cho Gateway Path.
9. **CÒN MỞ — UXP có nhận wildcard trong `network.domains` không** (cho Vertex
   region host).
10. **CÒN MỞ — `batchPlay` transform trên Photoshop thật.** Prototype chỉ mô phỏng
    hình học, chưa chứng minh `freeTransformCenterState: QCSIndependent` và
    `offset` hoạt động như hiểu. Cần Bước 2 với UXP Developer Tools.

### 14.3b Rủi ro #3 — prototype đã đo, cách sửa đã xác nhận

NOTES lo *"`transform` đặt `width/height = 100%`"* thì không scale. **Đúng** — và
lý do là `percentUnit` trong `batchPlay` là phần trăm so với kích thước **hiện
tại** của layer, nên `100%` nghĩa là "giữ nguyên".

Cách đúng (`OpenAI-PS/src/app.js:6767-6768`) tính từ `bounds` thật đo được sau khi
import:

```js
const scaleX = (targetRect.width  / bounds.width)  * 100;   // bounds = layer.boundsNoEffects
const scaleY = (targetRect.height / bounds.height) * 100;
```

Prototype chạy với đúng số đo thật (vùng chọn `362×509 @ (38,255)`, tài liệu
`1536×2048`):

| Scale variant | Model trả về | Layer đặt tại | |
|---|---|---|---|
| tính từ bounds | exact / `1024²` / `2048²` | `362×509 @ (38,255)` | PASS |
| `literal100` | exact | `362×509 @ (38,255)` | PASS |
| `literal100` | `1024²` | `797×683 @ (84,298)` | **FAIL** |
| `literal100` | `2048²` | `1594×1366 @ (167,469)` | **FAIL** |

**Chi tiết quan trọng nhất:** dòng `literal100 + exact` vẫn PASS — nên bug **ẩn
hoàn toàn** trong lúc dev, khi model tình cờ trả đúng cỡ đã gửi. Chỉ lộ ra khi
provider chuẩn hoá kích thước.

Hai chi tiết `OpenAI-PS` có mà `layerflow` không có:

- `freeTransformCenterState: QCSIndependent` (`app.js:6784-6786`) — neo **góc** thay
  vì tâm; không có thì scale làm lệch vị trí.
- `offset` tính riêng (`app.js:6791-6793`) — dịch về đúng `targetRect.left/top`, vì
  scale làm đổi vị trí.
- `rectsMatchWithinTolerance` (`app.js:6803`) — **verify 1px sau khi transform**,
  không tin transform mù.

Và điểm dễ bỏ qua: ảnh model trả về mô tả nội dung của **Context Rect**, không
phải Target Rect. Nên phải làm nó phủ Context Rect **trước**, rồi mới crop về vùng
chọn.

### 14.6b Padding clamp — prototype đã đo

| Vùng chọn | Clamp | Padding | Context Rect |
|---|---|---|---|
| `20×20` | on | 64/64 | `148×148` |
| `20×20` | **off** | 4/5 | **`28×30`** |
| `1400×1900` | on | 252/384 | `1536×2048` |

Vùng chọn `20×20` không clamp thì Context Rect chỉ `28×30` — model gần như **không
thấy gì** xung quanh, không thể hoà vào ảnh.

Phát hiện ngoài dự đoán: vùng chọn `1400×1900` thì clamp **không tạo khác biệt** —
Context Rect bị chặn ở biên tài liệu trước cả khi padding kịp có ý nghĩa. Nên lo
"padding 720px tốn băng thông" ở mục 7 chỉ đúng với tài liệu rất lớn.

## 15. Ghi chú phương pháp

Trong phiên này đã kết luận sai **ba lần** vì suy diễn thay vì đọc code:

1. "OpenLayer đáng làm base code" — bỏ qua việc nó 2 sao / 0 fork / 6 tuần tuổi.
2. "alpha=0 = sửa là convention chung" — nhầm convention nội bộ với convention
   trên dây.
3. "grayscale opaque là convention chung" — vẫn giả định chỉ có một convention.

**Bổ sung 2026-08-06 — lần thứ tư, cùng loại lỗi:**

4. "`chatgpt2api` nhận grayscale là chạy đúng" (lý do 3 của mục 4.4) — nói
   **ngược**. Mở `openai_v1_image_edit.py` ra đọc thì thấy nó `putalpha(luminance)`
   rồi coi alpha thấp = vùng sửa, nên gửi trắng=sửa vào đó sẽ sửa vùng đen. Xem
   4.4b.

Và một lỗi **khác loại** — không phải suy diễn từ code, mà từ **giả định kiến
trúc**:

5. "Gateway là đường duy nhất" — dẫn tới ba kết luận sai liên đới: bỏ
   `src/providers/*` (mục 3), "plugin chỉ biết một convention" (mục 4.4), và xoá
   domain provider ở Bước 6. Thực tế có hai Execution Path (ADR 0002). Bài học
   khác với bốn lần trên: đọc code không cứu được lỗi này, vì code đọc đúng hết —
   sai ở chỗ **chưa hỏi ai là người dùng và họ có credential loại gì**.

Mỗi lần đều được sửa sau khi đọc code thật. Nguyên tắc rút ra: **trước khi
khẳng định convention nào, mở file trong `.ref/` ra đọc**, đừng suy từ tài liệu
hay trí nhớ. Metadata (số sao, tuổi repo, license) phải nêu bằng con số, không
bằng tính từ.

## 15b. Bản đồ đọc nhanh — file nào ở đâu, giá trị gì

Index để session sau tra nhanh, không phải grep lại. Repo gốc `monet88/layerflow`
đọc qua `gh api repos/monet88/layerflow/contents/...` (chưa clone).

### `.ref/OpenAI-PS` — MIT, 5 sao — kho encode PNG và padding

File chính: `src/app.js` (~9700 dòng, monolith — tra bằng số dòng, đừng đọc cả).

| Dòng | Nội dung | Dùng cho |
|---|---|---|
| ~8151 | Padding 18% ngang / 25% dọc + clamp `[64, 384]` | Bước 2 (port `selection.ts`) |
| ~8221 | `rgba[o+3] = 255 - selected` — bằng chứng convention alpha | mục 4.3 |
| ~9205 | `toDataURL` ở nhánh dự phòng — chú ý đây KHÔNG phải đường chính | mục 5 |
| ~9316 | Encode PNG **1-bit palette + tRNS** cho mask nhị phân | **Bước 1 — cốt lõi** |
| ~9327 | `encodePngRgba` — encoder đầy đủ | Bước 1 (ảnh màu) |
| ~9690 | `zlibDeflateRle` — fixed-Huffman + back-ref distance=1, fallback `zlibStore` | Bước 1 |

Có sẵn CCX packaging (Releases ra `.ccx`) — tham khảo khi đóng gói.

### `.ref/OpenLayer` — MIT, 2 sao, tạo 2026-06 — kho host-interaction nâng cao

| File | Nội dung | Dùng cho |
|---|---|---|
| `src/utils/png.ts` | Encoder PNG tự viết ~300 dòng, deflate "stored" (không nén) | Đối chiếu Bước 1 |
| `src/utils/multipart.ts` | Multipart thủ công + ghi chú UXP thiếu `TextEncoder` | **Bước 3 — port nguyên** |
| `src/photoshop/photoshopAdapter.ts` | 2490 dòng. Dòng ~1332: mask "vẽ layer tạm rồi `getPixels`"; `MASK_ALPHA_TRUST_THRESHOLD = 8`; comment có ngày xác nhận bug thật trên PS | Bước 2 (phương án dự phòng lấy mask) |
| `src/photoshop/exactInpaintMask.ts` | Kiểm 3 lớp mask↔source↔result | Đối chiếu `mask_dimension_mismatch` |
| `src/photoshop/selectionUtils.ts` | `snapBoundsToMultiple` — snap bội số 8 | Bước 2 |
| `src/comfy/comfyClient.ts` | Poll là nguồn sự thật, WebSocket chỉ đánh thức (`waitForWake`) | **Bước 4 — mô hình poll job** |

### `.ref/Auto-Photoshop-StableDiffusion-Plugin` — MIT, 7.3k sao, push cuối 2024-04

| Vị trí | Nội dung | Dùng cho |
|---|---|---|
| `utility/io.js` | **BẪY**: `transparentToMask` và `inpaintTransparentToMask` ngược convention nhau trong cùng file | Ví dụ điển hình lý do phải chốt 1 convention + test khoá |
| toàn repo | Bundle Jimp để encode — hướng không chọn | Không đọc sâu |

### `.ref/uxp-photoshop-plugin-samples` — MIT, Adobe chính chủ

| Thư mục | Dùng cho |
|---|---|
| `secure-storage-sample` | **Bước 3 — lưu API key** |
| `web-service-call-js-sample` | Gọi HTTP cơ bản trong UXP |
| `oauth-workflow-sample` | Tham khảo nếu sau này cần OAuth |
| `io-websocket-example` | Tham khảo nếu gateway có SSE/socket |
| `layer-creation-js-sample` | batchPlay tạo layer |
| `swc-uxp-react-starter` | React + SWC wrapper: cần `featureFlags.enableSWCSupport: true` trong manifest, alias `@swc-uxp-wrappers/utils`, version SWC bị khoá theo wrapper |

### `.ref/bolt-uxp` — MIT, 187 sao — khung build (clone 2026-08-05, `e9eaa7d`)

| Vị trí | Nội dung | Dùng cho |
|---|---|---|
| `uxp.config.ts` | Cấu hình manifest/build tập trung | **Bước 1** |
| `vite.config.ts` + `vite-uxp-plugin` | Build + hot reload qua UDT | Bước 1 |
| `src/api/` | Type definitions UXP + Photoshop API cập nhật | Đối chiếu types |
| `.github/workflows/` | CCX release pipeline sẵn | Bước 6 (đóng gói) |

### `.ref/sd-ppp` — ⚠️ license mâu thuẫn (BSD-3/GPL-3), 122 MB

Không dùng cho host interaction (mục 9b). Đọc được duy nhất wire contract:
`typescripts/src/socket/PhotoshopCalleeInterface.mts` (PS↔server đổi
`jpegData` + `alphaData` riêng; đã bỏ PNG). Giữ lại chỉ để tham khảo kiến trúc.

### Repo team (chưa clone) — `monet88/layerflow` (InpaintKit)

| Đường dẫn | Kích thước | Quyết định |
|---|---|---|
| `src/photoshop/place-result.ts` | 4556 B | **Port nguyên xi** (Bước 2). Rủi ro #3: `transform` width/height=100% có thể không scale đúng |
| `src/photoshop/selection.ts` | 3565 B | Port + thêm clamp padding (12.3) |
| `src/photoshop/batch-play-helpers.ts` | 2666 B | Port nguyên xi |
| `src/photoshop/export-image.ts` | 2067 B | Port nguyên xi |
| `src/photoshop/document-utils.ts` | 1252 B | Port nguyên xi |
| `src/providers/*` | ~25 KB | **Bỏ** — gateway lo |
| `src/auth/*` | ~13 KB | **Bỏ** — thay bằng một API key |
| `src/services/generation-service.ts` | — | **Viết lại** — đồng bộ → async job (Bước 4) |
| `src/services/image-processing.ts` | — | Ép alpha=255, luminance — đọc khi chốt Bước 0 |
| `src/components/*` | — | Port được; progress map theo `execution_phase` (Bước 5) |
| `manifest.json` | — | v5, `minVersion 24.0.0` → nâng `25.0.0` để xoá fallback mask chữ nhật (12.2) |

## 15c. Repo mới phát hiện 2026-08-05 (chưa clone — quét web, chưa đọc code)

Chưa repo nào được clone hay đọc code; metadata qua GitHub API/web. Quy tắc cũ:
**không kết luận gì trước khi mở file ra đọc.**

| Repo | Sao | License | Đánh giá nhanh |
|---|---|---|---|
| `hyperbrew/bolt-uxp` | 187 | MIT | **Đáng clone nhất.** Boilerplate Vite+TS+React/Vue/Svelte cho UXP: hot reload, `uxp.config.ts`, đóng gói CCX, GitHub Actions release sẵn. Tham khảo cho Bước 1 (khung build) thay vì tự dựng. Push gần nhất 2026-07-29 — còn sống |
| `shuaige-dev/erect-banana` | 0 | **NONE** (all rights reserved) | Khớp nhất về mặt tính năng: BYOK multi-provider (Gemini/OpenAI/OpenRouter), generative fill + outpaint + harmonize, "automatic feathered-mask blending" — đúng chỗ rủi ro #3. Đã lên Adobe Exchange. Nhưng không có LICENSE → **chỉ đọc học, tuyệt đối không copy** |
| `jeffgyf/nano-banana-ps-plugin` | 57 | **NONE** | Inpaint bằng Gemini qua UXP, nhỏ gọn (598 KB), có CCX release. Cùng lưu ý license: đọc học được, không copy |
| `LiuYangArt/PSBananaUXP` | 28 | **NONE** | Có kinh nghiệm thật ghi trong README: dùng `localhost` thay `127.0.0.1` để tránh lỗi permission UXP, layer-group theo tên `source`/`reference`. 39 MB — nặng, chưa rõ vì sao. Không copy |
| `isekaidev/stable.art` | 1149 | MIT | Vue + A1111, push cuối 2023-08 — cũ, hướng CEP-legacy. Ưu tiên thấp |
| `DimaChaichan/LAizypainter` | 21 | MIT | ComfyUI realtime loop (LCM), `minVersion 25.0.0`, quan niệm selection = quick-mask image. Nếu làm live-preview sau này thì đọc |
| `psdwizzard/comfyui-photoshop-bridge` | — | — | **CEP, không phải UXP** — loại |
| `NimaNzrii/comfyui-photoshop` | — | — | CCX đóng gói sẵn, source UXP không rõ — loại khỏi tham khảo code |
| `Experience-Monks/comfy-monk-public` | — | — | Plugin + FastAPI + nhiều mode (guided inpaint, upscale). Chưa kiểm license; nếu MIT thì đáng đọc phần crop/mask-res |
| `SmokeyRGB/ComphyDiffusion` | — | — | UXP + WebSocket progress + workflow editor. Chưa kiểm license |
| `TheWhykiki/photoshop-comfyui-mcp-bridge` | — | — | UXP plugin expose API endpoint + MCP. Kiến trúc ngược (server gọi vào plugin) — tham khảo nếu muốn gateway chủ động đẩy |
| `marelegsaa/split-ai` | 1 | Apache-2.0 | LLM → JSON plan → người duyệt → `batchPlay` trong `executeAsModal`. Khác bài toán (không inpaint) nhưng pattern validate-trước-khi-batchPlay đáng học |
| `newraina/ps-ai-diffusion` | 2 | **GPL-3.0** | Port từ krita-ai-diffusion. Copyleft mạnh — chỉ đọc học |

**Quyết định:** **đã clone `hyperbrew/bolt-uxp`** (MIT) vào `.ref/bolt-uxp`
(`e9eaa7d`, 2.9 MB, push cuối 2026-07-29) — dùng trực tiếp cho khung build
Bước 1: hot reload, `uxp.config.ts`, đóng gói CCX, GitHub Actions release.
Ba repo nano-banana (`erect-banana`, `nano-banana-ps-plugin`,
`PSBananaUXP`) đọc online qua `gh api` khi cần đối chiếu blending/placement —
vì không có license, code của họ chỉ để học, không để port.

## 16. Điều phối workflow và phụ thuộc backend (2026-08-05)

Câu hỏi: có cần thêm `/research`, `/prototype`, `/wayfinder` cho frontend
không, và có phải xong hết backend issues mới được `/grill-with-docs` không.

### 16.1 Ba skill — không cái nào bắt buộc ở mức full

- **`/research` — không cần.** Bản thân tài liệu này đã là research log hoàn
  chỉnh: 5 repo đã đọc code thật, license đã verify, convention mask đã lần tới
  nguồn (mục 4). Chỉ còn một câu hỏi mở có thể giao `/research` chạy nền nếu
  muốn: xác nhận trên tài liệu Adobe chính thức rằng UXP 25.x (a) không có
  `canvas.toDataURL`, (b) `imaging.getSelection` ổn định từ `minVersion 25.0.0`.
  Không chặn gì — Bước 1 đã chọn encoder tự viết, không phụ thuộc canvas.
- **`/prototype` — không bắt buộc, chỉ dùng để dập rủi ro #3 sớm.** Rủi ro duy
  nhất cần câu trả lời *chạy được* mà không cần backend: `place-result.ts` scale
  sai khi output khác kích thước `targetRect` (mục 14.3). Prototype throwaway gồm
  Bước 1 + Bước 2 + một PNG hardcode kích thước khác vùng chọn là đủ trả lời.
  Nếu làm thì chen giữa grill và `/to-spec`.
- **`/wayfinder` — không.** Wayfinder dành cho effort mù mờ nhiều session, sản
  phẩm là *quyết định*. Frontend đã hết mù: mục 12 chọn xong nguồn cho từng
  hạng mục, mục 13 có sẵn 7 bước theo thứ tự tối thiểu hoá rủi ro. Đi thẳng
  `/grill-with-docs` → `/to-spec` → `/to-tickets`.

### 16.2 Không cần xong hết backend — chỉ hai món thật sự chặn

Trạng thái kiểm chứng trong code ngày 2026-08-05:

- Các endpoint plugin cần **đã tồn tại** trong gateway:
  `POST /v1/assets`, `GET /v1/assets/{id}`, `GET /v1/assets/{id}/content`
  (`transport/http/asset.go:40-42`) và `POST /v1/images/inpaints`,
  `GET /v1/render-jobs/{id}`, `POST .../cancel`, `POST .../outputs/{id}/retry`
  (`transport/http/render.go:35-38`), kèm contract test đầy đủ.
- Nhưng production composition đang dùng **`FailClosedRenderAdapter`**
  (`infrastructure/vault/foundation.go:59-76`): mọi render trả
  `ErrRenderAdapterUnavailable` vì chưa có adapter provider nào được nối cho
  image. Chat đã có adapter (T17, PR #93), **image thì chưa**.

Bản đồ chặn:

| Việc backend | Chặn frontend ở đâu | Ghi chú |
|---|---|---|
| Bước 0 — chốt convention mask vào OpenAPI `description` | **Bước 3** (`src/api/`) | Chỉ cần quyết định + ghi contract, không cần code mới |
| Một adapter image chạy được — gần nhất là T18 (#61), fixtures có cả "image operations" | **Bước 4** (nối hai đầu) và E2E | Không có nó thì `POST /images/inpaints` fail-closed |
| Từ chối mask JPEG ở gateway (mục 4.5) | Không chặn; nên xong trước E2E | Hardening độc lập, một issue nhỏ |
| T19–T22, T25, S02, S03, T24 | **Không chặn** | Provider khác, durable ledger, release, conformance — làm song song |

> **Sửa 2026-08-06 — bảng trên thiếu ba thứ.**
>
> **1. Còn một adapter thứ ba chặn, không chỉ render.** Production composition
> thay fail-closed cho **ba** port, không phải một:
>
> | Port | Vị trí | Hệ quả |
> |---|---|---|
> | `FailClosedRenderAdapter` | `infrastructure/vault/foundation.go` | không render được |
> | `FailClosedProbeAdapter` | `composition/runtime.go:498` | account **không đạt `active` được** (`I-ACCOUNT-ENABLE-PROBED`) |
> | `FailClosedOAuthExchangeAdapter` | `composition/runtime.go:527` | **không bắt đầu được OAuth journey nào** |
>
> Probe là chỗ bảng cũ bỏ sót và nó nặng nhất: không có probe thì **không
> Provider Account nào usable được**, kể cả khi đã có adapter image.
>
> Logic OAuth phía application thì **đã có sẵn**: `application/oauth.go`
> `StartOAuthAuthorization:52`, `GetOAuthAuthorization:208`, xử lý cả
> `ExchangedMaterial`. Chỉ thiếu adapter.
>
> **2. T18 đã xong** (`eb39b45`, `8256afd`, `2fc2fdc` — Refs #61) và có
> `adapters/chatgptweb/` với `Probe` tại `adapter.go:54`. Nhưng đó là **chat-only**
> — grep `Render`/`inpaint`/`ImageEdit` trong adapter đó ra **0 kết quả**. Nên
> render vẫn fail-closed thật.
>
> **3. Bảng này chỉ đúng cho Gateway Path.** **Direct Path không bị chặn gì cả** —
> bảy trong tám bước của chuỗi cốt lõi không chạm mạng, bước thứ tám gọi thẳng
> provider bằng key của user. Đây là hệ quả lớn nhất của ADR 0002 với việc điều
> phối: có thể làm plugin **ngay**, song song với backend.
>
> Thứ tự đã chốt: đường dây OAuth đầu tiên là **ChatGPT Codex OAuth (T19 #62)**,
> làm **trọn hai đầu cùng lúc** thay vì backend-xong-rồi-frontend. Phần OAuth của
> plugin là phần **dễ nhất** — `openExternal` + poll, đúng mẫu Adobe
> `oauth-workflow-sample/index.js:1-19`, khoảng 19 dòng.

Hệ quả điều phối:

1. **`/grill-with-docs` cho frontend bắt đầu ngay được**, song song với backend.
   Grill là align phase — đầu vào là tài liệu này + contract OpenAPI, không cần
   gateway chạy. Quyết định convention mask (Bước 0) vốn là sản phẩm của
   grill/domain-modeling, nên grill sớm còn giúp backend chốt Bước 0 nhanh hơn.
2. Thứ tự đề xuất: `/grill-with-docs` → `/to-spec` → `/to-tickets` → implement
   Bước 1 + 2 (không cần backend) → khi T18 và Bước 0 xong thì implement tiếp
   Bước 3–6.
