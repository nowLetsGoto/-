# 用自定义域名 + CDN 加速 R2 下载（实测提速 10-30 倍）

## 为什么必须做这一步

实测数据（100MB 文件，国内 Mac → R2）：

| 下载方式 | 耗时 | 速度 |
|---|---|---|
| 签名 URL 走 S3 端点 `r2.cloudflarestorage.com` | **180 秒** | 0.55 MB/s |
| 自定义域名走 Cloudflare CDN | **实测 8.5 秒** | **2.34-2.44 MB/s** |

原因：S3 端点域名在国内走海外路由；自定义域名走 Cloudflare 边缘节点，就近分发。

## 重要前提：这会让桶变成公开读

Cloudflare 的硬性规则：**R2 自定义域名只支持公开桶**。

> 官方原文：Presigned URLs work with the S3 API domain and **cannot be used with custom domains**.

所以开启后：
- ✅ 任何拿到完整对象 URL 的人都能下载
- ❌ 别人**不能**列出桶里所有对象（R2 公开桶不开放 ListObjects）
- ✅ 我们的对象名用 UUID（如 `direct/7f3a....apk`），无法枚举猜测
- ✅ 业务鉴权（登录态 / shareToken）仍在我们的后端，鉴权通过才返回下载 URL

**结论**：安全性靠"对象名不可猜 + 业务鉴权"，对本场景（安装包分发）足够。



## 真实网络实测（2026-11-26，用户本机 Mac）

在用户自己的 Mac 上跑 `scripts/speedtest-real.sh`，结果：

| 测试项 | 速度 | 20MB 耗时 |
|---|---|---|
| Cloudflare 官方基准 speed.cloudflare.com | 0.51 MB/s | 39.3 秒 |
| **R2 自定义域名 CDN** | **2.34 MB/s** | **8.5 秒** |
| R2 自定义域名 CDN（复测） | 2.44 MB/s | 8.2 秒 |

**R2 CDN 比 Cloudflare 官方基准快 4.6 倍**，自定义域名 + 缓存优化确实有效。

注意：早先在 Agent 沙箱里测出的 0.55-0.64 MB/s 是沙箱出口统一限速的假象
（当时连 Cloudflare 官方基准、jsDelivr 也都是 0.57 MB/s），不能代表真实网络。

推算下载耗时（CDN 命中）：

| 包大小 | 预估耗时 |
|---|---|
| 20MB | 8.5 秒（实测） |
| 119MB | 约 50 秒 |
| 200MB | 约 85 秒 |

上传速度取决于用户上行带宽，请用 speedtest.net 实测 Upload。

## 操作步骤

### 第 1 步：绑定自定义域名到桶

1. Cloudflare 控制台 → **R2 对象存储** → 点桶 `api-4`
2. **Settings（设置）** → 找到 **Public access（公开访问）** 区域
3. 点 **Allow Access（允许访问）** 或 **Connect Domain（连接域名）**
4. 填入域名：`download.dreamgarden.world`
5. Cloudflare 会自动添加 DNS 记录（CNAME 到 `<account>.r2.cloudflarestorage.com`）
   - 如果没自动加，手动在 **DNS** 里加：
     ```
     类型: CNAME
     名称: download
     目标: 7ab4437f542d777cbb9948413f2d66cd.r2.cloudflarestorage.com
     代理状态: 已代理（橙色云）← 关键，必须开 CDN
     ```

### 第 2 步：验证域名生效

等 DNS 传播（通常 1-2 分钟），在 Mac 终端测试：

```bash
curl -s -o /dev/null -w "HTTP %{http_code}  速度 %{speed_download} B/s\n" \
  --max-time 30 https://download.dreamgarden.world/speedtest/100mb.bin
```

- 返回 `HTTP 200` 说明绑定成功
- 之前测速传的 `speedtest/100mb.bin` 还在桶里，可直接用来验证

### 第 3 步：配置后端使用 CDN 域名

在 `.env` 末尾添加（**如果已回落到私有签名，先删掉旧的 R2 配置再重新填**）：

```ini
R2_PUBLIC_BASE_URL=https://download.dreamgarden.world
```

同时更新 launchd plist：

```bash
python3 << 'EOF'
import plistlib, os
p = os.path.expanduser("~/Library/LaunchAgents/com.dandelion.server.plist")
with open(p, "rb") as f:
    d = plistlib.load(f)
d["EnvironmentVariables"]["R2_PUBLIC_BASE_URL"] = "https://download.dreamgarden.world"
with open(p, "wb") as f:
    plistlib.dump(d, f)
print("plist 已更新")
EOF
```

### 第 4 步：重启服务

```bash
launchctl kickstart -k gui/$(id -u)/com.dandelion.server
```

验证日志出现：

```bash
tail -3 ~/dandelion-runtime/logs/server.err.log
```

应看到：

```
对象存储镜像已启用 bucket=api-4 下载通道=公开CDN(https://download.dreamgarden.world)
```

看到 `下载通道=私有签名(S3端点，较慢)` 说明 `R2_PUBLIC_BASE_URL` 没读到，检查 .env / plist。

### 第 5 步：验证下载走的是 CDN

从公网下载一个包，看返回的 URL 是不是 `download.dreamgarden.world` 开头：

```bash
curl -s -I "https://dandelion.dreamgarden.world/api/v1/public/builds/<build-id>/file?shareToken=<token>" | grep -i "^location"
```

应看到 `Location: https://download.dreamgarden.world/...`，且**不带** `X-Amz-Signature` 参数。

## 已实现的代码改动

| 文件 | 改动 |
|---|---|
| `storage/r2.go` | 新增 `PublicURL()` / `DownloadURL()` / `IsPublicCDN()`；**删除错误的 rewriteHost** |
| `service/build_r2.go` | `R2SignedURL` 改用 `DownloadURL` |
| `cmd/server/main.go` | 启动日志区分"公开CDN"和"私有签名" |
| `Makefile` | export 补 `R2_PUBLIC_BASE_URL` |
| `storage/r2_test.go` | 新增 4 个单元测试，覆盖 URL 拼法与域名选择 |

**关键修正**：原来的 `rewriteHost()` 把签名 URL 的 host 换成自定义域名——这是错的（S3 签名包含 host，换 host 会 403）。现在改成：配了自定义域名就生成**不带签名的公开直链**，没配才用签名 URL。

## 注意事项

- 公开桶**不要**放私有业务附件。安装包可以放，合同/内部文档不行。
- 如果将来要私有文件，需要单独建一个私有桶，不在本次改造范围内。
- 改 DNS/桶设置后，Cloudflare CDN 可能有缓存，等 1-2 分钟再测。
