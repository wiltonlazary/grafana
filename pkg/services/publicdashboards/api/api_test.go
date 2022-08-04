package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana/pkg/api/dtos"
	"github.com/grafana/grafana/pkg/components/simplejson"
	"github.com/grafana/grafana/pkg/infra/localcache"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/dashboards"
	dashboardStore "github.com/grafana/grafana/pkg/services/dashboards/database"
	"github.com/grafana/grafana/pkg/services/datasources"
	fakeDatasources "github.com/grafana/grafana/pkg/services/datasources/fakes"
	"github.com/grafana/grafana/pkg/services/datasources/service"
	"github.com/grafana/grafana/pkg/services/featuremgmt"
	"github.com/grafana/grafana/pkg/services/publicdashboards"
	publicdashboardsStore "github.com/grafana/grafana/pkg/services/publicdashboards/database"
	. "github.com/grafana/grafana/pkg/services/publicdashboards/models"
	publicdashboardsService "github.com/grafana/grafana/pkg/services/publicdashboards/service"
	"github.com/grafana/grafana/pkg/services/sqlstore"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/grafana/grafana/pkg/web"
)

func TestAPIGetPublicDashboard(t *testing.T) {
	t.Run("It should 404 if featureflag is not enabled", func(t *testing.T) {
		cfg := setting.NewCfg()
		qs := buildQueryDataService(t, nil, nil, nil)
		service := publicdashboards.NewFakePublicDashboardService(t)
		service.On("GetPublicDashboard", mock.Anything, mock.AnythingOfType("string")).
			Return(&models.Dashboard{}, nil).Maybe()

		testServer := setupTestServer(t, cfg, qs, featuremgmt.WithFeatures(), service, nil)

		response := callAPI(testServer, http.MethodGet, "/api/public/dashboards", nil, t)
		assert.Equal(t, http.StatusNotFound, response.Code)

		response = callAPI(testServer, http.MethodGet, "/api/public/dashboards/asdf", nil, t)
		assert.Equal(t, http.StatusNotFound, response.Code)

		// control set. make sure routes are mounted
		testServer = setupTestServer(t, cfg, qs, featuremgmt.WithFeatures(featuremgmt.FlagPublicDashboards), service, nil)
		response = callAPI(testServer, http.MethodGet, "/api/public/dashboards/asdf", nil, t)
		assert.NotEqual(t, http.StatusNotFound, response.Code)
	})

	DashboardUid := "dashboard-abcd1234"
	token, err := uuid.NewRandom()
	require.NoError(t, err)
	accessToken := fmt.Sprintf("%x", token)

	testCases := []struct {
		Name                  string
		AccessToken           string
		ExpectedHttpResponse  int
		PublicDashboardResult *models.Dashboard
		PublicDashboardErr    error
	}{
		{
			Name:                 "It gets a public dashboard",
			AccessToken:          accessToken,
			ExpectedHttpResponse: http.StatusOK,
			PublicDashboardResult: &models.Dashboard{
				Data: simplejson.NewFromAny(map[string]interface{}{
					"Uid": DashboardUid,
				}),
			},
			PublicDashboardErr: nil,
		},
		{
			Name:                  "It should return 404 if no public dashboard",
			AccessToken:           accessToken,
			ExpectedHttpResponse:  http.StatusNotFound,
			PublicDashboardResult: nil,
			PublicDashboardErr:    ErrPublicDashboardNotFound,
		},
	}

	for _, test := range testCases {
		t.Run(test.Name, func(t *testing.T) {
			service := publicdashboards.NewFakePublicDashboardService(t)
			service.On("GetPublicDashboard", mock.Anything, mock.AnythingOfType("string")).
				Return(test.PublicDashboardResult, test.PublicDashboardErr).Maybe()

			testServer := setupTestServer(
				t,
				setting.NewCfg(),
				buildQueryDataService(t, nil, nil, nil),
				featuremgmt.WithFeatures(featuremgmt.FlagPublicDashboards),
				service,
				nil,
			)

			response := callAPI(testServer, http.MethodGet,
				fmt.Sprintf("/api/public/dashboards/%s", test.AccessToken),
				nil,
				t,
			)

			assert.Equal(t, test.ExpectedHttpResponse, response.Code)

			if test.PublicDashboardErr == nil {
				var dashResp dtos.DashboardFullWithMeta
				err := json.Unmarshal(response.Body.Bytes(), &dashResp)
				require.NoError(t, err)

				assert.Equal(t, DashboardUid, dashResp.Dashboard.Get("Uid").MustString())
				assert.Equal(t, false, dashResp.Meta.CanEdit)
				assert.Equal(t, false, dashResp.Meta.CanDelete)
				assert.Equal(t, false, dashResp.Meta.CanSave)
			} else {
				var errResp struct {
					Error string `json:"error"`
				}
				err := json.Unmarshal(response.Body.Bytes(), &errResp)
				require.NoError(t, err)
				assert.Equal(t, test.PublicDashboardErr.Error(), errResp.Error)
			}
		})
	}
}

