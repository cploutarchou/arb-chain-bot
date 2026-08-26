package platform

import "github.com/cploutarchou/arb-chain-bot/internal/auth"

// PermissionForSection maps a platform-settings top-level section to the
// permission required to change it (design §3): "venues" is
// exchange:config; "paper" and "telegram" are system:config. Every path
// that mutates the document — POST /platform/settings and its rollback —
// must use this one mapping. Unknown sections fail closed to the
// highest bar this document ever requires (system:config) rather than
// silently granting a lower one to a future section nobody has
// classified yet.
func PermissionForSection(section string) auth.Permission {
	switch section {
	case "venues":
		return auth.PermExchangeConfig
	default:
		return auth.PermSystemConfig
	}
}
