// dev_handler_test.go · 研发任务 HTTP 层的安全底线测试。
// 职责：钉死编辑 DTO 白名单（顶层与嵌套 status 一律 400）、路径 id 校验与审计 action 常量。
// 输入：httptest 构造的 JSON 请求与最小业务层替身。
// 输出：断言 400 / 404 与合法解码结果。
// 边界：不连数据库、不起真实服务；不测路由中间件（由 router 装配保证）。
package rdc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dandelion/server/internal/contract"
	rdcsvc "dandelion/server/internal/service/rdc"
)

// devFakeRepository 只实现 DevService 所需的仓储契约，方法体在本测试中不产生数据库行为。
type devFakeRepository struct{}

func (devFakeRepository) CreateWithSubtasks(
	_ context.Context, in contract.DevTaskInput, _, _ string,
) (contract.DevTaskDetail, error) {
	return contract.DevTaskDetail{DevTask: contract.DevTask{ID: "id"}}, nil
}
func (devFakeRepository) List(
	_ context.Context, _ contract.DevTaskQuery, _ contract.Scope,
) (contract.DevTaskList, error) {
	return contract.DevTaskList{}, nil
}
func (devFakeRepository) FindByID(
	_ context.Context, _ string, _ contract.Scope,
) (contract.DevTaskDetail, error) {
	return contract.DevTaskDetail{}, contract.NotFound(contract.ResourceDevTask)
}
func (devFakeRepository) FindSubtask(
	_ context.Context, _, _ string, _ contract.Scope,
) (contract.DevSubtask, error) {
	return contract.DevSubtask{}, contract.NotFound(contract.ResourceDevSubtask)
}
func (devFakeRepository) UpdateTask(
	_ context.Context, _ string, _ contract.DevTaskUpdate, _ contract.Scope,
) (contract.DevTaskDetail, error) {
	return contract.DevTaskDetail{}, nil
}
func (devFakeRepository) UpdateSubtaskNote(
	_ context.Context, _, _, _ string, _ contract.Scope,
) (bool, error) {
	return false, nil
}
func (devFakeRepository) UpdateStatusIf(
	_ context.Context, _, _, _, _ string, _ contract.Scope,
) (bool, error) {
	return false, nil
}
func (devFakeRepository) Delete(_ context.Context, _ string, _ contract.Scope) error {
	return nil
}

// devJSONRequest 构造一个带 JSON body 的 POST 请求。
func devJSONRequest(body string) *http.Request {
	return httptest.NewRequest(http.MethodPut, "/api/v1/rdc/dev/tasks/id", strings.NewReader(body))
}

// TestDecodeDevCreateWhitelist 钉死新建只接受 title / requirementId。
func TestDecodeDevCreateWhitelist(t *testing.T) {
	for _, body := range []string{
		`{"title":"任务","requirementId":"id","status":"pending"}`,
		`{"title":"任务","requirementId":"id","tenantId":"other"}`,
		`{"title":"任务","requirementId":"id","creatorUserId":"other"}`,
	} {
		var in contract.DevTaskInput
		err := decodeDevCreate(httptest.NewRecorder(), devJSONRequest(body), &in)
		assertStatus(t, err, http.StatusBadRequest)
	}
	var in contract.DevTaskInput
	if err := decodeDevCreate(httptest.NewRecorder(), devJSONRequest(
		`{"title":"任务","requirementId":"id"}`), &in); err != nil {
		t.Fatalf("合法新建请求体不应被拒: %v", err)
	}
}

// TestDecodeDevUpdateRejectsStatusAtAnyLevel 是编辑安全底线最关键的一条：
// 顶层 status 与 subtasks[] 内的 status 都必须被 DisallowUnknownFields 拒绝。
func TestDecodeDevUpdateRejectsStatusAtAnyLevel(t *testing.T) {
	for _, body := range []string{
		`{"title":"任务","requirementId":"id","status":"completed"}`,
		`{"title":"任务","requirementId":"id","subtasks":[{"id":"sub","note":"x","status":"pending"}]}`,
		`{"title":"任务","requirementId":"id","subtasks":[{"id":"sub","note":"x","unexpected":1}]}`,
		`{"title":"任务","requirementId":"id","subtasks":[{"id":"sub","note":"x","status":"skipped"}]}`,
	} {
		var in contract.DevTaskUpdate
		err := decodeDevUpdate(httptest.NewRecorder(), devJSONRequest(body), &in)
		assertStatus(t, err, http.StatusBadRequest)
	}
	var in contract.DevTaskUpdate
	if err := decodeDevUpdate(httptest.NewRecorder(), devJSONRequest(
		`{"title":"任务","requirementId":"id","subtasks":[{"id":"sub","note":"x"}]}`), &in); err != nil {
		t.Fatalf("合法编辑请求体不应被拒: %v", err)
	}
	if len(in.Subtasks) != 1 || in.Subtasks[0].Note != "x" {
		t.Fatalf("合法编辑请求体解码错误: %+v", in)
	}
}

// TestDevGetRejectsMissingID 断言路径参数缺失时由 service 统一转 404，不会静默返回空详情。
func TestDevGetRejectsMissingID(t *testing.T) {
	h := NewDevHandler(rdcsvc.NewDevService(devFakeRepository{}, nil), nil)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/rdc/dev/tasks/", nil)
	err := h.Get(httptest.NewRecorder(), request)
	var missing *contract.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("路径参数缺失应统一转 404，实际 %T: %v", err, err)
	}
}

// TestDevAuditActions 锁死 5 类审计 action 与对象类型，避免某处手写成另一种拼写。
func TestDevAuditActions(t *testing.T) {
	want := map[string]string{
		devActionCreated:      devObjectTask,
		devActionUpdated:      devObjectTask,
		devActionDeleted:      devObjectTask,
		devActionTransitioned: devObjectSubtask,
		devActionSkipped:      devObjectSubtask,
	}
	for action, objectType := range want {
		if !strings.HasPrefix(action, "rdc.dev_") || objectType == "" {
			t.Fatalf("审计 action 或对象类型不合法: %s / %s", action, objectType)
		}
	}
	if len(want) != 5 {
		t.Fatalf("研发任务必须有 5 类审计事件，实际 %d", len(want))
	}
}
