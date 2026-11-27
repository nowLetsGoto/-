#!/usr/bin/env python3
"""bootstrap-plists.py · 按当前机器环境生成四个 LaunchAgent 配置。

职责：在 plist 缺失或路径过期时，生成 com.dandelion.{server,vite,tunnel,keepawake}。
输入：仓库根目录（参数 1）。环境变量从仓库 .env 读取，不硬编码任何密钥。
输出：~/Library/LaunchAgents/ 下的四个 plist，已存在且路径正确的会被保留。
边界：只生成 plist 文件，不注册服务、不启停进程——注册由 install-autostart.sh 负责。

为什么需要它：plist 原本是手工创建的，只存在于作者那台机器上。换一台机器
clone 仓库后 install-autostart.sh 会因「缺少 plist」直接退出，脚本价值归零。
把「生成」与「注册」分成两步，各自只做一件事。

为什么数据目录用 ~/dandelion-data 而不是仓库内的 ./storage：
macOS 的 TCC 保护 Desktop / Documents / Downloads。仓库通常放在桌面，
于是服务每次读写 storage 都会弹一次授权框，且给的是整个桌面的访问权。
放到家目录根部即可彻底避开——代价是数据不在仓库里，
但这本来就是运行时数据（已 gitignore），放哪里都不影响代码。

为什么日志也不放仓库：日志是程序运行期持续写入的文件，
放在桌面同样触发 TCC。统一收进 ~/dandelion-runtime/logs。
"""
import os
import plistlib
import shutil
import sys

# 运行时目录：二进制与日志都放这里，与仓库分离。
RUNTIME = os.path.expanduser("~/dandelion-runtime")
# 数据目录：刻意放在家目录根部，避开 TCC 保护的桌面。
DATA = os.path.expanduser("~/dandelion-data/storage")

# 从 .env 读取的键。只取服务需要的，不把整份文件塞进 plist：
# plist 是明文可读的，字段越少暴露面越小。
ENV_KEYS = [
    "DATABASE_URL",
    "AUTH_SECRET",
    "ADMIN_USERNAME",
    "ADMIN_PASSWORD",
    "PUBLIC_BASE_URL",
    "R2_ACCOUNT_ID",
    "R2_ACCESS_KEY_ID",
    "R2_SECRET_ACCESS_KEY",
    "R2_BUCKET",
    "R2_PUBLIC_BASE_URL",
    "MAX_UPLOAD_MB",
]


def read_env(root):
    """读取仓库 .env 为字典；文件不存在时返回空字典。

    不报错是刻意的：新机器先跑本脚本、后填 .env 是合理顺序，
    此时应该生成一份部分可用的 plist 并提示补全，而不是整体失败。
    """
    path = os.path.join(root, ".env")
    result = {}
    if not os.path.exists(path):
        return result

    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, _, value = line.partition("=")
            key = key.strip()
            # 去掉可选的 export 前缀与两端引号，与常见 .env 写法兼容。
            if key.startswith("export "):
                key = key[len("export ") :].strip()
            value = value.strip().strip('"').strip("'")
            if key and value:
                result[key] = value
    return result


def build_server(env):
    """后端服务：唯一带敏感环境变量的那个。

    环境变量直接内联进 plist 而不是让服务启动时读 .env：
    launchd 的工作目录不一定是仓库根，靠相对路径找 .env 不可靠；
    且 plist 本身在 ~/Library 下、权限 600，比仓库里的 .env 更不容易被误提交。
    """
    variables = {
        "GO_ENV": "production",
        "HOST": "0.0.0.0",
        "PORT": "10088",
        # 数据目录用绝对路径：WorkingDirectory 是 runtime，
        # 相对路径 ./.storage 会解析到那里，与仓库里的数据错位。
        "STORAGE_DIR": DATA,
    }
    for key in ENV_KEYS:
        if key in env:
            variables[key] = env[key]

    return {
        "Label": "com.dandelion.server",
        "ProgramArguments": [os.path.join(RUNTIME, "dandelion")],
        "WorkingDirectory": RUNTIME,
        "EnvironmentVariables": variables,
        "RunAtLoad": True,
        "KeepAlive": True,
        "StandardOutPath": os.path.join(RUNTIME, "logs/server.log"),
        "StandardErrorPath": os.path.join(RUNTIME, "logs/server.err.log"),
    }


def build_vite(root):
    """前端开发服务。

    用 bash -lc 而不是直接调 npx：Vite 依赖用户 shell 里的 node（nvm 装的），
    launchd 的默认 PATH 里没有那条路径，直接调会报 command not found。
    """
    web = os.path.join(root, "web")
    return {
        "Label": "com.dandelion.vite",
        "ProgramArguments": ["/bin/bash", "-lc", f"cd {web} && exec npm run dev"],
        "WorkingDirectory": web,
        "RunAtLoad": True,
        "KeepAlive": True,
        # 日志进 runtime 而不是仓库：仓库常在桌面，TCC 会拦。
        "StandardOutPath": os.path.join(RUNTIME, "logs/vite.log"),
        "StandardErrorPath": os.path.join(RUNTIME, "logs/vite.err.log"),
    }


