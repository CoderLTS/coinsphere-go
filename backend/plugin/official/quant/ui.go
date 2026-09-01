package quant

import "coinsphere/backend/plugin/sdk"

func quantNodeMeta(desc sdk.NodeDescriptor, title, description, category, color, icon string) sdk.NodeDescriptor {
	desc.Title, desc.Description, desc.Category, desc.Color, desc.Icon = title, description, category, color, icon
	desc.Width, desc.Height = 220, 72
	desc.Capabilities.Stateless = desc.State == sdk.StateStateless
	desc.Capabilities.Deterministic = desc.Capabilities.Deterministic || desc.SideEffect == sdk.SideEffectNone
	return desc
}
