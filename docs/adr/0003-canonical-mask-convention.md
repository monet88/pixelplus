# ADR 0003: Canonical mask convention và nơi đặt phép đảo

- Status: Accepted
- Date: 2026-08-05

## Context

Mask không có convention chung. Khảo sát code thật trong `.ref/` cho năm nguồn
ra hai convention đối nghịch:

| Nguồn | Convention | Bằng chứng |
|---|---|---|
| `layerflow` → ChatGPT | alpha, alpha=0 = sửa | `rgbaToPngBytes` gửi thẳng, không đảo |
| `layerflow` → fal/Replicate | luminance, trắng = sửa | `invertMaskConvention` trước khi gửi |
| `OpenAI-PS` | alpha, alpha=0 = sửa | `rgba[o+3] = 255 - selected` (`app.js:8221`) |
| `OpenLayer` | luminance, trắng = sửa | `maskValue = luminance`, alpha ép 255 |
| `Auto-Photoshop-SD` | luminance, **tự mâu thuẫn** | `transparentToMask` và `inpaintTransparentToMask` ngược nhau trong cùng `utility/io.js` |

`Auto-Photoshop-SD` là repo trưởng thành nhất mảng này (7.3k sao) và vẫn có hai
hàm ngược nhau trong một file, code cũ bị comment ngay phía trên. Đây là hình
dạng bug cần tránh: không phải một phép đảo sai, mà là không có chỗ nào chốt
convention nên mỗi call site tự quyết.

Mô hình hai Execution Path (ADR 0002) làm bài toán khó hơn chứ không dễ đi: trên
Direct Path, Plugin nói trực tiếp với nhiều Provider có convention khác nhau, nên
không thể chỉ biết một convention.

## Decision

Canonical Mask Convention của Plugin là **luminance, trắng = sửa, alpha=255**.
`src/image/` chỉ sinh ra dạng này.

Phép đảo thuộc về đúng thành phần trực tiếp nói chuyện với upstream:

- Direct Path — provider client trong Plugin tự đảo (client OpenAI đảo sang
  alpha; client fal/Replicate gửi thẳng canonical).
- Gateway Path — Plugin gửi canonical lên `POST /assets` với `kind=mask`;
  Provider Adapter trong Gateway tự đảo khi Provider đòi khác.

Không thành phần nào khác được đảo mask. Mỗi phép đảo phải có test khoá bằng một
mask cố định và so byte đầu ra.

Chọn luminance thay vì alpha vì: grayscale opaque sống sót qua flatten và các
bước xử lý ảnh, còn mask alpha dễ bị `applyAlpha` nhân vào RGB làm mất thông
tin; và mở file mask luminance ra là kiểm chứng được bằng mắt, còn mask alpha
hiện ra ô trong suốt gần như không debug được.

## Consequences

- `.ref/chatgpt2api` **đòi phải đảo**, không chống lưng cho canonical này:
  `openai_v1_image_edit.py:36-44` làm `putalpha(luminance)` và dòng 26 ghi
  "vùng alpha thấp = vùng cần sửa", nên gửi trắng=sửa vào đó sẽ sửa đúng vùng
  đen. NOTES.md §4.4 lý do 3 nói ngược điều này và phải sửa.
- Đường ChatGPT dùng **alpha**, và điều này đã được chốt bằng bốn nguồn độc
  lập. Codex OAuth không gọi `/images/edits` mà gọi `/responses` với tool
  `image_generation`, trong đó mask là data URL base64 tại
  `input_image_mask.image_url`
  (`.ref/CLIProxyAPI/.../codex_openai_images.go:739,799`;
  `.ref/OpenAI-PS/src/app.js:3108`). Ngữ nghĩa của trường đó được
  `OpenAI-PS/src/app.js:3308` phát biểu tường minh trong prompt — *"transparent
  pixels are the editable brush area; non-transparent pixels are protected
  context"* — và bị khoá bằng test tại `scripts/smoke-plugin.js:625`. Cộng
  `layerflow/backend-provider.ts:237` gửi mask alpha thô không đảo,
  `OpenAI-PS/src/app.js:8221` với `rgba[o+3] = 255 - selected`, và
  `chatgpt2api` nêu trên.
- Vì vậy Provider Adapter cho ChatGPT phải biến đổi `alpha_out = 255 -
  luminance_in`: trắng (255) thành alpha 0 nghĩa là sửa, đen (0) thành alpha 255
  nghĩa là giữ. Đây không chỉ là một phép nghịch đảo bit mà còn là đổi loại ảnh,
  từ PNG grayscale/palette opaque sang PNG RGBA, nên Adapter cần encoder RGBA.
- Mask trên đường Codex OAuth đi trong JSON dưới dạng base64 nên phình thêm
  khoảng một phần ba. Điều này làm encoder 1-bit palette quan trọng hơn chứ
  không bớt: một mask 2048² dạng RGBA thô sẽ vượt 20 MB sau khi base64.
- Mask là dữ liệu Plugin tự sinh từ vùng chọn nên Plugin chọn được format:
  `kind=mask` chỉ nhận PNG. Cả ba repo tham khảo đều chỉ dùng PNG cho mask và
  không coi đó là một lựa chọn (`OpenLayer/exactInpaintMask.ts:48` hardcode
  `image/png`; `OpenAI-PS` dùng `decodePngRgbaBase64`, không có nhánh JPEG).
- Ảnh input thì ngược lại — nó bắt nguồn từ tài liệu của user nên `kind=input`
  vẫn nhận PNG/JPEG/WebP như hiện tại. JPEG cho ảnh màu còn có lợi vì
  `POST /assets` có giới hạn `413 request_too_large`.
- `src/image/` phải sinh được hai biến thể từ canonical, nên cần cả encoder
  1-bit palette cho mask luminance (`OpenAI-PS app.js:9316`) và `encodePngRgba`
  cho biến thể alpha (`app.js:9327`) — hai encoder, không phải một.
- Gateway hiện chỉ kiểm `kind` và dimension (`domain/asset.go:216`), chưa chặn
  mask JPEG. Việc siết là thay đổi phía Gateway, độc lập với Plugin.
