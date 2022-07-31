package loginservice

import (
	"context"
	"errors"

	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/login"
	"github.com/grafana/grafana/pkg/services/quota"
	"github.com/grafana/grafana/pkg/services/sqlstore"
	"github.com/grafana/grafana/pkg/services/user"
)

var (
	logger = log.New("login.ext_user")
)

func ProvideService(
	sqlStore sqlstore.Store,
	userService user.Service,
	quotaService quota.Service,
	authInfoService login.AuthInfoService,
) *Implementation {
	s := &Implementation{
		SQLStore:        sqlStore,
		userService:     userService,
		QuotaService:    quotaService,
		AuthInfoService: authInfoService,
	}
	return s
}

type Implementation struct {
	SQLStore        sqlstore.Store
	userService     user.Service
	AuthInfoService login.AuthInfoService
	QuotaService    quota.Service
	TeamSync        login.TeamSyncFunc
}

// CreateUser creates inserts a new one.
func (ls *Implementation) CreateUser(cmd user.CreateUserCommand) (*user.User, error) {
	return ls.SQLStore.CreateUser(context.Background(), cmd)
}

// UpsertUser updates an existing user, or if it doesn't exist, inserts a new one.
func (ls *Implementation) UpsertUser(ctx context.Context, cmd *models.UpsertUserCommand) error {
	extUser := cmd.ExternalUser

	usr, err := ls.AuthInfoService.LookupAndUpdate(ctx, &models.GetUserByAuthInfoQuery{
		AuthModule:       extUser.AuthModule,
		AuthId:           extUser.AuthId,
		UserLookupParams: cmd.UserLookupParams,
	})
	if err != nil {
		if !errors.Is(err, user.ErrUserNotFound) {
			return err
		}
		if !cmd.SignupAllowed {
			cmd.ReqContext.Logger.Warn("Not allowing login, user not found in internal user database and allow signup = false", "authmode", extUser.AuthModule)
			return login.ErrSignupNotAllowed
		}

		limitReached, err := ls.QuotaService.QuotaReached(cmd.ReqContext, "user")
		if err != nil {
			cmd.ReqContext.Logger.Warn("Error getting user quota.", "error", err)
			return login.ErrGettingUserQuota
		}
		if limitReached {
			return login.ErrUsersQuotaReached
		}

		result, err := ls.createUser(extUser)
		if err != nil {
			return err
		}

		cmd.Result = &user.User{
			ID:               result.ID,
			Version:          result.Version,
			Email:            result.Email,
			Name:             result.Name,
			Login:            result.Login,
			Password:         result.Password,
			Salt:             result.Salt,
			Rands:            result.Rands,
			Company:          result.Company,
			EmailVerified:    result.EmailVerified,
			Theme:            result.Theme,
			HelpFlags1:       result.HelpFlags1,
			IsDisabled:       result.IsDisabled,
			IsAdmin:          result.IsAdmin,
			IsServiceAccount: result.IsServiceAccount,
			OrgID:            result.OrgID,
			Created:          result.Created,
			Updated:          result.Updated,
			LastSeenAt:       result.LastSeenAt,
		}

		if extUser.AuthModule != "" {
			cmd2 := &models.SetAuthInfoCommand{
				UserId:     cmd.Result.ID,
				AuthModule: extUser.AuthModule,
				AuthId:     extUser.AuthId,
				OAuthToken: extUser.OAuthToken,
			}
			if err := ls.AuthInfoService.SetAuthInfo(ctx, cmd2); err != nil {
				return err
			}
		}
	} else {
		cmd.Result = usr

		err = ls.updateUser(ctx, cmd.Result, extUser)
		if err != nil {
			return err
		}

		// Always persist the latest token at log-in
		if extUser.AuthModule != "" && extUser.OAuthToken != nil {
			err = ls.updateUserAuth(ctx, cmd.Result, extUser)
			if err != nil {
				return err
			}
		}

		if extUser.AuthModule == models.AuthModuleLDAP && usr.IsDisabled {
			// Re-enable user when it found in LDAP
			if err := ls.SQLStore.DisableUser(ctx, &models.DisableUserCommand{UserId: cmd.Result.ID, IsDisabled: false}); err != nil {
				return err
			}
		}
	}

	if err := ls.syncOrgRoles(ctx, cmd.Result, extUser); err != nil {
		return err
	}

	// Sync isGrafanaAdmin permission
	if extUser.IsGrafanaAdmin != nil && *extUser.IsGrafanaAdmin != cmd.Result.IsAdmin {
		if err := ls.SQLStore.UpdateUserPermissions(cmd.Result.ID, *extUser.IsGrafanaAdmin); err != nil {
			return err
		}
	}

	if ls.TeamSync != nil {
		err := ls.TeamSync(cmd.Result, extUser)
		if err != nil {
			return err
		}
	}

	return nil
}

func (ls *Implementation) DisableExternalUser(ctx context.Context, username string) error {
	// Check if external user exist in Grafana
	userQuery := &models.GetExternalUserInfoByLoginQuery{
		LoginOrEmail: username,
	}

	if err := ls.AuthInfoService.GetExternalUserInfoByLogin(ctx, userQuery); err != nil {
		return err
	}

	userInfo := userQuery.Result
	if userInfo.IsDisabled {
		return nil
	}

	logger.Debug(
		"Disabling external user",
		"user",
		userQuery.Result.Login,
	)

	// Mark user as disabled in grafana db
	disableUserCmd := &models.DisableUserCommand{
		UserId:     userQuery.Result.UserId,
		IsDisabled: true,
	}

	if err := ls.SQLStore.DisableUser(ctx, disableUserCmd); err != nil {
		logger.Debug(
			"Error disabling external user",
			"user",
			userQuery.Result.Login,
			"message",
			err.Error(),
		)
		return err
	}
	return nil
}

