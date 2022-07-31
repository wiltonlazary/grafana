package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/grafana/pkg/services/accesscontrol"
	"github.com/grafana/grafana/pkg/services/dashboards"
	"github.com/grafana/grafana/pkg/services/datasources"
	"github.com/grafana/grafana/pkg/services/ngalert/provisioning"
	"github.com/grafana/grafana/pkg/services/ngalert/store"
	"github.com/grafana/grafana/pkg/services/quota"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/grafana/grafana/pkg/util/cmputil"

	"github.com/prometheus/common/model"

	"github.com/grafana/grafana/pkg/api/apierrors"
	"github.com/grafana/grafana/pkg/api/response"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/models"
	apimodels "github.com/grafana/grafana/pkg/services/ngalert/api/tooling/definitions"
	ngmodels "github.com/grafana/grafana/pkg/services/ngalert/models"
	"github.com/grafana/grafana/pkg/services/ngalert/schedule"
	"github.com/grafana/grafana/pkg/util"
)

type RulerSrv struct {
	xactManager     provisioning.TransactionManager
	provenanceStore provisioning.ProvisioningStore
	store           store.RuleStore
	DatasourceCache datasources.CacheService
	QuotaService    quota.Service
	scheduleService schedule.ScheduleService
	log             log.Logger
	cfg             *setting.UnifiedAlertingSettings
	ac              accesscontrol.AccessControl
}

var (
	errProvisionedResource = errors.New("request affects resources created via provisioning API")
)

// RouteDeleteAlertRules deletes all alert rules user is authorized to access in the given namespace
// or, if non-empty, a specific group of rules in the namespace
func (srv RulerSrv) RouteDeleteAlertRules(c *models.ReqContext, namespaceTitle string, group string) response.Response {
	namespace, err := srv.store.GetNamespaceByTitle(c.Req.Context(), namespaceTitle, c.SignedInUser.OrgId, c.SignedInUser, true)
	if err != nil {
		return toNamespaceErrorResponse(err)
	}
	var loggerCtx = []interface{}{
		"namespace",
		namespace.Title,
	}
	var ruleGroup string
	if group != "" {
		ruleGroup = group
		loggerCtx = append(loggerCtx, "group", group)
	}
	logger := srv.log.New(loggerCtx...)

	hasAccess := func(evaluator accesscontrol.Evaluator) bool {
		return accesscontrol.HasAccess(srv.ac, c)(accesscontrol.ReqOrgAdminOrEditor, evaluator)
	}

	provenances, err := srv.provenanceStore.GetProvenances(c.Req.Context(), c.SignedInUser.OrgId, (&ngmodels.AlertRule{}).ResourceType())
	if err != nil {
		return ErrResp(http.StatusInternalServerError, err, "failed to fetch provenances of alert rules")
	}

	var deletableRules []string
	err = srv.xactManager.InTransaction(c.Req.Context(), func(ctx context.Context) error {
		q := ngmodels.ListAlertRulesQuery{
			OrgID:         c.SignedInUser.OrgId,
			NamespaceUIDs: []string{namespace.Uid},
			RuleGroup:     ruleGroup,
		}
		if err = srv.store.ListAlertRules(ctx, &q); err != nil {
			return err
		}

		if len(q.Result) == 0 {
			logger.Debug("no alert rules to delete from namespace/group")
			return nil
		}

		var canDelete []*ngmodels.AlertRule
		var cannotDelete []string

		// partition will partation the given rules in two, one partition
		// being the rules that fulfill the predicate the other partation being
		// the ruleIDs not fulfilling it.
		partition := func(alerts []*ngmodels.AlertRule, predicate func(rule *ngmodels.AlertRule) bool) ([]*ngmodels.AlertRule, []string) {
			positive, negative := make([]*ngmodels.AlertRule, 0, len(alerts)), make([]string, 0, len(alerts))
			for _, rule := range alerts {
				if predicate(rule) {
					positive = append(positive, rule)
					continue
				}
				negative = append(negative, rule.UID)
			}
			return positive, negative
		}

		canDelete, cannotDelete = partition(q.Result, func(rule *ngmodels.AlertRule) bool {
			return authorizeDatasourceAccessForRule(rule, hasAccess)
		})
		if len(canDelete) == 0 {
			return fmt.Errorf("%w to delete rules because user is not authorized to access data sources used by the rules", ErrAuthorization)
		}
		if len(cannotDelete) > 0 {
			logger.Info("user cannot delete one or many alert rules because it does not have access to data sources. Those rules will be skipped", "expected", len(q.Result), "authorized", len(canDelete), "unauthorized", cannotDelete)
		}

		canDelete, cannotDelete = partition(canDelete, func(rule *ngmodels.AlertRule) bool {
			provenance, exists := provenances[rule.UID]
			return (exists && provenance == ngmodels.ProvenanceNone) || !exists
		})

		if len(canDelete) == 0 {
			return fmt.Errorf("all rules have been provisioned and cannot be deleted through this api")
		}

		if len(cannotDelete) > 0 {
			logger.Info("user cannot delete one or many alert rules because it does have a provenance set. Those rules will be skipped", "expected", len(q.Result), "provenance_none", len(canDelete), "provenance_set", cannotDelete)
		}

		for _, rule := range canDelete {
			deletableRules = append(deletableRules, rule.UID)
		}

		return srv.store.DeleteAlertRulesByUID(ctx, c.SignedInUser.OrgId, deletableRules...)
	})

	if err != nil {
		if errors.Is(err, ErrAuthorization) {
			return ErrResp(http.StatusUnauthorized, err, "")
		}
		return ErrResp(http.StatusInternalServerError, err, "failed to delete rule group")
	}

	logger.Debug("rules have been deleted from the store. updating scheduler")

	for _, uid := range deletableRules {
		srv.scheduleService.DeleteAlertRule(ngmodels.AlertRuleKey{
			OrgID: c.SignedInUser.OrgId,
			UID:   uid,
		})
	}

	return response.JSON(http.StatusAccepted, util.DynMap{"message": "rules deleted"})
}

