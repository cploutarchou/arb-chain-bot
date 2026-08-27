package auth

// Permission names one guarded capability. The matrix below is the single
// authorization truth for web AND Telegram (SKILL.md §52, §56): services
// check permissions; handlers never re-derive them.
type Permission string

const (
	PermViewDashboard   Permission = "view:dashboard"
	PermViewOpportunity Permission = "view:opportunities"
	PermViewPortfolio   Permission = "view:portfolio"
	PermViewRisk        Permission = "view:risk"
	PermViewAudit       Permission = "view:audit"
	PermViewSystem      Permission = "view:system"

	PermPaperControl   Permission = "paper:control"  // start/pause/resume
	PermPaperReset     Permission = "paper:reset"    // destructive, ADMIN only
	PermScannerConfig  Permission = "scanner:config" // strategy config within bounds
	PermAIApprove      Permission = "ai:approve"     // approve/reject recommendations
	PermAlertAck       Permission = "alerts:ack"
	PermReportView     Permission = "reports:view"
	PermReportGenerate Permission = "reports:generate"   // on-demand generation (costs queries)
	PermRecordControl  Permission = "recordings:control" // start/stop in-process market-data recording
	PermCampaignRun    Permission = "campaigns:run"      // launch a §80 campaign over a recording

	PermRiskConfig     Permission = "risk:config"     // limits/breaker policy
	PermExchangeConfig Permission = "exchange:config" // keys/markets/fees
	PermUserManage     Permission = "users:manage"
	PermSystemConfig   Permission = "system:config"
)

// matrix maps each role to its permissions. VIEWER: read-only. OPERATOR:
// paper operations, bounded scanner config, AI approvals, reports.
// ADMIN: everything (SKILL.md §52).
var matrix = map[Role]map[Permission]bool{
	RoleViewer: setOf(
		PermViewDashboard, PermViewOpportunity, PermViewPortfolio,
		PermViewRisk, PermViewSystem, PermReportView,
	),
	RoleOperator: setOf(
		PermViewDashboard, PermViewOpportunity, PermViewPortfolio,
		PermViewRisk, PermViewSystem, PermReportView, PermViewAudit,
		PermPaperControl, PermScannerConfig, PermAIApprove, PermAlertAck,
		PermReportGenerate, PermRecordControl, PermCampaignRun,
	),
	RoleAdmin: setOf(
		PermViewDashboard, PermViewOpportunity, PermViewPortfolio,
		PermViewRisk, PermViewSystem, PermReportView, PermViewAudit,
		PermPaperControl, PermScannerConfig, PermAIApprove, PermAlertAck,
		PermReportGenerate, PermRecordControl, PermCampaignRun,
		PermPaperReset, PermRiskConfig,
		PermExchangeConfig, PermUserManage, PermSystemConfig,
	),
}

func setOf(ps ...Permission) map[Permission]bool {
	m := make(map[Permission]bool, len(ps))
	for _, p := range ps {
		m[p] = true
	}
	return m
}

// Can reports whether a role holds a permission. Unknown roles hold
// nothing (fail closed).
func Can(r Role, p Permission) bool { return matrix[r][p] }

// PermissionForConfigSection maps a strategy-config top-level section to
// the permission required to change it. Every path that mutates config
// — the config API, rollbacks, AND AI-recommendation approvals — must
// use this one mapping so risk limits stay ADMIN-only everywhere.
func PermissionForConfigSection(section string) Permission {
	if section == "risk" {
		return PermRiskConfig
	}
	return PermScannerConfig
}
