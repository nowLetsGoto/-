# Cloudflare R2 配置（下载加速）

## 为什么需要它

安装包存在本机磁盘（权威副本），但**下载要从本机出带宽**。家宽上行小、云主机出带宽 3Mbps，
下载一个 119MB 的包要 5 分钟。

R2 的作用：把包镜像一份到 Cloudflare，下载时后端返回 **302 跳转到 R2 直链**，
文件字节由 Cloudflare 下发，**不占用本机带宽**。R2 的**出流量免费**，只有存储费（¥0.1/GB/月级别）。

## 关键设计：磁盘是权威，R2 只是加速

| 行为 | 说明 |
|---|---|
| 上传 | 仍然先落本机 `storage/`，成功后**后台异步**传 R2 |
| 下载（已镜像） | 302 跳到 R2 签名直链，本机不参与传输 |
| 下载（未镜像） | 走本机流式，**同时后台补传**，下次起走直链 |
| R2 故障/断网 | 自动回落本机流式，下载不中断 |
| 未配 R2 | 完全等同改造前行为 |

所以 **R2 挂了不会导致平台不可用**，只是下载变回原来的慢速。

## 配置步骤

### 1. 创建桶

Cloudflare 控制台 → **R2** → **Create bucket**

- 名称：如 `dandelion-builds`
- 位置：选 **Automatic** 即可（R2 全球任播，无需选地域）
- 完事后进桶 → **Settings** → 记下 **S3 API** 端点里的 Account ID

### 2. 创建 API 令牌

R2 → **Manage R2 API Tokens** → **Create API token**

- 权限：**Object Read & Write**
- 范围：**Only apply to specific buckets** → 选你刚建的桶（最小权限）

创建后会显示一次：
- **Access Key ID**
- **Secret Access Key**
- **Account ID**（页面顶部）

**Secret 只显示一次**，立即保存。

### 3. 免费额度（通常够用）

| 项 | 免费额度 |
|---|---|
| 存储 | 10 GB / 月 |
| A 类操作（写） | 100 万次 / 月 |
| B 类操作（读） | 1000 万次 / 月 |
| **出流量** | **完全免费，无限制** |

**出流量免费是 R2 相比腾讯云 COS 的核心优势**——COS 的流量费才是主要成本。

### 4. 填入服务器 `.env`

登录服务器编辑 `~/dandelion/.env`，追加：

```bash
nano ~/dandelion/.env
```

```ini
R2_ACCOUNT_ID=你的账号ID
R2_ACCESS_KEY_ID=你的AccessKeyID
R2_SECRET_ACCESS_KEY=你的SecretKey
R2_BUCKET=dandelion-builds
# 可选，见下节
R2_PUBLIC_BASE_URL=
```

保存（`Ctrl+O` 回车，`Ctrl+X` 退出），然后重启：

```bash
cd ~/dandelion && sudo docker compose up -d server
```

### 5. 验证生效

```bash
cd ~/dandelion && sudo docker compose logs --tail=20 server | grep 对象存储
```

应看到：

```
对象存储镜像已启用 bucket=dandelion-builds
```

然后上传一个新包，观察日志出现 `R2 同步完成`：

```bash
sudo docker compose logs -f server | grep R2
```

## 关于 R2_PUBLIC_BASE_URL（建议配）

默认用 `https://<account>.r2.cloudflarestorage.com` 端点，**国内访问较慢**（走 Cloudflare 海外节点）。

配自定义域名能显著改善：

1. 在 Cloudflare 上有个域名（如 `files.example.com`，需要在 Cloudflare 托管）
2. R2 桶 → **Settings** → **Public access** → **Connect Domain** → 填 `files.example.com`
3. `.env` 里设 `R2_PUBLIC_BASE_URL=https://files.example.com`

**注意**：绑自定义域名后桶会变为公开可读（Cloudflare 要求），任何人拿到对象路径都能访问。
如果需要严格防盗链，就不绑域名，接受稍慢的速度——签名 URL 机制本身仍能防止路径被猜。

## 本地开发怎么用

`.env` 里填同样五项，`make dev` 启动后即生效。

不填则完全不启用，下载走本地磁盘，行为和改造前一致。

## 数据流概览

```
上传：浏览器 → 服务器（落盘 storage/）→ 异步 → R2
                                      ↓
下载：浏览器 ←──── 302 跳转 ──── 服务器（只签名，不传文件）
              ↓
             R2 直链（文件字节从这里走）
```

## 数据库变更

新增列 `team.builds.r2_key` / `solo.builds.r2_key`（TEXT NOT NULL DEFAULT ''）。

**DDL 已 embed 进二进制**，进程启动时幂等执行，无需手工迁移。空串表示尚未镜像。

## 成本估算

