REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA vault FROM js_auth,js_social,js_ops,js_publication,js_worker,js_vault,js_vault_auth;
GRANT USAGE ON SCHEMA vault TO js_vault;
GRANT SELECT,INSERT ON vault.subject,vault.account_locator TO js_vault;
GRANT SELECT,INSERT,UPDATE ON vault.pseudonym_binding TO js_vault;
GRANT INSERT ON vault.audit_event TO js_vault;

GRANT SELECT ON vault.key_configuration TO js_vault;
