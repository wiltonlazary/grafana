package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/grafana/grafana/pkg/infra/log"
	models2 "github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/accesscontrol"
	acMock "github.com/grafana/grafana/pkg/services/accesscontrol/mock"
	"github.com/grafana/grafana/pkg/services/datasources"
	apimodels "github.com/grafana/grafana/pkg/services/ngalert/api/tooling/definitions"
	"github.com/grafana/grafana/pkg/services/ngalert/models"
	"github.com/grafana/grafana/pkg/services/ngalert/provisioning"
	"github.com/grafana/grafana/pkg/services/ngalert/schedule"
	"github.com/grafana/grafana/pkg/services/ngalert/store"
	"github.com/grafana/grafana/pkg/util"
	"github.com/grafana/grafana/pkg/web"
)

func TestRouteDeleteAlertRules(t *testing.T) {
	getRecordedCommand := func(ruleStore *store.FakeRuleStore) []store.GenericRecordedQuery {
		results := ruleStore.GetRecordedCommands(func(cmd interface{}) (interface{}, bool) {
			c, ok := cmd.(store.GenericRecordedQuery)
			if !ok || c.Name != "DeleteAlertRulesByUID" {
				return nil, false
			}
			return c, ok
		})
		var result []store.GenericRecordedQuery
		for _, cmd := range results {
			result = append(result, cmd.(store.GenericRecordedQuery))
		}
		return result
	}

	assertRulesDeleted := func(t *testing.T, expectedRules []*models.AlertRule, ruleStore *store.FakeRuleStore, scheduler *schedule.FakeScheduleService) {
		deleteCommands := getRecordedCommand(ruleStore)
		require.Len(t, deleteCommands, 1)
		cmd := deleteCommands[0]
		actualUIDs := cmd.Params[1].([]string)
		require.Len(t, actualUIDs, len(expectedRules))
		for _, rule := range expectedRules {
			require.Containsf(t, actualUIDs, rule.UID, "Rule %s was expected to be deleted but it wasn't", rule.UID)
		}

		require.Len(t, scheduler.Calls, len(expectedRules))
		for _, call := range scheduler.Calls {
			require.Equal(t, "DeleteAlertRule", call.Method)
			key, ok := call.Arguments.Get(0).(models.AlertRuleKey)
			require.Truef(t, ok, "Expected AlertRuleKey but got something else")
			found := false
			for _, rule := range expectedRules {
				if rule.GetKey() == key {
					found = true
					break
				}
			}
			require.Truef(t, found, "Key %v was not expected to be submitted to scheduler", key)
		}
	}

	t.Run("when fine-grained access is disabled", func(t *testing.T) {
		t.Run("viewer should not be authorized", func(t *testing.T) {
			ruleStore := store.NewFakeRuleStore(t)
			orgID := rand.Int63()
			folder := randFolder()
			ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
			ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))...)
			ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID)))...)

			scheduler := &schedule.FakeScheduleService{}
			scheduler.On("DeleteAlertRule", mock.Anything).Panic("should not be called")

			ac := acMock.New().WithDisabled()
			request := createRequestContext(orgID, models2.ROLE_VIEWER, nil)
			response := createService(ac, ruleStore, scheduler).RouteDeleteAlertRules(request, folder.Title, "")
			require.Equalf(t, 401, response.Status(), "Expected 403 but got %d: %v", response.Status(), string(response.Body()))

			scheduler.AssertNotCalled(t, "DeleteAlertRule")
			require.Empty(t, getRecordedCommand(ruleStore))
		})
		t.Run("editor should be able to delete all rules in folder", func(t *testing.T) {
			ruleStore := store.NewFakeRuleStore(t)
			orgID := rand.Int63()
			folder := randFolder()
			ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
			rulesInFolder := models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))
			ruleStore.PutRule(context.Background(), rulesInFolder...)
			ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID)))...)

			scheduler := &schedule.FakeScheduleService{}
			scheduler.On("DeleteAlertRule", mock.Anything)

			ac := acMock.New().WithDisabled()
			request := createRequestContext(orgID, models2.ROLE_EDITOR, nil)
			response := createService(ac, ruleStore, scheduler).RouteDeleteAlertRules(request, folder.Title, "")
			require.Equalf(t, 202, response.Status(), "Expected 202 but got %d: %v", response.Status(), string(response.Body()))
			assertRulesDeleted(t, rulesInFolder, ruleStore, scheduler)
		})
		t.Run("editor should be able to delete rules in a group in a folder", func(t *testing.T) {
			ruleStore := store.NewFakeRuleStore(t)
			orgID := rand.Int63()
			groupName := util.GenerateShortUID()
			folder := randFolder()
			ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
			rulesInFolderInGroup := models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder), withGroup(groupName)))
			ruleStore.PutRule(context.Background(), rulesInFolderInGroup...)
			// rules in different groups but in the same namespace
			ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))...)
			// rules in the same group but different folder
			ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withGroup(groupName)))...)

			scheduler := &schedule.FakeScheduleService{}
			scheduler.On("DeleteAlertRule", mock.Anything)

			ac := acMock.New().WithDisabled()
			request := createRequestContext(orgID, models2.ROLE_EDITOR, nil)
			response := createService(ac, ruleStore, scheduler).RouteDeleteAlertRules(request, folder.Title, groupName)
			require.Equalf(t, 202, response.Status(), "Expected 202 but got %d: %v", response.Status(), string(response.Body()))
			assertRulesDeleted(t, rulesInFolderInGroup, ruleStore, scheduler)
		})
		t.Run("editor shouldn't be able to delete provisioned rules", func(t *testing.T) {
			ruleStore := store.NewFakeRuleStore(t)
			orgID := rand.Int63()
			folder := randFolder()
			ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
			rulesInFolder := models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))
			ruleStore.PutRule(context.Background(), rulesInFolder...)
			ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID)))...)

			scheduler := &schedule.FakeScheduleService{}
			scheduler.On("DeleteAlertRule", mock.Anything)

			ac := acMock.New().WithDisabled()

			svc := createService(ac, ruleStore, scheduler)

			err := svc.provenanceStore.SetProvenance(context.Background(), rulesInFolder[0], orgID, models.ProvenanceAPI)
			require.NoError(t, err)

			request := createRequestContext(orgID, models2.ROLE_EDITOR, nil)
			response := svc.RouteDeleteAlertRules(request, folder.Title, "")
			require.Equalf(t, 202, response.Status(), "Expected 202 but got %d: %v", response.Status(), string(response.Body()))
			assertRulesDeleted(t, rulesInFolder[1:], ruleStore, scheduler)
		})
	})
	t.Run("when fine-grained access is enabled", func(t *testing.T) {
		t.Run("and user does not have access to any of data sources used by alert rules", func(t *testing.T) {
			ruleStore := store.NewFakeRuleStore(t)
			orgID := rand.Int63()
			folder := randFolder()
			ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
			ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))...)
			ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID)))...)

			scheduler := &schedule.FakeScheduleService{}
			scheduler.On("DeleteAlertRule", mock.Anything).Panic("should not be called")

			ac := acMock.New()
			request := createRequestContext(orgID, "None", nil)
			response := createService(ac, ruleStore, scheduler).RouteDeleteAlertRules(request, folder.Title, "")
			require.Equalf(t, 401, response.Status(), "Expected 403 but got %d: %v", response.Status(), string(response.Body()))

			scheduler.AssertNotCalled(t, "DeleteAlertRule")
			require.Empty(t, getRecordedCommand(ruleStore))
		})
		t.Run("and user has access to all alert rules", func(t *testing.T) {
			t.Run("should delete all rules", func(t *testing.T) {
				ruleStore := store.NewFakeRuleStore(t)
				orgID := rand.Int63()
				folder := randFolder()
				ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
				rulesInFolder := models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))
				ruleStore.PutRule(context.Background(), rulesInFolder...)
				ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID)))...)

				scheduler := &schedule.FakeScheduleService{}
				scheduler.On("DeleteAlertRule", mock.Anything)

				ac := acMock.New().WithPermissions(createPermissionsForRules(rulesInFolder))
				request := createRequestContext(orgID, "None", nil)

				response := createService(ac, ruleStore, scheduler).RouteDeleteAlertRules(request, folder.Title, "")
				require.Equalf(t, 202, response.Status(), "Expected 202 but got %d: %v", response.Status(), string(response.Body()))
				assertRulesDeleted(t, rulesInFolder, ruleStore, scheduler)
			})
			t.Run("shouldn't be able to delete provisioned rules", func(t *testing.T) {
				ruleStore := store.NewFakeRuleStore(t)
				orgID := rand.Int63()
				folder := randFolder()
				ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
				rulesInFolder := models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))
				ruleStore.PutRule(context.Background(), rulesInFolder...)
				ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID)))...)

				scheduler := &schedule.FakeScheduleService{}
				scheduler.On("DeleteAlertRule", mock.Anything)

				ac := acMock.New().WithPermissions(createPermissionsForRules(rulesInFolder))
				svc := createService(ac, ruleStore, scheduler)

				err := svc.provenanceStore.SetProvenance(context.Background(), rulesInFolder[0], orgID, models.ProvenanceAPI)
				require.NoError(t, err)

				request := createRequestContext(orgID, "None", nil)

				response := svc.RouteDeleteAlertRules(request, folder.Title, "")
				require.Equalf(t, 202, response.Status(), "Expected 202 but got %d: %v", response.Status(), string(response.Body()))
				assertRulesDeleted(t, rulesInFolder[1:], ruleStore, scheduler)
			})
		})
		t.Run("and user has access to data sources of some of alert rules", func(t *testing.T) {
			t.Run("should delete only those that are accessible in folder", func(t *testing.T) {
				ruleStore := store.NewFakeRuleStore(t)
				orgID := rand.Int63()
				folder := randFolder()
				ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
				authorizedRulesInFolder := models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))
				ruleStore.PutRule(context.Background(), authorizedRulesInFolder...)
				// more rules in the same namespace but user does not have access to them
				ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))...)
				ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID)))...)

				scheduler := &schedule.FakeScheduleService{}
				scheduler.On("DeleteAlertRule", mock.Anything)

				ac := acMock.New().WithPermissions(createPermissionsForRules(authorizedRulesInFolder))
				request := createRequestContext(orgID, "None", nil)

				response := createService(ac, ruleStore, scheduler).RouteDeleteAlertRules(request, folder.Title, "")
				require.Equalf(t, 202, response.Status(), "Expected 202 but got %d: %v", response.Status(), string(response.Body()))
				assertRulesDeleted(t, authorizedRulesInFolder, ruleStore, scheduler)
			})
			t.Run("should delete only rules in a group that are authorized", func(t *testing.T) {
				ruleStore := store.NewFakeRuleStore(t)
				orgID := rand.Int63()
				groupName := util.GenerateShortUID()
				folder := randFolder()
				ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
				authorizedRulesInGroup := models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder), withGroup(groupName)))
				ruleStore.PutRule(context.Background(), authorizedRulesInGroup...)
				// more rules in the same group but user is not authorized to access them
				ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder), withGroup(groupName)))...)
				// rules in different groups but in the same namespace
				ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))...)
				// rules in the same group but different folder
				ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withGroup(groupName)))...)

				scheduler := &schedule.FakeScheduleService{}
				scheduler.On("DeleteAlertRule", mock.Anything)

				ac := acMock.New().WithPermissions(createPermissionsForRules(authorizedRulesInGroup))
				request := createRequestContext(orgID, "None", nil)
				response := createService(ac, ruleStore, scheduler).RouteDeleteAlertRules(request, folder.Title, groupName)
				require.Equalf(t, 202, response.Status(), "Expected 202 but got %d: %v", response.Status(), string(response.Body()))
				assertRulesDeleted(t, authorizedRulesInGroup, ruleStore, scheduler)
			})
		})
	})
}

