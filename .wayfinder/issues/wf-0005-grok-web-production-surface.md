---
id: wf-0005
title: "Xác minh Grok Web SSO và xAI OAuth adapters"
status: open
labels:
  - wayfinder:research
parent: wf-0001
blocked_by: []
assignee:
---

## Question

Trong hai Auth Mode độc lập — Grok Web SSO được adapt từ chenyme/grok2api và Grok xAI OAuth được adapt từ CLIProxyAPI — mỗi Adapter cần hỗ trợ auth/refresh, chat, streaming, Grok Imagine generation/edit và Photoshop reference/masked-inpaint đến đâu; entitlement theo account, quota, challenge/protocol-drift risk, Statsig/signing và phần nào gateway phải tự sở hữu thay vì phụ thuộc bên thứ ba?