func TestAPIGetPublicDashboardConfig(t *testing.T) {
	pubdash := &PublicDashboard{IsEnabled: true}

	testCases := []struct {
		Name                  string
		DashboardUid          string
		ExpectedHttpResponse  int
		PublicDashboardResult *PublicDashboard
		PublicDashboardErr    error
	}{
		{
			Name:                  "retrieves public dashboard config when dashboard is found",
			DashboardUid:          "1",
			ExpectedHttpResponse:  http.StatusOK,
			PublicDashboardResult: pubdash,
			PublicDashboardErr:    nil,
		},
		{
			Name:                  "returns 404 when dashboard not found",
			DashboardUid:          "77777",
			ExpectedHttpResponse:  http.StatusNotFound,
			PublicDashboardResult: nil,
			PublicDashboardErr:    dashboards.ErrDashboardNotFound,
		},
		{
			Name:                  "returns 500 when internal server error",
			DashboardUid:          "1",
			ExpectedHttpResponse:  http.StatusInternalServerError,
			PublicDashboardResult: nil,
			PublicDashboardErr:    errors.New("database broken"),
		},
	}

	for _, test := range testCases {
		t.Run(test.Name, func(t *testing.T) {
			service := publicdashboards.NewFakePublicDashboardService(t)
			service.On("GetPublicDashboardConfig", mock.Anything, mock.AnythingOfType("int64"), mock.AnythingOfType("string")).
				Return(test.PublicDashboardResult, test.PublicDashboardErr)

			testServer := setupTestServer(
				t,
				setting.NewCfg(),
				buildQueryDataService(t, nil, nil, nil),
				featuremgmt.WithFeatures(featuremgmt.FlagPublicDashboards),
				service,
				nil,
			)

			response := callAPI(
				testServer,
				http.MethodGet,
				"/api/dashboards/uid/1/public-config",
				nil,
				t,
			)

			assert.Equal(t, test.ExpectedHttpResponse, response.Code)

			if response.Code == http.StatusOK {
				var pdcResp PublicDashboard
				err := json.Unmarshal(response.Body.Bytes(), &pdcResp)
				require.NoError(t, err)
				assert.Equal(t, test.PublicDashboardResult, &pdcResp)
			}
		})
	}
}

