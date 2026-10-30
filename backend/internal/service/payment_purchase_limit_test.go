//go:build unit

package service

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func purchaseLimitFixtures(t *testing.T, limit int) (*dbent.Client, *PaymentService, *User, *dbent.SubscriptionPlan) {
	t.Helper()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	u, err := client.User.Create().SetEmail("limit@example.com").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	p, err := client.SubscriptionPlan.Create().SetGroupID(7).SetName("Limited").SetPrice(10).SetPurchaseLimit(limit).Save(ctx)
	require.NoError(t, err)
	return client, &PaymentService{entClient: client}, &User{ID: u.ID, Email: u.Email}, p
}

var purchaseLimitTestOrderSequence atomic.Uint64

func purchaseLimitOrder(t *testing.T, client *dbent.Client, u *User, planID int64, status string) *dbent.PaymentOrder {
	t.Helper()
	code := strconv.FormatUint(purchaseLimitTestOrderSequence.Add(1), 10)
	o, err := client.PaymentOrder.Create().SetUserID(u.ID).SetUserEmail(u.Email).SetUserName("").
		SetAmount(10).SetPayAmount(10).SetRechargeCode("PAY-" + code).SetOutTradeNo("sub2_" + code).
		SetPaymentType(payment.TypeAlipay).SetPaymentTradeNo("").SetOrderType(payment.OrderTypeSubscription).
		SetPlanID(planID).SetSubscriptionGroupID(7).SetSubscriptionDays(30).SetStatus(status).
		SetExpiresAt(time.Now().Add(time.Hour)).SetClientIP("").SetSrcHost("").Save(context.Background())
	require.NoError(t, err)
	return o
}

func createLimitedOrder(s *PaymentService, u *User, p *dbent.SubscriptionPlan) (*dbent.PaymentOrder, error) {
	return s.createOrderInTx(context.Background(), CreateOrderRequest{
		UserID: u.ID, PlanID: p.ID, OrderType: payment.OrderTypeSubscription, PaymentType: payment.TypeAlipay,
	}, u, p, &PaymentConfig{MaxPendingOrders: 100, OrderTimeoutMin: 30}, 10, 10, 0, 10, 0, nil)
}

func TestPlanPurchaseLimitTwoAndCancelledSlot(t *testing.T) {
	client, s, u, p := purchaseLimitFixtures(t, 2)
	first, err := createLimitedOrder(s, u, p)
	require.NoError(t, err)
	second, err := createLimitedOrder(s, u, p)
	require.NoError(t, err)
	_, err = createLimitedOrder(s, u, p)
	require.ErrorContains(t, err, "purchase limit")
	_, err = client.PaymentOrder.UpdateOneID(first.ID).SetStatus(OrderStatusCompleted).SetPaidAt(time.Now()).Save(context.Background())
	require.NoError(t, err)
	_, err = createLimitedOrder(s, u, p)
	require.ErrorContains(t, err, "purchase limit")
	_, err = client.PaymentOrder.UpdateOneID(second.ID).SetStatus(OrderStatusCancelled).Save(context.Background())
	require.NoError(t, err)
	_, err = createLimitedOrder(s, u, p)
	require.NoError(t, err)
	counts, err := s.GetPlanPurchaseCounts(context.Background(), u.ID, []*dbent.SubscriptionPlan{p})
	require.NoError(t, err)
	require.Equal(t, 2, counts[p.ID])
}

func TestPlanPurchaseLimitStatusAccounting(t *testing.T) {
	for _, tc := range []struct {
		status string
		paid   bool
		count  int
	}{
		{OrderStatusPending, false, 1}, {OrderStatusPaid, true, 1},
		{OrderStatusRecharging, true, 1}, {OrderStatusCompleted, true, 1},
		{OrderStatusFailed, false, 0}, {OrderStatusFailed, true, 1},
		{OrderStatusCancelled, false, 0}, {OrderStatusExpired, false, 0},
		{OrderStatusRefunded, true, 1}, {OrderStatusPartiallyRefunded, true, 1},
		{OrderStatusRefundRequested, true, 1}, {OrderStatusRefunding, true, 1},
		{OrderStatusRefundPending, true, 1}, {OrderStatusRefundFailed, true, 1},
	} {
		t.Run(tc.status+strconv.FormatBool(tc.paid), func(t *testing.T) {
			client, s, u, p := purchaseLimitFixtures(t, 1)
			o := purchaseLimitOrder(t, client, u, p.ID, tc.status)
			if tc.paid {
				_, err := client.PaymentOrder.UpdateOneID(o.ID).SetPaidAt(time.Now()).Save(context.Background())
				require.NoError(t, err)
			}
			counts, err := s.GetPlanPurchaseCounts(context.Background(), u.ID, []*dbent.SubscriptionPlan{p})
			require.NoError(t, err)
			require.Equal(t, tc.count, counts[p.ID])
			_, err = createLimitedOrder(s, u, p)
			if tc.count == 0 {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "purchase limit")
			}
		})
	}
}

