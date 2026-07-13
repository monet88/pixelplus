---
id: wf-0003
title: "Xác minh ChatGPT Web Access và Codex OAuth adapters"
status: open
labels:
  - wayfinder:research
parent: wf-0001
blocked_by: []
assignee:
---

## Question

Với seam đã chọn — ChatGPT Web Adapter lấy behavioral protocol và implementation reference chính từ basketikun/chatgpt2api; ChatGPT Codex OAuth Adapter lấy pure-Go OAuth/runtime/image implementation chính từ CLIProxyAPI và đối chiếu hành vi với cả hai repo — mỗi Adapter thực sự hỗ trợ đến đâu cho subscription chat, streaming, image generation, image edit và Photoshop masked-inpaint; credential lifecycle, entitlement theo account, quota, challenge/protocol-drift risk và phần MIT nào có thể adapt hợp pháp/kỹ thuật vào gateway pure Go?
