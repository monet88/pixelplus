# Provider Gateway Specification — Issue Execution Plan

Tài liệu này ghi lại thứ tự thực hiện các GitHub issues thuộc parent [#1 — Specify the Pure-Go Multi-Provider BYOA Gateway](https://github.com/monet88/pixelplus/issues/1).

Đây là **specification work**. Không triển khai Gateway trong các issues này.

## Nguyên tắc thực hiện

1. Chỉ bắt đầu một issue khi mọi issue trong `Blocked by` đã đóng.
2. Các issue cùng một frontier có thể giao cho các subagent riêng và chạy song song.
3. Mỗi subagent chỉ giải quyết phạm vi và acceptance criteria của một issue.
4. Trước khi làm, subagent phải đọc parent #1, issue được giao, comments và deliverable của toàn bộ blockers.
5. Dùng domain vocabulary trong `CONTEXT.md` và tuân thủ ADR hiện có.
6. Mọi capability claim phải dùng một trong bốn trạng thái:
   - `verified`
   - `conditionally supported`
   - `unsupported`
   - `unverified`
7. Mọi quyết định phải nêu observable behavior, failure semantics, security impact và evidence khi áp dụng.
8. Không sửa parent #1 hoặc downstream issues ngoài việc tham chiếu kết quả cần thiết.
9. Chỉ close issue khi toàn bộ acceptance criteria đã đạt. Nếu còn product decision cần chủ sở hữu duyệt, đăng recommendation và giữ issue mở.
10. Sau mỗi issue, kiểm tra native GitHub dependencies để xác định frontier mới thay vì dựa riêng vào sơ đồ tĩnh trong file này.

## Frontier hiện tại

Có thể giao ngay cho năm subagent chạy song song:

| Subagent | Issue | Phạm vi |
|---|---|---|
| A | [#2](https://github.com/monet88/pixelplus/issues/2) | Research compliance, ToS, account-ban và Web-to-API risk |
| B | [#3](https://github.com/monet88/pixelplus/issues/3) | Xác minh ChatGPT Web Access và ChatGPT Codex OAuth |
| C | [#4](https://github.com/monet88/pixelplus/issues/4) | Xác minh Gemini Web Cookie và Gemini Antigravity OAuth |
| D | [#5](https://github.com/monet88/pixelplus/issues/5) | Xác minh Grok Web SSO và Grok xAI OAuth |
| E | [#6](https://github.com/monet88/pixelplus/issues/6) | Khóa Tenant ownership và authorization invariants |

Ranh giới để tránh làm trùng:

- **#2:** policy, compliance, ToS và risk; không quyết định capability kỹ thuật.
- **#3–#5:** capability, credential lifecycle và failure behavior của từng Provider family; không tự quyết định PixelPlus chấp nhận rủi ro nào.
- **#6:** domain và Tenant security; không nghiên cứu protocol Provider.

## Thứ tự thực hiện

### Đợt 1 — Evidence và security foundation

Chạy song song:

- [#2 — Establish the SaaS Web-to-API Compliance and Risk Evidence Base](https://github.com/monet88/pixelplus/issues/2)
- [#3 — Verify ChatGPT Web Access and ChatGPT Codex OAuth](https://github.com/monet88/pixelplus/issues/3)
- [#4 — Verify Gemini Web Cookie and Gemini Antigravity OAuth](https://github.com/monet88/pixelplus/issues/4)
- [#5 — Verify Grok Web SSO and Grok xAI OAuth](https://github.com/monet88/pixelplus/issues/5)
- [#6 — Define Tenant Ownership and Authorization Invariants](https://github.com/monet88/pixelplus/issues/6)

### Đợt 2 — Auth Mode risk decision

Sau khi #2–#5 hoàn tất:

- [#7 — Set the Risk Envelope and Kill Criteria for Six Auth Modes](https://github.com/monet88/pixelplus/issues/7)

### Đợt 3 — Client access và account connection

Sau khi #6 và #7 hoàn tất, chạy song song:

- [#8 — Define Client API Key Lifecycle and Admission Controls](https://github.com/monet88/pixelplus/issues/8)
- [#9 — Define Provider Account Connection and Provider Credential Lifecycle](https://github.com/monet88/pixelplus/issues/9)

### Đợt 4 — Capability model

Sau khi #9 hoàn tất:

- [#10 — Define Capability Snapshot and Model Availability Semantics](https://github.com/monet88/pixelplus/issues/10)

### Đợt 5 — Routing policy

Sau khi #8, #9 và #10 hoàn tất:

- [#11 — Define Tenant-Scoped Routing, Fallback, Affinity, and Account Leases](https://github.com/monet88/pixelplus/issues/11)

### Đợt 6 — Chat và Asset branches

Sau khi #11 hoàn tất, chạy song song:

- [#12 — Define Chat Execution and Streaming Lifecycle](https://github.com/monet88/pixelplus/issues/12)
- [#13 — Define Asset Exchange, Authorization, and Retention Lifecycle](https://github.com/monet88/pixelplus/issues/13)

### Đợt 7 — Durable Render Job

Ngay khi #13 hoàn tất; không cần chờ #12:

- [#14 — Define Durable Render Job and Output-Retry Lifecycle](https://github.com/monet88/pixelplus/issues/14)

### Đợt 8 — Credential vault

Sau khi #14 hoàn tất:

- [#15 — Define Credential Vault and Sensitive-Data Lifecycle](https://github.com/monet88/pixelplus/issues/15)

Chuỗi `#13 → #14 → #15` có thể chạy trong lúc một subagent khác hoàn tất #12.

### Đợt 9 — Errors và retry ownership

Sau khi cả #12 và #15 hoàn tất:

- [#16 — Define Canonical Errors and Retry Ownership](https://github.com/monet88/pixelplus/issues/16)

### Đợt 10 — Provider Account health

Sau khi #16 hoàn tất:

- [#17 — Define Provider Account Health, Cooldown, and Operator Controls](https://github.com/monet88/pixelplus/issues/17)

### Đợt 11 — OpenAPI prototypes

Sau khi #17 hoàn tất, chạy song song:

- [#18 — Validate the OpenAI-Compatible Inference Contract](https://github.com/monet88/pixelplus/issues/18)
- [#19 — Validate the Provider Account and Capability Management Contract](https://github.com/monet88/pixelplus/issues/19)

### Đợt 12 — Contract policy

Sau khi #18 và #19 hoàn tất:

- [#20 — Set API Versioning, Compatibility, Idempotency, and Contract Testing Policy](https://github.com/monet88/pixelplus/issues/20)

### Đợt 13 — Pure-Go architecture

Sau khi #20 hoàn tất:

- [#21 — Set Pure-Go Module Seams and Dependency Budget](https://github.com/monet88/pixelplus/issues/21)

### Đợt 14 — Completion gate

Sau khi toàn bộ #2–#21 hoàn tất:

- [#22 — Assemble the Implementation-Ready Provider Gateway Specification](https://github.com/monet88/pixelplus/issues/22)

## Dependency flow rút gọn

```text
#2  #3  #4  #5  #6
 │   │   │   │   │
 └───┴───┴───┘   │
         ▼        │
        #7        │
         └────┬───┘
              ▼
          #8     #9
           └──┬──┘
              ▼
             #10
              ▼
             #11
          ┌───┴───┐
          ▼       ▼
         #12     #13
                  ▼
                 #14
                  ▼
                 #15
          └───┬───┘
              ▼
             #16
              ▼
             #17
          ┌───┴───┐
          ▼       ▼
         #18     #19
          └───┬───┘
              ▼
             #20
              ▼
             #21
              ▼
             #22
```

Sơ đồ trên là bản rút gọn. Native GitHub dependencies là nguồn chính xác cuối cùng cho mọi blocker.

## Prompt mẫu cho subagent

Thay `N` bằng số issue được giao:

```text
Xử lý GitHub issue #N trong repository monet88/pixelplus.

Yêu cầu:
1. Đọc đầy đủ parent #1, issue #N, comments và deliverable của các blocker.
2. Chỉ giải quyết phạm vi và acceptance criteria của #N.
3. Đây là specification work; không triển khai Gateway.
4. Không sửa parent #1 hoặc downstream issues.
5. Dùng vocabulary trong CONTEXT.md và tuân thủ ADR hiện có.
6. Mọi capability claim phải là verified, conditionally supported,
   unsupported hoặc unverified.
7. Mọi kết luận phải nêu observable behavior, failure semantics,
   security impact và evidence khi áp dụng.
8. Nếu phải sửa repository, dùng branch hoặc worktree riêng.
9. Được phép đăng deliverable lên #N và close #N chỉ khi toàn bộ
   acceptance criteria đã đạt.
10. Nếu còn product decision thực sự cần chủ sở hữu chấp thuận,
    đăng recommendation và giữ issue mở thay vì tự quyết định.
```

Bổ sung cho #2–#5:

```text
Ưu tiên nguồn chính thức và reference repository được nêu trong parent #1.
Ghi URL, ngày truy cập, mức độ tin cậy và giới hạn của từng nguồn.
```

Bổ sung cho #6:

```text
Dùng domain-modeling để duy trì domain vocabulary và kiểm tra invariant
xuyên suốt Client API Key, Provider Account, Provider Credential, Asset
và Render Job.
```

## Checkpoints có thể cần maintainer quyết định

- **#7:** chấp nhận, cấm hoặc feature-gate từng Auth Mode dựa trên risk evidence.
- **#20:** API versioning và compatibility policy.
- **#21:** dependency budget và architectural trade-offs.
- **#22:** xác nhận specification đủ điều kiện chuyển sang Gateway implementation planning.

Khi gặp nhiều lựa chọn hợp lệ, subagent phải đưa ra một recommendation cụ thể, nguyên nhân và hệ quả; không chỉ liệt kê các lựa chọn.
