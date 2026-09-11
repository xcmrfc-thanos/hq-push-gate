package transport

import (
	"context"
	"errors"
	"testing"
)

// fakeTicketStore 票据存取桩。
type fakeTicketStore struct {
	data map[string]int64
}

func (f *fakeTicketStore) Take(_ context.Context, ticket string) (int64, bool) {
	uid, ok := f.data[ticket]
	if ok {
		delete(f.data, ticket) // 一次性：取后即焚
	}
	return uid, ok
}

// fakeInner 用户 JWT 鉴权桩。
type fakeInner struct{ uid int64 }

func (f *fakeInner) VerifyToken(string) (int64, error) {
	if f.uid <= 0 {
		return 0, errors.New("invalid jwt")
	}
	return f.uid, nil
}

func TestTicketAuthRoutesTicket(t *testing.T) {
	a := NewTicketAuth(&fakeInner{uid: 1}, &fakeTicketStore{data: map[string]int64{"tk_abc": 42}})
	uid, err := a.VerifyToken("tk_abc")
	if err != nil || uid != 42 {
		t.Fatalf("ticket: uid=%d err=%v", uid, err)
	}
	// 一次性：二次取应失败
	if _, err := a.VerifyToken("tk_abc"); err == nil {
		t.Fatal("ticket must be single-use")
	}
}

func TestTicketAuthFallbackToJWT(t *testing.T) {
	a := NewTicketAuth(&fakeInner{uid: 7}, &fakeTicketStore{data: map[string]int64{}})
	uid, err := a.VerifyToken("eyJhbGciOiJIUzI1NiJ9.xxx")
	if err != nil || uid != 7 {
		t.Fatalf("jwt fallback: uid=%d err=%v", uid, err)
	}
	// 未知票据 → 不回落 JWT，直接失败
	if _, err := a.VerifyToken("tk_unknown"); err == nil {
		t.Fatal("unknown ticket must fail")
	}
}

func TestTicketAuthNilStore(t *testing.T) {
	a := NewTicketAuth(&fakeInner{uid: 1}, nil)
	if _, err := a.VerifyToken("tk_x"); err == nil {
		t.Fatal("nil store must fail ticket path")
	}
	if uid, err := a.VerifyToken("jwt"); err != nil || uid != 1 {
		t.Fatal("nil store must not block jwt path")
	}
}
