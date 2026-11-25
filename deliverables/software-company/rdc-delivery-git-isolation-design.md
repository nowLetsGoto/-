# 交付中心 Git 读取执行器：Windows / macOS 隔离阶段设计

> 职责：补充 `rdc-delivery-design.md` 的 Git 安全架构与交付门槛。输入：用户要求 Windows + macOS 本机部署、从零建设隔离运行环境、完整隔离后才交付；已拍板 B 安全语义及非标准仓库受控允许；S1–S5 仍是硬约束。输出：候选部署路径、威胁模型、验证条件与待提供的环境信息。边界：**架构候选而非已实测的安全证明**；不改变测试/验收/归档设计，不包含实现代码。

## 1. 先给结论

- 当前设计的 `git -C <path> status --porcelain` **不能直接交付**。即使命令 argv 固定，仓库 `.git/config`、`.gitattributes` 中的 `filter.*.process/clean`、`core.fsmonitor` 等可能间接执行程序；`core.worktree`、`.git` 的 `gitdir:`、`commondir`、`objects/info/alternates` 可将读取重定向到允许根之外。`git status` 还可能刷新并写回 index，`--no-optional-locks` 仅针对文档描述的可选后台刷新，不是所有副作用的证明。
- **完整隔离的可行方向**是独立的本地 Git 读取执行器，按平台分别构建最小权限运行环境，主服务不直接启动 Git；实际能否部署，必须在目标 Windows/macOS 版本上通过攻击测试。Windows AppContainer/受限令牌+ACL+Job、macOS 独立 VM/受限 helper 均为**候选**，不是已验证方案。
- **已拍板：B 安全语义。** 主服务仍只发起固定 Git 读取，不接受任意客户端命令；Git 配置触发的 filter/helper 子进程允许在隔离域内派生，但**必须证明**整棵进程树不能读取未授权文件、写宿主/原仓库或联网，且受资源限额约束。B 不等于“子进程绝对不执行”，不能沿用 A 的验收断言；任一 B 边界未验证即 fail-closed。
- 不将 `/usr/bin/sandbox-exec` 存在、Windows API 可调用、只读挂载或 `git status` 命令固定视为完成隔离。不能证明平台安全边界、完整功能与运维可部署时，**Git 读取保持禁用、整版不能声称四个功能均已交付**；用户已拒绝“暂不读取/人工登记”，需向用户明确阻断条件，而不是偷偷降标。

## 2. 可信边界与调用协议

```text
Dandelion 主服务（HTTP/身份/tenant scope）
  → 固定本地 IPC：ReadGit(tenant-scoped repository ID)
  → 可信 broker（按公司配置查绝对路径、解析并固定输入、启动/管理隔离实例）
  → OS-specific 沙箱执行器（只接受固定 ReadSnapshot；内部固定 rev-parse/log/status/branch）
  → 有界、脱敏的 GitSnapshot（10 commits、branch、clean/dirty、changedCount）
```

- 主服务只传受服务端 scope 校验的仓库 ID，不传客户端 op/argv/path；broker 仅以本公司 `system_settings.git_repo_path` 精确取值，缺失时不回退平台/其他公司。平台 `RDC_GIT_ROOTS` 是不可由 UI 改的允许根。
- broker 为可信代码，负责启动沙箱与限额，不把数据库凭据/认证 token/主服务环境传给执行器；IPC 要有同机身份校验、长度上限、请求超时。执行器不得从网络或客户端接收命令，输出仅为结构化且脱敏后的字段；stderr/原始路径/文件名不出边界。
- **只读仓库快照**优于直接挂载活动仓库：在宿主可信 broker 侧先固定真实路径，复制/快照到隔离区域后以只读方式提供给执行器；快照创建过程也不能跟随未授权符号链接或读取白名单外路径，须有句柄级/OS 级路径限制和大小/文件数/耗时上限。**已拍板非标准仓库受控允许**：linked worktree、外部 gitdir/commonDir/alternates、submodule/LFS 等每个真实路径先由运维逐项批准，纳入平台允许根与只读快照许可集合，再对该形态逐类验证；未批准、漏纳入或验证失败即拒绝，不“自动扩权”。快照一致性（HEAD/index/worktree 同一时刻）须在目标文件系统实测；普通逐文件复制不能声称原子快照。
- 若不用快照而直接只读挂载，OS 权限仍须使 `core.worktree`、gitdir、commondir、alternates、软链解析与路径替换无法读出允许集合，并保证 Git 无法写 index/lock；否则 fail-closed。`status` 对只读 index 的行为必须验证，不可假设 `GIT_OPTIONAL_LOCKS=0` 足够。
- 白名单路径使用绝对路径+规范化+`filepath.Rel` 只作应用层预检，**不能单独抵抗 TOCTOU**；实际访问由沙箱文件系统/ACL/句柄边界强制约束。已批准的非标准仓库逐类支持并验证（含 submodule、LFS、linked worktree、外部 alternates）；未批准或尚未验证的形态显式拒绝，不得误报 clean。

