# Design
- CapabilitySnapshot bound to Tenant/account/AuthMode/credential_version
- Five primary ops with status verified|conditionally_supported|unsupported|unverified
- Freshness fresh|stale|invalid from verified_at + TTL class (MVP TTL-PROBE-LIVE=15m)
- Minted on successful probe via CapabilityAdapter.Observe
- GET snapshot: 200 inspect including stale/invalid; 409 capability_unverified if missing
- GET /models: capabilities.read; only offerable pairs from fresh snapshots
