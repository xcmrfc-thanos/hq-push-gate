package application

import (
	"context"
	"log/slog"
	"testing"
	"time"

	hqv1 "github.com/hqpush/gate/packages/contract/go/hq/v1"

	"github.com/hqpush/gate/packages/contract"
	"github.com/hqpush/gate/packages/obs"
	"github.com/hqpush/gate/services/notify/internal/domain"
)

type fakeStore struct {
	rule       *RuleRow
	inserted   []*domain.AlertRecord
	markedSent []string
}

func (f *fakeStore) RuleByID(context.Context, int64) (*RuleRow, error) { return f.rule, nil }
func (f *fakeStore) InsertAlertRecords(_ context.Context, rs []*domain.AlertRecord) (int64, error) {
	f.inserted = append(f.inserted, rs...)
	return int64(len(rs)), nil
}
func (f *fakeStore) MarkSent(_ context.Context, ids []string) error {
	f.markedSent = append(f.markedSent, ids...)
	return nil
}

type fakeRoute struct {
	gw    string
	slots []uint32
}

func (f *fakeRoute) GatewayOfUser(context.Context, int64) (string, error) { return f.gw, nil }
func (f *fakeRoute) SlotsOfGateway(context.Context, string) ([]uint32, error) {
	return f.slots, nil
}

type fakePublisher struct{ published int }

func (f *fakePublisher) PublishAlert(context.Context, string, uint32, *hqv1.AlertDelivery) error {
	f.published++
	return nil
}

func newTestInbox(store *fakeStore, route *fakeRoute, pub *fakePublisher) *InboxService {
	return NewInboxService(store, route, pub, slog.Default(), obs.NewMetrics(), 72*time.Hour)
}

func testEvent(id string, ruleID int64) *hqv1.AlertEvent {
	return &hqv1.AlertEvent{EventId: id, RuleId: ruleID, TriggerTs: time.Now().UnixMilli()}
}

func TestInboxOnlineUserDelivered(t *testing.T) {
	store := &fakeStore{rule: &RuleRow{ID: 5, UserID: 42}}
	route := &fakeRoute{gw: "ws-a", slots: []uint32{3}}
	pub := &fakePublisher{}
	svc := newTestInbox(store, route, pub)

	if err := svc.HandleAlertEvent(context.Background(), testEvent("r5-v1-w100", 5)); err != nil {
		t.Fatal(err)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("want 1 record inserted, got %d", len(store.inserted))
	}
	if want := contract.DeliveryID("r5-v1-w100", 42); store.inserted[0].DeliveryID != want {
		t.Fatalf("delivery_id mismatch: got %s want %s", store.inserted[0].DeliveryID, want)
	}
	if pub.published != 1 {
		t.Fatalf("want 1 published, got %d", pub.published)
	}
	if len(store.markedSent) != 1 {
		t.Fatal("delivered alert should be marked SENT")
	}
}

func TestInboxOfflineUserStaysPending(t *testing.T) {
	store := &fakeStore{rule: &RuleRow{ID: 5, UserID: 42}}
	route := &fakeRoute{gw: "", slots: nil} // 不在线
	pub := &fakePublisher{}
	svc := newTestInbox(store, route, pub)

	if err := svc.HandleAlertEvent(context.Background(), testEvent("r5-v1-w100", 5)); err != nil {
		t.Fatal(err)
	}
	if len(store.inserted) != 1 {
		t.Fatal("offline user should still be persisted (PENDING)")
	}
	if pub.published != 0 {
		t.Fatal("offline user should not be pushed")
	}
	if len(store.markedSent) != 0 {
		t.Fatal("offline delivery must stay PENDING for 补拉")
	}
}

func TestInboxGatewayWithoutSlotsStaysPending(t *testing.T) {
	store := &fakeStore{rule: &RuleRow{ID: 5, UserID: 42}}
	route := &fakeRoute{gw: "ws-a", slots: nil} // 实例无租约槽位
	pub := &fakePublisher{}
	svc := newTestInbox(store, route, pub)

	if err := svc.HandleAlertEvent(context.Background(), testEvent("r5-v1-w100", 5)); err != nil {
		t.Fatal(err)
	}
	if pub.published != 0 || len(store.markedSent) != 0 {
		t.Fatal("no slot should mean no publish and stay PENDING")
	}
}

func TestInboxMissingRuleSkips(t *testing.T) {
	store := &fakeStore{rule: nil}
	svc := newTestInbox(store, &fakeRoute{}, &fakePublisher{})

	if err := svc.HandleAlertEvent(context.Background(), testEvent("r99-v1-w100", 99)); err != nil {
		t.Fatal(err)
	}
	if len(store.inserted) != 0 {
		t.Fatal("missing rule should skip delivery")
	}
}