// RouteGetNamespaceRulesConfig returns all rules in a specific folder that user has access to
func (srv RulerSrv) RouteGetNamespaceRulesConfig(c *models.ReqContext, namespaceTitle string) response.Response {
	namespace, err := srv.store.GetNamespaceByTitle(c.Req.Context(), namespaceTitle, c.SignedInUser.OrgId, c.SignedInUser, false)
	if err != nil {
		return toNamespaceErrorResponse(err)
	}

	q := ngmodels.ListAlertRulesQuery{
		OrgID:         c.SignedInUser.OrgId,
		NamespaceUIDs: []string{namespace.Uid},
	}
	if err := srv.store.ListAlertRules(c.Req.Context(), &q); err != nil {
		return ErrResp(http.StatusInternalServerError, err, "failed to update rule group")
	}

	result := apimodels.NamespaceConfigResponse{}

	hasAccess := func(evaluator accesscontrol.Evaluator) bool {
		return accesscontrol.HasAccess(srv.ac, c)(accesscontrol.ReqViewer, evaluator)
	}

	provenanceRecords, err := srv.provenanceStore.GetProvenances(c.Req.Context(), c.SignedInUser.OrgId, (&ngmodels.AlertRule{}).ResourceType())
	if err != nil {
		return ErrResp(http.StatusInternalServerError, err, "failed to get provenance for rule group")
	}

	ruleGroups := make(map[string]ngmodels.RulesGroup)
	for _, r := range q.Result {
		ruleGroups[r.RuleGroup] = append(ruleGroups[r.RuleGroup], r)
	}

	for groupName, rules := range ruleGroups {
		if !authorizeAccessToRuleGroup(rules, hasAccess) {
			continue
		}
		result[namespaceTitle] = append(result[namespaceTitle], toGettableRuleGroupConfig(groupName, rules, namespace.Id, provenanceRecords))
	}

	return response.JSON(http.StatusAccepted, result)
}

