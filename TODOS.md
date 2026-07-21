# Gateway Runtime Ticket Execution Playbook

Tài liệu này là kế hoạch thực thi nhanh nhất cho 24 implementation tickets và
ba support tickets thuộc
[#42 - Build the Pure-Go Provider Gateway](https://github.com/monet88/pixelplus/issues/42):
[#44](https://github.com/monet88/pixelplus/issues/44)-[#67](https://github.com/monet88/pixelplus/issues/67),
[#68 - Docker live-probe sandbox](https://github.com/monet88/pixelplus/issues/68),
[#69 - upstream reference drift](https://github.com/monet88/pixelplus/issues/69)
và
[#70 - changelog and versioned Docker releases](https://github.com/monet88/pixelplus/issues/70).

Mục tiêu không phải tăng số ticket đang làm cùng lúc. Mục tiêu là rút ngắn
critical path trong khi mỗi ticket vẫn được review độc lập, verify qua public
seam, merge sạch, ghi Harness evidence và chỉ đóng sau khi proof đầy đủ.

Native GitHub `blocked by` và `sub-issue` relationships là nguồn chính xác cuối
cùng. Sơ đồ trong file này chỉ là execution guide.

## Nguyên tắc tối ưu

1. Chỉ lấy ticket đang ở frontier: mọi native blocker đã đóng và ticket chưa
   có assignee khác.
2. Mỗi ticket dùng một branch/worktree riêng, bắt đầu từ `main` mới nhất sau
   khi blocker cuối cùng đã merge.
3. Không stack ticket bị block lên branch chưa merge. Cách này giảm rebase,
   review lại và integration drift.
4. Giới hạn WIP mặc định là ba build tickets, gồm implementation hoặc support
   ticket đang mở frontier. Giữ một agent/context riêng cho review và verify.
5. Ưu tiên ticket nằm trên critical path. Ticket song song chỉ được lấy khi
   còn build slot và không làm chậm ticket mở frontier tiếp theo.
6. Mở draft PR sớm sau khi public seam, acceptance tests đỏ và phạm vi thay đổi
   đã rõ. Reviewer có thể kiểm tra hướng đi trước khi implementation phình ra.
7. Một PR chỉ giải quyết một ticket. Không gộp cleanup hoặc ticket kế tiếp vì
   làm tăng thời gian review và làm mờ acceptance evidence.
8. Merge theo dependency order, mỗi lần một PR. Sau một merge, các PR frontier
   còn lại phải rebase lên `main` mới và chạy lại verify trước khi merge.
9. Không đóng issue chỉ vì code đã viết xong. Issue chỉ được đóng sau khi PR đã
   merge, review sạch, proof mới nhất pass và Harness trace đã ghi.
10. Không mở deployment, SLO, canary, launch hoặc legacy migration work trước
    khi #67 hoàn tất. Docker ở #68 chỉ là sandbox local dùng lại production
    composition; #70 chỉ tạo release foundation, không tự chọn production
    topology hoặc tự publish stable release.

## Mô hình nhân lực nhanh nhất

Với bốn agent/context hoạt động đồng thời:

| Slot | Trách nhiệm |
|---|---|
| Builder A | Ticket critical-path đang runnable |
| Builder B | Ticket runnable song song có downstream lớn nhất |
| Builder C | Ticket runnable tiếp theo hoặc xử lý review findings |
| Reviewer | Review draft/final PR, kiểm tra spec compliance và rerun proof |

Reviewer không review code do chính mình viết. Khi không có PR chờ review,
reviewer chuẩn bị fixture/proof matrix, kiểm tra native frontier hoặc review
acceptance-test plan của ticket high-risk kế tiếp; không tự bắt đầu ticket bị
block.

Nếu chỉ có một agent, giữ nguyên thứ tự merge bên dưới và dùng fresh context
cho review sau khi implementation xong. Nếu có nhiều hơn bốn agent, không tăng
WIP tùy ý: thêm reviewer/verification capacity trước, sau đó mới tăng builder.

## Lịch thực thi nhanh nhất

### Đợt 0 - Composition spine và upstream ledger

Bắt đầu ngay hai ticket độc lập:

- Critical path: [#44 - Bootstrap Pure-Go composition and readiness](https://github.com/monet88/pixelplus/issues/44).
- Parallel support: [#69 - Track upstream reference revisions and report drift](https://github.com/monet88/pixelplus/issues/69).

#44 phải tạo composition constructor, deterministic test ports và reusable
quick verification commands đủ tốt để các ticket sau không dựng test harness
riêng. #69 khóa reviewed SHA, license, Auth Mode, watched paths và evidence docs
cho từng upstream; workflow chỉ báo drift, không thực thi hoặc đồng bộ code
upstream.

Ngay khi #44 merge, chạy song song:

- Critical path: [#45 - Create and read Provider Account drafts through the protected request spine](https://github.com/monet88/pixelplus/issues/45).
- Parallel support: [#68 - Add a disposable Docker live-probe sandbox](https://github.com/monet88/pixelplus/issues/68).

#45 phải hoàn thiện protected HTTP request spine thay vì chỉ làm ba Provider
Account endpoints. #68 phải dùng chính production composition constructor của
#44, bind localhost, chạy non-root/read-only và cleanup được; không chọn
production topology hoặc tạo sandbox-only runtime path.

Inner loop vẫn là `go test`/`go run` trực tiếp để có feedback nhanh nhất. Chỉ
build/run Docker khi cần parity, isolation, startup/readiness smoke hoặc một
live probe đã được ủy quyền. Không truyền Provider Credential qua CLI, image,
Compose, `.env` hay log; credential chỉ đi qua Public API và Vault. Không mount
Docker socket, home, `.ref/` hoặc repository credential vào container.
Container không được dùng host network/privileged mode; phải drop toàn bộ Linux
capabilities, bật no-new-privileges và giới hạn CPU, memory, process.

### Đợt 1 - Mở account path và Asset path

Sau khi #45 merge, chạy song song:

- Critical path: [#46 - Activate a Provider Account with a direct credential](https://github.com/monet88/pixelplus/issues/46)
- Parallel path: [#53 - Exchange immutable Tenant Assets](https://github.com/monet88/pixelplus/issues/53)

Ưu tiên review và merge #46 trước vì nó mở cả #47 và #50. #53 có thể tiếp tục
song song nhưng không được chiếm reviewer khi #46 đang chờ merge.

### Đợt 2 - Tách OAuth và Capability

Sau khi #46 merge, chạy song song:

- [#47 - Connect a Provider Account through server-owned OAuth](https://github.com/monet88/pixelplus/issues/47)
- [#50 - Publish Capability Snapshots and offerable models](https://github.com/monet88/pixelplus/issues/50)
- Hoàn tất #53 nếu chưa merge.

Sau #47, giữ Builder A trên critical path:

1. [#48 - Reauthenticate and cut over Provider Credentials safely](https://github.com/monet88/pixelplus/issues/48)
2. [#49 - Disable, re-enable, and delete Provider Accounts](https://github.com/monet88/pixelplus/issues/49)

#50 phải merge trước hoặc cùng thời điểm #49 hoàn tất để mở #51 không bị idle.

### Đợt 3 - Health và Routing

Chạy tuần tự trên critical path:

1. [#51 - Enforce scoped health and Provider Account controls](https://github.com/monet88/pixelplus/issues/51), sau #49 và #50.
2. [#52 - Manage Tenant Routing Policy and controlled candidates](https://github.com/monet88/pixelplus/issues/52), sau #50 và #51.

Đây là đoạn dễ tạo integration drift nhất. Không tách health, control và
routing thành horizontal refactors ngoài phạm vi ticket. Mọi rejection phải có
HTTP result và side-effect counter chứng minh protected boundary chưa bị gọi.

### Đợt 4 - Render và Chat chạy song song

Ngay khi #52 merge và #53 đã đóng, chạy hai nhánh song song:

| Render branch | Chat branch |
|---|---|
| [#54 - Create and complete routed image-generation jobs](https://github.com/monet88/pixelplus/issues/54) | [#58 - Execute routed non-streaming chat](https://github.com/monet88/pixelplus/issues/58) |
| Sau #54: #55 và #56 song song | Sau #58: #59 |
| Sau #55 + #56: #57 | Sau #59: #60 |

Render branch:

- [#55 - Execute image edit and inpaint from Tenant Assets](https://github.com/monet88/pixelplus/issues/55)
- [#56 - Recover Render Jobs without duplicate Provider work](https://github.com/monet88/pixelplus/issues/56)
- [#57 - Cancel Render Jobs and retry outputs without re-rendering](https://github.com/monet88/pixelplus/issues/57)

Chat branch:

- [#59 - Stream chat with canonical terminal ordering](https://github.com/monet88/pixelplus/issues/59)
- [#60 - Cancel chat and reconcile residual execution](https://github.com/monet88/pixelplus/issues/60)

Với ba builder, lịch tối ưu sau #54 là: Builder A làm #55, Builder B làm #56,
Builder C tiếp tục #59. Khi #55 và #56 merge, một slot làm #57 trong khi chat
tiếp tục tới #60. Không để một branch chờ review trong khi reviewer đang rảnh.

### Đợt 5 - Adapter work queue

Sáu adapter tickets chỉ vào frontier sau khi #57, #60, #68 và #69 đều đóng:

- [#61 - ChatGPT Web Access](https://github.com/monet88/pixelplus/issues/61)
- [#62 - ChatGPT Codex OAuth](https://github.com/monet88/pixelplus/issues/62)
- [#63 - Gemini Web Cookie](https://github.com/monet88/pixelplus/issues/63)
- [#64 - Gemini Antigravity OAuth](https://github.com/monet88/pixelplus/issues/64)
- [#65 - Prove Grok Web SSO remains prohibited](https://github.com/monet88/pixelplus/issues/65)
- [#66 - Grok xAI OAuth](https://github.com/monet88/pixelplus/issues/66)

Dùng một shared work queue, không chia wave cứng. Ba builder lấy ba ticket đầu;
khi ticket nào merge thì lấy ticket chưa assign tiếp theo. Reviewer kiểm tra
fixture hygiene, risk gate và capability-status inflation liên tục.

Thứ tự lấy mặc định khi chưa có thông tin duration tốt hơn:

1. #61, #62, #63 vì protocol surface rộng.
2. #64 và #66 ngay khi có slot trống.
3. #65 dùng slot trống đầu tiên phù hợp; ticket này chủ yếu là negative product
   composition proof nhưng vẫn cần independent review như adapter code.

Không dùng live credential trong fixture hoặc CI. Live probe chỉ chạy trong
sandbox #68 khi có ủy quyền rõ ràng cho đúng Auth Mode, account và environment.
Controlled fixture proof phải pass trước live probe. #68 chỉ cung cấp sandbox
và controlled smoke; chính ticket Adapter sở hữu live-probe evidence để tránh
dependency vòng. Grok Web SSO vẫn bị cấm và không có live probe.

### Đợt 6 - Full conformance gate

Sau khi #61-#66 đều merge và direct drift gate #69 vẫn xanh:

- [#67 - Prove full stable Public API runtime conformance](https://github.com/monet88/pixelplus/issues/67)

#67 là release-quality integration ticket, không phải nơi sửa hàng loạt thiếu
sót của các ticket trước. Nếu #67 phát hiện regression thuộc một ticket đã
merge, mở lại hoặc fix qua issue/PR có ownership rõ ràng, rerun affected proof,
sau đó mới tiếp tục completion gate.

### Đợt 7 - Changelog và versioned Docker release foundation

Sau khi #67 và #68 đều đóng:

- [#70 - Establish changelog and versioned Docker releases](https://github.com/monet88/pixelplus/issues/70)

Hiện tại PixelPlus chưa có `CHANGELOG.md`, release workflow, Dockerfile/Compose,
Git tag, GitHub Release hoặc published container image. `.ref/CLIProxyAPI` có
pattern tag-driven release, generated notes, checksum và DockerHub multi-arch;
#70 chỉ học các nguyên tắc hữu ích, không copy workflow nguyên trạng.

Thiết kế release cho PixelPlus:

1. Dùng SemVer `vMAJOR.MINOR.PATCH` và Keep a Changelog.
2. Mỗi ticket thêm một validated changelog fragment thay vì cùng sửa
   `CHANGELOG.md`; release PR assemble/consume fragments một lần để giảm conflict.
3. PR chỉ build/test/scan image, tuyệt đối không push. Protected tag trên
   reviewed `main` mới được publish.
4. Publish multi-arch `linux/amd64` + `linux/arm64` lên GHCR trước, dùng
   immutable version tag/digest, SBOM, provenance attestation, OCI labels và
   vulnerability scan. Stable release mới được move `latest`, `MAJOR.MINOR` và
   `MAJOR`; prerelease không được move stable aliases.
5. Image release phải dùng cùng production composition entrypoint của #44/#68,
   không chứa credential, `.env`, `.ref/`, Harness state hoặc local paths.
6. Pin third-party Actions bằng full commit SHA và cấp write permissions chỉ
   cho publishing jobs trong protected release environment.
7. DockerHub mirror và standalone binaries chưa cần cho Gateway service; chỉ
   mở khi owner quyết định registry/distribution requirement và secret custody.

#70 chặn việc đóng umbrella #42, nhưng actual first stable release vẫn cần
maintainer approval riêng. Không được tự release vì #69 phát hiện upstream drift.

## Vòng cập nhật tối ưu từ upstream `.ref`

`.ref/` tiếp tục là checkout local chỉ để nghiên cứu. Source of truth cho code
PixelPlus vẫn là canonical specs, decisions, Public API contract, Adapter ports
và controlled fixtures trong repo.

Luồng cập nhật bắt buộc của #69:

1. Scheduled/manual checker dùng `git ls-remote`; chỉ shallow-clone vào thư mục
   tạm khi cần xem watched paths, và không chạy code upstream.
   Ledger URL/branch/SHA phải được parse/validate trước khi gọi process; dùng
   structured arguments, chỉ chấp nhận HTTPS không chứa credential và SHA hex.
2. Nếu SHA thay đổi, workflow mở hoặc cập nhật một drift issue chứa old/new SHA,
   compare link, Auth Modes, watched paths, evidence docs và Adapter tickets bị
   ảnh hưởng. Report chỉ dùng metadata đã bound; không chèn nội dung file hoặc
   commit message upstream chưa tin cậy.
3. Reviewer chỉ đọc diff liên quan watched paths, kiểm tra license, protocol,
   credential/risk/capability impact và xác định PixelPlus có cần đổi hay không.
4. Nếu cần đổi, cập nhật Adapter/controlled fixture/evidence thủ công trong một
   ticket có review và verify đầy đủ; không copy hoặc merge upstream tự động.
5. Chỉ nâng reviewed SHA trong ledger sau khi thay đổi PixelPlus tương ứng đã
   review và proof pass. Upstream mới không tự động nâng capability/risk status.

Cơ chế này tối ưu việc theo upstream bằng cách thu hẹp review vào phần protocol
đã watch, nhưng vẫn giữ human gate. Tuyệt đối không vendor, cherry-pick, sync
`.ref/*` hoặc temporary clone vào production code bằng automation.
Scheduled workflow phải pin third-party Actions bằng full commit SHA, chỉ cấp
`contents: read` và `issues: write`, và không nhận thêm secrets.

## Quy trình bắt buộc cho mỗi ticket

### 1. Claim và tạo workspace

1. Query native GitHub dependencies; xác nhận ticket runnable và chưa có
   assignee.
2. Assign ticket trước khi tạo thay đổi để tránh hai agent làm trùng.
3. Fetch `main`, tạo branch/worktree riêng từ `origin/main` mới nhất.
4. Bootstrap Harness trên Windows bằng `.\scripts\bootstrap-harness.ps1`.
5. Record intake và story. Dùng story id ổn định theo issue, ví dụ `GW-044`.
6. Giữ ticket worktree đến khi issue đã close. `harness.db` là local/ignored,
   nên story/trace của ticket phải được hoàn tất trong worktree đã tạo chúng.

Lane mặc định để giảm thời gian phân loại lặp lại:

- #44 là `normal` nếu chỉ triển khai các seam/readiness đã khóa; escalate thành
  `high-risk` nếu chọn dependency slot hoặc thay architecture meaning.
- #45-#70 là `high-risk` vì chạm một hoặc nhiều hard gate: authentication,
  authorization, audit/security, public contract, durable data hoặc external
  Provider behavior, secret isolation, upstream provenance hoặc release supply
  chain.
- Implementation của semantics đã khóa không cần decision record mới. Chỉ tạo
  decision khi một deferred trigger thực sự mở hoặc behavior/architecture cần
  đổi nghĩa.

#44 dùng normal story packet. #45-#70 dùng folder từ
`docs/templates/high-risk-story/` và điền `overview.md`, `design.md`,
`execplan.md`, `validation.md` trước implementation.

### 2. Nạp context có giới hạn

Đọc theo thứ tự:

1. `AGENTS.md`, full issue body/comments và parent #42.
2. Deliverable đã merge của mọi blocker trực tiếp.
3. Chỉ các normative sections được ticket liệt kê.
4. Relevant decisions, stable OpenAPI operation rows và story validation.
5. Code tại public seam và controlled ports bị ảnh hưởng.

Không preload toàn bộ specification set cho mỗi ticket. Ghi exact authority và
public seam vào story packet/PR trước khi viết test.

### 3. Red-green theo public seam

1. Với runtime ticket, viết acceptance test đỏ qua `Runtime.Handler()` hoặc
   exported `JobExecutor`/`RunWorkers` path. Support ticket dùng public
   operational seam ghi trong issue: container lifecycle ở #68, ledger/checker/
   scheduled-report path ở #69, release prepare/dry-run/publish contract ở #70.
2. Test phải assert cả wire result và safe side-effect absence, identity, order
   hoặc count tại controlled ports.
3. Với concurrency/replay/recovery, test phải điều khiển Clock, IDs và race
   ordering; không dùng sleep làm proof chính.
4. Implement smallest vertical behavior để test pass.
5. Refactor chỉ khi suite vẫn xanh và refactor cần cho acceptance criteria của
   chính ticket.

### 4. Draft PR sớm

Draft PR phải có:

- `Refs #N` và parent #42. Không dùng `Closes #N`: issue phải còn mở để ghi
  post-merge proof và Harness trace trước khi close.
- Exact normative sections consumed.
- Public HTTP/worker proof seam cho runtime ticket, hoặc public operational seam
  được issue khóa cho support ticket.
- Side effects được quan sát và các protected effects phải bằng zero khi reject.
- Validation commands đã chạy và kết quả.
- Deferred decisions đã gặp nhưng không tự ý mở lại.

Reviewer kiểm tra test plan/public seam ngay từ draft đối với auth,
authorization, Vault, provider, retry, concurrency hoặc stable contract work.

### 5. Fix findings ngay trong ticket

Builder xử lý mọi actionable finding trước khi chuyển ticket khác. Sau mỗi fix:

1. Chạy focused regression test cho finding.
2. Chạy lại ticket verify command.
3. Yêu cầu reviewer xác nhận finding đã đóng.
4. Không tự resolve review thread mà chưa có proof mới.

## Review gate

Một ticket chỉ đạt review gate khi một reviewer fresh-context xác nhận:

- Diff chỉ chứa scope của ticket và không lẫn deferred work.
- Dependency direction và accepted package boundaries được giữ.
- Behavior khớp exact normative sections và stable wire contract.
- Tenant ownership, non-enumeration và gate ordering được chứng minh trước
  protected access/side effect.
- Retry owner, commit certainty, leases/fencing và accounting không bị nhân đôi.
- Secret/content/Provider payload/internal detail không đi vào prohibited
  projections.
- Runtime tests đi qua real composition/exported worker seam; support tests đi
  qua container, drift-report hoặc release seam công khai của ticket, không dùng
  private shortcut làm completion evidence.
- Negative cases đủ mạnh, deterministic và kiểm tra cả zero forbidden effects.
- Không có Sev 1/Sev 2 hoặc actionable finding chưa xử lý.

Builder self-review là bắt buộc nhưng không thay independent review. Sau rebase
hoặc conflict resolution, reviewer phải xem lại phần diff thay đổi.

## Verify gate

### Fast loop trong lúc code

Chạy formatter, focused package tests và focused contract scenario liên quan.
Mục tiêu là feedback dưới vài phút; không đợi full suite mới phát hiện lỗi cơ
bản.

### Trước khi request final review

Runtime implementation tickets tối thiểu:

```text
gofmt -l trên toàn bộ Go files bị ảnh hưởng phải không có output
go -C apps/gateway vet ./...
go -C apps/gateway test ./...
ticket-specific public HTTP/worker contract tests
scripts/bin/harness-cli.exe story verify <story-id>
git diff --check
```

Chạy thêm khi ticket chạm concurrency, replay, leases, fencing, cancellation,
residual work hoặc shared state:

```text
go -C apps/gateway test -race ./...
```

Nếu race detector không chạy được trong environment, PR phải ghi lý do cụ thể
và CI/reviewer phải cung cấp proof tương đương trước merge; không được bỏ qua
im lặng.

Chạy stable API validation khi ticket thêm hoặc thay behavior của operation,
scope, error, idempotency hoặc response projection:

```text
node scripts/validate-public-api-contract.mjs
node --test scripts/test-public-api-contract-validator.mjs
```

Adapter tickets phải chạy sanitized protocol fixtures, production risk-gate
negative tests và secret scan. #67 chạy toàn bộ Go, race, contract, Adapter,
secret/redaction, dependency-budget và frozen compatibility proof.

#68 phải build image, inspect non-root/read-only/localhost/no-prohibited-mount,
no-host-network/no-privileged, capability-drop/no-new-privileges và resource
limit configuration, chạy readiness/worker/shutdown smoke và chứng minh
cleanup. #69 phải validate ledger, process arguments và bounded report metadata;
chạy deterministic remote fixtures cho unchanged, advanced, rewritten/missing,
unavailable, malformed và deduplicated report; kiểm tra least-privilege/pinned
Actions, sau đó chạy read-only live metadata check không thực thi upstream code.

#70 phải validate changelog fragments và deterministic assembly; chạy stable/
prerelease tag mapping fixtures; build/inspect/scan multi-arch image không push
trên PR; kiểm tra full-SHA Actions, protected triggers, job permissions, OCI
labels, SBOM/provenance, digest consistency, failed-publication rollback và
stable-alias rules. Publish test artifact chỉ khi maintainer phê duyệt.

### Trước merge

1. Rebase lên `origin/main` mới nhất.
2. Rerun ticket verify command và mọi suite bị rebase ảnh hưởng.
3. Xác nhận independent review vẫn clean.
4. Trong ticket worktree, cập nhật final evidence, chạy
   `scripts/bin/harness-cli.exe story complete <story-id>` và ghi implementation
   trace. Nếu lệnh fail, không merge.
5. Dùng squash merge để một ticket có một merge commit dễ bisect/revert.
6. Sau merge, chạy smoke/verify từ `main` nếu ticket thay shared composition,
   contract fixtures, persistence semantics hoặc worker runtime.
7. Comment merge commit/PR cùng post-merge proof lên issue rồi close thủ công;
   sau đó mới xóa ticket worktree.

## Bằng chứng tối thiểu theo nhóm ticket

| Tickets | Bằng chứng bắt buộc ngoài common verify |
|---|---|
| #44 | Compile-time import checks; production/test composition parity; readiness fail-closed; worker lifecycle smoke |
| #45-#52 | Public management HTTP; auth/scope/admission order; non-enumeration; Vault/Adapter/persistence zero-effect counters; replay/race tests |
| #53-#57 | Asset content boundary; atomic reservation; HTTP + exported worker; leases/fencing; commit certainty; no duplicate render; output-only retry |
| #58-#60 | HTTP/SSE ordering; exactly one terminal; cancel/disconnect/timeout; residual occupancy; one retry/fallback owner |
| #68 | Production-composition image; direct-Go default; non-root/read-only; localhost-only; no host network/privilege/capabilities/prohibited mounts/secret ingress; bounded disposable smoke/cleanup |
| #69 | Validated HTTPS URL/SHA/license/Auth Mode/watched-path ledger; structured no-exec drift fixtures; pinned least-privilege deduplicated bounded report; manual reviewed-SHA gate |
| #70 | Validated changelog fragments/SemVer; protected no-push PR path; multi-arch GHCR digest; scan/SBOM/provenance; pinned least-privilege release; stable/prerelease alias and rollback proof |
| #61-#66 | Sanitized Adapter fixtures; production risk gate; exact-account capability evidence; no status inflation; no full-operation retry in Adapter |
| #67 | All 26 operations; frozen `/v1` compatibility; complete negative gate matrix; secret scan; race and dependency-budget review |

## Merge và close checklist

Chỉ merge/close khi tất cả đều đúng:

- [ ] Native blockers đã đóng và branch bắt đầu từ blocker deliverable đã merge.
- [ ] Harness intake/story đúng lane; story packet và validation current.
- [ ] Runtime acceptance tests đi qua public HTTP/exported worker seam; support
      acceptance tests đi qua container, drift-report hoặc release seam đã khóa.
- [ ] Focused tests, full affected suite và story verify pass trên commit cuối.
- [ ] Required race/contract/fixture/security proof pass.
- [ ] Independent review không còn actionable finding.
- [ ] PR đã rebase lên `main` mới nhất và CI xanh.
- [ ] Pre-merge `story complete` và implementation trace pass trong ticket
      worktree; PR không dùng auto-close keyword.
- [ ] PR squash-merged; smoke cần thiết trên `main` pass.
- [ ] Issue có comment tóm tắt merge commit/PR, validation evidence và frontier
      mới, sau đó mới được close thủ công.

## Frontier update sau mỗi merge

Sau mỗi ticket:

1. Query native `blockedBy` relationships, không suy luận chỉ từ file này.
2. Assign ngay ticket critical-path vừa được mở.
3. Rebase các ticket song song còn mở nếu chúng dùng shared composition/ports.
4. Chuyển reviewer sang PR gần merge nhất, không review theo thứ tự bắt đầu.
5. Giữ WIP tối đa ba implementation tickets.

Frontier ban đầu là [#44](https://github.com/monet88/pixelplus/issues/44) và
[#69](https://github.com/monet88/pixelplus/issues/69). Sau khi #44 đóng, bắt đầu
#45 và #68 song song. #68 và #69 không cần chặn core runtime, nhưng cả hai phải
đóng trước khi lấy #61-#66 từ Adapter queue. Sau #67, hoàn tất #70 rồi mới đóng
umbrella #42; actual stable release vẫn là maintainer-approved action.
