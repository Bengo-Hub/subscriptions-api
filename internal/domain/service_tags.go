package domain

// Canonical service tag constants used as service_tag values on SubscriptionPlan and
// FeatureDefinition. Must match the values used in seed files and frontend SERVICE_TAGS.
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
	// ServiceTagHospital replaces the retired "afya" tag: every hospital plan and feature is
	// seeded with service_tag "hospital" (cmd/seed/plans_hospital.go, feature_catalog.go).
	ServiceTagHospital = "hospital"
	ServiceTagLibrary  = "library"
	ServiceTagMaskani  = "maskani"
)

// ServiceCatalogEntry names one billable service for clients that list a tenant's services
// (for example the tenant data backup picker).
type ServiceCatalogEntry struct {
	Tag  string `json:"service_tag"`
	Name string `json:"name"`
}

// ServiceCatalog is the single ordered list of billable services; AllServiceTags derives from it.
// Platform services (auth, subscriptions, codevertex-website, notifications) are not included.
// cafe-website is also excluded: it derives subscription gating from the ordering plan.
var ServiceCatalog = []ServiceCatalogEntry{
	{ServiceTagOrdering, "Ordering"},
	{ServiceTagPOS, "POS"},
	{ServiceTagLogistics, "Logistics"},
	{ServiceTagInventory, "Inventory"},
	{ServiceTagERP, "ERP"},
	{ServiceTagTreasury, "Treasury"},
	{ServiceTagTruLoad, "TruLoad"},
	{ServiceTagMarketFlow, "Marketflow"},
	{ServiceTagISPBilling, "ISP Billing"},
	{ServiceTagProjects, "Projects"},
	{ServiceTagHospital, "Hospital"},
	{ServiceTagLibrary, "Library"},
	{ServiceTagMaskani, "Maskani"},
}

// AllServiceTags lists all billable service tags.
var AllServiceTags = func() []string {
	tags := make([]string, 0, len(ServiceCatalog))
	for _, e := range ServiceCatalog {
		tags = append(tags, e.Tag)
	}
	return tags
}()
