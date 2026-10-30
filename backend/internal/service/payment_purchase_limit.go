package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const subscriptionPurchaseLimitRefundReason = "Subscription purchase limit exceeded; payment received but no subscription was granted."

// lockPlanPurchaser serializes checks and reservations across server instances.
// Call only with a transaction client and hold the lock through the write.
func lockPlanPurchaser(ctx context.Context, client *dbent.Client, userID int64) error {
	if client.Driver().Dialect() == dialect.SQLite {
		// SQLite has no row locks; acquire its writer lock without changing data.
		_, err := client.ExecContext(ctx, "UPDATE users SET id = id WHERE id = ?", userID)
		return err
	}
	_, err := client.User.Query().Where(user.IDEQ(userID)).Select(user.FieldID).ForUpdate().Only(ctx)
	return err
}

// purchaseSlotPredicate counts all paid purchases, including refunds, as lifetime
// purchases. Pending orders reserve a slot until cancellation/expiry is confirmed
// by the existing payment lifecycle. Unpaid failed orders do not consume a slot.
func purchaseSlotPredicate() predicate.PaymentOrder {
	return paymentorder.Or(
		paymentorder.PaidAtNotNil(),
		paymentorder.CompletedAtNotNil(),
		paymentorder.StatusIn(OrderStatusPending, OrderStatusPaid, OrderStatusRecharging,
			OrderStatusCompleted, OrderStatusRefundRequested, OrderStatusRefunding,
			OrderStatusRefundPending, OrderStatusPartiallyRefunded, OrderStatusRefunded,
			OrderStatusRefundFailed),
	)
}

func planPurchaseLimitError(limit int) error {
	return infraerrors.Conflict("PLAN_PURCHASE_LIMIT_REACHED", "purchase limit reached for this subscription plan").
		WithMetadata(map[string]string{"limit": strconv.Itoa(limit)})
}

func checkPlanPurchaseLimit(ctx context.Context, client *dbent.Client, userID int64, plan *dbent.SubscriptionPlan) error {
	if plan.PurchaseLimit <= 0 {
		return nil
	}
	count, err := client.PaymentOrder.Query().Where(
		paymentorder.UserIDEQ(userID), paymentorder.PlanIDEQ(plan.ID),
		paymentorder.OrderTypeEQ(payment.OrderTypeSubscription), purchaseSlotPredicate(),
	).Count(ctx)
	if err != nil {
		return fmt.Errorf("count plan purchases: %w", err)
	}
	if count >= plan.PurchaseLimit {
		return planPurchaseLimitError(plan.PurchaseLimit)
	}
	return nil
}

// GetPlanPurchaseCounts batches the current user's occupied slots for checkout.
func (s *PaymentService) GetPlanPurchaseCounts(ctx context.Context, userID int64, plans []*dbent.SubscriptionPlan) (map[int64]int, error) {
	counts := make(map[int64]int)
	ids := make([]int64, 0, len(plans))
	for _, plan := range plans {
		if plan.PurchaseLimit > 0 {
			ids = append(ids, plan.ID)
		}
	}
	if len(ids) == 0 {
		return counts, nil
	}
	var rows []struct {
		PlanID int64 `json:"plan_id"`
		Count  int   `json:"count"`
	}
	err := s.entClient.PaymentOrder.Query().Where(
		paymentorder.UserIDEQ(userID), paymentorder.PlanIDIn(ids...),
		paymentorder.OrderTypeEQ(payment.OrderTypeSubscription), purchaseSlotPredicate(),
	).GroupBy(paymentorder.FieldPlanID).Aggregate(dbent.Count()).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("get plan purchase counts: %w", err)
	}
	for _, row := range rows {
		counts[row.PlanID] = row.Count
	}
	return counts, nil
}

// assignedPlanPurchasePredicate also recognizes an assignment whose subsequent
// fulfillment step failed. The durable audit prevents retries and refunds from
// releasing a purchase that has already granted subscription days.
func assignedPlanPurchasePredicate() predicate.PaymentOrder {
	return func(s *sql.Selector) {
		builder := sql.Dialect(s.Dialect())
		audit := builder.Table(paymentauditlog.Table)
		assigned := builder.Select(audit.C(paymentauditlog.FieldID)).From(audit).Where(sql.And(
			sql.ExprP(audit.C(paymentauditlog.FieldOrderID)+" = CAST("+s.C(paymentorder.FieldID)+" AS text)"),
			sql.In(audit.C(paymentauditlog.FieldAction), "SUBSCRIPTION_ASSIGNED", "SUBSCRIPTION_SUCCESS"),
		))
		s.Where(sql.Or(
			sql.NotNull(s.C(paymentorder.FieldCompletedAt)),
			sql.EQ(s.C(paymentorder.FieldStatus), OrderStatusCompleted),
			sql.Exists(assigned),
		))
	}
}

// checkPlanAssignmentLimit runs under the same purchaser lock as order creation.
// Late payments for cancelled/expired orders cannot grant additional purchases.
// Such paid orders enter the existing admin refund workflow without entitlement.
func checkPlanAssignmentLimit(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, plan *dbent.SubscriptionPlan) error {
	if plan == nil || plan.PurchaseLimit <= 0 {
		return nil
	}
	count, err := client.PaymentOrder.Query().Where(
		paymentorder.UserIDEQ(order.UserID), paymentorder.PlanIDEQ(plan.ID),
		paymentorder.IDNEQ(order.ID), paymentorder.OrderTypeEQ(payment.OrderTypeSubscription),
		assignedPlanPurchasePredicate(),
	).Count(ctx)
	if err != nil {
		return fmt.Errorf("count assigned plan purchases: %w", err)
	}
	if count >= plan.PurchaseLimit {
		return planPurchaseLimitError(plan.PurchaseLimit)
	}
	return nil
}

func (s *PaymentService) requestPurchaseLimitRefund(ctx context.Context, orderID int64, lease *paymentFulfillmentLease) error {
	now := time.Now()
	count, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(orderID), paymentorder.StatusEQ(OrderStatusRecharging),
		paymentorder.UpdatedAtEQ(lease.version),
	).SetStatus(OrderStatusRefundRequested).
		SetRefundRequestedAt(now).SetRefundRequestedBy("system").
		SetRefundRequestReason(subscriptionPurchaseLimitRefundReason).
		SetFailedAt(now).SetFailedReason(subscriptionPurchaseLimitRefundReason).Save(ctx)
	if err != nil {
		return fmt.Errorf("request purchase limit refund: %w", err)
	}
	if count == 0 {
		return infraerrors.Conflict("CONFLICT", "fulfillment lease was lost")
	}
	s.writeAuditLog(ctx, orderID, "REFUND_REQUESTED", "system", map[string]any{
		"reason": subscriptionPurchaseLimitRefundReason, "subscription_granted": false,
	})
	return nil
}
