-- Create index "usageevent_tenant_id_created_at" to table: "usage_events"
CREATE INDEX "usageevent_tenant_id_created_at" ON "usage_events" ("tenant_id", "created_at");
