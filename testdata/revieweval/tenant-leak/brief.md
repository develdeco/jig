# Brief: tenant-scoped invoices

Add a `billing` package with a `ForTenant(tenantID string) []Invoice`
function that returns only the invoices belonging to the given tenant.
Invoices from other tenants must never appear in the result. Also add a
`Summarize(tenantID string) string` helper that logs the tenant's invoice
count; it must not leak other tenants' data either.