// RouteGetRulesGroupConfig returns rules that belong to a specific group in a specific namespace (folder).
// If user does not have access to at least one of the rule in the group, returns status 401 Unauthorized
func (srv RulerSrv) RouteGetRulesGroupConfig(c *models.ReqContext, namespaceTitle string, ruleGroup string) response.Response {
	namespace, err := srv.store.GetNamespaceByTitle(c.Req.Context(), namespaceTitle, c.SignedInUser.OrgId, c.SignedInUser, false)
	if err != nil {
		return toNamespaceErrorResponse(err)
	}

	q := ngmodels.ListAlertRulesQuery{
		OrgID:         c.SignedInUser.OrgId,
		NamespaceUIDs: []string{namespace.Uid},
		RuleGroup:     ruleGroup,
	}
	if err := srv.store.ListAlertRules(c.Req.Context(), &q); err != nil {
		return ErrResp(http.StatusInternalServerError, err, "failed to get group alert rules")
	}

	hasAccess := func(evaluator accesscontrol.Evaluator) bool {
		return accesscontrol.HasAccess(srv.ac, c)(accesscontrol.ReqViewer, evaluator)
	}

	provenanceRecords, err := srv.provenanceStore.GetProvenances(c.Req.Context(), c.SignedInUser.OrgId, (&ngmodels.AlertRule{}).ResourceType())
	if err != nil {
		return ErrResp(http.StatusInternalServerError, err, "failed to get group alert rules")
	}

	if !authorizeAccessToRuleGroup(q.Result, hasAccess) {
		return ErrResp(http.StatusUnauthorized, fmt.Errorf("%w to access the group because it does not have access to one or many data sources one or many rules in the group use", ErrAuthorization), "")
	}

	result := apimodels.RuleGroupConfigResponse{
		GettableRuleGroupConfig: toGettableRuleGroupConfig(ruleGroup, q.Result, namespace.Id, provenanceRecords),
	}
	return response.JSON(http.StatusAccepted, result)
}

// RouteGetRulesConfig returns all alert rules that are available to the current user
func (srv RulerSrv) RouteGetRulesConfig(c *models.ReqContext) response.Response {
	namespaceMap, err := srv.store.GetUserVisibleNamespaces(c.Req.Context(), c.OrgId, c.SignedInUser)
	if err != nil {
		return ErrResp(http.StatusInternalServerError, err, "failed to get namespaces visible to the user")
	}
	result := apimodels.NamespaceConfigResponse{}

	if len(namespaceMap) == 0 {
		srv.log.Debug("user has no access to any namespaces")
		return response.JSON(http.StatusOK, result)
	}

	namespaceUIDs := make([]string, len(namespaceMap))
	for k := range namespaceMap {
		namespaceUIDs = append(namespaceUIDs, k)
	}

	dashboardUID := c.Query("dashboard_uid")
	panelID, err := getPanelIDFromRequest(c.Req)
	if err != nil {
		return ErrResp(http.StatusBadRequest, err, "invalid panel_id")
	}
	if dashboardUID == "" && panelID != 0 {
		return ErrResp(http.StatusBadRequest, errors.New("panel_id must be set with dashboard_uid"), "")
	}

	q := ngmodels.ListAlertRulesQuery{
		OrgID:         c.SignedInUser.OrgId,
		NamespaceUIDs: namespaceUIDs,
		DashboardUID:  dashboardUID,
		PanelID:       panelID,
	}

	if err := srv.store.ListAlertRules(c.Req.Context(), &q); err != nil {
		return ErrResp(http.StatusInternalServerError, err, "failed to get alert rules")
	}

	hasAccess := func(evaluator accesscontrol.Evaluator) bool {
		return accesscontrol.HasAccess(srv.ac, c)(accesscontrol.ReqViewer, evaluator)
	}

	provenanceRecords, err := srv.provenanceStore.GetProvenances(c.Req.Context(), c.SignedInUser.OrgId, (&ngmodels.AlertRule{}).ResourceType())
	if err != nil {
		return ErrResp(http.StatusInternalServerError, err, "failed to get alert rules")
	}

	configs := make(map[ngmodels.AlertRuleGroupKey]ngmodels.RulesGroup)
	for _, r := range q.Result {
		groupKey := r.GetGroupKey()
		group := configs[groupKey]
		group = append(group, r)
		configs[groupKey] = group
	}

	for groupKey, rules := range configs {
		folder, ok := namespaceMap[groupKey.NamespaceUID]
		if !ok {
			srv.log.Error("namespace not visible to the user", "user", c.SignedInUser.UserId, "namespace", groupKey.NamespaceUID)
			continue
		}
		if !authorizeAccessToRuleGroup(rules, hasAccess) {
			continue
		}
		namespace := folder.Title
		result[namespace] = append(result[namespace], toGettableRuleGroupConfig(groupKey.RuleGroup, rules, folder.Id, provenanceRecords))
	}
	return response.JSON(http.StatusOK, result)
}

