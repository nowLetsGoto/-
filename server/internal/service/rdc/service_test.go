// service_test.go · 需求池业务层的纯逻辑测试。
// 职责：钉住字段校验、项目必填与可见性、图片数量/体积/格式复校、缺省状态与请求体形状。
// 输入：contract.RequirementInput、staged 图片与非法需求编号；项目校验器用测试替身。
// 输出：断言 normalize / validateImages / Create / Get 的拒绝与兜底行为。
// 边界：只测纯函数与无需 DB / 文件系统的分支；仓储与文件搬运由集成测试覆盖。
package rdc

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"dandelion/server/internal/contract"
)

const validProjectID = "11111111-1111-4111-8111-111111111111"

// fakeProjectValidator 是可控的项目可见性替身：exists 决定校验结果，calls 记录是否被调用。
type fakeProjectValidator struct {
	exists bool
	calls  int
}

func (v *fakeProjectValidator) ExistsVisibleByID(_ context.Context, _ string, _ contract.Scope) bool {
	v.calls++
	return v.exists
}

// validInput 返回一份刚好通过全部校验的输入，各用例只改一个字段，避免相互干扰。
func validInput() contract.RequirementInput {
	return contract.RequirementInput{
		Title:              "优化登录流程",
		Description:        "缩短首屏加载时间，保持现有登录方式不变。",
		Status:             contract.ReqStatusConfirmed,
		AcceptanceCriteria: "首屏加载时间低于 2 秒",
		ProjectID:          validProjectID,
	}
}

// validImage 返回一张能被三层校验接受的 staged 图片描述。
func validImage() contract.StagedImage {
	return contract.StagedImage{
		Token:       "token",
		AbsPath:     "/tmp/token.png",
		Ext:         "png",
		ContentType: "image/png",
		CleanName:   "设计稿.png",
		FileSize:    1024,
	}
}

func TestNormalizeAcceptsValidInput(t *testing.T) {
	got, err := normalize(validInput())
	if err != nil {
		t.Fatalf("合法输入不应被拒绝: %v", err)
	}
	want := validInput()
	if got != want {
		t.Fatalf("归一化不应改变合法输入: got=%+v want=%+v", got, want)
	}
}

func TestNormalizeRequiresTitleAndDescription(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*contract.RequirementInput)
		wantMsg string
	}{
		{"标题为空", func(in *contract.RequirementInput) { in.Title = "" }, "标题"},
		{"标题全空白", func(in *contract.RequirementInput) { in.Title = "   " }, "标题"},
		{"描述为空", func(in *contract.RequirementInput) { in.Description = "" }, "描述"},
		{"描述全空白", func(in *contract.RequirementInput) { in.Description = "\t\n" }, "描述"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := validInput()
			test.mutate(&in)
			_, err := normalize(in)
			if err == nil {
				t.Fatal("必填字段为空应被拒绝")
			}
			var invalid *contract.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("应返回 ValidationError，实际 %T", err)
			}
			if !strings.Contains(invalid.Message, test.wantMsg) {
				t.Fatalf("文案应指出 %s，实际 %q", test.wantMsg, invalid.Message)
			}
		})
	}
}

func TestNormalizeRequiresProjectID(t *testing.T) {
	for _, projectID := range []string{"", "   ", "not-a-uuid", "11111111-1111-4111-8111-11111111111z"} {
		in := validInput()
		in.ProjectID = projectID
		_, err := normalize(in)
		var invalid *contract.ValidationError
		if !errors.As(err, &invalid) || !strings.Contains(invalid.Message, "项目") {
			t.Fatalf("非法项目 id %q 应返回指出项目的 ValidationError，实际 %v", projectID, err)
		}
	}
}

func TestNormalizeDefaultsEmptyStatus(t *testing.T) {
	for _, status := range []string{"", "   "} {
		in := validInput()
		in.Status = status
		got, err := normalize(in)
		if err != nil {
			t.Fatalf("空状态应兜底而不是报错: %v", err)
		}
		if got.Status != contract.ReqStatusPendingReview {
			t.Fatalf("空状态应兜底为 pending_review，实际 %q", got.Status)
		}
	}
}

func TestNormalizeRejectsInvalidStatus(t *testing.T) {
	for _, status := range []string{"reviewing", "PENDING_REVIEW", "done"} {
		in := validInput()
		in.Status = status
		if _, err := normalize(in); err == nil {
			t.Fatalf("非法状态 %q 应被拒绝", status)
		}
	}
}

func TestNormalizeEnforcesLengthLimits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*contract.RequirementInput)
	}{
		{"标题超长", func(in *contract.RequirementInput) { in.Title = strings.Repeat("字", contract.MaxReqTitleLen+1) }},
		{"描述超长", func(in *contract.RequirementInput) { in.Description = strings.Repeat("字", contract.MaxReqDescLen+1) }},
		{"验收标准超长", func(in *contract.RequirementInput) {
			in.AcceptanceCriteria = strings.Repeat("字", contract.MaxReqAcceptanceLen+1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := validInput()
			test.mutate(&in)
			if _, err := normalize(in); err == nil {
				t.Fatal("超长字段应被拒绝")
			}
		})
	}
}

func TestNormalizeTrimsWhitespace(t *testing.T) {
	in := validInput()
	in.Title = "  标题 "
	in.Description = "  描述  "
	in.AcceptanceCriteria = "  验收  "
	in.ProjectID = "  " + validProjectID + "  "
	got, err := normalize(in)
	if err != nil {
		t.Fatalf("首尾空白应被裁剪而不是拒绝: %v", err)
	}
	if got.Title != "标题" || got.Description != "描述" || got.AcceptanceCriteria != "验收" || got.ProjectID != validProjectID {
		t.Fatalf("首尾空白未被裁剪: %+v", got)
	}
}