func TestRouteGetNamespaceRulesConfig(t *testing.T) {
	t.Run("fine-grained access is enabled", func(t *testing.T) {
		t.Run("should return rules for which user has access to data source", func(t *testing.T) {
			orgID := rand.Int63()
			folder := randFolder()
			ruleStore := store.NewFakeRuleStore(t)
			ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
			expectedRules := models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))
			ruleStore.PutRule(context.Background(), expectedRules...)
			ruleStore.PutRule(context.Background(), models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))...)
			ac := acMock.New().WithPermissions(createPermissionsForRules(expectedRules))

			req := createRequestContext(orgID, "", nil)
			response := createService(ac, ruleStore, nil).RouteGetNamespaceRulesConfig(req, folder.Title)

			require.Equal(t, http.StatusAccepted, response.Status())
			result := &apimodels.NamespaceConfigResponse{}
			require.NoError(t, json.Unmarshal(response.Body(), result))
			require.NotNil(t, result)
			for namespace, groups := range *result {
				require.Equal(t, folder.Title, namespace)
				for _, group := range groups {
				grouploop:
					for _, actualRule := range group.Rules {
						for i, expected := range expectedRules {
							if actualRule.GrafanaManagedAlert.UID == expected.UID {
								expectedRules = append(expectedRules[:i], expectedRules[i+1:]...)
								continue grouploop
							}
						}
						assert.Failf(t, "rule in a group was not found in expected", "rule %s group %s", actualRule.GrafanaManagedAlert.Title, group.Name)
					}
				}
			}
			assert.Emptyf(t, expectedRules, "not all expected rules were returned")
		})
	})
	t.Run("fine-grained access is disabled", func(t *testing.T) {
		t.Run("should return all rules from folder", func(t *testing.T) {
			orgID := rand.Int63()
			folder := randFolder()
			ruleStore := store.NewFakeRuleStore(t)
			ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
			expectedRules := models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))
			ruleStore.PutRule(context.Background(), expectedRules...)
			ac := acMock.New().WithDisabled()

			req := createRequestContext(orgID, models2.ROLE_VIEWER, nil)
			response := createService(ac, ruleStore, nil).RouteGetNamespaceRulesConfig(req, folder.Title)

			require.Equal(t, http.StatusAccepted, response.Status())
			result := &apimodels.NamespaceConfigResponse{}
			require.NoError(t, json.Unmarshal(response.Body(), result))
			require.NotNil(t, result)
			for namespace, groups := range *result {
				require.Equal(t, folder.Title, namespace)
				for _, group := range groups {
				grouploop:
					for _, actualRule := range group.Rules {
						for i, expected := range expectedRules {
							if actualRule.GrafanaManagedAlert.UID == expected.UID {
								expectedRules = append(expectedRules[:i], expectedRules[i+1:]...)
								continue grouploop
							}
						}
						assert.Failf(t, "rule in a group was not found in expected", "rule %s group %s", actualRule.GrafanaManagedAlert.Title, group.Name)
					}
				}
			}
			assert.Emptyf(t, expectedRules, "not all expected rules were returned")
		})
	})
	t.Run("should return the provenance of the alert rules", func(t *testing.T) {
		orgID := rand.Int63()
		folder := randFolder()
		ruleStore := store.NewFakeRuleStore(t)
		ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
		expectedRules := models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withOrgID(orgID), withNamespace(folder)))
		ruleStore.PutRule(context.Background(), expectedRules...)
		ac := acMock.New().WithDisabled()

		svc := createService(ac, ruleStore, nil)

		// add provenance to the first generated rule
		rule := &models.AlertRule{
			UID: expectedRules[0].UID,
		}
		err := svc.provenanceStore.SetProvenance(context.Background(), rule, orgID, models.ProvenanceAPI)
		require.NoError(t, err)

		req := createRequestContext(orgID, models2.ROLE_VIEWER, nil)
		response := svc.RouteGetNamespaceRulesConfig(req, folder.Title)

		require.Equal(t, http.StatusAccepted, response.Status())
		result := &apimodels.NamespaceConfigResponse{}
		require.NoError(t, json.Unmarshal(response.Body(), result))
		require.NotNil(t, result)
		found := false
		for namespace, groups := range *result {
			require.Equal(t, folder.Title, namespace)
			for _, group := range groups {
				for _, actualRule := range group.Rules {
					if actualRule.GrafanaManagedAlert.UID == expectedRules[0].UID {
						require.Equal(t, models.ProvenanceAPI, actualRule.GrafanaManagedAlert.Provenance)
						found = true
					} else {
						require.Equal(t, models.ProvenanceNone, actualRule.GrafanaManagedAlert.Provenance)
					}
				}
			}
		}
		require.True(t, found)
	})
	t.Run("should enforce order of rules in the group", func(t *testing.T) {
		orgID := rand.Int63()
		folder := randFolder()
		ruleStore := store.NewFakeRuleStore(t)
		ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
		groupKey := models.GenerateGroupKey(orgID)
		groupKey.NamespaceUID = folder.Uid

		expectedRules := models.GenerateAlertRules(rand.Intn(5)+5, models.AlertRuleGen(withGroupKey(groupKey), models.WithUniqueGroupIndex()))
		ruleStore.PutRule(context.Background(), expectedRules...)
		ac := acMock.New().WithDisabled()

		response := createService(ac, ruleStore, nil).RouteGetNamespaceRulesConfig(createRequestContext(orgID, models2.ROLE_VIEWER, nil), folder.Title)

		require.Equal(t, http.StatusAccepted, response.Status())
		result := &apimodels.NamespaceConfigResponse{}
		require.NoError(t, json.Unmarshal(response.Body(), result))
		require.NotNil(t, result)

		models.RulesGroup(expectedRules).SortByGroupIndex()

		require.Contains(t, *result, folder.Title)
		groups := (*result)[folder.Title]
		require.Len(t, groups, 1)
		group := groups[0]
		require.Equal(t, groupKey.RuleGroup, group.Name)
		for i, actual := range groups[0].Rules {
			expected := expectedRules[i]
			if actual.GrafanaManagedAlert.UID != expected.UID {
				var actualUIDs []string
				var expectedUIDs []string
				for _, rule := range group.Rules {
					actualUIDs = append(actualUIDs, rule.GrafanaManagedAlert.UID)
				}
				for _, rule := range expectedRules {
					expectedUIDs = append(expectedUIDs, rule.UID)
				}
				require.Fail(t, fmt.Sprintf("rules are not sorted by group index. Expected: %v. Actual: %v", expectedUIDs, actualUIDs))
			}
		}
	})
}

