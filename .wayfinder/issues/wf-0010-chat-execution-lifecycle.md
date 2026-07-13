---
id: wf-0010
title: "Định nghĩa chat execution lifecycle"
status: open
labels:
  - wayfinder:grilling
parent: wf-0001
blocked_by:
  - wf-0003
  - wf-0004
  - wf-0005
  - wf-0006
  - wf-0009
assignee:
---

## Question

Chat Completions và Responses sẽ được thực thi, stream, cancel, retry, preserve conversation affinity và account lease như thế nào qua sáu Web/OAuth Adapter, trong khi vẫn giữ khác biệt về protocol, credential lifecycle và quota của từng Auth Mode?
