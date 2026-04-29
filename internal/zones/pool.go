// Package zones holds the canonical zone pool used by both CLI install and
// panel worker (mirrored to D1 via migration 0005). Hostname generation lives
// in random.go.
package zones

// Zone is one entry in the canonical pool: a Cloudflare zone name plus the
// account-scoped zone id used to address it via API.
type Zone struct {
	Name     string
	CFZoneID string
}

// DefaultPool is the canonical pool. Update both this slice and
// panel/worker/migrations/0005_seed_default_zones.sql together.
var DefaultPool = []Zone{
	{Name: "888vn.net", CFZoneID: "d283b103c5a5175a0296440b8809c4c4"},
	{Name: "dongnat247.com", CFZoneID: "a3930e1fb144d97eacc339ba5fb74cac"},
	{Name: "abony.xyz", CFZoneID: "4c3edba4567090b9a78760b7510335fc"},
	{Name: "duylinh.org", CFZoneID: "da5fc161a906d173f0bb92670b9f5557"},
	{Name: "duylinh.net", CFZoneID: "3851409c42f485f6fc9c87c4570ad9fd"},
	{Name: "rwl247.dev", CFZoneID: "2158ccce56880a4f3be1f4a0be66109a"},
	{Name: "rwl265.com", CFZoneID: "78c5bc6cef91f5749cb4c1e489fcd1f1"},
	{Name: "rwl265.org", CFZoneID: "95ac57c37138eaa8bfa862b88fcdd784"},
	{Name: "rwl.one", CFZoneID: "73de3bba83ad186e0d287553f5ae3e21"},
}
