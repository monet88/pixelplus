---
id: wf-0014
title: "Định nghĩa error, retry và provider-health semantics"
status: open
labels:
  - wayfinder:grilling
parent: wf-0001
blocked_by:
  - wf-0002
  - wf-0003
  - wf-0004
  - wf-0005
  - wf-0010
  - wf-0011
assignee:
---

## Question

Gateway phân loại auth expiry, reauth, rate limit, quota exhaustion, anti-bot challenge, upstream drift, policy refusal, timeout và internal failure ra sao; lỗi nào retry, cooldown, kill-switch hoặc trả trực tiếp cho client?
