// 职责：验证分享入口的来源鉴权、目标版本凭证和撤销状态，防止切换时复用来源凭证。
// 输入：内存分享仓储、固定包及签名；输出：可选版本与越界访问断言。
// 边界：不连接数据库、不发网络、不读取登录态；SQL 的项目和应用隔离由仓储集成测试验证。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
)

type memoryShares map[string]repository.BuildShare

func (m memoryShares) FindByID(_ context.Context, id string) (repository.BuildShare, error) {
	for _, share := range m {
		if share.ID == id {
			return share, nil
		}
	}
	return repository.BuildShare{}, contract.NotFound(contract.ResourceShare)
}

func (m memoryShares) ListByBuildIDs(_ context.Context, ids []string) (map[string]repository.BuildShare, error) {
	result := make(map[string]repository.BuildShare)
	for _, id := range ids {
		if share, ok := m[id]; ok {
			result[id] = share
		}
	}
	return result, nil
}

func TestSharedVersionsRejectInvalidSourceBeforeReadingBuilds(t *testing.T) {
	now := time.Now()
	source := repository.BuildShare{ID: "0138c7a0-8c3c-462d-9fdf-48f0b3491f52", BuildID: "67ebdb8c-81ec-4598-a3d8-dc9f99980754", Revision: 1}
	signer := newShareSigner("test-share-secret")
	valid := signer.issue(contract.ModeTeam, source)
	for _, tc := range []struct {
		name, id, token string
		change          func(*repository.BuildShare)
	}{
		{"缺失凭证", source.BuildID, "", nil},
		{"登录令牌不是分享凭证", source.BuildID, "user|1|9999999999|team.signature", nil},
		{"API Key不是分享凭证", source.BuildID, "company-upload-key", nil},
		{"篡改签名", source.BuildID, valid[:len(valid)-1] + "!", nil},
		{"跨包使用", "4abafadb-8e1b-4281-b016-ce297467d1bc", valid, nil},
		{"伪造分区", source.BuildID, strings.Replace(valid, ".team.", ".solo.", 1), nil},
		{"到期", source.BuildID, valid, func(s *repository.BuildShare) { s.ExpiresAt = &now }},
		{"撤销", source.BuildID, valid, func(s *repository.BuildShare) { s.RevokedAt = &now }},
		{"旧修订", source.BuildID, valid, func(s *repository.BuildShare) { s.Revision++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			share := source
			if tc.change != nil {
				tc.change(&share)
			}
			// 包仓储刻意留空：无效来源若继续查询包会直接触发失败。
			svc := &BuildService{signer: signer, shares: repository.NewPartitioned[shareLookup](memoryShares{share.BuildID: share}, memoryShares{})}
			if _, err := svc.SharedVersions(context.Background(), tc.id, tc.token); !errors.Is(err, contract.ErrNotFound) {
				t.Fatalf("无效来源必须返回404：%v", err)
			}
			if _, err := svc.SharedIOSManifest(context.Background(), tc.id, tc.token); !errors.Is(err, contract.ErrNotFound) {
				t.Fatalf("无效来源不得读取安装清单：%v", err)
			}
		})
	}
}

func TestSharedVersionChoicesUseEachTargetGrantAndOmitInactiveShares(t *testing.T) {
	now := time.Now()
	active := repository.BuildShare{ID: "0138c7a0-8c3c-462d-9fdf-48f0b3491f52", BuildID: "67ebdb8c-81ec-4598-a3d8-dc9f99980754", Revision: 1}
	target := repository.BuildShare{ID: "9ee64cc1-b194-4514-9165-0026cc112977", BuildID: "4abafadb-8e1b-4281-b016-ce297467d1bc", Revision: 2}
	revoked := repository.BuildShare{BuildID: "revoked", Revision: 1, RevokedAt: &now}
	expired := repository.BuildShare{BuildID: "expired", Revision: 1, ExpiresAt: &now}
	store := memoryShares{active.BuildID: active, target.BuildID: target, revoked.BuildID: revoked, expired.BuildID: expired}
	svc := &BuildService{
		cfg: config.Config{PublicBaseURL: "https://downloads.example"}, signer: newShareSigner("test-share-secret"),
		shares: repository.NewPartitioned[shareLookup](store, memoryShares{}),
	}
	builds := []contract.Build{}
	for _, id := range []string{active.BuildID, "revoked", "expired", "missing", target.BuildID} {
		builds = append(builds, contract.Build{ID: id, Version: "1.2", Environment: "prd", Platform: "ios", FileSize: 2048,
			Changelog: "更新说明", RelativePath: "private/path", OwnerUserID: "private-owner", OriginalFileName: "private-file"})
	}
	choices, err := svc.sharedVersionChoices(context.Background(), builds, contract.ModeTeam)
	if err != nil || len(choices) != 2 {
		t.Fatalf("只应返回两个有效分享，实际 %d err=%v", len(choices), err)
	}
	for index, choice := range choices {
		page, err := url.Parse(choice.DownloadPageURL)
		if err != nil || page.Host != "downloads.example" || page.Path != choice.DownloadPagePath || choice.ProjectKey != "" || choice.Changelog != "更新说明" {
			t.Fatalf("目标地址或公开投影错误：%+v err=%v", choice, err)
		}
		grant, mode, err := svc.validateShare(context.Background(), choice.ID, page.Query().Get("shareToken"))
		if err != nil || mode != contract.ModeTeam || grant.BuildID != choice.ID {
			t.Fatalf("目标包凭证不可用：%v", err)
		}
		if index == 1 {
			if _, _, err := svc.validateShare(context.Background(), choice.ID, svc.signer.issue(contract.ModeTeam, active)); !errors.Is(err, contract.ErrNotFound) {
				t.Fatal("目标包不得接受来源包的凭证")
			}
		}
	}
	body, _ := json.Marshal(choices)
	for _, private := range []string{"private/path", "private-owner", "private-file", "relativePath", "ownerUserId", "originalFileName"} {
		if strings.Contains(string(body), private) {
			t.Fatalf("分享列表泄露内部字段：%s", private)
		}
	}
	if _, _, err := svc.validateShare(context.Background(), active.BuildID, svc.signer.issue(contract.ModeSolo, active)); !errors.Is(err, contract.ErrNotFound) {
		t.Fatal("同一授权不得跨分区访问")
	}
	// 目标在列表加载后被撤销，下一次访问也必须即时拒绝。
	target.RevokedAt = &now
	store[target.BuildID] = target
	if _, _, err := svc.validateShare(context.Background(), target.BuildID, svc.signer.issue(contract.ModeTeam, target)); !errors.Is(err, contract.ErrNotFound) {
		t.Fatal("目标分享撤销后仍可访问")
	}
}
