# ADR 0001: Product monorepo

- Status: Accepted
- Date: 2026-07-13

## Context

Sản phẩm gồm một SaaS Provider Gateway pure Go và một Adobe Photoshop UXP Plugin TypeScript. Hai phần dùng chung domain language và public HTTP contract nhưng có build, test và release lifecycle khác nhau.

## Decision

Đặt Gateway và Photoshop Plugin trong cùng một monorepo:

- `apps/gateway` chứa Gateway và được triển khai trước.
- `apps/photoshop-plugin` được viết lại sau khi public OpenAPI contract ổn định.
- `contracts` là seam giữa hai app.
- Hai app vẫn có pipeline build và release độc lập.

## Consequences

- Domain, contract và migration thay đổi trong cùng một history.
- Plugin có thể dùng client sinh từ OpenAPI thay vì tự duy trì request shape.
- CI phải phân biệt phạm vi Go và TypeScript/UXP.
- Không được để Plugin phụ thuộc vào implementation nội bộ của Gateway.
