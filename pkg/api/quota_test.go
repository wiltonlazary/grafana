package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	macaron "gopkg.in/macaron.v1"

	"github.com/grafana/grafana/pkg/api/routing"
	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/accesscontrol"
	accesscontrolmock "github.com/grafana/grafana/pkg/services/accesscontrol/mock"
	"github.com/grafana/grafana/pkg/services/quota"
	"github.com/grafana/grafana/pkg/services/sqlstore"
	"github.com/grafana/grafana/pkg/setting"
)

var getCurrentOrgUrl = "/api/org/"
var getCurrentOrgQuotasUrl = "/api/org/quotas"

func setAccessControlPermissions(acmock *accesscontrolmock.Mock, perms []*accesscontrol.Permission) {
	acmock.GetUserPermissionsFunc = func(_ context.Context, _ *models.SignedInUser) ([]*accesscontrol.Permission, error) {
		return perms, nil
	}
}

func setupHTTPServer(t *testing.T, enableAccessControl bool, signedInUser *models.SignedInUser) (*macaron.Macaron, *HTTPServer, *accesscontrolmock.Mock) {
	t.Helper()

	// Use an accesscontrol mock
	acmock := accesscontrolmock.New()
	if !enableAccessControl {
		acmock = acmock.WithDisabled()
	}

	// Use a new conf
	cfg := setting.NewCfg()
	cfg.FeatureToggles = make(map[string]bool)
	cfg.FeatureToggles["accesscontrol"] = enableAccessControl

	// Use a test DB
	db := sqlstore.InitTestDB(t)

	// Create minimal HTTP Server
	hs := &HTTPServer{
		Cfg:           cfg,
		Bus:           bus.GetBus(),
		Live:          newTestLive(t),
		QuotaService:  &quota.QuotaService{Cfg: cfg},
		RouteRegister: routing.NewRouteRegister(),
		AccessControl: acmock,
		SQLStore:      db,
	}

	// Instantiate a new Server
	m := macaron.New()

	// Pretend middleware to sign the user in
	if signedInUser != nil {
		m.Use(func(c *macaron.Context) {
			ctx := &models.ReqContext{
				Context:      c,
				IsSignedIn:   true,
				SignedInUser: signedInUser,
				Logger:       log.New("api-test"),
			}
			c.Map(ctx)
		})
	}

	// Register all routes
	hs.registerRoutes()
	hs.RouteRegister.Register(m.Router)

	return m, hs, acmock
}

func callAPI(server *macaron.Macaron, method, path string, body io.Reader, t *testing.T) *httptest.ResponseRecorder {
	req, err := http.NewRequest(method, path, body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	return recorder
}

func TestAPIEndpoint_GetCurrentOrg_LegacyAccessControl(t *testing.T) {
	testuser := &models.SignedInUser{UserId: testUserID, OrgId: testOrgID, OrgRole: models.ROLE_VIEWER, Login: testUserLogin}
	server, hs, _ := setupHTTPServer(t, false, testuser)

	_, err := hs.SQLStore.CreateOrgWithMember("TestOrg", testUserID)
	require.NoError(t, err)

	t.Run("Viewer can view CurrentOrg", func(t *testing.T) {
		response := callAPI(server, http.MethodGet, getCurrentOrgUrl, nil, t)
		assert.Equal(t, http.StatusOK, response.Code)
	})
}

func TestAPIEndpoint_GetCurrentOrg_AccessControl(t *testing.T) {
	testuser := &models.SignedInUser{UserId: testUserID, OrgId: testOrgID, OrgRole: models.ROLE_VIEWER, Login: testUserLogin}
	server, hs, acmock := setupHTTPServer(t, true, testuser)

	_, err := hs.SQLStore.CreateOrgWithMember("TestOrg", testUserID)
	require.NoError(t, err)

	t.Run("AccessControl allows viewing CurrentOrg with correct permissions", func(t *testing.T) {
		setAccessControlPermissions(acmock, []*accesscontrol.Permission{{Action: ActionOrgsRead, Scope: ScopeOrgsAll}})
		response := callAPI(server, http.MethodGet, getCurrentOrgUrl, nil, t)
		assert.Equal(t, http.StatusOK, response.Code)
	})
	t.Run("AccessControl prevents viewing CurrentOrg with incorrect permissions", func(t *testing.T) {
		setAccessControlPermissions(acmock, []*accesscontrol.Permission{{Action: "orgs:invalid"}})
		response := callAPI(server, http.MethodGet, getCurrentOrgUrl, nil, t)
		assert.Equal(t, http.StatusForbidden, response.Code)
	})
}

func TestAPIEndpoint_GetCurrentOrgQuotas_LegacyAccessControl(t *testing.T) {
	testuser := &models.SignedInUser{UserId: testUserID, OrgId: testOrgID, OrgRole: models.ROLE_VIEWER, Login: testUserLogin}
	server, hs, _ := setupHTTPServer(t, false, testuser)

	// TODO Tidy that
	hs.Cfg.Quota.Enabled = true
	setting.Quota.Enabled = true
	hs.Cfg.Quota.Org = &setting.OrgQuota{
		User:       10,
		DataSource: 10,
		Dashboard:  10,
		ApiKey:     10,
		AlertRule:  10,
	}
	setting.Quota.Org = &setting.OrgQuota{
		User:       10,
		DataSource: 10,
		Dashboard:  10,
		ApiKey:     10,
		AlertRule:  10,
	}

	_, err := hs.SQLStore.CreateOrgWithMember("TestOrg", testUserID)
	require.NoError(t, err)

	t.Run("Viewer can view CurrentOrgQuotas", func(t *testing.T) {
		response := callAPI(server, http.MethodGet, getCurrentOrgQuotasUrl, nil, t)
		assert.Equal(t, http.StatusOK, response.Code)
	})
}

func TestAPIEndpoint_GetCurrentOrgQuotas_AccessControl(t *testing.T) {
	testuser := &models.SignedInUser{UserId: testUserID, OrgId: testOrgID, OrgRole: models.ROLE_VIEWER, Login: testUserLogin}
	server, hs, acmock := setupHTTPServer(t, true, testuser)

	// TODO Tidy that
	hs.Cfg.Quota.Enabled = true
	setting.Quota.Enabled = true
	hs.Cfg.Quota.Org = &setting.OrgQuota{
		User:       10,
		DataSource: 10,
		Dashboard:  10,
		ApiKey:     10,
		AlertRule:  10,
	}
	setting.Quota.Org = &setting.OrgQuota{
		User:       10,
		DataSource: 10,
		Dashboard:  10,
		ApiKey:     10,
		AlertRule:  10,
	}

	_, err := hs.SQLStore.CreateOrgWithMember("TestOrg", testUserID)
	require.NoError(t, err)

	t.Run("AccessControl allows viewing CurrentOrgQuotas with correct permissions", func(t *testing.T) {
		setAccessControlPermissions(acmock, []*accesscontrol.Permission{{Action: ActionOrgsRead, Scope: ScopeOrgsAll}})
		response := callAPI(server, http.MethodGet, getCurrentOrgQuotasUrl, nil, t)
		assert.Equal(t, http.StatusOK, response.Code)
	})
	t.Run("AccessControl prevents viewing CurrentOrgQuotas with incorrect permissions", func(t *testing.T) {
		setAccessControlPermissions(acmock, []*accesscontrol.Permission{{Action: "orgs:invalid"}})
		response := callAPI(server, http.MethodGet, getCurrentOrgQuotasUrl, nil, t)
		assert.Equal(t, http.StatusForbidden, response.Code)
	})
}
