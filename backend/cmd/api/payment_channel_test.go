package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/db/mock"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/gin-gonic/gin"
	"go.uber.org/mock/gomock"
)

// newPaymentChannelTestApp is newPropertyTestApp plus a fake PayHero client
// and an account id, which registerPaymentChannelHandler /
// showPaymentChannelStatusHandler both need.
func newPaymentChannelTestApp(t *testing.T) (*application, *mock.MockQuerier, *fakePush) {
	t.Helper()
	app, q := newPropertyTestApp(t)
	push := &fakePush{}
	app.payhero = push
	app.config.payhero.accountID = "9"
	return app, q, push
}

func expectPropertyUpdateToChannel(t *testing.T, q *mock.MockQuerier, wantChannelID string) {
	q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
	q.EXPECT().UpdateProperty(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg sqlc.UpdatePropertyParams) (int32, error) {
			if arg.PayheroChannelID == nil || *arg.PayheroChannelID != wantChannelID {
				t.Errorf("saved payhero_channel_id = %v, want %q", arg.PayheroChannelID, wantChannelID)
			}
			return arg.Version + 1, nil
		})
}

func TestRegisterPaymentChannelHandler(t *testing.T) {
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}

	t.Run("bank: registers a new channel and saves its id", func(t *testing.T) {
		app, q, push := newPaymentChannelTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		push.listResp = nil // nothing registered yet
		push.registerResp = &payhero.Channel{ID: 13236, ChannelType: "bank", ShortCode: 522522, AccountNumber: "1322334437", IsActive: true}
		expectPropertyUpdateToChannel(t, q, "13236")

		body := `{"type":"bank","bank":"KCB Bank","account_number":"1322334437","description":"Landlord KCB account"}`
		w := serveAsManager(app.registerPaymentChannelHandler, http.MethodPost, "/", body, params)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
		if !strings.Contains(w.Body.String(), `"id": 13236`) || !strings.Contains(w.Body.String(), `"reused": false`) {
			t.Errorf("body = %s", w.Body)
		}
		if len(push.registerCalls) != 1 || push.registerCalls[0].ShortCode != 522522 || push.registerCalls[0].AccountID != 9 {
			t.Errorf("register calls = %+v (want KCB's paybill 522522, account_id 9)", push.registerCalls)
		}
	})

	t.Run("unrecognised bank name is rejected before calling PayHero", func(t *testing.T) {
		app, q, push := newPaymentChannelTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		body := `{"type":"bank","bank":"Not A Real Bank","account_number":"123","description":"x"}`
		w := serveAsManager(app.registerPaymentChannelHandler, http.MethodPost, "/", body, params)

		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
		if len(push.registerCalls) != 0 {
			t.Error("must not call PayHero for an unrecognised bank")
		}
	})

	t.Run("paybill: reuses a matching active channel instead of registering a duplicate", func(t *testing.T) {
		app, q, push := newPaymentChannelTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		push.listResp = []payhero.Channel{
			{ID: 500, ChannelType: "paybill", ShortCode: 987654, AccountNumber: "UNIT-A1", IsActive: true},
		}
		expectPropertyUpdateToChannel(t, q, "500")

		body := `{"type":"paybill","short_code":987654,"account_number":"UNIT-A1","description":"Runda Arcade paybill"}`
		w := serveAsManager(app.registerPaymentChannelHandler, http.MethodPost, "/", body, params)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
		if !strings.Contains(w.Body.String(), `"reused": true`) {
			t.Errorf("body = %s, want reused: true", w.Body)
		}
		if len(push.registerCalls) != 0 {
			t.Error("must not register a duplicate when a matching channel already exists")
		}
	})

	t.Run("inactive channel with the same details is not reused", func(t *testing.T) {
		app, q, push := newPaymentChannelTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		push.listResp = []payhero.Channel{
			{ID: 500, ChannelType: "paybill", ShortCode: 987654, AccountNumber: "UNIT-A1", IsActive: false},
		}
		push.registerResp = &payhero.Channel{ID: 501, ChannelType: "paybill", ShortCode: 987654, AccountNumber: "UNIT-A1", IsActive: true}
		expectPropertyUpdateToChannel(t, q, "501")

		body := `{"type":"paybill","short_code":987654,"account_number":"UNIT-A1","description":"Runda Arcade paybill"}`
		w := serveAsManager(app.registerPaymentChannelHandler, http.MethodPost, "/", body, params)

		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"reused": false`) {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("PayHero's rejection reason is shown to the manager in plain words", func(t *testing.T) {
		app, q, push := newPaymentChannelTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		push.registerErr = &payhero.APIError{StatusCode: 422, Body: "account_number is already registered to another channel"}

		body := `{"type":"till","short_code":123456,"description":"Shop till"}`
		w := serveAsManager(app.registerPaymentChannelHandler, http.MethodPost, "/", body, params)

		if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "already registered to another channel") {
			t.Errorf("status %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("missing type is rejected", func(t *testing.T) {
		app, q, _ := newPaymentChannelTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		w := serveAsManager(app.registerPaymentChannelHandler, http.MethodPost, "/", `{"description":"x"}`, params)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
	})
}

func TestSendPaymentChannelTestHandler(t *testing.T) {
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}

	t.Run("sends a KES 10 STK push to the given phone through the property's channel", func(t *testing.T) {
		app, q, push := newPaymentChannelTestApp(t)
		row := testPropertyRow()
		id := "13236"
		row.PayheroChannelID = &id
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(row, nil)

		w := serveAsManager(app.sendPaymentChannelTestHandler, http.MethodPost, "/", `{"phone":"+254722000001"}`, params)

		if w.Code != http.StatusAccepted {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
		if len(push.calls) != 1 || push.calls[0].Amount != 10 || push.calls[0].ChannelID != "13236" || push.calls[0].PhoneNumber != "254722000001" {
			t.Errorf("push calls = %+v", push.calls)
		}
		if !strings.HasPrefix(push.calls[0].ExternalReference, "TEST-") {
			t.Errorf("reference = %q, want a TEST- prefix", push.calls[0].ExternalReference)
		}
	})

	t.Run("no channel connected yet", func(t *testing.T) {
		app, q, push := newPaymentChannelTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil) // no channel id

		w := serveAsManager(app.sendPaymentChannelTestHandler, http.MethodPost, "/", `{"phone":"+254722000001"}`, params)

		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
		if len(push.calls) != 0 {
			t.Error("must not push without a connected channel")
		}
	})

	t.Run("a non-Kenyan number is rejected", func(t *testing.T) {
		app, q, _ := newPaymentChannelTestApp(t)
		row := testPropertyRow()
		id := "13236"
		row.PayheroChannelID = &id
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(row, nil)

		w := serveAsManager(app.sendPaymentChannelTestHandler, http.MethodPost, "/", `{"phone":"+15551234567"}`, params)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
	})
}

func TestListPaymentChannelBanksHandler(t *testing.T) {
	app, _, _ := newPaymentChannelTestApp(t)
	w := serveAsManager(app.listPaymentChannelBanksHandler, http.MethodGet, "/", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "KCB") {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
}

func TestShowPaymentChannelStatusHandler(t *testing.T) {
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}

	t.Run("no channel saved yet", func(t *testing.T) {
		app, q, _ := newPaymentChannelTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)

		w := serveAsManager(app.showPaymentChannelStatusHandler, http.MethodGet, "/", "", params)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status": "missing"`) {
			t.Fatalf("status %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("saved channel is active", func(t *testing.T) {
		app, q, push := newPaymentChannelTestApp(t)
		row := testPropertyRow()
		id := "13236"
		row.PayheroChannelID = &id
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(row, nil)
		push.listResp = []payhero.Channel{{ID: 13236, ChannelType: "bank", AccountNumber: "1322334437", IsActive: true}}

		w := serveAsManager(app.showPaymentChannelStatusHandler, http.MethodGet, "/", "", params)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status": "active"`) {
			t.Fatalf("status %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("saved channel now inactive on PayHero's side", func(t *testing.T) {
		app, q, push := newPaymentChannelTestApp(t)
		row := testPropertyRow()
		id := "13236"
		row.PayheroChannelID = &id
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(row, nil)
		push.listResp = []payhero.Channel{{ID: 13236, IsActive: false}}

		w := serveAsManager(app.showPaymentChannelStatusHandler, http.MethodGet, "/", "", params)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status": "inactive"`) {
			t.Fatalf("status %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("saved channel no longer found on PayHero's side", func(t *testing.T) {
		app, q, push := newPaymentChannelTestApp(t)
		row := testPropertyRow()
		id := "13236"
		row.PayheroChannelID = &id
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(row, nil)
		push.listResp = nil

		w := serveAsManager(app.showPaymentChannelStatusHandler, http.MethodGet, "/", "", params)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status": "inactive"`) {
			t.Fatalf("status %d, body %s", w.Code, w.Body)
		}
	})
}
