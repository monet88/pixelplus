# PixelPlus

Monorepo cho InpaintKit/PixelPlus, gồm SaaS Provider Gateway pure Go và Adobe Photoshop UXP Plugin.

## Trình tự triển khai

1. Hoàn tất Wayfinder và khóa domain/public contract.
2. Xây dựng `apps/gateway` bằng Go.
3. Ổn định OpenAPI contract trong `contracts/`.
4. Viết lại `apps/photoshop-plugin` dựa trên contract đã khóa.
5. Kiểm chứng parity và migration trước khi ngừng repo legacy.

## Verification

Một entrypoint duy nhất cho mọi nền tảng (Windows, macOS, Linux và CI):

```bash
node scripts/verify-repository.mjs --fast      # PR gate: gofmt, vet, test, contract, hygiene
node scripts/verify-repository.mjs --full      # fast + race, mutation suite, architecture, docker smoke
node scripts/verify-repository.mjs --release   # full + supply chain, container build
```

Hoặc qua npm: `npm run verify:fast`, `npm run verify:full`, `npm run verify:release`.

Toolchain được pin trong [`.tool-versions`](./.tool-versions) / [`mise.toml`](./mise.toml),
`package.json` (`packageManager`, `engines`) và
[`requirements-validation.txt`](./requirements-validation.txt). Cài đặt:

```bash
mise install
npm ci
python -m pip install -r requirements-validation.txt
```

Thiếu tool là **failure có remediation**, không phải silent skip. Chỉ check phụ thuộc
Docker mới được conditional, và luôn báo rõ khi bị skip.

## Tài liệu canonical

- Domain glossary: [`CONTEXT.md`](./CONTEXT.md)
- Wayfinder map: [`.wayfinder/issues/wf-0001-provider-gateway-spec-map.md`](./.wayfinder/issues/wf-0001-provider-gateway-spec-map.md)
- Upstream references: `.ref/` (local-only, không commit)