## 3. 平台部署候选（均需原型验证）

### Windows：AppContainer + Job + ACL（或更强 VM）

1. 可信 Windows Service broker 以专用低权限身份运行，创建每次读取的隔离实例。候选为 AppContainer/受限令牌、低完整性级别；为其仅授权受控 Git 可执行文件/运行库及只读仓库快照的读取权限。**受限令牌不等于 AppContainer；Job 不提供文件或网络隔离**，各能力需要分别配置和验证。
2. 文件边界：专用快照目录按 ACL 只给隔离 SID 读/遍历，不授予写或主服务数据目录读；宿主其他路径 ACL 拒绝。注意 `Everyone`、用户组继承权限、reparse point/junction、UNC 路径、卷挂载点、8.3/大小写别名、设备路径及硬链接；使用 Windows 句柄/最终路径核验与 ACL 实测，不凭字符串前缀。
3. 网络边界：无网络 capability 的 AppContainer + Windows Filtering Platform/防火墙显式阻断（含 loopback、DNS、IPv4/IPv6），测试不能访问本机服务和外网；**仅“不请求 internetClient”不作为网络隔离证明**。执行器与主服务仅通过受控本地 IPC 通信。
4. 进程树/资源（按已拍板 B）：在启动即暂停或等价安全阶段把进程加入 Job，设置 kill-on-job-close、CPU/内存/活动进程数/时间限制；禁 breakaway，超时关闭 Job 终止整棵树。允许 Git 配置触发的子进程，但必须实测它们继承 AppContainer/ACL/网络边界、不能 break away，且主服务无法通过请求指定任意程序；Job 限额本身不提供文件或网络隔离。
5. 若 AppContainer 下受控 Git/运行库无法运行，或 ACL/网络/子进程限制无法同时满足，改为 Windows Hyper-V/隔离 VM 候选；这会增加安装、授权、镜像、更新与运维成本，不作为自动回退。

### macOS：独立 VM 优先；受限 helper 仅在可验证时采用

1. 优先候选为由主服务外的可信 broker 管理的独立 VM（Apple Virtualization.framework 或经运维批准的等价运行时；宿主 CPU/系统版本/签名与 entitlement 先核实）。VM 内固定受信 Git 与最小 guest，仓库只读快照只以受控共享/磁盘挂载提供；不配置网络设备（再由宿主网络监控验证），无宿主目录共享，VM 磁盘写入只用于可销毁的 guest 临时区；每次任务超时销毁 guest/实例并限内存/CPU/输出。
2. 已拍板 B：VM 候选允许恶意 filter 在 guest 内派生，但必须实测它不能读未授权宿主文件、写宿主/原仓库或联网，且 VM 资源限额能终止整棵进程树；VM 的存在**本身不是**这些断言的证明，也不能自动解决快照时的宿主越界读取。
3. 备选受限 helper：独立低权 macOS 用户 + App Sandbox/系统权限配置 + 宿主只读快照；需验证文件读白名单、写阻断、网络拒绝、所有子进程继承权限且受资源限额约束。已选 B，不要求拒绝后续 exec，但绝不能让子进程逃逸隔离域。`sandbox-exec` 即便存在也是未承诺的候选，不能把 SBPL profile 文字或二进制存在当作可部署保证；若无支持的、可签名/可分发/可维护的机制，就不采用 helper 路径。
4. Intel/Apple Silicon、目标 macOS 版本、是否允许安装 VM guest 与 entitlement/签名均是部署前置；macOS VM 不得被写成 Windows 的通用实现。

