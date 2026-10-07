package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	platformmock "github.com/codercollo/willcoll/backend/internal/platform/db/mock"
	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/mock"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/mock/gomock"
)

// newChannelClaimTestApp is newPropertyTestApp plus a platform Channels
// model on a mock platform store (build-phases.txt 3i-d: a hand-typed
// payhero_channel_id is claimed in platform.payment_channels).
func newChannelClaimTestApp(t *testing.T) (*application, *mock.MockQuerier, *platformmock.MockQuerier) {
	t.Helper()
	app, q := newPropertyTestApp(t)

	ctrl := gomock.NewController(t)
	pStore := platformmock.NewMockStore(ctrl)
	pQuerier := platformmock.NewMockQuerier(ctrl)
	pStore.EXPECT().ExecTenantTx(gomock.Any(), testTenantID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(platformsqlc.Querier) error) error { return fn(pQuerier) }).
		AnyTimes()
	app.platform = &platformdata.Models{Channels: platformdata.ChannelModel{Store: pStore}}
	return app, q, pQuerier
}

func propertyRowWithChannel(channel string) sqlc.Property {
	row := testPropertyRow()
	row.PayheroChannelID = &channel
	return row
}

var (
	errChannelConflict = &pgconn.PgError{Code: "23505", ConstraintName: "payment_channels_pkey"}
	errChannelHasInbox = &pgconn.PgError{Code: "23503", ConstraintName: "payment_inbox_channel_id_fkey"}
)

