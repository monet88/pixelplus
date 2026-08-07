---
status: historical-research
canonical: false
superseded_by:
  - GitHub issue tracker (native `blocked by` / `sub-issue` relationships)
  - docs/ARCHITECTURE.md
  - CONTEXT.md
---

# PixelPlus Ticket Execution Playbook

> **Historical execution playbook.** The front matter above marks this file as
> a historical record of how Gateway runtime tickets #44-#70 and later lanes
> were executed. It is **not** live state: GitHub is the authoritative tracker,
> and `docs/ARCHITECTURE.md` + `CONTEXT.md` are the authority for system shape.
> The snapshots below describe what was true at the time of writing (2026-08-06)
> and must not be read as current.

- Cập nhật: 2026-08-06
- Phạm vi: [#42 - Build the Pure-Go Provider Gateway](https://github.com/monet88/pixelplus/issues/42),
  [#95 - Photoshop UXP Plugin](https://github.com/monet88/pixelplus/issues/95)
  và lane enforcement ENF-01..ENF-07.

Native GitHub `blocked by` và `sub-issue` relationships là nguồn chính xác cuối
cùng. File này chỉ là execution guide và **không** phải nguồn trạng thái. Khi
file này và GitHub bất đồng, GitHub đúng.

Mục tiêu không phải tăng số ticket đang làm cùng lúc. Mục tiêu là rút ngắn
critical path trong khi mỗi ticket vẫn được review độc lập, verify qua public
seam, merge sạch, ghi Harness evidence và chỉ đóng sau khi proof đầy đủ.

## Trạng thái tại thời điểm viết (snapshot 2026-08-06)

Tại thời điểm snapshot: Gateway runtime spine đã xong — #44-#61 và #68 đã đóng,
nghĩa là composition, Provider Account lifecycle, OAuth application layer,
Capability, Health, Routing, Asset, Render và Chat đều đã merge, cùng Docker
live-probe sandbox. Các câu sau là snapshot lịch sử, không phải trạng thái hiện
tại: kiểm tra GitHub để biết frontier và trạng thái thật.

Ba lane đang chạy song song tại thời điểm đó, không lane nào chặn lane nào:

| Lane | Nội dung | Frontier |
|---|---|---|
| Gateway adapters | #62-#67, #88, #111, #112, #121, #122 | #62, #63, #65 |
| Plugin | #114-#120, #98-#110 | #114 |
| Enforcement | #123-#129 | #123, #124, #126 |

Tại thời điểm snapshot, enforcement lane được khuyến nghị chạy **trước** khi
Plugin lane phình to. Vào clean HEAD hôm đó: `.github/workflows/` không có
workflow nào được track, `gofmt -l apps/gateway` in ra hơn 10 file, và
`node scripts/validate-provider-gateway-implementation-spec.mjs` fail vì
fingerprint `CONTEXT.md` không khớp. Mỗi surface thêm vào lúc đó là một surface
mà enforcement chưa bảo vệ.

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
    khi #67 hoàn tất. Docker ở #68 là sandbox local dùng lại production
    composition; #70 chỉ tạo release foundation, không tự chọn production
    topology hoặc tự publish stable release.
11. #67 **không** chặn enforcement lane hay Plugin lane. Chờ full runtime
    conformance mới bắt đầu chặn code chưa format, authority drift hoặc test
    regression là đảo ngược thứ tự: enforcement càng muộn thì càng nhiều surface
    phải bảo vệ ngược. #123, #124, #125, #126 đều runnable ngay.

## Mô hình nhân lực nhanh nhất

Với bốn agent/context hoạt động đồng thời:

| Slot | Trách nhiệm |
|---|---|
| Builder A | Gateway lane: adapter queue hoặc readiness gate |
| Builder B | Plugin lane: #114, sau đó frontier Plugin |
| Builder C | Enforcement lane, hoặc xử lý review findings |
| Reviewer | Review draft/final PR, kiểm tra spec compliance và rerun proof |

Reviewer không review code do chính mình viết. Khi không có PR chờ review,
reviewer chuẩn bị fixture/proof matrix, kiểm tra native frontier hoặc review
acceptance-test plan của ticket high-risk kế tiếp; không tự bắt đầu ticket bị
block.

Nếu chỉ có một agent, giữ nguyên thứ tự merge bên dưới và dùng fresh context
cho review sau khi implementation xong. Nếu có nhiều hơn bốn agent, không tăng
WIP tùy ý: thêm reviewer/verification capacity trước, sau đó mới tăng builder.

## Lịch thực thi

### Đã hoàn tất

`#44` composition spine, `#45` protected request spine, `#46`-`#49` Provider
Account lifecycle, `#47` server-owned OAuth application layer, `#50` Capability
Snapshots, `#51` health/controls, `#52` Routing Policy, `#53` Tenant Assets,
`#54`-`#57` Render, `#58`-`#60` Chat, `#61` ChatGPT Web Access Adapter, `#68`
Docker live-probe sandbox.

Lưu ý về #47: nó cung cấp application-level start/poll/account lifecycle qua
stable HTTP. Nó **không** chứng minh rằng một Provider-specific OAuth exchange
đã được compose trong production. Khi `Dependencies.OAuth` là nil,
`composition/runtime.go` vẫn thay bằng `NewFailClosedOAuthExchangeAdapter()`, và
`ProductionDependencies()` không bao giờ set nó.

### Lane A - Gateway adapters và readiness

Adapter queue còn lại, lấy theo shared work queue chứ không chia wave cứng:

- [#62 - ChatGPT Codex OAuth](https://github.com/monet88/pixelplus/issues/62) — chat/stream/probe/capability đã compose tại thời điểm snapshot; render đã defer sang #112
- [#63 - Gemini Web Cookie](https://github.com/monet88/pixelplus/issues/63)
- [#64 - Gemini Antigravity OAuth](https://github.com/monet88/pixelplus/issues/64)
- [#65 - Prove Grok Web SSO remains prohibited](https://github.com/monet88/pixelplus/issues/65)
- [#66 - Grok xAI OAuth](https://github.com/monet88/pixelplus/issues/66)

Song song, không chặn adapter queue:

- [#111 - Bind the probe and capability ports to a per-account credential](https://github.com/monet88/pixelplus/issues/111) — security blocker. Transport hiện là deployment-wide nên probe account B có thể đi trên session account A. Không wire real Transport cho gated mode nào trước khi cái này đóng.
- [#112 - Serve the gated ChatGPT Codex render and masked image-edit surface](https://github.com/monet88/pixelplus/issues/112) — RenderAdapter, gated render registry và relaxation của render candidate gate, cả ba trong cùng một change theo decision 0014.
- [#88 - Durable chat replay ledger](https://github.com/monet88/pixelplus/issues/88)
- [#98 - Lock the canonical Mask Convention](https://github.com/monet88/pixelplus/issues/98) → [#121 - Reject non-PNG masks at asset ingest](https://github.com/monet88/pixelplus/issues/121)
- [#99 - Publish the Auth Modes a deployment permits](https://github.com/monet88/pixelplus/issues/99) — cần điền bảng quyết định wire contract trước khi giao agent

Hai gate cuối lane:

1. [#122 - Gateway Image Readiness Gate](https://github.com/monet88/pixelplus/issues/122), sau #111 và #112. Đây là blocker thật của Plugin #106.
2. [#67 - Prove full stable Public API runtime conformance](https://github.com/monet88/pixelplus/issues/67), sau khi #62-#66 merge và drift gate #69 vẫn xanh.

Không dùng live credential trong fixture hoặc CI. Live probe chỉ chạy trong
sandbox #68 khi có ủy quyền rõ ràng cho đúng Auth Mode, account và environment.
Controlled fixture proof phải pass trước live probe. Grok Web SSO vẫn bị cấm và
không có live probe.

### Lane B - Photoshop Plugin

Frontier duy nhất tại thời điểm snapshot là
[#114 - P00 foundation](https://github.com/monet88/pixelplus/issues/114). Mọi
ticket Plugin khác đều chờ nó, vì nếu không thì agent đầu tiên nhận việc sẽ mặc
nhiên chọn framework, build system, state model, folder layout, OpenAPI
generator và packaging thay cho cả team.

Sau #114, ba lane chạy song song:

```text
#114 P00 Foundation
├── #100 P01 Geometry
├── #115 P02A Target Rect mapping + PNG mask codecs
└── #103 P04 Secure Connection storage

#100 + #115
    -> #116 P02B Export Context Rect và capture selection
    -> #117 P02C Place, mask, verify từ local fixture     <- local-fixture vertical proven

#117 + #103 + một Direct Provider ĐÃ ĐƯỢC NÊU TÊN
    -> #104 P05 Direct Path edit hoàn chỉnh
    -> #105 P06 Phase, elapsed, cancel, recent runs       <- designer hoàn tất một edit không cần Gateway

#99 -> #108 P09 Connection catalogue
#122 -> #106 P07 Gateway Path E2E -> #107 P08 Resume after reopen
#108 + OAuth exchange thật + #111 -> #109 P10 Sign-in

Enhancements sau cùng: #118 variants, #119 reference images, #120 local data
```

Hai ticket **không** được giao agent cho tới khi điền xong quyết định sản phẩm
trong chính issue body:

- #104 cần bảng: Provider, credential class, operation, model, wire shape, mask
  convention của surface đó, output format, cancel semantics, error classes.
- #99 cần bảng: method/path, response shape, enum policy đóng hay mở, reason
  khi Tenant chưa có Provider Account, scope, cache semantics.

#101 là capability research cho ChatGPT Codex OAuth, không phải Plugin ticket,
và **không** chặn #109. Nó chỉ gate việc offer `inpaint`/`image_edit` trên một
account đã kết nối.

#102 và #110 đã chuyển thành split parent; không implement từ chúng.

### Lane C - Enforcement

Chạy trước khi Plugin lane phình to:

```text
Wave 1
├── #124 ENF-02 authority fingerprint reconciliation   <- cần người, không giao agent
├── gofmt cleanup                                       <- commit riêng, trước #123
└── #123 ENF-01 basic required CI

Wave 2
├── #126 ENF-04 canonical verify + pinned toolchain
├── #125 ENF-03 sandbox lifecycle correction
└── dependency consistency check giữa GitHub và Harness

Wave 3
├── #127 ENF-05 architecture và agent authority cleanup
├── #128 ENF-06 mutation-suite optimization
└── #129 ENF-07 governance
```

#124 và #127 cố ý không mang `ready-for-agent`. #124 đặc biệt nguy hiểm nếu giao
agent: có sẵn `scripts/refresh-provider-gateway-implementation-spec-contract.mjs`
làm validator xanh lại mà bỏ qua toàn bộ review, biến một authority gate thành
con dấu.

Mỗi gate phải có negative control chứng minh nó thật sự quan sát được failure.
Job xanh không phải bằng chứng:

```text
format gate       -> cố ý làm lệch gofmt và thấy fail
fingerprint gate  -> sửa authority không refresh và thấy fail
sandbox gate      -> giữ marker ngoài opt-in và thấy fail
readiness gate    -> gắn ready label khi blocker mở và thấy check fail
```

Mutation phải revert sau proof, không commit vào product branch.

### Lane D - Release foundation

[#70 - Establish changelog and versioned Docker releases](https://github.com/monet88/pixelplus/issues/70),
sau khi #67 đóng. #68 đã đóng.

Phạm vi #70 đã được thu hẹp: basic PR CI (`gofmt`, `go vet`, `go test`, Public
API validator, authority validator) **không** thuộc #70 mà thuộc #123, và không
được chờ #67. Chặn code chưa format không cần release pipeline.

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
- Plugin tickets: #114 và #100/#115/#116/#117/#105/#118/#120 là `normal`;
  #103, #104, #106, #107, #108, #109, #119 là `high-risk` vì chạm credential
  storage, Provider egress, public contract hoặc durable local state.
- Enforcement tickets: #123, #125, #126, #128, #129 là `normal`; #124 và #127 là
  `high-risk docs/authority` và cần human authority review, không giao agent.
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
| #111, #112, #122 | Exact-account credential binding; pixel-level mask conversion cả hai chiều; masked edit để pixel ngoài mask byte-identical; failed render terminal `failed` với zero output entries; readiness state chạy được bằng một lệnh |
| #114-#120 | Pure image seam (rects/bytes in, rects/bytes out); application/run-state seam với fake clock và substituted transport; client/contract seam cho drift, decoding, timeout, abort; Photoshop host layer verify trên host thật theo checklist |
| #123-#129 | Negative control cho từng gate: mutation cố ý làm gate fail rồi revert. Job xanh không phải bằng chứng gate hoạt động |

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

Frontier tại thời điểm snapshot, ba lane song song:

| Lane | Lấy ngay | Lý do |
|---|---|---|
| Enforcement | #124, gofmt cleanup, rồi #123 | Chạy trước khi thêm surface mới; #124 cần người |
| Plugin | #114 | Mọi ticket Plugin khác chờ nó |
| Gateway | #62, #63, #65 từ adapter queue; #111 song song | #111 chặn real Transport cho mọi gated mode |

Thứ tự mở gate còn lại:

```text
#111 + #112 -> #122 -> #106 -> #107
#62..#66    -> #67  -> #70  -> đóng umbrella #42
#114        -> Plugin lane
```

Actual stable release vẫn là maintainer-approved action, không tự động.

## Giới hạn của file này

Đây là execution guide, không phải nguồn trạng thái. Các dòng `Blocked by` trong
issue body hiện vẫn chỉ là Markdown: GitHub API báo `blocked_by=0` cho toàn bộ
#100-#110, nên scheduler không tự động tôn trọng thứ tự. Trước khi dựa vào
dependency order, kiểm tra native relationships hoặc Harness story graph, đừng
suy luận chỉ từ file này.