## 4. Git 配置与命令边界（沙箱之外仍需）

- `rev-parse` 只用于定位而不作为信任证明；`log` 固定仅输出提交元数据，不请求 patch/diff/textconv；`branch` 固定只读；所有命令禁 pager/terminal prompt/credential helper，清空危险 `GIT_*`/SSH 环境，固定可执行路径与 argv，禁客户端 `-c`。配置 `include/includeIf`、本地 `.git/config`、属性文件、replace refs 可能改变读到的对象或触发额外行为；沙箱边界必须在恶意仓库条件下仍成立。为确定“最近 10 条提交”语义，禁 replace refs 或在契约说明展示的是 Git 解析后的可见历史，并用测试锁定。
- `git status` 可能调用 filter/fsmonitor，也默认可能刷新 index；只读挂载、`--no-optional-locks`、禁 hooks/fsmonitor 不等于无间接执行。**按已拍板 B**，派生程序可以运行，但必须证明只能在 VM/AppContainer 的同一权限边界内运行、不能读未授权文件或写宿主/原仓库/联网。过滤器失败时 Git status 可能报错，必须返回明确失败，**不能将失败或不完整扫描当作 clean**。
- 当前公司配置仓库未设置/越界/隔离启动失败分别返回 `not_configured`/`path_forbidden`/安全失败状态，禁裸 stderr/完整路径/凭据回显。无沙箱时**不得回退普通 CLI**。

## 5. 可验证验收矩阵与失败关闭

| 类别 | 攻击输入 / 验证 | 通过标准 |
| --- | --- | --- |
| 派生进程 | `.gitattributes` + `filter.*.process/clean` 写哨兵/访问本机 HTTP；fsmonitor、pager、diff textconv、credential helper、includeIf 注入 | 已拍板 B：允许派生，但必须继承隔离域、无法写宿主/原仓库、联网或读未授权文件；Git 的成功/失败状态真实 |
| 路径逃逸 | `core.worktree` 指根外；`.git` gitdir、commondir、alternates 指根外；symlink/junction/reparse point、UNC/卷挂载、路径并发替换 | OS/VM 拒绝根外读，未泄漏路径；不可补挂/自动扩权 |
| 只读 | `git status` 尝试 index.refresh/lock；filter 写仓库/宿主、创建新文件 | 原仓库与快照均不可写；无 index/lock、无宿主哨兵；B 仅允许可销毁 guest 临时区写 |
| 网络 | filter/helper 访问 loopback、DNS、IPv4/IPv6、外网；本机 IPC 冒用 | 无网络连接；IPC 限本地可信身份，不能发任意路径/argv |
| 资源与故障 | 长 commit/status、无穷 filter、超时、CPU/内存/进程数、broker/VM 启动失败 | 字节/时间/CPU/内存/进程树限额生效；超时后无孤儿；初始化失败关闭，绝不回退 CLI |
| 结果正确性 | 普通/dirty/untracked、逐项获批的子模块/LFS/linked worktree/外部 gitdir/alternates、split/sparse index、packed refs 与 SHA-256 仓库 | 已获批路径全部纳入只读快照并逐类与受信 Git 对照；未获批或未验证形态显式报错，不误报 clean |
| 发布环境 | Windows/macOS 各目标版本、正常与恶意仓库集成测试；升级 Git/OS 后回归 | 两平台分别通过，同一生产镜像/签名/配置可复现；未验证平台禁用 Git 功能 |

每项都须保存测试环境版本、沙箱策略/镜像哈希、拒绝事件、无副作用证据。攻击测试的“没看到输出”不足以证明无写/无联网/无越界读取，须在宿主/guest/OS 事件或 ACL 拒绝日志层观测。策略更新、Git/OS 升级、允许根变更均需重新验收；实例启动失败/配置漂移/策略加载失败一律 fail-closed。

## 6. 分阶段任务与运维前置