func TestApiSavePublicDashboardConfig(t *testing.T) {
	testCases := []struct {
		Name                  string
		DashboardUid          string
		publicDashboardConfig *PublicDashboard
		ExpectedHttpResponse  int
		SaveDashboardErr      error
	}{
		{
			Name:                  "returns 200 when update persists",
			DashboardUid:          "1",
			publicDashboardConfig: &PublicDashboard{IsEnabled: true},
			ExpectedHttpResponse:  http.StatusOK,
			SaveDashboardErr:      nil,
		},
		{
			Name:                  "returns 500 when not persisted",
			ExpectedHttpResponse:  http.StatusInternalServerError,
			publicDashboardConfig: &PublicDashboard{},
			SaveDashboardErr:      errors.New("backend failed to save"),
		},
		{
			Name:                  "returns 404 when dashboard not found",
			ExpectedHttpResponse:  http.StatusNotFound,
			publicDashboardConfig: &PublicDashboard{},
			SaveDashboardErr:      dashboards.ErrDashboardNotFound,
		},
	}

	for _, test := range testCases {
		t.Run(test.Name, func(t *testing.T) {
			service := publicdashboards.NewFakePublicDashboardService(t)
			service.On("SavePublicDashboardConfig", mock.Anything, mock.AnythingOfType("*models.SavePublicDashboardConfigDTO")).
				Return(&PublicDashboard{IsEnabled: true}, test.SaveDashboardErr)

			testServer := setupTestServer(
				t,
				setting.NewCfg(),
				buildQueryDataService(t, nil, nil, nil),
				featuremgmt.WithFeatures(featuremgmt.FlagPublicDashboards),
				service,
				nil,
			)

			response := callAPI(
				testServer,
				http.MethodPost,
				"/api/dashboards/uid/1/public-config",
				strings.NewReader(`{ "isPublic": true }`),
				t,
			)

			assert.Equal(t, test.ExpectedHttpResponse, response.Code)

			//check the result if it's a 200
			if response.Code == http.StatusOK {
				val, err := json.Marshal(test.publicDashboardConfig)
				require.NoError(t, err)
				assert.Equal(t, string(val), response.Body.String())
			}
		})
	}
}