// TestRequirementInputHasNoOwnershipFields 防止有人往请求体加 reqKey / tenantId / creatorUserId。
// projectId 是用户选的业务归属字段，允许出现；真正的隔离仍由 service 的租户校验负责。
func TestRequirementInputHasNoOwnershipFields(t *testing.T) {
	typ := reflect.TypeOf(contract.RequirementInput{})
	want := map[string]bool{
		"title": true, "description": true, "status": true,
		"acceptanceCriteria": true, "projectId": true,
	}
	if typ.NumField() != len(want) {
		t.Fatalf("RequirementInput 应恰好 %d 字段，实际 %d", len(want), typ.NumField())
	}
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if !want[tag] {
			t.Fatalf("RequirementInput 出现非契约字段 %q", tag)
		}
	}
}

func TestValidateImagesAcceptsEmptyAndValid(t *testing.T) {
	if err := validateImages(nil); err != nil {
		t.Fatalf("无图片是合法的存量兼容形态: %v", err)
	}
	images := make([]contract.StagedImage, 0, contract.MaxRDCImages)
	for i := 0; i < contract.MaxRDCImages; i++ {
		images = append(images, validImage())
	}
	if err := validateImages(images); err != nil {
		t.Fatalf("%d 张合法图片应通过: %v", contract.MaxRDCImages, err)
	}
}

func TestValidateImagesRejectsInvalid(t *testing.T) {
	tooMany := make([]contract.StagedImage, 0, contract.MaxRDCImages+1)
	for i := 0; i <= contract.MaxRDCImages; i++ {
		tooMany = append(tooMany, validImage())
	}
	tooLarge := validImage()
	tooLarge.FileSize = contract.MaxRDCImageBytes + 1
	badExt := validImage()
	badExt.Ext = "svg"
	badMIME := validImage()
	badMIME.ContentType = "image/svg+xml"

	for name, images := range map[string][]contract.StagedImage{
		"超过 9 张":     tooMany,
		"单张超限":       {tooLarge},
		"扩展名不在白名单":   {badExt},
		"MIME 不在白名单": {badMIME},
	} {
		t.Run(name, func(t *testing.T) {
			var invalid *contract.ValidationError
			if !errors.As(validateImages(images), &invalid) {
				t.Fatal("非法图片应返回 ValidationError")
			}
		})
	}
}

// TestCreateRejectsInvisibleProject 验证越界项目在访问仓储前就被拒绝（repo 为 nil 是刻意探针）。
func TestCreateRejectsInvisibleProject(t *testing.T) {
	validator := &fakeProjectValidator{exists: false}
	svc := NewService(nil, validator, nil)
	_, err := svc.Create(context.Background(), contract.Identity{}, contract.Scope{},
		validInput(), []contract.StagedImage{validImage()})
	var invalid *contract.ValidationError
	if !errors.As(err, &invalid) || !strings.Contains(invalid.Message, "项目") {
		t.Fatalf("不可见项目应返回 ValidationError，实际 %v", err)
	}
	if validator.calls != 1 {
		t.Fatalf("项目校验器应被调用一次，实际 %d", validator.calls)
	}
}

// TestCreateRejectsMissingProjectBeforeValidator 验证 projectId 为空时 normalize 先拦，不会误调校验器。
func TestCreateRejectsMissingProjectBeforeValidator(t *testing.T) {
	validator := &fakeProjectValidator{exists: true}
	svc := NewService(nil, validator, nil)
	in := validInput()
	in.ProjectID = ""
	_, err := svc.Create(context.Background(), contract.Identity{}, contract.Scope{}, in, nil)
	var invalid *contract.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("缺少项目应返回 ValidationError，实际 %v", err)
	}
	if validator.calls != 0 {
		t.Fatalf("缺少项目不应触发项目校验，实际调用 %d 次", validator.calls)
	}
}

// TestGetRejectsInvalidReqKeyBeforeRepo 验证 service.Get 在访问仓储前拦掉非法编号。
func TestGetRejectsInvalidReqKeyBeforeRepo(t *testing.T) {
	svc := NewService(nil, nil, nil) // 非法编号走不到 repo，nil 依赖是刻意的探针
	for _, key := range []string{"", "REQ-20250614-1", "REQ-20250614-0011", "random"} {
		_, err := svc.Get(context.Background(), key, contract.Scope{All: true})
		if err == nil {
			t.Fatalf("非法编号 %q 应返回 404", key)
		}
		var missing *contract.NotFoundError
		if !errors.As(err, &missing) {
			t.Fatalf("非法编号应返回 NotFoundError，实际 %T", err)
		}
	}
}

// TestResolveAttachmentRejectsInvalidIDBeforeRepo 验证图片访问在访问仓储前拦掉非法 id。
func TestResolveAttachmentRejectsInvalidIDBeforeRepo(t *testing.T) {
	svc := NewService(nil, nil, nil)
	for _, id := range []string{"", "../secret", "not-a-uuid"} {
		_, err := svc.ResolveAttachment(context.Background(), id, contract.Scope{All: true})
		var missing *contract.NotFoundError
		if !errors.As(err, &missing) {
			t.Fatalf("非法附件 id %q 应返回 404，实际 %v", id, err)
		}
	}
}