func (srv RulerSrv) RoutePostNameRulesConfig(c *models.ReqContext, ruleGroupConfig apimodels.PostableRuleGroupConfig, namespaceTitle string) response.Response {
	namespace, err := srv.store.GetNamespaceByTitle(c.Req.Context(), namespaceTitle, c.SignedInUser.OrgId, c.SignedInUser, true)
	if err != nil {
		return toNamespaceErrorResponse(err)
	}

	rules, err := validateRuleGroup(&ruleGroupConfig, c.SignedInUser.OrgId, namespace, conditionValidator(c, srv.DatasourceCache), srv.cfg)
	if err != nil {
		return ErrResp(http.StatusBadRequest, err, "")
	}

	groupKey := ngmodels.AlertRuleGroupKey{
		OrgID:        c.SignedInUser.OrgId,
		NamespaceUID: namespace.Uid,
		RuleGroup:    ruleGroupConfig.Name,
	}

	return srv.updateAlertRulesInGroup(c, groupKey, rules)
}

// updateAlertRulesInGroup calculates changes (rules to add,update,delete), verifies that the user is authorized to do the calculated changes and updates database.
// All operations are performed in a single transaction
func (srv RulerSrv) updateAlertRulesInGroup(c *models.ReqContext, groupKey ngmodels.AlertRuleGroupKey, rules []*ngmodels.AlertRule) response.Response {
	var finalChanges *changes
	hasAccess := accesscontrol.HasAccess(srv.ac, c)
	err := srv.xactManager.InTransaction(c.Req.Context(), func(tranCtx context.Context) error {
		logger := srv.log.New("namespace_uid", groupKey.NamespaceUID, "group", groupKey.RuleGroup, "org_id", groupKey.OrgID, "user_id", c.UserId)
		groupChanges, err := calculateChanges(tranCtx, srv.store, groupKey, rules)
		if err != nil {
			return err
		}

		if groupChanges.isEmpty() {
			finalChanges = groupChanges
			logger.Info("no changes detected in the request. Do nothing")
			return nil
		}

		// if RBAC is disabled the permission are limited to folder access that is done upstream
		if !srv.ac.IsDisabled() {
			err = authorizeRuleChanges(groupChanges, func(evaluator accesscontrol.Evaluator) bool {
				return hasAccess(accesscontrol.ReqOrgAdminOrEditor, evaluator)
			})
			if err != nil {
				return err
			}
		}

		if err := verifyProvisionedRulesNotAffected(c.Req.Context(), srv.provenanceStore, c.OrgId, groupChanges); err != nil {
			return err
		}

		finalChanges = calculateAutomaticChanges(groupChanges)
		logger.Debug("updating database with the authorized changes", "add", len(finalChanges.New), "update", len(finalChanges.New), "delete", len(finalChanges.Delete))

		if len(finalChanges.Update) > 0 || len(finalChanges.New) > 0 {
			updates := make([]store.UpdateRule, 0, len(finalChanges.Update))
			inserts := make([]ngmodels.AlertRule, 0, len(finalChanges.New))
			for _, update := range finalChanges.Update {
				logger.Debug("updating rule", "rule_uid", update.New.UID, "diff", update.Diff.String())
				updates = append(updates, store.UpdateRule{
					Existing: update.Existing,
					New:      *update.New,
				})
			}
			for _, rule := range finalChanges.New {
				inserts = append(inserts, *rule)
			}
			_, err = srv.store.InsertAlertRules(tranCtx, inserts)
			if err != nil {
				return fmt.Errorf("failed to add rules: %w", err)
			}
			err = srv.store.UpdateAlertRules(tranCtx, updates)
			if err != nil {
				return fmt.Errorf("failed to update rules: %w", err)
			}
		}

		if len(finalChanges.Delete) > 0 {
			UIDs := make([]string, 0, len(finalChanges.Delete))
			for _, rule := range finalChanges.Delete {
				UIDs = append(UIDs, rule.UID)
			}

			if err = srv.store.DeleteAlertRulesByUID(tranCtx, c.SignedInUser.OrgId, UIDs...); err != nil {
				return fmt.Errorf("failed to delete rules: %w", err)
			}
		}

		if len(finalChanges.New) > 0 {
			limitReached, err := srv.QuotaService.CheckQuotaReached(tranCtx, "alert_rule", &quota.ScopeParameters{
				OrgID:  c.OrgId,
				UserID: c.UserId,
			}) // alert rule is table name
			if err != nil {
				return fmt.Errorf("failed to get alert rules quota: %w", err)
			}
			if limitReached {
				return ngmodels.ErrQuotaReached
			}
		}
		return nil
	})

	if err != nil {
		if errors.Is(err, ngmodels.ErrAlertRuleNotFound) {
			return ErrResp(http.StatusNotFound, err, "failed to update rule group")
		} else if errors.Is(err, ngmodels.ErrAlertRuleFailedValidation) || errors.Is(err, errProvisionedResource) {
			return ErrResp(http.StatusBadRequest, err, "failed to update rule group")
		} else if errors.Is(err, ngmodels.ErrQuotaReached) {
			return ErrResp(http.StatusForbidden, err, "")
		} else if errors.Is(err, ErrAuthorization) {
			return ErrResp(http.StatusUnauthorized, err, "")
		} else if errors.Is(err, store.ErrOptimisticLock) {
			return ErrResp(http.StatusConflict, err, "")
		}
		return ErrResp(http.StatusInternalServerError, err, "failed to update rule group")
	}

	for _, rule := range finalChanges.Update {
		srv.scheduleService.UpdateAlertRule(ngmodels.AlertRuleKey{
			OrgID: c.SignedInUser.OrgId,
			UID:   rule.Existing.UID,
		}, rule.Existing.Version+1)
	}

	for _, rule := range finalChanges.Delete {
		srv.scheduleService.DeleteAlertRule(ngmodels.AlertRuleKey{
			OrgID: c.SignedInUser.OrgId,
			UID:   rule.UID,
		})
	}

	if finalChanges.isEmpty() {
		return response.JSON(http.StatusAccepted, util.DynMap{"message": "no changes detected in the rule group"})
	}

	return response.JSON(http.StatusAccepted, util.DynMap{"message": "rule group updated successfully"})
}

