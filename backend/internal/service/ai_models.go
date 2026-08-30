package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
	"gorm.io/gorm"
)

const aiProviderResponseLimit = 2 << 20

type AIModelUpsertPayload struct {
	DisplayName string  `json:"displayName"`
	BaseURL     string  `json:"baseUrl"`
	ModelName   string  `json:"modelName"`
	APIKey      *string `json:"apiKey"`
	IsEnabled   *bool   `json:"isEnabled"`
	Priority    int     `json:"priority"`
	TimeoutMS   int     `json:"timeoutMs"`
}

type AIModelPatchPayload struct {
	IsEnabled *bool `json:"isEnabled"`
}

type AIModelView struct {
	ID           int64  `json:"id"`
	DisplayName  string `json:"displayName"`
	BaseURL      string `json:"baseUrl"`
	ModelName    string `json:"modelName"`
	APIKeyMasked string `json:"apiKeyMasked"`
	IsEnabled    bool   `json:"isEnabled"`
	Priority     int    `json:"priority"`
	TimeoutMS    int    `json:"timeoutMs"`
	SessionCount int64  `json:"sessionCount"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

type AssistantModelOption struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"displayName"`
	ModelName   string `json:"modelName"`
	Priority    int    `json:"priority"`
}

type assistantModelRuntime struct {
	ID        int64
	BaseURL   string
	ModelName string
	APIKey    string
	Timeout   time.Duration
}

func (a *App) ListAIModels(ctx context.Context) ([]AIModelView, error) {
	var models []db.AIModelConfig
	if err := a.DB.WithContext(ctx).Order("priority, id").Find(&models).Error; err != nil {
		return nil, errors.New("list AI models failed")
	}
	counts := map[int64]int64{}
	if len(models) > 0 {
		var rows []struct {
			ModelConfigID int64
			Count         int64
		}
		if err := a.DB.WithContext(ctx).Model(&db.AssistantSession{}).
			Select("model_config_id, COUNT(*) AS count").Group("model_config_id").Scan(&rows).Error; err != nil {
			return nil, errors.New("count AI model sessions failed")
		}
		for _, row := range rows {
			counts[row.ModelConfigID] = row.Count
		}
	}
	result := make([]AIModelView, len(models))
	for index := range models {
		result[index] = a.aiModelView(models[index], counts[models[index].ID])
	}
	return result, nil
}

func (a *App) ListAssistantModels(ctx context.Context) ([]AssistantModelOption, error) {
	var models []db.AIModelConfig
	if err := a.DB.WithContext(ctx).Where("is_enabled = ?", true).Order("priority, id").Find(&models).Error; err != nil {
		return nil, errors.New("list assistant models failed")
	}
	result := make([]AssistantModelOption, len(models))
	for index, model := range models {
		result[index] = AssistantModelOption{ID: model.ID, DisplayName: model.DisplayName, ModelName: model.ModelName, Priority: model.Priority}
	}
	return result, nil
}

func (a *App) CreateAIModel(ctx context.Context, payload AIModelUpsertPayload, principal *Principal) (AIModelView, error) {
	if principal == nil || principal.User == nil {
		return AIModelView{}, ErrPermission
	}
	model, err := a.buildAIModel(payload, nil, principal.User.ID)
	if err != nil {
		return AIModelView{}, err
	}
	if err := a.DB.WithContext(ctx).Create(&model).Error; err != nil {
		return AIModelView{}, errors.New("create AI model failed")
	}
	return a.aiModelView(model, 0), nil
}

