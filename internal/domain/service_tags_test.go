package domain

import "testing"

func TestServiceCatalogConsistent(t *testing.T) {
	if len(AllServiceTags) != len(ServiceCatalog) {
		t.Fatalf("AllServiceTags has %d entries, catalog has %d", len(AllServiceTags), len(ServiceCatalog))
	}
	seen := map[string]bool{}
	for _, e := range ServiceCatalog {
		if e.Tag == "" || e.Name == "" {
			t.Errorf("catalog entry %+v has an empty tag or name", e)
		}
		if seen[e.Tag] {
			t.Errorf("duplicate service tag %q", e.Tag)
		}
		seen[e.Tag] = true
	}
	for _, want := range []string{ServiceTagHospital, ServiceTagLibrary, ServiceTagMaskani, ServiceTagProjects, ServiceTagERP} {
		if !seen[want] {
			t.Errorf("catalog is missing %q", want)
		}
	}
	if seen["afya"] {
		t.Error("retired tag afya must not return; hospital plans use service_tag hospital")
	}
}
