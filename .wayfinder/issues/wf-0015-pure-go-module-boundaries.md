---
id: wf-0015
title: "Chốt pure-Go module boundaries và dependency budget"
status: open
labels:
  - wayfinder:grilling
parent: wf-0001
blocked_by:
  - wf-0012
  - wf-0013
  - wf-0014
assignee:
---

## Question

Gateway pure Go sẽ chia module, seam và composition root thế nào; phần nào adapt từ bốn MIT reference; dependency nào được chấp nhận cho HTTP, persistence, TLS/browser emulation, WebSocket, crypto và distributed runtime?