1. **隔离可行性原型（新的安全阻塞阶段，先于既有设计 T02）**：待用户提供目标 Windows/macOS 版本、VM/签名权限、测试机及仓库真实路径后，分别实现最小 broker + 真实沙箱，验证固定 Git 正常读与已拍板 B 攻击矩阵。只产出可复现报告，不先接业务接口。任一平台失败则整体 Git 交付阻断。**目前未取得环境信息，也未进行双端原型验证。**
2. **执行器与部署**：B 边界在目标环境验证通过后，才实现 Windows/macOS 专属 launcher/沙箱策略和受控 IPC，最小权限安装/签名/账号/ACL 或 VM 镜像/快照/只读共享；主服务只注入执行器接口。无隔离启动时返回安全失败，不回退 CLI。
3. **业务整合**：在原 `rdc-delivery-design.md` 的 Git reader T02/T03/T04 基础上接入执行器，保留公司配置/env 双层白名单、脱敏、超时、历史/验收/归档原设计；跨平台安全回归作为放行门槛。原“零新增依赖”仅适用于业务代码：隔离运行时/VM/签名/安装器/平台 API 是新的**部署依赖与工程成本**，不能再宣称整体零新增。

运维必须提供：Windows 版本/服务身份/可授予 ACL 与 AppContainer 的安装权限、目标 Git 发行版与签名；macOS 架构/版本/签名 entitlement/VM 安装权限；每公司**真实** worktree/gitdir/commonDir/alternates 所在目录、允许快照的文件系统、快照容量/时延/一致性要求；网络策略及日志采集能力。仅给出 `/usr/bin/sandbox-exec` 路径不算提供运行环境。

## 7. 已决策与待提供信息

**用户已拍板**：

1. **B 安全语义**：允许 Git 配置触发的子进程在隔离域内派生；主服务仍只发固定白名单 Git 读取，子进程不得读未授权文件、写宿主/原仓库或联网，必须受限额并证明不能逃逸。不能把“子进程绝对不执行”作为 B 的验收口径。
2. **非标准仓库受控允许**：linked worktree、外部 gitdir/commonDir/alternates、submodule/LFS 等可纳入，但必须由运维逐项批准**真实路径**、加入平台允许集合与只读快照，并对每种形态分别验证；未批准/未验证默认拒绝，不能误报 clean。
3. **Windows + macOS 两端均须交付**：本项目从零建设隔离环境；不是仅靠现成 `sandbox-exec` 或配置约束。

**尚待用户/运维提供，收到后才能设计可执行原型**：Windows/macOS 最低版本与测试机、macOS CPU 架构、VM/签名/entitlement/服务账号与安装权限、Git 发行版、每公司实际仓库及其 worktree/gitdir/commonDir/alternates/submodule/LFS 的真实路径、快照文件系统与容量/时延要求、可用网络策略和系统事件日志能力。缺这些前置，不设计或实现未经验证的 launcher，不宣称两端通过；任务 #6「设计并验证」继续进行中。

## 8. 依据与剩余风险

- Git 官方：`git status` 默认刷新并可能写回 index，`--no-optional-locks` 用于避免背景刷新锁，并非全命令安全声明：<https://git-scm.com/docs/git-status#_background_refresh>。
- Git 官方：filter 的 `clean`/`smudge` 是外部转换命令，`process` 优先于单文件 filter；不能用固定 CLI argv 推出没有派生命令：<https://git-scm.com/docs/gitattributes#_filter>。
- Git 官方：`.git` 可为 `gitdir:` 文件，`commondir`、packed refs、`objects/info/alternates` 可重定向元数据/对象读取：<https://git-scm.com/docs/gitrepository-layout>。`core.worktree` 的根外指向是本次工程攻击向量，须在目标 Git 版本实测，不把该页误称其定义来源。
- Windows 官方候选能力：AppContainer 的配置与启动 <https://learn.microsoft.com/en-us/windows/win32/secauthz/implementing-an-appcontainer>；Job Objects <https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects>。这些是组件说明，不是组合后安全性的证明。
- Apple 官方候选能力：Virtualization.framework <https://developer.apple.com/documentation/virtualization>。框架存在不等于目标平台可安装、可签名、可只读共享或可禁止子进程。

剩余风险包括：沙箱逃逸与 OS/虚拟化漏洞、快照 TOCTOU/不一致、Git 新版本改变配置行为、ACL/网络策略漂移、恶意仓库资源耗尽、宿主 broker 被攻破。依靠最小权限、独立进程/VM、攻击回归、版本固定、策略审计和失败关闭降低风险；**不能宣称绝对零风险**。未验证目标平台的完整隔离前禁止启用 Git 读取。
