package userimpl

import (
	"context"
	"errors"
	"time"

	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/accesscontrol"
	"github.com/grafana/grafana/pkg/services/dashboards"
	"github.com/grafana/grafana/pkg/services/org"
	pref "github.com/grafana/grafana/pkg/services/preference"
	"github.com/grafana/grafana/pkg/services/quota"
	"github.com/grafana/grafana/pkg/services/sqlstore"
	"github.com/grafana/grafana/pkg/services/sqlstore/db"
	"github.com/grafana/grafana/pkg/services/star"
	"github.com/grafana/grafana/pkg/services/teamguardian"
	"github.com/grafana/grafana/pkg/services/user"
	"github.com/grafana/grafana/pkg/services/userauth"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/grafana/grafana/pkg/util"

	"golang.org/x/sync/errgroup"
)

type Service struct {
	store              store
	orgService         org.Service
	starService        star.Service
	dashboardService   dashboards.DashboardService
	preferenceService  pref.Service
	teamMemberService  teamguardian.TeamGuardian
	userAuthService    userauth.Service
	quotaService       quota.Service
	accessControlStore accesscontrol.AccessControl
	// TODO remove sqlstore
	sqlStore *sqlstore.SQLStore

	cfg *setting.Cfg
}

func ProvideService(
	db db.DB,
	orgService org.Service,
	starService star.Service,
	dashboardService dashboards.DashboardService,
	preferenceService pref.Service,
	teamMemberService teamguardian.TeamGuardian,
	userAuthService userauth.Service,
	quotaService quota.Service,
	accessControlStore accesscontrol.AccessControl,
	cfg *setting.Cfg,
	ss *sqlstore.SQLStore,
) user.Service {
	return &Service{
		store: &sqlStore{
			db:      db,
			dialect: db.GetDialect(),
		},
		orgService:         orgService,
		starService:        starService,
		dashboardService:   dashboardService,
		preferenceService:  preferenceService,
		teamMemberService:  teamMemberService,
		userAuthService:    userAuthService,
		quotaService:       quotaService,
		accessControlStore: accessControlStore,
		cfg:                cfg,
		sqlStore:           ss,
	}
}

func (s *Service) Create(ctx context.Context, cmd *user.CreateUserCommand) (*user.User, error) {
	cmdOrg := org.GetOrgIDForNewUserCommand{
		Email:        cmd.Email,
		Login:        cmd.Login,
		OrgID:        cmd.OrgID,
		OrgName:      cmd.OrgName,
		SkipOrgSetup: cmd.SkipOrgSetup,
	}
	orgID, err := s.orgService.GetIDForNewUser(ctx, cmdOrg)
	cmd.OrgID = orgID
	if err != nil {
		return nil, err
	}

	if cmd.Email == "" {
		cmd.Email = cmd.Login
	}
	usr := &user.User{
		Login: cmd.Login,
		Email: cmd.Email,
	}
	usr, err = s.store.Get(ctx, usr)
	if err != nil && !errors.Is(err, user.ErrUserNotFound) {
		return usr, err
	}

	// create user
	usr = &user.User{
		Email:            cmd.Email,
		Name:             cmd.Name,
		Login:            cmd.Login,
		Company:          cmd.Company,
		IsAdmin:          cmd.IsAdmin,
		IsDisabled:       cmd.IsDisabled,
		OrgID:            cmd.OrgID,
		EmailVerified:    cmd.EmailVerified,
		Created:          time.Now(),
		Updated:          time.Now(),
		LastSeenAt:       time.Now().AddDate(-10, 0, 0),
		IsServiceAccount: cmd.IsServiceAccount,
	}

	salt, err := util.GetRandomString(10)
	if err != nil {
		return nil, err
	}
	usr.Salt = salt
	rands, err := util.GetRandomString(10)
	if err != nil {
		return nil, err
	}
	usr.Rands = rands

	if len(cmd.Password) > 0 {
		encodedPassword, err := util.EncodePassword(cmd.Password, usr.Salt)
		if err != nil {
			return nil, err
		}
		usr.Password = encodedPassword
	}

	userID, err := s.store.Insert(ctx, usr)
	if err != nil {
		return nil, err
	}

	// create org user link
	if !cmd.SkipOrgSetup {
		orgUser := org.OrgUser{
			OrgID:   orgID,
			UserID:  usr.ID,
			Role:    org.ROLE_ADMIN,
			Created: time.Now(),
			Updated: time.Now(),
		}

		if setting.AutoAssignOrg && !usr.IsAdmin {
			if len(cmd.DefaultOrgRole) > 0 {
				orgUser.Role = org.RoleType(cmd.DefaultOrgRole)
			} else {
				orgUser.Role = org.RoleType(setting.AutoAssignOrgRole)
			}
		}
		_, err = s.orgService.InsertOrgUser(ctx, &orgUser)
		if err != nil {
			err := s.store.Delete(ctx, userID)
			return usr, err
		}
	}

	return usr, nil
}