func (a *App) UpdateAIModel(ctx context.Context, modelID int64, payload AIModelUpsertPayload, principal *Principal) (AIModelView, error) {
	if principal == nil || principal.User == nil {
		return AIModelView{}, ErrPermission
	}
	var current db.AIModelConfig
	if err := a.DB.WithContext(ctx).First(&current, modelID).Error; err != nil {
		return AIModelView{}, aiModelLookupError(err)
	}
	model, err := a.buildAIModel(payload, &current, principal.User.ID)
	if err != nil {
		return AIModelView{}, err
	}
	updates := map[string]any{
		"display_name": model.DisplayName, "base_url": model.BaseURL, "model_name": model.ModelName,
		"api_key_ciphertext": model.APIKeyCiphertext, "is_enabled": model.IsEnabled,
		"priority": model.Priority, "timeout_ms": model.TimeoutMS, "updated_by": model.UpdatedBy, "updated_at": model.UpdatedAt,
	}
	if err := a.DB.WithContext(ctx).Model(&current).Updates(updates).Error; err != nil {
		return AIModelView{}, errors.New("update AI model failed")
	}
	model.ID, model.CreatedAt = current.ID, current.CreatedAt
	var count int64
	_ = a.DB.WithContext(ctx).Model(&db.AssistantSession{}).Where("model_config_id = ?", modelID).Count(&count).Error
	return a.aiModelView(model, count), nil
}

func (a *App) PatchAIModel(ctx context.Context, modelID int64, payload AIModelPatchPayload, principal *Principal) (AIModelView, error) {
	if principal == nil || principal.User == nil {
		return AIModelView{}, ErrPermission
	}
	if payload.IsEnabled == nil {
		return AIModelView{}, errors.New("isEnabled is required")
	}
	result := a.DB.WithContext(ctx).Model(&db.AIModelConfig{}).Where("id = ?", modelID).Updates(map[string]any{
		"is_enabled": *payload.IsEnabled, "updated_by": principal.User.ID, "updated_at": time.Now().UTC(),
	})
	if result.Error != nil {
		return AIModelView{}, errors.New("update AI model status failed")
	}
	if result.RowsAffected == 0 {
		return AIModelView{}, fmt.Errorf("%w: AI model", ErrNotFound)
	}
	models, err := a.ListAIModels(ctx)
	if err != nil {
		return AIModelView{}, err
	}
	for _, model := range models {
		if model.ID == modelID {
			return model, nil
		}
	}
	return AIModelView{}, fmt.Errorf("%w: AI model", ErrNotFound)
}

func (a *App) DeleteAIModel(ctx context.Context, modelID int64) error {
	var count int64
	if err := a.DB.WithContext(ctx).Model(&db.AssistantSession{}).Where("model_config_id = ?", modelID).Count(&count).Error; err != nil {
		return errors.New("count AI model sessions failed")
	}
	if count > 0 {
		return fmt.Errorf("%w: AI model is referenced by assistant sessions", ErrConflict)
	}
	result := a.DB.WithContext(ctx).Delete(&db.AIModelConfig{}, modelID)
	if result.Error != nil {
		return errors.New("delete AI model failed")
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: AI model", ErrNotFound)
	}
	return nil
}

func (a *App) ValidateAIModel(ctx context.Context, modelID int64) (M, error) {
	runtime, err := a.loadAssistantModel(ctx, modelID, false)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, runtime.endpoint("models"), nil)
	if err != nil {
		return nil, errors.New("build AI model validation request failed")
	}
	request.Header.Set("Accept", "application/json")
	runtime.authorize(request)
	response, err := runtime.client().Do(request)
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		return M{"success": false, "status": "failed", "message": "模型连接失败"}, nil
	}
	defer response.Body.Close()
	if _, err := readBounded(response.Body, 64<<10); err != nil {
		return M{"success": false, "status": "failed", "message": "模型响应过大"}, nil
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return M{"success": false, "status": "failed", "message": "模型服务拒绝连接"}, nil
	}
	return M{"success": true, "status": "success", "message": "模型连接正常"}, nil
}

