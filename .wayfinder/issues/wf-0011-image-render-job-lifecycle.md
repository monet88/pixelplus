---
id: wf-0011
title: "Định nghĩa image asset và render-job lifecycle"
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

Image generation/edit/inpaint sẽ upload input và mask, tạo durable job, lease worker, theo dõi progress, cancel/recover, lưu output và cho Photoshop retry placement mà không chạy lại Provider như thế nào?