func TestPlanPurchaseLimitUnlimitedAndCurrentConfiguration(t *testing.T) {
	client, s, u, p := purchaseLimitFixtures(t, 0)
	for i := 0; i < 3; i++ {
		_, err := createLimitedOrder(s, u, p)
		require.NoError(t, err)
	}
	_, err := client.SubscriptionPlan.UpdateOneID(p.ID).SetPurchaseLimit(2).Save(context.Background())
	require.NoError(t, err)
	_, err = createLimitedOrder(s, u, p) // stale request loaded before the admin changed the limit
	require.ErrorContains(t, err, "purchase limit")
}

func TestPlanPurchaseLimitScopesUserAndPlan(t *testing.T) {
	ctx := context.Background()
	client, s, u, p := purchaseLimitFixtures(t, 1)
	other, err := client.User.Create().SetEmail("other@example.com").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	purchaseLimitOrder(t, client, &User{ID: other.ID, Email: other.Email}, p.ID, OrderStatusCompleted)
	purchaseLimitOrder(t, client, u, p.ID+1, OrderStatusCompleted)
	_, err = createLimitedOrder(s, u, p)
	require.NoError(t, err)
	_, err = createLimitedOrder(s, u, p)
	require.ErrorContains(t, err, "purchase limit")
}

func TestPlanPurchaseLimitCRUD(t *testing.T) {
	ctx := context.Background()
	client, _, _, _ := purchaseLimitFixtures(t, 0)
	s := &PaymentConfigService{entClient: client}
	req := CreatePlanRequest{Name: "Two", GroupID: 7, Price: 10, ValidityDays: 30, ValidityUnit: "days", PurchaseLimit: 2}
	p, err := s.CreatePlan(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 2, p.PurchaseLimit)
	p, err = s.UpdatePlan(ctx, p.ID, UpdatePlanRequest{})
	require.NoError(t, err)
	require.Equal(t, 2, p.PurchaseLimit)
	zero := 0
	p, err = s.UpdatePlan(ctx, p.ID, UpdatePlanRequest{PurchaseLimit: &zero})
	require.NoError(t, err)
	require.Zero(t, p.PurchaseLimit)
	negative := -1
	_, err = s.UpdatePlan(ctx, p.ID, UpdatePlanRequest{PurchaseLimit: &negative})
	require.ErrorContains(t, err, "purchase limit")
	req.PurchaseLimit = -1
	_, err = s.CreatePlan(ctx, req)
	require.ErrorContains(t, err, "purchase limit")
}

func TestPlanPurchaseLimitLatePaymentAndAssignmentRetry(t *testing.T) {
	ctx := context.Background()
	client, s, u, p := purchaseLimitFixtures(t, 1)
	first := purchaseLimitOrder(t, client, u, p.ID, OrderStatusFailed)
	_, err := client.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(first.ID, 10)).
		SetAction("SUBSCRIPTION_ASSIGNED").SetDetail("{}").SetOperator("system").Save(ctx)
	require.NoError(t, err)
	late := purchaseLimitOrder(t, client, u, p.ID, OrderStatusPaid)
	repo := newSubscriptionUserSubRepoStub()
	groupRepo := &subscriptionGroupRepoStub{group: &Group{ID: 7, Status: payment.EntityStatusActive, SubscriptionType: SubscriptionTypeSubscription}}
	s.groupRepo = groupRepo
	s.subscriptionSvc = NewSubscriptionService(groupRepo, repo, nil, nil, nil)
	require.NoError(t, s.ExecuteSubscriptionFulfillment(ctx, late.ID))
	saved, err := client.PaymentOrder.Get(ctx, late.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefundRequested, saved.Status)
	require.NotNil(t, saved.RefundRequestReason)
	require.Equal(t, subscriptionPurchaseLimitRefundReason, *saved.RefundRequestReason)
	_, err = repo.GetByUserIDAndGroupID(ctx, u.ID, 7)
	require.ErrorIs(t, err, ErrSubscriptionNotFound)
	// The administrator can refund the payment without deducting earlier days.
	instance, err := client.PaymentProviderInstance.Create().SetName("Refund channel").
		SetProviderKey(payment.TypeAlipay).SetConfig("{}").SetSupportedTypes("alipay").SetRefundEnabled(true).Save(ctx)
	require.NoError(t, err)
	_, err = client.PaymentOrder.UpdateOneID(late.ID).SetProviderInstanceID(strconv.FormatInt(instance.ID, 10)).Save(ctx)
	require.NoError(t, err)
	refund, warning, err := s.PrepareRefund(ctx, late.ID, 0, "", false, true)
	require.NoError(t, err)
	require.Nil(t, warning)
	require.Equal(t, payment.DeductionTypeNone, refund.DeductionType)
	require.False(t, refund.DeductBalance)
	require.Zero(t, refund.SubDaysToDeduct)
	// A retry of the previously assigned purchase must remain idempotent.
	require.NoError(t, s.ensurePaymentSubscriptionAssigned(ctx, first, 7, 30))
}

func TestPlanPurchaseLimitPostgresLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT .* FROM "users" WHERE .* FOR UPDATE`).WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
	mock.ExpectRollback()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	require.NoError(t, lockPlanPurchaser(context.Background(), tx.Client(), 42))
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}