func toGettableRuleGroupConfig(groupName string, rules ngmodels.RulesGroup, namespaceID int64, provenanceRecords map[string]ngmodels.Provenance) apimodels.GettableRuleGroupConfig {
	rules.SortByGroupIndex()
	ruleNodes := make([]apimodels.GettableExtendedRuleNode, 0, len(rules))
	var interval time.Duration
	if len(rules) > 0 {
		interval = time.Duration(rules[0].IntervalSeconds) * time.Second
	}
	for _, r := range rules {
		ruleNodes = append(ruleNodes, toGettableExtendedRuleNode(*r, namespaceID, provenanceRecords))
	}
	return apimodels.GettableRuleGroupConfig{
		Name:     groupName,
		Interval: model.Duration(interval),
		Rules:    ruleNodes,
	}
}

func toGettableExtendedRuleNode(r ngmodels.AlertRule, namespaceID int64, provenanceRecords map[string]ngmodels.Provenance) apimodels.GettableExtendedRuleNode {
	provenance := ngmodels.ProvenanceNone
	if prov, exists := provenanceRecords[r.ResourceID()]; exists {
		provenance = prov
	}
	gettableExtendedRuleNode := apimodels.GettableExtendedRuleNode{
		GrafanaManagedAlert: &apimodels.GettableGrafanaRule{
			ID:              r.ID,
			OrgID:           r.OrgID,
			Title:           r.Title,
			Condition:       r.Condition,
			Data:            r.Data,
			Updated:         r.Updated,
			IntervalSeconds: r.IntervalSeconds,
			Version:         r.Version,
			UID:             r.UID,
			NamespaceUID:    r.NamespaceUID,
			NamespaceID:     namespaceID,
			RuleGroup:       r.RuleGroup,
			NoDataState:     apimodels.NoDataState(r.NoDataState),
			ExecErrState:    apimodels.ExecutionErrorState(r.ExecErrState),
			Provenance:      provenance,
		},
	}
	forDuration := model.Duration(r.For)
	gettableExtendedRuleNode.ApiRuleNode = &apimodels.ApiRuleNode{
		For:         &forDuration,
		Annotations: r.Annotations,
		Labels:      r.Labels,
	}
	return gettableExtendedRuleNode
}

