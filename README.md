# PixelPlus

Monorepo cho InpaintKit/PixelPlus, gồm SaaS Provider Gateway pure Go và Adobe Photoshop UXP Plugin.

## Trình tự triển khai

1. Hoàn tất Wayfinder và khóa domain/public contract.
2. Xây dựng `apps/gateway` bằng Go.
3. Ổn định OpenAPI contract trong `contracts/`.
4. Viết lại `apps/photoshop-plugin` dựa trên contract đã khóa.
5. Kiểm chứng parity và migration trước khi ngừng repo legacy.

## Tài liệu canonical

- Domain glossary: [`CONTEXT.md`](./CONTEXT.md)
- Wayfinder map: [`.wayfinder/issues/wf-0001-provider-gateway-spec-map.md`](./.wayfinder/issues/wf-0001-provider-gateway-spec-map.md)
- Upstream references: `.ref/` (local-only, không commit)
