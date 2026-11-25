// Package rdc 的交付规则测试。
// 职责：固定拆条向量、逐项验收拒绝条件与 Git 安全闸门。
// 输入：标准文本及伪造结果；输出：纯函数断言。
// 边界：不依赖数据库；事务集成由仓储测试负责。
package rdc

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"dandelion/server/internal/contract"
)

type guardedGitSettings struct {
	called bool
}

func (s *guardedGitSettings) GitRepoPath(context.Context, string) (string, error) {
	s.called = true
	return "/allowed/company/repo", nil
}

type guardedGitReader struct {
	called bool
}

func (r *guardedGitReader) Read(context.Context, string) contract.GitSnapshot {
	r.called = true
	return contract.GitSnapshot{Status: contract.GitOK}
}

func TestRefreshGitFailsClosedAfterScopeCheck(t *testing.T) {
	settings := &guardedGitSettings{}
	reader := &guardedGitReader{}
	var seenID string
	service := &DeliveryService{
		visible: func(_ context.Context, id string, _ contract.Scope) error {
			seenID = id
			return nil
		},
		settings: settings,
		reader:   reader,
	}
	snapshot, err := service.RefreshGit(context.Background(), "requirement-id", contract.Scope{})
	var invalid *contract.ValidationError
	if !errors.As(err, &invalid) || snapshot.Status == contract.GitOK || seenID != "requirement-id" {
		t.Fatalf("安全闸门没有阻断已授权请求: snapshot=%+v err=%v id=%q", snapshot, err, seenID)
	}
	if settings.called || reader.called {
		t.Fatal("已有仓库配置仍不得读取路径或执行未隔离 Git")
	}
}

func TestRefreshGitPreservesScopeNotFound(t *testing.T) {
	settings := &guardedGitSettings{}
	reader := &guardedGitReader{}
	notFound := contract.NotFound(contract.ResourceRequirement)
	service := &DeliveryService{
		visible:  func(context.Context, string, contract.Scope) error { return notFound },
		settings: settings,
		reader:   reader,
	}
	snapshot, err := service.RefreshGit(context.Background(), "other-company-requirement", contract.Scope{})
	if !errors.Is(err, notFound) || snapshot.Status == contract.GitOK || settings.called || reader.called {
		t.Fatalf("跨公司/不存在需求必须保持 404 且不读仓库: snapshot=%+v err=%v", snapshot, err)
	}
}

func TestAcceptanceVectors(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"", []string{}}, {" \n\r\n", []string{}}, {"A\nB", []string{"A", "B"}},
		{"1. 第一\n2、第二\r\n- 第三", []string{"第一", "第二", "第三"}},
		{"1) A 2) B • C", []string{"A", "B", "C"}},
		{"普通文字", []string{"普通文字"}},
	}
	for _, tc := range cases {
		got := SplitAcceptanceCriteria(tc.text)
		texts := []string{}
		for i, item := range got {
			if item.Index != i {
				t.Fatalf("索引不连续: %+v", got)
			}
			texts = append(texts, item.Text)
		}
		if !reflect.DeepEqual(texts, tc.want) {
			t.Fatalf("%q got %v want %v", tc.text, texts, tc.want)
		}
	}
}
func TestAcceptanceValidation(t *testing.T) {
	good := []contract.AcceptanceItemResult{{Index: 0, Result: "passed"}, {Index: 1, Result: "passed"}}
	if _, err := ValidateAcceptanceItems("A\nB", "passed", good); err != nil {
		t.Fatal(err)
	}
	bad := [][]contract.AcceptanceItemResult{
		{}, {{Index: 0, Result: "passed"}}, {{Index: 0, Result: "passed"}, {Index: 0, Result: "passed"}},
		{{Index: 0, Result: "passed"}, {Index: 2, Result: "passed"}}, {{Index: 0, Result: "passed"}, {Index: 1, Result: "failed"}},
	}
	for _, items := range bad {
		if _, err := ValidateAcceptanceItems("A\nB", "passed", items); err == nil {
			t.Fatalf("伪通过未拒绝: %+v", items)
		}
	}
	if _, err := ValidateAcceptanceItems("", "passed", good); err == nil {
		t.Fatal("空标准未拒绝")
	}
	if _, err := ValidateAcceptanceItems("A\nB", "failed", []contract.AcceptanceItemResult{{Index: 0, Result: "passed"}, {Index: 1, Result: "failed"}}); err != nil {
		t.Fatal(err)
	}
}