| 场景 | 费用 |
|---|---|
| 存 882 MB（你现在的全部包） | 免费额度内（10 GB） |
| 每月下载 100 次 × 119 MB | **￥0**（出流量免费） |
| 超过 10 GB 存储后 | 约 $0.015/GB/月 |

---

## 浏览器直传（绕开 Tunnel 100MB 上限）

### 为什么需要

`dandelion.dreamgarden.world` 走 Cloudflare Tunnel，免费版**请求体上限 100 MB**。
超过直接 413，300 MB 的包传不上去。

直传方案让浏览器拿着预签名 URL **直接 PUT 到 R2**，字节完全不经过本机和 Tunnel，
只受 R2 自身限制（单次 PUT 5 GiB）。因此**不需要分片**。

### 流程

```
1. 前端选文件 → POST /builds/upload-ticket（小请求，走 Tunnel）
                  返回 putUrl + 必需请求头 + uploadToken
2. 前端 XHR PUT putUrl（大文件，直连 R2，带进度条）
3. 前端 POST /builds/stage-from-r2
      后端校验对象存在、大小与声明一致 → 回拉本地 → 解析 → 返回 stageToken
4. 前端 POST /builds（原有 commit，不变）
```

对象已在 R2，commit 时直接把 `r2_key` 指向它，**不会二次上传**。

### ⚠️ 必须配置 CORS（否则浏览器拒绝上传）

R2 桶 → **Settings** → **CORS Policy** → 粘贴：

```json
[
  {
    "AllowedOrigins": [
      "https://dandelion.dreamgarden.world",
      "http://localhost:5173"
    ],
    "AllowedMethods": ["PUT", "GET", "HEAD"],
    "AllowedHeaders": ["*"],
    "ExposeHeaders": ["ETag"],
    "MaxAgeSeconds": 3600
  }
]
```

**没有这段配置，浏览器控制台会报 CORS 错误，PUT 直接失败。**

### 自动回落

未配置 R2 时 `/builds/upload-ticket` 返回 400，
前端会**静默回落**到原来的 multipart 上传（`/builds/stage`）。
所以本地开发不需要 R2 也能正常上传小包。

其余错误（网络中断、大小不符、签名过期）会明确提示用户，不会静默回落——
否则用户会以为成功了，实际传的是另一个文件。

### 上限对照

| 通道 | 单文件上限 | 适用 |
|---|---|---|
| multipart（Tunnel） | **100 MB** | 小包、本地开发 |
| 直传 R2 | **5 GiB** | 大包（300MB 无压力） |

服务端 `MAX_UPLOAD_MB` 默认 200，直传时同样受它约束——
要传 300 MB 需要把它调到 `300` 以上（`.env` 里改）。

---

## 实测速度数据（2026-11-26，本机 Mac → Cloudflare R2）

测试对象：100MB 二进制文件，桶 `api-4`，走 `r2.cloudflarestorage.com` 签名地址。

| 操作 | 耗时 | 速度 | 说明 |
|---|---|---|---|
| **上传**（Mac → R2） | 199 秒 | **0.50 MB/s** | 受 Mac 上行带宽限制（家宽典型值） |
| **下载**（R2 → Mac 公网签名地址） | 180 秒 | **0.55 MB/s** | ⚠️ 远低于 R2 能力 |

### 为什么下载这么慢

R2 本身不限速、出流量免费。但**签名 URL 用的是 S3 API 端点**
（`https://<account>.r2.cloudflarestorage.com`），这个域名在国内走海外路由，
延迟高、速度差，实测只有 0.55 MB/s。

### 提速方案：绑定自定义域名（强烈建议）

把 `download.dreamgarden.world` 绑定到 R2 桶，走 Cloudflare CDN 国内节点：

```
R2 桶 api-4 → Settings → Public access → Custom domain → download.dreamgarden.world
```

绑定后，把 `R2_PUBLIC_BASE_URL=https://download.dreamgarden.world` 写进 `.env`，
代码里的 `rewriteHost()` 会把签名 URL 自动换成自定义域名。

**注意：自定义域名要求桶公开读**。若仍要私有，只能接受 S3 端点的慢速。
折中方案：公开读 + 随机长对象名（UUID），靠"猜不到"防盗链。

绑定后预期速度：**5-20 MB/s**（取决于 Cloudflare 国内节点和用户本地带宽），
比 S3 端点快 10-30 倍。100MB 包从 180 秒降到 5-20 秒。

### 判断依据

官方明确说明（R2 Presigned URLs 文档 Custom domains 章节）：
> Presigned URLs work with the S3 API domain and **cannot be used with custom domains**.

即签名 URL 与自定义域名互斥——若要走 CDN 自定义域名，桶必须公开读，
下载时用公开的 `https://download.dreamgarden.world/<key>` 而非签名 URL。
代码里的 `SignedGetURL()` 已预留 `R2_PUBLIC_BASE_URL` 参数，
配置后会自动把签名参数挂到自定义域名（适用于公开桶场景）。
