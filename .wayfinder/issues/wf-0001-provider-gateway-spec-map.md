---
id: wf-0001
title: "Đặc tả SaaS Provider Gateway pure Go cho InpaintKit"
status: open
labels:
  - wayfinder:map
parent:
blocked_by: []
assignee:
---

## Destination

Một đặc tả đủ chi tiết để đội triển khai monorepo PixelPlus gồm SaaS gateway pure Go và Photoshop UXP Plugin viết lại: public OpenAI-compatible API cho chat và ảnh, BYOA cô lập theo Tenant, credential nằm trong SaaS vault, ba Provider family ChatGPT/Gemini/Grok với các Web và OAuth/CLI Adapter độc lập; Gateway được triển khai trước và Plugin được triển khai sau khi public contract ổn định.

Map hoàn tất khi không còn quyết định sản phẩm, domain, interface, bảo mật, vận hành hoặc migration nào chưa được khóa trước khi bắt đầu implementation planning.

## Notes

- Đây là effort planning; không viết backend hoặc migration trong map này.
- Mỗi session chỉ resolve tối đa một ticket.
- Dùng `/grilling` và `/domain-modeling` cho quyết định HITL; dùng `/research` cho ticket cần nguồn ngoài working directory; dùng `/prototype` khi cần artifact cụ thể để phản hồi.
- Nguồn local chính: `.ref/CLIProxyAPI`, `.ref/chatgpt2api`, `.ref/gemini-web-to-api`, `.ref/grok2api`; implementation target là `apps/gateway`, Photoshop client target là `apps/photoshop-plugin`, và public seam nằm trong `contracts/`. Backend Python và Photoshop Plugin của repo `layerflow` chỉ còn là legacy behavior reference trong giai đoạn migration.
- Quyết định nền đã khóa khi chart map: SaaS tập trung; credential lưu trong SaaS vault; Web-to-API là năng lực cốt lõi; chat và ảnh; public developer API; BYOA không chia sẻ chéo Tenant; OpenAI-compatible core; pure Go; ChatGPT Web adapt từ `chatgpt2api`, còn ChatGPT Codex OAuth lấy pure-Go runtime chính từ `CLIProxyAPI` và dùng cả hai repo để đối chiếu hành vi image.

## Decisions so far

## Not yet specified

- Topology persistence/runtime và ngưỡng chuyển từ single-region sang distributed coordination, sau khi execution lifecycle được xác định.
- Observability, SLO, audit và privacy controls, sau khi risk envelope và data lifecycle rõ ràng.
- Migration từ backend Python và Photoshop Plugin legacy sang `apps/gateway`/`apps/photoshop-plugin`, compatibility window và rollout, sau khi public contract được khóa.
- Validation matrix, canary strategy và tiêu chí launch, sau khi provider failure semantics được quyết định.
- Commercial metering/billing, nếu nó trở thành yêu cầu của destination sau khi quota và abuse model rõ hơn.

## Out of scope

- Viết code backend Go, migration hoặc deploy production trong effort này.
- Video generation/editing.
- Claude/Anthropic-compatible façade và Gemini-native public façade.
- Account pool dùng chung giữa các Tenant.
- Official API adapters cho OpenAI, Gemini hoặc xAI trong đặc tả MVP Web-to-API này.