func TestCreatePropertyClaimsHandTypedChannel(t *testing.T) {
	const body = `{"landlord_id": "00000000-0000-0000-0000-0000000000aa", "name": "Runda Arcade", "location": "Runda", "slug": "runda-arcade", "payhero_channel_id": "777"}`

	t.Run("claims the channel for the new property's own id, then saves", func(t *testing.T) {
		app, q, p := newChannelClaimTestApp(t)

		var claimedOwner uuid.UUID
		p.EXPECT().RegisterPaymentChannel(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg platformsqlc.RegisterPaymentChannelParams) (platformsqlc.PaymentChannel, error) {
				if arg.ChannelID != "777" || arg.Product != platformdata.ProductRentals || arg.ManagerID != testTenantID || arg.OwnerID == nil {
					t.Errorf("claim = %+v", arg)
				} else {
					claimedOwner = *arg.OwnerID
				}
				return platformsqlc.PaymentChannel{}, nil
			})
		q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().CreateProperty(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg sqlc.CreatePropertyParams) (sqlc.Property, error) {
				if arg.ID == nil || *arg.ID != claimedOwner {
					t.Errorf("property saved with id %v, but the channel was claimed for %v", arg.ID, claimedOwner)
				}
				return propertyRowWithChannel("777"), nil
			})
		q.EXPECT().CreateReceiptCounter(gomock.Any(), gomock.Any()).Return(nil)

		w := serveAsTenant(app.createPropertyHandler, http.MethodPost, "/v1/properties", body, nil)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("a channel owned by someone else is 409 and the property is not saved", func(t *testing.T) {
		app, _, p := newChannelClaimTestApp(t)
		p.EXPECT().RegisterPaymentChannel(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentChannel{}, errChannelConflict)
		// Not ours: another manager's claim is invisible under RLS, so the
		// follow-up lookup finds nothing. No property query is expected.
		p.EXPECT().GetPaymentChannel(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentChannel{}, pgx.ErrNoRows)

		w := serveAsTenant(app.createPropertyHandler, http.MethodPost, "/v1/properties", body, nil)
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("platform unreachable is 503 and the property is not saved", func(t *testing.T) {
		app, _, p := newChannelClaimTestApp(t)
		p.EXPECT().RegisterPaymentChannel(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentChannel{}, errors.New("connection refused"))

		w := serveAsTenant(app.createPropertyHandler, http.MethodPost, "/v1/properties", body, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, body %s, want 503", w.Code, w.Body)
		}
	})

	t.Run("a failed property save releases the claim it just made", func(t *testing.T) {
		app, q, p := newChannelClaimTestApp(t)
		p.EXPECT().RegisterPaymentChannel(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentChannel{}, nil)
		q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().CreateProperty(gomock.Any(), gomock.Any()).
			Return(sqlc.Property{}, &pgconn.PgError{Code: "23505", ConstraintName: "properties_slug_key"})
		p.EXPECT().ReleasePaymentChannel(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg platformsqlc.ReleasePaymentChannelParams) (int64, error) {
				if arg.ChannelID != "777" {
					t.Errorf("released %q, want 777", arg.ChannelID)
				}
				return 1, nil
			})

		w := serveAsTenant(app.createPropertyHandler, http.MethodPost, "/v1/properties", body, nil)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("no channel id means platform is never touched", func(t *testing.T) {
		app, q, _ := newChannelClaimTestApp(t)
		q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().CreateProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		q.EXPECT().CreateReceiptCounter(gomock.Any(), gomock.Any()).Return(nil)

		noChannel := strings.Replace(body, `, "payhero_channel_id": "777"`, "", 1)
		w := serveAsTenant(app.createPropertyHandler, http.MethodPost, "/v1/properties", noChannel, nil)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
	})
}

func TestUpdatePropertyClaimsAndReleasesChannel(t *testing.T) {
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}

	t.Run("changing the channel claims the new one, saves, then releases the old", func(t *testing.T) {
		app, q, p := newChannelClaimTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(propertyRowWithChannel("111"), nil)

		gomock.InOrder(
			p.EXPECT().RegisterPaymentChannel(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, arg platformsqlc.RegisterPaymentChannelParams) (platformsqlc.PaymentChannel, error) {
					if arg.ChannelID != "222" || arg.OwnerID == nil || *arg.OwnerID != testPropertyID {
						t.Errorf("claim = %+v", arg)
					}
					return platformsqlc.PaymentChannel{}, nil
				}),
			q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil),
			q.EXPECT().UpdateProperty(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, arg sqlc.UpdatePropertyParams) (int32, error) {
					return arg.Version + 1, nil
				}),
			p.EXPECT().ReleasePaymentChannel(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, arg platformsqlc.ReleasePaymentChannelParams) (int64, error) {
					if arg.ChannelID != "111" || arg.OwnerID == nil || *arg.OwnerID != testPropertyID {
						t.Errorf("release = %+v, want channel 111 owned by the property", arg)
					}
					return 1, nil
				}),
		)

		w := serveAsTenant(app.updatePropertyHandler, http.MethodPatch, "/", `{"payhero_channel_id": "222"}`, params)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("clearing the channel only releases the old claim", func(t *testing.T) {
		app, q, p := newChannelClaimTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(propertyRowWithChannel("111"), nil)
		q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().UpdateProperty(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg sqlc.UpdatePropertyParams) (int32, error) { return arg.Version + 1, nil })
		p.EXPECT().ReleasePaymentChannel(gomock.Any(), gomock.Any()).Return(int64(1), nil)

		w := serveAsTenant(app.updatePropertyHandler, http.MethodPatch, "/", `{"payhero_channel_id": ""}`, params)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("a channel owned by someone else is 409 and nothing is saved", func(t *testing.T) {
		app, q, p := newChannelClaimTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(propertyRowWithChannel("111"), nil)
		p.EXPECT().RegisterPaymentChannel(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentChannel{}, errChannelConflict)
		p.EXPECT().GetPaymentChannel(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentChannel{
			ChannelID: "222", ManagerID: testTenantID, Product: platformdata.ProductRentals, OwnerID: ptr(uuid.New()),
		}, nil)

		w := serveAsTenant(app.updatePropertyHandler, http.MethodPatch, "/", `{"payhero_channel_id": "222"}`, params)
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, body %s, want 409", w.Code, w.Body)
		}
	})

	t.Run("a channel claimed under another product is 409 too", func(t *testing.T) {
		app, q, p := newChannelClaimTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		p.EXPECT().RegisterPaymentChannel(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentChannel{}, errChannelConflict)
		p.EXPECT().GetPaymentChannel(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentChannel{
			ChannelID: "222", ManagerID: testTenantID, Product: "estates", OwnerID: ptr(testPropertyID),
		}, nil)

		w := serveAsTenant(app.updatePropertyHandler, http.MethodPatch, "/", `{"payhero_channel_id": "222"}`, params)
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, body %s, want 409", w.Code, w.Body)
		}
	})

	t.Run("a claim this property already holds is accepted (retry after a failed save)", func(t *testing.T) {
		app, q, p := newChannelClaimTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		p.EXPECT().RegisterPaymentChannel(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentChannel{}, errChannelConflict)
		p.EXPECT().GetPaymentChannel(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentChannel{
			ChannelID: "222", ManagerID: testTenantID, Product: platformdata.ProductRentals, OwnerID: ptr(testPropertyID),
		}, nil)
		q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().UpdateProperty(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg sqlc.UpdatePropertyParams) (int32, error) { return arg.Version + 1, nil })

		w := serveAsTenant(app.updatePropertyHandler, http.MethodPatch, "/", `{"payhero_channel_id": "222"}`, params)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("platform unreachable is 503 and nothing is saved", func(t *testing.T) {
		app, q, p := newChannelClaimTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		p.EXPECT().RegisterPaymentChannel(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentChannel{}, errors.New("connection refused"))

		w := serveAsTenant(app.updatePropertyHandler, http.MethodPatch, "/", `{"payhero_channel_id": "222"}`, params)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, body %s, want 503", w.Code, w.Body)
		}
	})

	t.Run("an unchanged channel makes no platform calls", func(t *testing.T) {
		app, q, _ := newChannelClaimTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(propertyRowWithChannel("111"), nil)
		q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().UpdateProperty(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg sqlc.UpdatePropertyParams) (int32, error) { return arg.Version + 1, nil })

		w := serveAsTenant(app.updatePropertyHandler, http.MethodPatch, "/", `{"name": "Renamed", "payhero_channel_id": "111"}`, params)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("a failed save releases the claim it just made, not the old one", func(t *testing.T) {
		app, q, p := newChannelClaimTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(propertyRowWithChannel("111"), nil)
		p.EXPECT().RegisterPaymentChannel(gomock.Any(), gomock.Any()).Return(platformsqlc.PaymentChannel{}, nil)
		q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().UpdateProperty(gomock.Any(), gomock.Any()).Return(int32(0), &pgconn.PgError{Code: "23505", ConstraintName: "properties_slug_key"})
		p.EXPECT().ReleasePaymentChannel(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg platformsqlc.ReleasePaymentChannelParams) (int64, error) {
				if arg.ChannelID != "222" {
					t.Errorf("released %q, want the new claim 222", arg.ChannelID)
				}
				return 1, nil
			})

		w := serveAsTenant(app.updatePropertyHandler, http.MethodPatch, "/", `{"payhero_channel_id": "222"}`, params)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("an old claim with recorded payments is kept without failing the save", func(t *testing.T) {
		app, q, p := newChannelClaimTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(propertyRowWithChannel("111"), nil)
		q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().UpdateProperty(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg sqlc.UpdatePropertyParams) (int32, error) { return arg.Version + 1, nil })
		p.EXPECT().ReleasePaymentChannel(gomock.Any(), gomock.Any()).Return(int64(0), errChannelHasInbox)

		w := serveAsTenant(app.updatePropertyHandler, http.MethodPatch, "/", `{"payhero_channel_id": ""}`, params)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
	})
}

func ptr[T any](v T) *T { return &v }