def build_tunnel():
    """Cloudflare 隧道。

    隧道名固定为 dandelion；凭据在 ~/.cloudflared/，不随仓库走。
    新机器需要先 `cloudflared tunnel login` 与 `tunnel create dandelion`，
    本脚本不代做——那涉及浏览器授权与账号选择，无法自动化。
    """
    return {
        "Label": "com.dandelion.tunnel",
        "ProgramArguments": [
            "/opt/homebrew/bin/cloudflared",
            "tunnel",
            "run",
            "dandelion",
        ],
        "WorkingDirectory": RUNTIME,
        "RunAtLoad": True,
        "KeepAlive": True,
        "StandardOutPath": os.path.join(RUNTIME, "logs/tunnel.log"),
        "StandardErrorPath": os.path.join(RUNTIME, "logs/tunnel.err.log"),
    }


def build_keepawake():
    """防休眠断言。

    -i 阻止空闲休眠，-s 阻止系统休眠。用 caffeinate 而不是改 pmset：
    pmset 是全局设置、需要 sudo、关机后仍生效；caffeinate 只在运行时有效，
    不污染用户的电源策略。
    """
    return {
        "Label": "com.dandelion.keepawake",
        "ProgramArguments": ["/usr/bin/caffeinate", "-i", "-s"],
        "WorkingDirectory": RUNTIME,
        "RunAtLoad": True,
        "KeepAlive": True,
        "StandardOutPath": os.path.join(RUNTIME, "logs/keepawake.log"),
        "StandardErrorPath": os.path.join(RUNTIME, "logs/keepawake.err.log"),
    }


def is_current(path, expected):
    """判断已有 plist 是否已经指向本机正确路径。

    比较三处与机器绑定的值，任一不符就重写：
      - ProgramArguments：可执行文件位置（换机器必变）
      - STORAGE_DIR：数据目录（若该服务有此项）
      - Standard*Path：日志目录

    日志路径必须一起比：只看前两项会漏掉「数据已移出桌面、日志还在桌面」
    这种半迁移状态，而日志是持续写入的，同样会反复触发 TCC 授权弹窗
    （实测本机 vite 与 keepawake 就是这样，数据搬走了但日志仍指向桌面）。
    """
    try:
        with open(path, "rb") as f:
            existing = plistlib.load(f)
    except (OSError, plistlib.InvalidFileException):
        return False

    if existing.get("ProgramArguments") != expected.get("ProgramArguments"):
        return False

    want = expected.get("EnvironmentVariables", {}).get("STORAGE_DIR")
    if want is not None:
        got = existing.get("EnvironmentVariables", {}).get("STORAGE_DIR")
        if got != want:
            return False

    for key in ("StandardOutPath", "StandardErrorPath"):
        if existing.get(key) != expected.get(key):
            return False

    return True


def main():
    if len(sys.argv) < 2:
        print("用法: bootstrap-plists.py <仓库根目录>", file=sys.stderr)
        return 1

    root = os.path.abspath(sys.argv[1])
    agents = os.path.expanduser("~/Library/LaunchAgents")
    os.makedirs(agents, exist_ok=True)

    # 日志目录必须先存在，否则 launchd 启动时无法打开重定向目标，
    # 服务会以 EX_CONFIG 78 直接失败，且错误信息里看不出是日志路径的问题。
    os.makedirs(os.path.join(RUNTIME, "logs"), exist_ok=True)
    os.makedirs(DATA, exist_ok=True)

    env = read_env(root)
    if not env:
        print(f"  提示：未找到 {root}/.env，生成的配置将缺少数据库与对象存储凭据", file=sys.stderr)
        print("  先 `cp .env.example .env` 并填写，再重跑本脚本", file=sys.stderr)

    configs = [
        build_server(env),
        build_vite(root),
        build_tunnel(),
        build_keepawake(),
    ]

    written = 0
    for cfg in configs:
        path = os.path.join(agents, cfg["Label"] + ".plist")
        if is_current(path, cfg):
            print(f"  保持 {cfg['Label']}（已是本机路径）")
            continue
        # 覆盖前留一份备份：用户可能手工调过端口或参数，
        # 直接覆盖会静默丢失，出问题时也无从对比。
        if os.path.exists(path):
            shutil.copy2(path, path + ".bak")
        with open(path, "wb") as f:
            plistlib.dump(cfg, f)
        written += 1
        print(f"  生成 {cfg['Label']}")

    print(f"  数据目录: {DATA}")
    print(f"  日志目录: {os.path.join(RUNTIME, 'logs')}")
    print(f"  共写入 {written} 个 plist")

    # 隧道依赖外部凭据，缺失时单独提示：它不影响本地使用，
    # 只影响公网访问，用户可能暂时不需要。
    cred = os.path.expanduser("~/.cloudflared")
    if not os.path.isdir(cred):
        print(f"  提示：{cred} 不存在，公网访问需先执行 cloudflared tunnel login", file=sys.stderr)

    return 0


if __name__ == "__main__":
    sys.exit(main())