func toNamespaceErrorResponse(err error) response.Response {
	if errors.Is(err, ngmodels.ErrCannotEditNamespace) {
		return ErrResp(http.StatusForbidden, err, err.Error())
	}
	if errors.Is(err, dashboards.ErrDashboardIdentifierNotSet) {
		return ErrResp(http.StatusBadRequest, err, err.Error())
	}
	return apierrors.ToFolderErrorResponse(err)
}

type ruleUpdate struct {
	Existing *ngmodels.AlertRule
	New      *ngmodels.AlertRule
	Diff     cmputil.DiffReport
}

type changes struct {
	GroupKey ngmodels.AlertRuleGroupKey
	// AffectedGroups contains all rules of all groups that are affected by these changes.
	// For example, during moving a rule from one group to another this map will contain all rules from two groups
	AffectedGroups map[ngmodels.AlertRuleGroupKey]ngmodels.RulesGroup
	New            []*ngmodels.AlertRule
	Update         []ruleUpdate
	Delete         []*ngmodels.AlertRule
}

func (c *changes) isEmpty() bool {
	return len(c.Update)+len(c.New)+len(c.Delete) == 0
}

// verifyProvisionedRulesNotAffected check that neither of provisioned alerts are affected by changes.
// Returns errProvisionedResource if there is at least one rule in groups affected by changes that was provisioned.
func verifyProvisionedRulesNotAffected(ctx context.Context, provenanceStore provisioning.ProvisioningStore, orgID int64, ch *changes) error {
	provenances, err := provenanceStore.GetProvenances(ctx, orgID, (&ngmodels.AlertRule{}).ResourceType())
	if err != nil {
		return err
	}
	errorMsg := strings.Builder{}
	for group, alertRules := range ch.AffectedGroups {
		for _, rule := range alertRules {
			if provenance, exists := provenances[rule.UID]; (exists && provenance == ngmodels.ProvenanceNone) || !exists {
				continue
			}
			if errorMsg.Len() > 0 {
				errorMsg.WriteRune(',')
			}
			errorMsg.WriteString(group.String())
			break
		}
	}
	if errorMsg.Len() == 0 {
		return nil
	}
	return fmt.Errorf("%w: alert rule group [%s]", errProvisionedResource, errorMsg.String())
}