func TestRouteGetRulesConfig(t *testing.T) {
	t.Run("fine-grained access is enabled", func(t *testing.T) {
		t.Run("should check access to data source", func(t *testing.T) {
			orgID := rand.Int63()
			ruleStore := store.NewFakeRuleStore(t)
			folder1 := randFolder()
			folder2 := randFolder()
			ruleStore.Folders[orgID] = []*models2.Folder{folder1, folder2}

			group1Key := models.GenerateGroupKey(orgID)
			group1Key.NamespaceUID = folder1.Uid
			group2Key := models.GenerateGroupKey(orgID)
			group2Key.NamespaceUID = folder2.Uid

			group1 := models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withGroupKey(group1Key)))
			group2 := models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withGroupKey(group2Key)))
			ruleStore.PutRule(context.Background(), append(group1, group2...)...)

			request := createRequestContext(orgID, "", nil)
			t.Run("and do not return group if user does not have access to one of rules", func(t *testing.T) {
				ac := acMock.New().WithPermissions(createPermissionsForRules(append(group1, group2[1:]...)))
				response := createService(ac, ruleStore, nil).RouteGetRulesConfig(request)
				require.Equal(t, http.StatusOK, response.Status())

				result := &apimodels.NamespaceConfigResponse{}
				require.NoError(t, json.Unmarshal(response.Body(), result))
				require.NotNil(t, result)

				require.Contains(t, *result, folder1.Title)
				require.NotContains(t, *result, folder2.Title)

				groups := (*result)[folder1.Title]
				require.Len(t, groups, 1)
				require.Equal(t, group1Key.RuleGroup, groups[0].Name)
				require.Len(t, groups[0].Rules, len(group1))
			})
		})
	})

	t.Run("should return rules in group sorted by group index", func(t *testing.T) {
		orgID := rand.Int63()
		folder := randFolder()
		ruleStore := store.NewFakeRuleStore(t)
		ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
		groupKey := models.GenerateGroupKey(orgID)
		groupKey.NamespaceUID = folder.Uid

		expectedRules := models.GenerateAlertRules(rand.Intn(5)+5, models.AlertRuleGen(withGroupKey(groupKey), models.WithUniqueGroupIndex()))
		ruleStore.PutRule(context.Background(), expectedRules...)
		ac := acMock.New().WithDisabled()

		response := createService(ac, ruleStore, nil).RouteGetRulesConfig(createRequestContext(orgID, models2.ROLE_VIEWER, nil))

		require.Equal(t, http.StatusOK, response.Status())
		result := &apimodels.NamespaceConfigResponse{}
		require.NoError(t, json.Unmarshal(response.Body(), result))
		require.NotNil(t, result)

		models.RulesGroup(expectedRules).SortByGroupIndex()

		require.Contains(t, *result, folder.Title)
		groups := (*result)[folder.Title]
		require.Len(t, groups, 1)
		group := groups[0]
		require.Equal(t, groupKey.RuleGroup, group.Name)
		for i, actual := range groups[0].Rules {
			expected := expectedRules[i]
			if actual.GrafanaManagedAlert.UID != expected.UID {
				var actualUIDs []string
				var expectedUIDs []string
				for _, rule := range group.Rules {
					actualUIDs = append(actualUIDs, rule.GrafanaManagedAlert.UID)
				}
				for _, rule := range expectedRules {
					expectedUIDs = append(expectedUIDs, rule.UID)
				}
				require.Fail(t, fmt.Sprintf("rules are not sorted by group index. Expected: %v. Actual: %v", expectedUIDs, actualUIDs))
			}
		}
	})
}

