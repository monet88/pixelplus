# AGENTS.md

PixelPlus là monorepo gồm Provider Gateway pure Go và Adobe Photoshop UXP Plugin.

## Canonical planning artifacts

- `CONTEXT.md` là domain glossary; không đưa implementation detail vào file này.
- `.wayfinder/issues/wf-0001-provider-gateway-spec-map.md` là planning map canonical.
- Không biến quyết định Wayfinder chưa resolve thành assumption trong code.

## Repository layout

- `apps/gateway/`: SaaS Provider Gateway pure Go, được triển khai trước.
- `apps/photoshop-plugin/`: Photoshop UXP Plugin, viết lại sau khi public contract ổn định.
- `contracts/`: OpenAPI và generated-contract artifacts khi contract được khóa.
- `docs/`: ADR, research và migration documentation.
- `.ref/`: upstream reference repositories local-only; không phải production source.

## Security invariants

- Không đưa Provider Credential, Client API Key hoặc secret vào source, logs, fixtures hay docs.
- Provider Account và credential phải được cô lập theo Tenant.
- Không fallback âm thầm giữa Web và OAuth account hoặc giữa các Provider.
- Chỉ công bố capability đã được probe theo từng Provider Account.

## Reference adaptation

Không copy nguyên reference project vào production packages. Adapt tại seam đã chốt, giữ attribution/license cần thiết và viết test qua interface của module mới.
