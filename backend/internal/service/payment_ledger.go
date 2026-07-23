package service

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"dengdeng/internal/model"

	"gorm.io/gorm"
)

type PaymentLedgerFilter struct {
	Page        int
	Size        int
	Period      string
	Kind        string
	Currency    string
	ProviderKey string
	User        string
}

type PaymentLedgerSummary struct {
	Currency           string `json:"currency"`
	IncomeMinor        int64  `json:"income_minor"`
	ExpenseMinor       int64  `json:"expense_minor"`
	NetMinor           int64  `json:"net_minor"`
	IncomeCreditMicro  int64  `json:"income_credit_micro"`
	ExpenseCreditMicro int64  `json:"expense_credit_micro"`
	IncomeCount        int64  `json:"income_count"`
	ExpenseCount       int64  `json:"expense_count"`
}

type PaymentLedgerTrend struct {
	Date         string `json:"date"`
	IncomeMinor  int64  `json:"income_minor"`
	ExpenseMinor int64  `json:"expense_minor"`
	IncomeCount  int64  `json:"income_count"`
	ExpenseCount int64  `json:"expense_count"`
}

type PaymentLedgerItem struct {
	ID            int64     `json:"id"`
	EventKey      string    `json:"event_key"`
	OrderID       int64     `json:"order_id"`
	OrderNo       string    `json:"order_no"`
	UserID        int64     `json:"user_id"`
	UserEmail     string    `json:"user_email"`
	Kind          string    `json:"kind"`
	Currency      string    `json:"currency"`
	AmountMinor   int64     `json:"amount_minor"`
	CreditMicro   int64     `json:"credit_micro"`
	ProviderKey   string    `json:"provider_key"`
	PaymentMethod string    `json:"payment_method"`
	OccurredAt    time.Time `json:"occurred_at"`
}

type PaymentLedgerPage struct {
	Items      []PaymentLedgerItem  `json:"items"`
	Total      int64                `json:"total"`
	Page       int                  `json:"page"`
	Size       int                  `json:"size"`
	Period     string               `json:"period"`
	Summary    PaymentLedgerSummary `json:"summary"`
	Trend      []PaymentLedgerTrend `json:"trend"`
	Currencies []string             `json:"currencies"`
	Providers  []string             `json:"providers"`
}