// `/public/dashboards/:uid/query`` endpoint test
func TestAPIQueryPublicDashboard(t *testing.T) {
	cacheService := &fakeDatasources.FakeCacheService{
		DataSources: []*datasources.DataSource{
			{Uid: "mysqlds"},
			{Uid: "promds"},
			{Uid: "promds2"},
		},
	}

	// used to determine whether fakePluginClient returns an error
	queryReturnsError := false

	fakePluginClient := &fakePluginClient{
		QueryDataHandlerFunc: func(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
			if queryReturnsError {
				return nil, errors.New("error")
			}

			resp := backend.Responses{}

			for _, query := range req.Queries {
				resp[query.RefID] = backend.DataResponse{
					Frames: []*data.Frame{
						{
							RefID: query.RefID,
							Name:  "query-" + query.RefID,
						},
					},
				}
			}
			return &backend.QueryDataResponse{Responses: resp}, nil
		},
	}

	qds := buildQueryDataService(t, cacheService, fakePluginClient, nil)

	setup := func(enabled bool) (*web.Mux, *publicdashboards.FakePublicDashboardService) {
		service := publicdashboards.NewFakePublicDashboardService(t)

		testServer := setupTestServer(
			t,
			setting.NewCfg(),
			qds,
			featuremgmt.WithFeatures(featuremgmt.FlagPublicDashboards, enabled),
			service,
			nil,
		)

		return testServer, service
	}

	t.Run("Status code is 404 when feature toggle is disabled", func(t *testing.T) {
		server, _ := setup(false)
		resp := callAPI(server, http.MethodPost, "/api/public/dashboards/abc123/panels/2/query", strings.NewReader("{}"), t)
		require.Equal(t, http.StatusNotFound, resp.Code)
	})

	t.Run("Status code is 400 when the panel ID is invalid", func(t *testing.T) {
		server, _ := setup(true)
		resp := callAPI(server, http.MethodPost, "/api/public/dashboards/abc123/panels/notanumber/query", strings.NewReader("{}"), t)
		require.Equal(t, http.StatusBadRequest, resp.Code)
	})

	t.Run("Returns query data when feature toggle is enabled", func(t *testing.T) {
		server, fakeDashboardService := setup(true)

		fakeDashboardService.On("GetPublicDashboard", mock.Anything, mock.Anything).Return(&models.Dashboard{}, nil)
		fakeDashboardService.On("GetPublicDashboardConfig", mock.Anything, mock.Anything, mock.Anything).Return(&PublicDashboard{}, nil)
		fakeDashboardService.On("BuildAnonymousUser", mock.Anything, mock.Anything, mock.Anything).Return(&models.SignedInUser{}, nil)
		fakeDashboardService.On("BuildPublicDashboardMetricRequest", mock.Anything, mock.Anything, mock.Anything, int64(2)).Return(dtos.MetricRequest{
			Queries: []*simplejson.Json{
				simplejson.MustJson([]byte(`
        {
          "datasource": {
          "type": "prometheus",
          "uid": "promds"
          },
          "exemplar": true,
          "expr": "query_2_A",
          "interval": "",
          "legendFormat": "",
          "refId": "A"
        }
      `)),
			},
		}, nil)

		resp := callAPI(server, http.MethodPost, "/api/public/dashboards/abc123/panels/2/query", strings.NewReader("{}"), t)

		require.JSONEq(
			t,
			`{
      "results": {
        "A": {
          "frames": [
            {
              "data": {
                "values": []
              },
              "schema": {
                "fields": [],
                "refId": "A",
                "name": "query-A"
              }
            }
          ]
        }
      }
    }`,
			resp.Body.String(),
		)
		require.Equal(t, http.StatusOK, resp.Code)
	})

	t.Run("Status code is 500 when the query fails", func(t *testing.T) {
		server, fakeDashboardService := setup(true)

		fakeDashboardService.On("GetPublicDashboard", mock.Anything, mock.Anything).Return(&models.Dashboard{}, nil)
		fakeDashboardService.On("GetPublicDashboardConfig", mock.Anything, mock.Anything, mock.Anything).Return(&PublicDashboard{}, nil)
		fakeDashboardService.On("BuildAnonymousUser", mock.Anything, mock.Anything, mock.Anything).Return(&models.SignedInUser{}, nil)
		fakeDashboardService.On("BuildPublicDashboardMetricRequest", mock.Anything, mock.Anything, mock.Anything, int64(2)).Return(dtos.MetricRequest{
			Queries: []*simplejson.Json{
				simplejson.MustJson([]byte(`
	        {
	          "datasource": {
	          "type": "prometheus",
	          "uid": "promds"
	          },
	          "exemplar": true,
	          "expr": "query_2_A",
	          "interval": "",
	          "legendFormat": "",
	          "refId": "A"
	        }
	      `)),
			},
		}, nil)

		queryReturnsError = true
		resp := callAPI(server, http.MethodPost, "/api/public/dashboards/abc123/panels/2/query", strings.NewReader("{}"), t)
		require.Equal(t, http.StatusInternalServerError, resp.Code)
		queryReturnsError = false
	})

	t.Run("Status code is 200 when a panel has queries from multiple datasources", func(t *testing.T) {
		server, fakeDashboardService := setup(true)

		fakeDashboardService.On("GetPublicDashboard", mock.Anything, mock.Anything).Return(&models.Dashboard{}, nil)
		fakeDashboardService.On("GetPublicDashboardConfig", mock.Anything, mock.Anything, mock.Anything).Return(&PublicDashboard{}, nil)
		fakeDashboardService.On("BuildAnonymousUser", mock.Anything, mock.Anything, mock.Anything).Return(&models.SignedInUser{}, nil)
		fakeDashboardService.On("BuildPublicDashboardMetricRequest", mock.Anything, mock.Anything, mock.Anything, int64(2)).Return(dtos.MetricRequest{
			Queries: []*simplejson.Json{
				simplejson.MustJson([]byte(`
{
						"datasource": {
						"type": "prometheus",
						"uid": "promds"
						},
						"exemplar": true,
						"expr": "query_2_A",
						"interval": "",
						"legendFormat": "",
						"refId": "A"
					}
				`)),
				simplejson.MustJson([]byte(`
{
						"datasource": {
						"type": "prometheus",
						"uid": "promds2"
						},
						"exemplar": true,
						"expr": "query_2_B",
						"interval": "",
						"legendFormat": "",
						"refId": "B"
					}
				`)),
			},
		}, nil)

		resp := callAPI(server, http.MethodPost, "/api/public/dashboards/abc123/panels/2/query", strings.NewReader("{}"), t)
		require.JSONEq(
			t,
			`{
				"results": {
					"A": {
						"frames": [
							{
								"data": {
									"values": []
								},
								"schema": {
									"fields": [],
									"refId": "A",
									"name": "query-A"
								}
							}
						]
					},
					"B": {
						"frames": [
							{
								"data": {
									"values": []
								},
								"schema": {
									"fields": [],
									"refId": "B",
									"name": "query-B"
								}
							}
						]
					}
				}
			}`,
			resp.Body.String(),
		)
		require.Equal(t, http.StatusOK, resp.Code)
	})
}

