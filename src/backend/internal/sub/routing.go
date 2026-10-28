package sub

import (
	"encoding/json"
	"errors"
	"fmt"

	"lattix/backend/internal/store"
)

// NormalizeRouting 校验并补全用户级路由配置（规则来源、预设、生效分组、模板槽位），
// 面板代改（/api/user/sub-settings）与订阅页用户自助（/api/sub/{token}/template-config）
// 共用同一套规则；返回携带规范化 CategoriesJSON 的 profile 副本。
func NormalizeRouting(profile store.SubscriptionProfile, categories []string) (store.SubscriptionProfile, error) {
	if profile.Mode == "" {
		profile.Mode = store.SubscriptionModeSuggested
	}
	if profile.Preset == "" {
		profile.Preset = "balanced"
	}
	if profile.Mode != store.SubscriptionModeSuggested && profile.Mode != store.SubscriptionModeTemplate {
		return store.SubscriptionProfile{}, errors.New("订阅规则模式无效")
	}
	if profile.Mode == store.SubscriptionModeTemplate && profile.PortableTemplateID == "" {
		return store.SubscriptionProfile{}, errors.New("自定义模板模式须选择中立或 ACL4SSR 模板")
	}
	if profile.Preset != "minimal" && profile.Preset != "balanced" && profile.Preset != "comprehensive" {
		return store.SubscriptionProfile{}, errors.New("订阅规则预设无效")
	}
	knownCategories := map[string]bool{}
	for _, category := range Categories() {
		knownCategories[category.ID] = true
	}
	for _, category := range categories {
		if !knownCategories[category] {
			return store.SubscriptionProfile{}, fmt.Errorf("未知订阅规则分类 %q", category)
		}
	}
	raw, err := json.Marshal(categories)
	if err != nil {
		return store.SubscriptionProfile{}, err
	}
	profile.CategoriesJSON = string(raw)
	return profile, nil
}
