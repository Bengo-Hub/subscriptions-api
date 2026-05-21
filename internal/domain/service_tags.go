package domain

// Canonical service tag constants used as service_tag values on SubscriptionPlan.
// Must match the values used in seed files and frontend SERVICE_TAGS.
const (
	ServiceTagOrdering   = "ordering"
	ServiceTagPOS        = "pos"
	ServiceTagLogistics  = "logistics"
	ServiceTagInventory  = "inventory"
	ServiceTagERP        = "erp"
	ServiceTagTreasury   = "treasury"
	ServiceTagTruLoad    = "truload"
	ServiceTagMarketFlow = "marketflow"
	ServiceTagISPBilling = "isp_billing"
	ServiceTagProjects   = "projects"
)

// AllServiceTags lists all billable service tags.
// Platform services (auth, subscriptions, codevertex-website, notifications) are not included.
// cafe-website is also excluded — it derives subscription gating from the ordering plan.
var AllServiceTags = []string{
	ServiceTagOrdering,
	ServiceTagPOS,
	ServiceTagLogistics,
	ServiceTagInventory,
	ServiceTagERP,
	ServiceTagTreasury,
	ServiceTagTruLoad,
	ServiceTagMarketFlow,
	ServiceTagISPBilling,
	ServiceTagProjects,
}