// calculateChanges calculates the difference between rules in the group in the database and the submitted rules. If a submitted rule has UID it tries to find it in the database (in other groups).
// returns a list of rules that need to be added, updated and deleted. Deleted considered rules in the database that belong to the group but do not exist in the list of submitted rules.
func calculateChanges(ctx context.Context, ruleStore store.RuleStore, groupKey ngmodels.AlertRuleGroupKey, submittedRules []*ngmodels.AlertRule) (*changes, error) {
	affectedGroups := make(map[ngmodels.AlertRuleGroupKey]ngmodels.RulesGroup)
	q := &ngmodels.ListAlertRulesQuery{
		OrgID:         groupKey.OrgID,
		NamespaceUIDs: []string{groupKey.NamespaceUID},
		RuleGroup:     groupKey.RuleGroup,
	}
	if err := ruleStore.ListAlertRules(ctx, q); err != nil {
		return nil, fmt.Errorf("failed to query database for rules in the group %s: %w", groupKey, err)
	}
	existingGroupRules := q.Result
	if len(existingGroupRules) > 0 {
		affectedGroups[groupKey] = existingGroupRules
	}

	existingGroupRulesUIDs := make(map[string]*ngmodels.AlertRule, len(existingGroupRules))
	for _, r := range existingGroupRules {
		existingGroupRulesUIDs[r.UID] = r
	}

	var toAdd, toDelete []*ngmodels.AlertRule
	var toUpdate []ruleUpdate
	loadedRulesByUID := map[string]*ngmodels.AlertRule{} // auxiliary cache to avoid unnecessary queries if there are multiple moves from the same group
	for _, r := range submittedRules {
		var existing *ngmodels.AlertRule = nil
		if r.UID != "" {
			if existingGroupRule, ok := existingGroupRulesUIDs[r.UID]; ok {
				existing = existingGroupRule
				// remove the rule from existingGroupRulesUIDs
				delete(existingGroupRulesUIDs, r.UID)
			} else if existing, ok = loadedRulesByUID[r.UID]; !ok { // check the "cache" and if there is no hit, query the database
				// Rule can be from other group or namespace
				q := &ngmodels.GetAlertRulesGroupByRuleUIDQuery{OrgID: groupKey.OrgID, UID: r.UID}
				if err := ruleStore.GetAlertRulesGroupByRuleUID(ctx, q); err != nil {
					return nil, fmt.Errorf("failed to query database for a group of alert rules: %w", err)
				}
				for _, rule := range q.Result {
					if rule.UID == r.UID {
						existing = rule
					}
					loadedRulesByUID[rule.UID] = rule
				}
				if existing == nil {
					return nil, fmt.Errorf("failed to update rule with UID %s because %w", r.UID, ngmodels.ErrAlertRuleNotFound)
				}
				affectedGroups[existing.GetGroupKey()] = q.Result
			}
		}

		if existing == nil {
			toAdd = append(toAdd, r)
			continue
		}

		ngmodels.PatchPartialAlertRule(existing, r)

		diff := existing.Diff(r, alertRuleFieldsToIgnoreInDiff...)
		if len(diff) == 0 {
			continue
		}

		toUpdate = append(toUpdate, ruleUpdate{
			Existing: existing,
			New:      r,
			Diff:     diff,
		})
		continue
	}

	for _, rule := range existingGroupRulesUIDs {
		toDelete = append(toDelete, rule)
	}

	return &changes{
		GroupKey:       groupKey,
		AffectedGroups: affectedGroups,
		New:            toAdd,
		Delete:         toDelete,
		Update:         toUpdate,
	}, nil
}

// calculateAutomaticChanges scans all affected groups and creates either a noop update that will increment the version of each rule as well as re-index other groups.
// this is needed to make sure that there are no any concurrent changes made to all affected groups.
// Returns a copy of changes enriched with either noop or group index changes for all rules in
func calculateAutomaticChanges(ch *changes) *changes {
	updatingRules := make(map[ngmodels.AlertRuleKey]struct{}, len(ch.Delete)+len(ch.Update))
	for _, update := range ch.Update {
		updatingRules[update.Existing.GetKey()] = struct{}{}
	}
	for _, del := range ch.Delete {
		updatingRules[del.GetKey()] = struct{}{}
	}
	var toUpdate []ruleUpdate
	for groupKey, rules := range ch.AffectedGroups {
		if groupKey != ch.GroupKey {
			rules.SortByGroupIndex()
		}
		idx := 1
		for _, rule := range rules {
			if _, ok := updatingRules[rule.GetKey()]; ok { // exclude rules that are going to be either updated or deleted
				continue
			}
			upd := ruleUpdate{
				Existing: rule,
				New:      rule,
			}
			if groupKey != ch.GroupKey {
				if rule.RuleGroupIndex != idx {
					upd.New = ngmodels.CopyRule(rule)
					upd.New.RuleGroupIndex = idx
					upd.Diff = rule.Diff(upd.New, alertRuleFieldsToIgnoreInDiff...)
				}
				idx++
			}
			toUpdate = append(toUpdate, upd)
		}
	}
	return &changes{
		GroupKey:       ch.GroupKey,
		AffectedGroups: ch.AffectedGroups,
		New:            ch.New,
		Update:         append(ch.Update, toUpdate...),
		Delete:         ch.Delete,
	}
}

// alertRuleFieldsToIgnoreInDiff contains fields that the AlertRule.Diff should ignore
var alertRuleFieldsToIgnoreInDiff = []string{"ID", "Version", "Updated"}