func (s *Service) Delete(ctx context.Context, cmd *user.DeleteUserCommand) error {
	_, err := s.store.GetNotServiceAccount(ctx, cmd.UserID)
	if err != nil {
		return err
	}
	// delete from all the stores
	if err := s.store.Delete(ctx, cmd.UserID); err != nil {
		return err
	}

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if err := s.starService.DeleteByUser(ctx, cmd.UserID); err != nil {
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := s.orgService.DeleteUserFromAll(ctx, cmd.UserID); err != nil {
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := s.dashboardService.DeleteACLByUser(ctx, cmd.UserID); err != nil {
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := s.preferenceService.DeleteByUser(ctx, cmd.UserID); err != nil {
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := s.teamMemberService.DeleteByUser(ctx, cmd.UserID); err != nil {
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := s.userAuthService.Delete(ctx, cmd.UserID); err != nil {
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := s.userAuthService.DeleteToken(ctx, cmd.UserID); err != nil {
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := s.quotaService.DeleteByUser(ctx, cmd.UserID); err != nil {
			return err
		}
		return nil
	})
	g.Go(func() error {
		if err := s.accessControlStore.DeleteUserPermissions(ctx, cmd.UserID); err != nil {
			return err
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return err
	}

	return nil
}

func (s *Service) GetByID(ctx context.Context, query *user.GetUserByIDQuery) (*user.User, error) {
	user, err := s.store.GetByID(ctx, query.ID)
	if err != nil {
		return nil, err
	}
	if s.cfg.CaseInsensitiveLogin {
		if err := s.store.CaseInsensitiveLoginConflict(ctx, user.Login, user.Email); err != nil {
			return nil, err
		}
	}
	return user, nil
}

//  TODO: remove wrapper around sqlstore
func (s *Service) GetByLogin(ctx context.Context, query *user.GetUserByLoginQuery) (*user.User, error) {
	q := models.GetUserByLoginQuery{LoginOrEmail: query.LoginOrEmail}
	err := s.sqlStore.GetUserByLogin(ctx, &q)
	if err != nil {
		return nil, err
	}
	return q.Result, nil
}

//  TODO: remove wrapper around sqlstore
func (s *Service) GetByEmail(ctx context.Context, query *user.GetUserByEmailQuery) (*user.User, error) {
	q := models.GetUserByEmailQuery{Email: query.Email}
	err := s.sqlStore.GetUserByEmail(ctx, &q)
	if err != nil {
		return nil, err
	}
	return q.Result, nil
}

//  TODO: remove wrapper around sqlstore
func (s *Service) Update(ctx context.Context, cmd *user.UpdateUserCommand) error {
	q := &models.UpdateUserCommand{
		Name:   cmd.Name,
		Email:  cmd.Email,
		Login:  cmd.Login,
		Theme:  cmd.Theme,
		UserId: cmd.UserID,
	}
	return s.sqlStore.UpdateUser(ctx, q)
}

//  TODO: remove wrapper around sqlstore
func (s *Service) ChangePassword(ctx context.Context, cmd *user.ChangeUserPasswordCommand) error {
	q := &models.ChangeUserPasswordCommand{
		UserId:      cmd.UserID,
		NewPassword: cmd.NewPassword,
		OldPassword: cmd.OldPassword,
	}
	return s.sqlStore.ChangeUserPassword(ctx, q)
}

//  TODO: remove wrapper around sqlstore
func (s *Service) UpdateLastSeenAt(ctx context.Context, cmd *user.UpdateUserLastSeenAtCommand) error {
	q := &models.UpdateUserLastSeenAtCommand{
		UserId: cmd.UserID,
	}
	return s.sqlStore.UpdateUserLastSeenAt(ctx, q)
}
