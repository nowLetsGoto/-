// r2check.go · 检测各公司 R2 凭据能否用当前 AUTH_SECRET 解开（只读）。
//
// 为什么需要它：AUTH_SECRET 变更后，所有已存的密文会一起解不开。
// 运行时的表现是「R2 静默降级为本机下载 + 日志里一行提示」，
// 管理员在页面上看到 secretConfigured=true，却怎么也想不到是密钥变了。
// 这个命令把故障从日志里拉到眼前，逐公司报告，并给出可执行的修复动作。
//
// 用法（在运行服务的同一套环境变量下执行）：
//
//	dandelion r2check              # 检测所有已配置的公司
//	dandelion r2check --json       # 机器可读输出，便于脚本巡检
//
// 边界：只读 system_settings。不写库、不重加密、不打印密钥明文或密文。
// 修复必须由管理员在设置页逐家公司重新填写——脚本替他决定用哪把密钥写库
// 是不可逆操作，且在新密钥只对部分公司有效时会造成更大混乱。
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"dandelion/server/internal/config"
	"dandelion/server/internal/repository/settings"
	"dandelion/server/internal/secret"
)

// r2CheckRow 是一家公司的检测结果。
//
// 只输出状态与 ID，不输出任何密钥材料：这是巡检工具，
// 打印出来的东西常被贴进工单或聊天窗口，不能带凭据。
type r2CheckRow struct {
	TenantID string `json:"tenantId"`
	Status   string `json:"status"` // ok / broken / incomplete / error
	Detail   string `json:"detail"`
}

func runR2Check(args []string) error {
	fs := flag.NewFlagSet("r2check", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "以 JSON 输出（用于脚本巡检）")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cipher, err := secret.New(cfg.AuthSecret)
	if err != nil {
		return fmt.Errorf("初始化凭据解密失败: %w", err)
	}

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("打开数据库失败: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}

	ctx := context.Background()
	repo := settings.NewRepository(db)
	tenants, err := repo.ListConfiguredTenants(ctx)
	if err != nil {
		return fmt.Errorf("列出已配置公司失败: %w", err)
	}

	rows := make([]r2CheckRow, 0, len(tenants))
	broken := 0
	for _, tenantID := range tenants {
		row := checkTenant(ctx, repo, cipher, tenantID)
		rows = append(rows, row)
		if row.Status == "broken" || row.Status == "error" {
			broken++
		}
	}

	if *asJSON {
		out := struct {
			Total  int          `json:"total"`
			Broken int          `json:"broken"`
			Rows   []r2CheckRow `json:"rows"`
		}{len(rows), broken, rows}
		return json.NewEncoder(os.Stdout).Encode(out)
	}

	printCheckReport(rows, broken)
	if broken > 0 {
		return fmt.Errorf("%d 家公司的 R2 凭据无法解密，请在平台集成 → 对象存储逐家重新填写 Secret", broken)
	}
	return nil
}

// checkTenant 检测一家公司。
//
// 空密钥不算故障：那是「该公司未启用 R2」的正常状态。
// 真正的问题是「有密文但解不开」——那才是 AUTH_SECRET 变更的症状。
func checkTenant(ctx context.Context, repo *settings.Repository, cipher *secret.Cipher, tenantID string) r2CheckRow {
	cfg, err := repo.LoadR2(ctx, tenantID)
	if err != nil {
		return r2CheckRow{tenantID, "error", "读取配置失败: " + err.Error()}
	}
	if cfg.SecretKey == "" {
		return r2CheckRow{tenantID, "incomplete", "未配置密钥（R2 未启用，非故障）"}
	}
	if _, err := cipher.Decrypt(cfg.SecretKey); err != nil {
		return r2CheckRow{
			tenantID, "broken",
			"解密失败：AUTH_SECRET 可能已变更，需在公司自己的平台集成页重新填写 Secret",
		}
	}
	return r2CheckRow{tenantID, "ok", "凭据可正常解密"}
}

// printCheckReport 输出人可读的报告。
//
// 按状态分组而不是逐行平铺：管理员真正要找的是 broken，
// 把十几行 ok 和一行 broken 混在一起，容易看漏。
func printCheckReport(rows []r2CheckRow, broken int) {
	fmt.Printf("R2 凭据检测：%d 家公司，%d 家异常\n\n", len(rows), broken)

	order := []string{"broken", "error", "incomplete", "ok"}
	labels := map[string]string{
		"broken":     "需要修复",
		"error":      "读取失败",
		"incomplete": "未启用",
		"ok":         "正常",
	}
	for _, status := range order {
		var group []r2CheckRow
		for _, row := range rows {
			if row.Status == status {
				group = append(group, row)
			}
		}
		if len(group) == 0 {
			continue
		}
		fmt.Printf("[%s] %d 家\n", labels[status], len(group))
		for _, row := range group {
			fmt.Printf("  %s  %s\n", row.TenantID, row.Detail)
		}
		fmt.Println()
	}

	if broken > 0 {
		fmt.Println("修复方式：用该公司管理员账号登录 → 平台集成 → 对象存储 → 点「替换」重新填写 Secret。")
		fmt.Println("注意：AUTH_SECRET 变更后所有公司的密文都会失效，需要逐家重填。")
	}
}
