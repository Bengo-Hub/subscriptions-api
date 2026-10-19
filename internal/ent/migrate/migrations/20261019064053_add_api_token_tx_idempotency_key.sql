-- Modify "api_token_transactions" table
ALTER TABLE "api_token_transactions" ADD COLUMN "idempotency_key" character varying NULL;
-- Create index "apitokentransaction_tenant_id_service_tag_created_at" to table: "api_token_transactions"
CREATE INDEX "apitokentransaction_tenant_id_service_tag_created_at" ON "api_token_transactions" ("tenant_id", "service_tag", "created_at");
-- Create index "apitokentransaction_wallet_id_idempotency_key" to table: "api_token_transactions"
CREATE UNIQUE INDEX "apitokentransaction_wallet_id_idempotency_key" ON "api_token_transactions" ("wallet_id", "idempotency_key");