func TestIntegrationUnauthenticatedUserCanGetPubdashPanelQueryData(t *testing.T) {
	db := sqlstore.InitTestDB(t)

	cacheService := service.ProvideCacheService(localcache.ProvideService(), db)
	qds := buildQueryDataService(t, cacheService, nil, db)

	_ = db.AddDataSource(context.Background(), &datasources.AddDataSourceCommand{
		Uid:      "ds1",
		OrgId:    1,
		Name:     "laban",
		Type:     datasources.DS_MYSQL,
		Access:   datasources.DS_ACCESS_DIRECT,
		Url:      "http://test",
		Database: "site",
		ReadOnly: true,
	})

	// Create Dashboard
	saveDashboardCmd := models.SaveDashboardCommand{
		OrgId:    1,
		FolderId: 1,
		IsFolder: false,
		Dashboard: simplejson.NewFromAny(map[string]interface{}{
			"id":    nil,
			"title": "test",
			"panels": []map[string]interface{}{
				{
					"id": 1,
					"targets": []map[string]interface{}{
						{
							"datasource": map[string]string{
								"type": "mysql",
								"uid":  "ds1",
							},
							"refId": "A",
						},
					},
				},
			},
		}),
	}

	// create dashboard
	dashboardStore := dashboardStore.ProvideDashboardStore(db, featuremgmt.WithFeatures())
	dashboard, err := dashboardStore.SaveDashboard(saveDashboardCmd)
	require.NoError(t, err)

	// Create public dashboard
	savePubDashboardCmd := &SavePublicDashboardConfigDTO{
		DashboardUid: dashboard.Uid,
		OrgId:        dashboard.OrgId,
		PublicDashboard: &PublicDashboard{
			IsEnabled: true,
		},
	}

	// create public dashboard
	store := publicdashboardsStore.ProvideStore(db)
	service := publicdashboardsService.ProvideService(setting.NewCfg(), store)
	pubdash, err := service.SavePublicDashboardConfig(context.Background(), savePubDashboardCmd)
	require.NoError(t, err)

	// setup test server
	server := setupTestServer(t,
		setting.NewCfg(),
		qds,
		featuremgmt.WithFeatures(featuremgmt.FlagPublicDashboards),
		service,
		db,
	)

	resp := callAPI(server, http.MethodPost,
		fmt.Sprintf("/api/public/dashboards/%s/panels/1/query", pubdash.AccessToken),
		strings.NewReader(`{}`),
		t,
	)
	require.Equal(t, http.StatusOK, resp.Code)
	require.NoError(t, err)
	require.JSONEq(
		t,
		`{
        "results": {
          "A": {
            "frames": [
              {
                "data": {
                  "values": []
                },
                "schema": {
                  "fields": []
                }
              }
            ]
          }
        }
      }`,
		resp.Body.String(),
	)
}