func TestRouteGetRulesGroupConfig(t *testing.T) {
	t.Run("fine-grained access is enabled", func(t *testing.T) {
		t.Run("should check access to data source", func(t *testing.T) {
			orgID := rand.Int63()
			folder := randFolder()
			ruleStore := store.NewFakeRuleStore(t)
			ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
			groupKey := models.GenerateGroupKey(orgID)
			groupKey.NamespaceUID = folder.Uid

			expectedRules := models.GenerateAlertRules(rand.Intn(4)+2, models.AlertRuleGen(withGroupKey(groupKey)))
			ruleStore.PutRule(context.Background(), expectedRules...)

			request := createRequestContext(orgID, "", map[string]string{
				":Namespace": folder.Title,
				":Groupname": groupKey.RuleGroup,
			})

			t.Run("and return 401 if user does not have access one of rules", func(t *testing.T) {
				ac := acMock.New().WithPermissions(createPermissionsForRules(expectedRules[1:]))
				response := createService(ac, ruleStore, nil).RouteGetRulesGroupConfig(request, folder.Title, groupKey.RuleGroup)
				require.Equal(t, http.StatusUnauthorized, response.Status())
			})

			t.Run("and return rules if user has access to all of them", func(t *testing.T) {
				ac := acMock.New().WithPermissions(createPermissionsForRules(expectedRules))
				response := createService(ac, ruleStore, nil).RouteGetRulesGroupConfig(request, folder.Title, groupKey.RuleGroup)

				require.Equal(t, http.StatusAccepted, response.Status())
				result := &apimodels.RuleGroupConfigResponse{}
				require.NoError(t, json.Unmarshal(response.Body(), result))
				require.NotNil(t, result)
				require.Len(t, result.Rules, len(expectedRules))
			})
		})
	})

	t.Run("should return rules in group sorted by group index", func(t *testing.T) {
		orgID := rand.Int63()
		folder := randFolder()
		ruleStore := store.NewFakeRuleStore(t)
		ruleStore.Folders[orgID] = append(ruleStore.Folders[orgID], folder)
		groupKey := models.GenerateGroupKey(orgID)
		groupKey.NamespaceUID = folder.Uid

		expectedRules := models.GenerateAlertRules(rand.Intn(5)+5, models.AlertRuleGen(withGroupKey(groupKey), models.WithUniqueGroupIndex()))
		ruleStore.PutRule(context.Background(), expectedRules...)
		ac := acMock.New().WithDisabled()

		response := createService(ac, ruleStore, nil).RouteGetRulesGroupConfig(createRequestContext(orgID, models2.ROLE_VIEWER, nil), folder.Title, groupKey.RuleGroup)

		require.Equal(t, http.StatusAccepted, response.Status())
		result := &apimodels.RuleGroupConfigResponse{}
		require.NoError(t, json.Unmarshal(response.Body(), result))
		require.NotNil(t, result)

		models.RulesGroup(expectedRules).SortByGroupIndex()

		for i, actual := range result.Rules {
			expected := expectedRules[i]
			if actual.GrafanaManagedAlert.UID != expected.UID {
				var actualUIDs []string
				var expectedUIDs []string
				for _, rule := range result.Rules {
					actualUIDs = append(actualUIDs, rule.GrafanaManagedAlert.UID)
				}
				for _, rule := range expectedRules {
					expectedUIDs = append(expectedUIDs, rule.UID)
				}
				require.Fail(t, fmt.Sprintf("rules are not sorted by group index. Expected: %v. Actual: %v", expectedUIDs, actualUIDs))
			}
		}
	})
}