// SetTeamSyncFunc sets the function received through args as the team sync function.
func (ls *Implementation) SetTeamSyncFunc(teamSyncFunc login.TeamSyncFunc) {
	ls.TeamSync = teamSyncFunc
}

func (ls *Implementation) createUser(extUser *models.ExternalUserInfo) (*user.User, error) {
	cmd := user.CreateUserCommand{
		Login:        extUser.Login,
		Email:        extUser.Email,
		Name:         extUser.Name,
		SkipOrgSetup: len(extUser.OrgRoles) > 0,
	}
	return ls.CreateUser(cmd)
}

func (ls *Implementation) updateUser(ctx context.Context, user *user.User, extUser *models.ExternalUserInfo) error {
	// sync user info
	updateCmd := &models.UpdateUserCommand{
		UserId: user.ID,
	}

	needsUpdate := false
	if extUser.Login != "" && extUser.Login != user.Login {
		updateCmd.Login = extUser.Login
		user.Login = extUser.Login
		needsUpdate = true
	}

	if extUser.Email != "" && extUser.Email != user.Email {
		updateCmd.Email = extUser.Email
		user.Email = extUser.Email
		needsUpdate = true
	}

	if extUser.Name != "" && extUser.Name != user.Name {
		updateCmd.Name = extUser.Name
		user.Name = extUser.Name
		needsUpdate = true
	}

	if !needsUpdate {
		return nil
	}

	logger.Debug("Syncing user info", "id", user.ID, "update", updateCmd)
	return ls.SQLStore.UpdateUser(ctx, updateCmd)
}

func (ls *Implementation) updateUserAuth(ctx context.Context, user *user.User, extUser *models.ExternalUserInfo) error {
	updateCmd := &models.UpdateAuthInfoCommand{
		AuthModule: extUser.AuthModule,
		AuthId:     extUser.AuthId,
		UserId:     user.ID,
		OAuthToken: extUser.OAuthToken,
	}

	logger.Debug("Updating user_auth info", "user_id", user.ID)
	return ls.AuthInfoService.UpdateAuthInfo(ctx, updateCmd)
}

func (ls *Implementation) syncOrgRoles(ctx context.Context, user *user.User, extUser *models.ExternalUserInfo) error {
	logger.Debug("Syncing organization roles", "id", user.ID, "extOrgRoles", extUser.OrgRoles)

	// don't sync org roles if none is specified
	if len(extUser.OrgRoles) == 0 {
		logger.Debug("Not syncing organization roles since external user doesn't have any")
		return nil
	}

	orgsQuery := &models.GetUserOrgListQuery{UserId: user.ID}
	if err := ls.SQLStore.GetUserOrgList(ctx, orgsQuery); err != nil {
		return err
	}

	handledOrgIds := map[int64]bool{}
	deleteOrgIds := []int64{}

	// update existing org roles
	for _, org := range orgsQuery.Result {
		handledOrgIds[org.OrgId] = true

		extRole := extUser.OrgRoles[org.OrgId]
		if extRole == "" {
			deleteOrgIds = append(deleteOrgIds, org.OrgId)
		} else if extRole != org.Role {
			// update role
			cmd := &models.UpdateOrgUserCommand{OrgId: org.OrgId, UserId: user.ID, Role: extRole}
			if err := ls.SQLStore.UpdateOrgUser(ctx, cmd); err != nil {
				return err
			}
		}
	}

	// add any new org roles
	for orgId, orgRole := range extUser.OrgRoles {
		if _, exists := handledOrgIds[orgId]; exists {
			continue
		}

		// add role
		cmd := &models.AddOrgUserCommand{UserId: user.ID, Role: orgRole, OrgId: orgId}
		err := ls.SQLStore.AddOrgUser(ctx, cmd)
		if err != nil && !errors.Is(err, models.ErrOrgNotFound) {
			return err
		}
	}

	// delete any removed org roles
	for _, orgId := range deleteOrgIds {
		logger.Debug("Removing user's organization membership as part of syncing with OAuth login",
			"userId", user.ID, "orgId", orgId)
		cmd := &models.RemoveOrgUserCommand{OrgId: orgId, UserId: user.ID}
		if err := ls.SQLStore.RemoveOrgUser(ctx, cmd); err != nil {
			if errors.Is(err, models.ErrLastOrgAdmin) {
				logger.Error(err.Error(), "userId", cmd.UserId, "orgId", cmd.OrgId)
				continue
			}

			return err
		}
	}

	// update user's default org if needed
	if _, ok := extUser.OrgRoles[user.OrgID]; !ok {
		for orgId := range extUser.OrgRoles {
			user.OrgID = orgId
			break
		}

		return ls.SQLStore.SetUsingOrg(ctx, &models.SetUsingOrgCommand{
			UserId: user.ID,
			OrgId:  user.OrgID,
		})
	}

	return nil
}
