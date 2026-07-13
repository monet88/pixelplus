# Local Markdown Issue Tracker

Wayfinder dùng thư mục này làm issue tracker cục bộ khi repository chưa cấu hình tracker integration.

## Issue metadata

Mỗi issue là một file Markdown với YAML frontmatter:

- `id`: định danh ổn định của issue.
- `title`: tên issue dùng trong mọi nội dung người đọc thấy.
- `status`: `open` hoặc `closed`.
- `labels`: gồm `wayfinder:map` hoặc một loại ticket `wayfinder:research`, `wayfinder:prototype`, `wayfinder:grilling`, `wayfinder:task`.
- `parent`: `id` của map đối với child issue; để trống cho map.
- `blocked_by`: danh sách `id` issue phải đóng trước.
- `assignee`: để trống nghĩa là chưa claim.

## Wayfinding operations

- Map: file có label `wayfinder:map`.
- Child issues: các file có `parent` trùng `id` của map.
- Claim: điền `assignee` trước khi bắt đầu xử lý ticket.
- Frontier: child issue `status: open`, `assignee` trống và mọi issue trong `blocked_by` đã `closed`.
- Resolution: thêm comment vào `comments/<issue-id>/`, đổi `status` thành `closed`, rồi thêm một context pointer vào `Decisions so far` của map.
- Blocking: được biểu diễn bằng `blocked_by` trong frontmatter vì local tracker không có dependency relationship native.

Tên issue phải được dùng thay cho bare ID trong mọi nội dung dành cho người đọc.