func TestVerifyProvisionedRulesNotAffected(t *testing.T) {
	orgID := rand.Int63()
	group := models.GenerateGroupKey(orgID)
	affectedGroups := make(map[models.AlertRuleGroupKey]models.RulesGroup)
	var allRules []*models.AlertRule
	{
		rules := models.GenerateAlertRules(rand.Intn(3)+1, models.AlertRuleGen(withGroupKey(group)))
		allRules = append(allRules, rules...)
		affectedGroups[group] = rules
		for i := 0; i < rand.Intn(3)+1; i++ {
			g := models.GenerateGroupKey(orgID)
			rules := models.GenerateAlertRules(rand.Intn(3)+1, models.AlertRuleGen(withGroupKey(g)))
			allRules = append(allRules, rules...)
			affectedGroups[g] = rules
		}
	}
	ch := &store.GroupDelta{
		GroupKey:       group,
		AffectedGroups: affectedGroups,
	}

	t.Run("should return error if at least one rule in affected groups is provisioned", func(t *testing.T) {
		rand.Shuffle(len(allRules), func(i, j int) {
			allRules[j], allRules[i] = allRules[i], allRules[j]
		})
		storeResult := make(map[string]models.Provenance, len(allRules))
		storeResult[allRules[0].UID] = models.ProvenanceAPI
		storeResult[allRules[1].UID] = models.ProvenanceFile

		provenanceStore := &provisioning.MockProvisioningStore{}
		provenanceStore.EXPECT().GetProvenances(mock.Anything, orgID, "alertRule").Return(storeResult, nil)

		result := verifyProvisionedRulesNotAffected(context.Background(), provenanceStore, orgID, ch)
		require.Error(t, result)
		require.ErrorIs(t, result, errProvisionedResource)
		assert.Contains(t, result.Error(), allRules[0].GetGroupKey().String())
		assert.Contains(t, result.Error(), allRules[1].GetGroupKey().String())
	})

	t.Run("should return nil if all have ProvenanceNone", func(t *testing.T) {
		storeResult := make(map[string]models.Provenance, len(allRules))
		for _, rule := range allRules {
			storeResult[rule.UID] = models.ProvenanceNone
		}

		provenanceStore := &provisioning.MockProvisioningStore{}
		provenanceStore.EXPECT().GetProvenances(mock.Anything, orgID, "alertRule").Return(storeResult, nil)

		result := verifyProvisionedRulesNotAffected(context.Background(), provenanceStore, orgID, ch)
		require.NoError(t, result)
	})

	t.Run("should return nil if no alerts have provisioning status", func(t *testing.T) {
		provenanceStore := &provisioning.MockProvisioningStore{}
		provenanceStore.EXPECT().GetProvenances(mock.Anything, orgID, "alertRule").Return(make(map[string]models.Provenance, len(allRules)), nil)

		result := verifyProvisionedRulesNotAffected(context.Background(), provenanceStore, orgID, ch)
		require.NoError(t, result)
	})
}