func (s *PaymentService) ListLedger(filter PaymentLedgerFilter) (PaymentLedgerPage, error) {
	filter.Page, filter.Size = normalizeLedgerPage(filter.Page, filter.Size)
	filter.Period = strings.ToLower(strings.TrimSpace(filter.Period))
	if filter.Period == "" {
		filter.Period = "30d"
	}
	if _, ok := ledgerPeriodStart(filter.Period); !ok {
		return PaymentLedgerPage{}, errors.New("unknown ledger period")
	}
	filter.Kind = strings.ToLower(strings.TrimSpace(filter.Kind))
	if filter.Kind != "" && filter.Kind != model.PaymentLedgerIncome && filter.Kind != model.PaymentLedgerExpense {
		return PaymentLedgerPage{}, errors.New("unknown ledger entry kind")
	}
	filter.Currency = strings.ToUpper(strings.TrimSpace(filter.Currency))
	if filter.Currency == "" {
		cfg, err := s.Config()
		if err != nil {
			return PaymentLedgerPage{}, err
		}
		filter.Currency = strings.ToUpper(cfg.Currency)
	}
	filter.ProviderKey = strings.TrimSpace(filter.ProviderKey)
	filter.User = strings.TrimSpace(filter.User)

	page := PaymentLedgerPage{
		Items:      []PaymentLedgerItem{},
		Page:       filter.Page,
		Size:       filter.Size,
		Period:     filter.Period,
		Currencies: []string{},
		Providers:  []string{},
		Trend:      []PaymentLedgerTrend{},
		Summary:    PaymentLedgerSummary{Currency: filter.Currency},
	}
	if err := s.db.Model(&model.PaymentLedgerEntry{}).Distinct().Order("currency ASC").Pluck("currency", &page.Currencies).Error; err != nil {
		return PaymentLedgerPage{}, err
	}
	if err := s.db.Model(&model.PaymentLedgerEntry{}).Where("provider_key <> ''").Distinct().Order("provider_key ASC").Pluck("provider_key", &page.Providers).Error; err != nil {
		return PaymentLedgerPage{}, err
	}

	table := s.ledgerQuery(filter, false)
	if err := table.Count(&page.Total).Error; err != nil {
		return PaymentLedgerPage{}, err
	}
	if err := table.
		Select(`payment_ledger_entries.*, COALESCE(users.email, '') AS user_email, COALESCE(payment_orders.out_trade_no, '') AS order_no`).
		Order("payment_ledger_entries.occurred_at DESC, payment_ledger_entries.id DESC").
		Offset((filter.Page - 1) * filter.Size).
		Limit(filter.Size).
		Scan(&page.Items).Error; err != nil {
		return PaymentLedgerPage{}, err
	}

	summaryQuery := s.ledgerQuery(filter, true)
	var aggregate struct {
		IncomeMinor        int64
		ExpenseMinor       int64
		IncomeCreditMicro  int64
		ExpenseCreditMicro int64
		IncomeCount        int64
		ExpenseCount       int64
	}
	if err := summaryQuery.Select(`
		COALESCE(SUM(CASE WHEN payment_ledger_entries.kind = ? THEN payment_ledger_entries.amount_minor ELSE 0 END), 0) AS income_minor,
		COALESCE(SUM(CASE WHEN payment_ledger_entries.kind = ? THEN payment_ledger_entries.amount_minor ELSE 0 END), 0) AS expense_minor,
		COALESCE(SUM(CASE WHEN payment_ledger_entries.kind = ? THEN payment_ledger_entries.credit_micro ELSE 0 END), 0) AS income_credit_micro,
		COALESCE(SUM(CASE WHEN payment_ledger_entries.kind = ? THEN payment_ledger_entries.credit_micro ELSE 0 END), 0) AS expense_credit_micro,
		COALESCE(SUM(CASE WHEN payment_ledger_entries.kind = ? THEN 1 ELSE 0 END), 0) AS income_count,
		COALESCE(SUM(CASE WHEN payment_ledger_entries.kind = ? THEN 1 ELSE 0 END), 0) AS expense_count`,
		model.PaymentLedgerIncome, model.PaymentLedgerExpense,
		model.PaymentLedgerIncome, model.PaymentLedgerExpense,
		model.PaymentLedgerIncome, model.PaymentLedgerExpense,
	).Scan(&aggregate).Error; err != nil {
		return PaymentLedgerPage{}, err
	}
	page.Summary.IncomeMinor = aggregate.IncomeMinor
	page.Summary.ExpenseMinor = aggregate.ExpenseMinor
	page.Summary.NetMinor = aggregate.IncomeMinor - aggregate.ExpenseMinor
	page.Summary.IncomeCreditMicro = aggregate.IncomeCreditMicro
	page.Summary.ExpenseCreditMicro = aggregate.ExpenseCreditMicro
	page.Summary.IncomeCount = aggregate.IncomeCount
	page.Summary.ExpenseCount = aggregate.ExpenseCount

	trendQuery := s.ledgerQuery(filter, true)
	if err := trendQuery.
		Select(`DATE(payment_ledger_entries.occurred_at) AS date,
			COALESCE(SUM(CASE WHEN payment_ledger_entries.kind = ? THEN payment_ledger_entries.amount_minor ELSE 0 END), 0) AS income_minor,
			COALESCE(SUM(CASE WHEN payment_ledger_entries.kind = ? THEN payment_ledger_entries.amount_minor ELSE 0 END), 0) AS expense_minor,
			COALESCE(SUM(CASE WHEN payment_ledger_entries.kind = ? THEN 1 ELSE 0 END), 0) AS income_count,
			COALESCE(SUM(CASE WHEN payment_ledger_entries.kind = ? THEN 1 ELSE 0 END), 0) AS expense_count`,
			model.PaymentLedgerIncome, model.PaymentLedgerExpense,
			model.PaymentLedgerIncome, model.PaymentLedgerExpense,
		).
		Group("DATE(payment_ledger_entries.occurred_at)").
		Order("DATE(payment_ledger_entries.occurred_at) ASC").
		Scan(&page.Trend).Error; err != nil {
		return PaymentLedgerPage{}, err
	}
	return page, nil
}

func normalizeLedgerPage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

func ledgerPeriodStart(period string) (time.Time, bool) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch period {
	case "7d":
		return today.AddDate(0, 0, -6), true
	case "30d":
		return today.AddDate(0, 0, -29), true
	case "90d":
		return today.AddDate(0, 0, -89), true
	case "all":
		return time.Time{}, true
	default:
		return time.Time{}, false
	}
}

func (s *PaymentService) ledgerQuery(filter PaymentLedgerFilter, ignoreKind bool) *gorm.DB {
	query := s.db.Table("payment_ledger_entries").
		Joins("LEFT JOIN users ON users.id = payment_ledger_entries.user_id").
		Joins("LEFT JOIN payment_orders ON payment_orders.id = payment_ledger_entries.order_id")
	if start, _ := ledgerPeriodStart(filter.Period); !start.IsZero() {
		query = query.Where("payment_ledger_entries.occurred_at >= ?", start)
	}
	if filter.Currency != "" {
		query = query.Where("payment_ledger_entries.currency = ?", filter.Currency)
	}
	if !ignoreKind && filter.Kind != "" {
		query = query.Where("payment_ledger_entries.kind = ?", filter.Kind)
	}
	if filter.ProviderKey != "" {
		query = query.Where("payment_ledger_entries.provider_key = ?", filter.ProviderKey)
	}
	if filter.User != "" {
		pattern := "%" + strings.ToLower(filter.User) + "%"
		query = query.Where("LOWER(users.email) LIKE ? OR CAST(payment_ledger_entries.user_id AS TEXT) = ?", pattern, numericUserID(filter.User))
	}
	return query
}

func numericUserID(value string) string {
	if _, err := strconv.ParseInt(value, 10, 64); err != nil {
		return ""
	}
	return value
}
