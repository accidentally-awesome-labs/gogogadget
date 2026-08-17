package templates

import (
	"time"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
)

type DashboardData struct {
	ActiveProjects int64
	MemberCount    int64
	Plan           billing.Plan
	Recent         []sqlc.RecentAuditByOrgRow
	Now            time.Time
}

type ProjectListData struct {
	Projects   []sqlc.Project
	Query      string
	Page       int
	TotalPages int
	Plan       billing.Plan
	Count      int64
}

type AdminHomeData struct {
	TotalUsers    int64
	TotalOrgs     int64
	ActiveSubs    int64
	MRR           int
	RecentSignups []sqlc.User
	Now           time.Time
}

type AdminUsersData struct {
	Users      []sqlc.User
	Query      string
	Page       int
	TotalPages int
}

type AdminOrgsData struct {
	Orgs []sqlc.ListOrgsWithStatsRow
}

type APITokensData struct {
	Tokens   []sqlc.ApiToken
	NewToken string // plaintext, shown ONCE right after creation
	NameErr  string
}

type BillingData struct {
	Plan             billing.Plan
	Sub              *sqlc.Subscription
	ProjectCount     int64
	UsedStorageBytes int64
	MeterUsage       map[string]int64 // meter key → current-month usage
	Plans            []billing.Plan
	Processing       bool // checkout redirect beat the webhook
	Success          bool
}
type ProjectFormData struct {
	ID       int64 // 0 = new
	Name     string
	NameErr  string
	LimitHit bool
	Plan     billing.Plan
}

type FilesData struct {
	Files      []sqlc.File
	Page       int
	TotalPages int
	Plan       billing.Plan
	UsedBytes  int64
	LimitHit   bool // last upload rejected for quota; re-render with CTA
	MaxMB      int  // the rejected upload's size, for the message
}

type NotificationsData struct {
	Items      []sqlc.Notification
	Page       int
	TotalPages int
	Unread     int64
}

type WebhooksData struct {
	Endpoints  []sqlc.WebhookEndpoint
	Deliveries []sqlc.ListDeliveriesByOrgRow
	EventTypes []string // checkbox catalog
	NewSecret  string   // plaintext, shown ONCE right after endpoint creation
	URLErr     string
}
