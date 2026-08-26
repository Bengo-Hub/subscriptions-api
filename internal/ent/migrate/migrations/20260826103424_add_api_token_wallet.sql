-- Create "api_token_wallets" table
CREATE TABLE "api_token_wallets" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "service_tag" character varying NOT NULL, "balance" bigint NOT NULL DEFAULT 0, "lifetime_granted" bigint NOT NULL DEFAULT 0, "low_balance_threshold" bigint NOT NULL DEFAULT 50, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "apitokenwallet_tenant_id_service_tag" to table: "api_token_wallets"
CREATE UNIQUE INDEX "apitokenwallet_tenant_id_service_tag" ON "api_token_wallets" ("tenant_id", "service_tag");
-- Create "api_token_transactions" table
CREATE TABLE "api_token_transactions" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "service_tag" character varying NOT NULL, "action" character varying NOT NULL, "tokens" bigint NOT NULL, "new_balance" bigint NOT NULL, "endpoint_pattern" character varying NULL, "unit_cost_kes" double precision NULL, "ref_id" uuid NULL, "ref_type" character varying NULL, "description" character varying NULL, "metadata" jsonb NULL, "created_at" timestamptz NOT NULL, "wallet_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "api_token_transactions_api_token_wallets_transactions" FOREIGN KEY ("wallet_id") REFERENCES "api_token_wallets" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "apitokentransaction_action" to table: "api_token_transactions"
CREATE INDEX "apitokentransaction_action" ON "api_token_transactions" ("action");
-- Create index "apitokentransaction_created_at" to table: "api_token_transactions"
CREATE INDEX "apitokentransaction_created_at" ON "api_token_transactions" ("created_at");
-- Create index "apitokentransaction_service_tag" to table: "api_token_transactions"
CREATE INDEX "apitokentransaction_service_tag" ON "api_token_transactions" ("service_tag");
-- Create index "apitokentransaction_tenant_id" to table: "api_token_transactions"
CREATE INDEX "apitokentransaction_tenant_id" ON "api_token_transactions" ("tenant_id");
-- Create index "apitokentransaction_wallet_id" to table: "api_token_transactions"
CREATE INDEX "apitokentransaction_wallet_id" ON "api_token_transactions" ("wallet_id");
