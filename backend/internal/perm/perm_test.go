package perm

import "testing"

func TestNotificationChannelViewPermissionIsSeeded(t *testing.T) {
	for _, spec := range ButtonSpecs["NodeDefinitions"] {
		if spec.Code == ConfigNotificationChannelsView {
			return
		}
	}
	t.Fatal("notification channel view permission is missing from seeded buttons")
}
