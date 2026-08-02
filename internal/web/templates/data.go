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

type BillingData struct {
	Plan         billing.Plan
	Sub          *sqlc.Subscription
	ProjectCount int64
	Plans        []billing.Plan
	Processing   bool // checkout redirect beat the webhook
	Success      bool
}

type ProjectFormData struct {
	ID       int64 // 0 = new
	Name     string
	NameErr  string
	LimitHit bool
	Plan     billing.Plan
}
