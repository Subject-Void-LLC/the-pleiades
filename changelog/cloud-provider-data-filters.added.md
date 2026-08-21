Added 14 cloud provider data filters, callable from `when`, `when_or`, and
`when_cel`: `parseARN`/`buildARN`, `parseAzureResourceID`/
`buildAzureResourceID`, `parseGCPSelfLink`, `parseGCPIAMMember`,
`awsTagListToMap`/`mapToAWSTagList`, `formatCurrency`, `cloudInitWrap`
(base64/MIME wrapping only; never validates the wrapped script),
`extractPaginationToken`, `resourceTShirtSize` (a default vCPU/RAM sizing
table, meant as a starting point to override, not authoritative),
`normalizeCloudRegion` (a small curated lookup table, not a live mirror of
any provider's region list), and `iamPolicyMerger` (a syntactic
Statement-list merge, not a semantically-aware policy engine). See the
generated filter reference for the full list.