func createService(ac *acMock.Mock, store *store.FakeRuleStore, scheduler schedule.ScheduleService) *RulerSrv {
	return &RulerSrv{
		xactManager:     store,
		store:           store,
		DatasourceCache: nil,
		QuotaService:    nil,
		provenanceStore: provisioning.NewFakeProvisioningStore(),
		scheduleService: scheduler,
		log:             log.New("test"),
		cfg:             nil,
		ac:              ac,
	}
}

func createRequestContext(orgID int64, role models2.RoleType, params map[string]string) *models2.ReqContext {
	uri, _ := url.Parse("http://localhost")
	ctx := web.Context{Req: &http.Request{
		URL: uri,
	}}
	if params != nil {
		ctx.Req = web.SetURLParams(ctx.Req, params)
	}

	return &models2.ReqContext{
		IsSignedIn: true,
		SignedInUser: &models2.SignedInUser{
			OrgRole: role,
			OrgId:   orgID,
		},
		Context: &ctx,
	}
}

func createPermissionsForRules(rules []*models.AlertRule) []accesscontrol.Permission {
	var permissions []accesscontrol.Permission
	for _, rule := range rules {
		for _, query := range rule.Data {
			permissions = append(permissions, accesscontrol.Permission{
				Action: datasources.ActionQuery, Scope: datasources.ScopeProvider.GetResourceScopeUID(query.DatasourceUID),
			})
		}
	}
	return permissions
}

func withOrgID(orgId int64) func(rule *models.AlertRule) {
	return func(rule *models.AlertRule) {
		rule.OrgID = orgId
	}
}

func withGroup(groupName string) func(rule *models.AlertRule) {
	return func(rule *models.AlertRule) {
		rule.RuleGroup = groupName
	}
}

func withNamespace(namespace *models2.Folder) func(rule *models.AlertRule) {
	return func(rule *models.AlertRule) {
		rule.NamespaceUID = namespace.Uid
	}
}

func withGroupKey(groupKey models.AlertRuleGroupKey) func(rule *models.AlertRule) {
	return func(rule *models.AlertRule) {
		rule.RuleGroup = groupKey.RuleGroup
		rule.OrgID = groupKey.OrgID
		rule.NamespaceUID = groupKey.NamespaceUID
	}
}