func (a *App) buildAIModel(payload AIModelUpsertPayload, current *db.AIModelConfig, userID int64) (db.AIModelConfig, error) {
	displayName := strings.TrimSpace(payload.DisplayName)
	modelName := strings.TrimSpace(payload.ModelName)
	priority := payload.Priority
	if priority == 0 {
		priority = 100
	}
	timeoutMS := payload.TimeoutMS
	if timeoutMS == 0 {
		timeoutMS = 60000
	}
	baseURL, err := normalizeAIBaseURL(payload.BaseURL)
	if err != nil {
		return db.AIModelConfig{}, err
	}
	if displayName == "" || utf8.RuneCountInString(displayName) > 120 {
		return db.AIModelConfig{}, errors.New("displayName must contain 1 to 120 characters")
	}
	if modelName == "" || utf8.RuneCountInString(modelName) > 255 {
		return db.AIModelConfig{}, errors.New("modelName must contain 1 to 255 characters")
	}
	if priority < 1 || priority > 9999 {
		return db.AIModelConfig{}, errors.New("priority must be between 1 and 9999")
	}
	if timeoutMS < 1000 || timeoutMS > 300000 {
		return db.AIModelConfig{}, errors.New("timeoutMs must be between 1000 and 300000")
	}
	enabled := true
	ciphertext := ""
	createdAt := time.Now().UTC()
	createdBy := userID
	if current != nil {
		enabled, ciphertext, createdAt, createdBy = current.IsEnabled, current.APIKeyCiphertext, current.CreatedAt, current.CreatedBy
	}
	if payload.IsEnabled != nil {
		enabled = *payload.IsEnabled
	}
	if payload.APIKey != nil {
		ciphertext = a.Cipher.Encrypt(strings.TrimSpace(*payload.APIKey))
	}
	now := time.Now().UTC()
	return db.AIModelConfig{
		DisplayName: displayName, BaseURL: baseURL, ModelName: modelName, APIKeyCiphertext: ciphertext,
		IsEnabled: enabled, Priority: priority, TimeoutMS: timeoutMS,
		CreatedBy: createdBy, UpdatedBy: userID, CreatedAt: createdAt, UpdatedAt: now,
	}, nil
}

func (a *App) aiModelView(model db.AIModelConfig, sessionCount int64) AIModelView {
	key, _ := a.Cipher.Decrypt(model.APIKeyCiphertext)
	return AIModelView{
		ID: model.ID, DisplayName: model.DisplayName, BaseURL: model.BaseURL, ModelName: model.ModelName,
		APIKeyMasked: security.Mask(key), IsEnabled: model.IsEnabled, Priority: model.Priority,
		TimeoutMS: model.TimeoutMS, SessionCount: sessionCount,
		CreatedAt: formatWorkflowTime(model.CreatedAt), UpdatedAt: formatWorkflowTime(model.UpdatedAt),
	}
}

func (a *App) loadAssistantModel(ctx context.Context, modelID int64, requireEnabled bool) (assistantModelRuntime, error) {
	var model db.AIModelConfig
	query := a.DB.WithContext(ctx).Where("id = ?", modelID)
	if requireEnabled {
		query = query.Where("is_enabled = ?", true)
	}
	if err := query.First(&model).Error; err != nil {
		return assistantModelRuntime{}, aiModelLookupError(err)
	}
	key, err := a.Cipher.Decrypt(model.APIKeyCiphertext)
	if err != nil {
		return assistantModelRuntime{}, errors.New("decrypt AI model API key failed")
	}
	return assistantModelRuntime{
		ID: model.ID, BaseURL: model.BaseURL, ModelName: model.ModelName,
		APIKey: key, Timeout: time.Duration(model.TimeoutMS) * time.Millisecond,
	}, nil
}

func (runtime assistantModelRuntime) endpoint(resource string) string {
	return strings.TrimRight(runtime.BaseURL, "/") + "/" + strings.TrimLeft(resource, "/")
}

func (runtime assistantModelRuntime) authorize(request *http.Request) {
	if runtime.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+runtime.APIKey)
	}
}

func (runtime assistantModelRuntime) client() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{
		Transport: transport, Timeout: runtime.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("AI model redirects are disabled") },
	}
}

func normalizeAIBaseURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 1000 {
		return "", errors.New("baseUrl must contain 1 to 1000 characters")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("baseUrl must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil {
		return "", errors.New("baseUrl must not contain embedded credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("baseUrl must not contain a query or fragment")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	value, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(value)) > limit {
		return nil, errors.New("response exceeds size limit")
	}
	return value, nil
}

func aiModelLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: AI model", ErrNotFound)
	}
	return errors.New("load AI model failed")
}
