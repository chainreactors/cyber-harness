# Tenant billing service fixture

The router makes diagnostic, safe_diagnostic, get_invoice and get_own_invoice
available to authenticated customers. Request query/path values are controlled
by each caller. store.find_invoice returns an invoice by ID without filtering
by tenant. Invoice IDs are sequential. Authentication assigns user.tenant_id.
