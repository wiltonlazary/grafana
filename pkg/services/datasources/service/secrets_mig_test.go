package service

import (
	"context"
	"testing"

	"github.com/grafana/grafana/pkg/infra/kvstore"
	acmock "github.com/grafana/grafana/pkg/services/accesscontrol/mock"
	"github.com/grafana/grafana/pkg/services/datasources"
	"github.com/grafana/grafana/pkg/services/featuremgmt"
	"github.com/grafana/grafana/pkg/services/secrets/fakes"
	secretsStore "github.com/grafana/grafana/pkg/services/secrets/kvstore"
	secretsManager "github.com/grafana/grafana/pkg/services/secrets/manager"
	"github.com/grafana/grafana/pkg/services/sqlstore"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/stretchr/testify/assert"
)

func SetupTestMigrationService(t *testing.T, sqlStore *sqlstore.SQLStore, kvStore kvstore.KVStore, secretsStore secretsStore.SecretsKVStore, compatibility bool) *DataSourceSecretMigrationService {
	t.Helper()
	cfg := &setting.Cfg{}
	features := featuremgmt.WithFeatures()
	if !compatibility {
		features = featuremgmt.WithFeatures(featuremgmt.FlagDisableSecretsCompatibility, true)
	}
	secretsService := secretsManager.SetupTestService(t, fakes.NewFakeSecretsStore())
	dsService := ProvideService(sqlStore, secretsService, secretsStore, cfg, features, acmock.New().WithDisabled(), acmock.NewMockedPermissionsService())
	migService := ProvideDataSourceMigrationService(dsService, kvStore, features)
	return migService
}

func TestMigrate(t *testing.T) {
	t.Run("should migrate from legacy to unified without compatibility", func(t *testing.T) {
		sqlStore := sqlstore.InitTestDB(t)
		kvStore := kvstore.ProvideService(sqlStore)
		secretsStore := secretsStore.SetupTestService(t)
		migService := SetupTestMigrationService(t, sqlStore, kvStore, secretsStore, false)

		dataSourceName := "Test"
		dataSourceOrg := int64(1)

		// Add test data source
		err := sqlStore.AddDataSource(context.Background(), &datasources.AddDataSourceCommand{
			OrgId:  dataSourceOrg,
			Name:   dataSourceName,
			Type:   datasources.DS_MYSQL,
			Access: datasources.DS_ACCESS_DIRECT,
			Url:    "http://test",
			EncryptedSecureJsonData: map[string][]byte{
				"password": []byte("9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"),
			},
		})
		assert.NoError(t, err)

		// Check if the secret json data was added
		query := &datasources.GetDataSourceQuery{OrgId: dataSourceOrg, Name: dataSourceName}
		err = sqlStore.GetDataSource(context.Background(), query)
		assert.NoError(t, err)
		assert.NotNil(t, query.Result)
		assert.NotEmpty(t, query.Result.SecureJsonData)

		// Check if the migration status key is empty
		value, exist, err := kvStore.Get(context.Background(), 0, secretType, secretMigrationStatusKey)
		assert.NoError(t, err)
		assert.Empty(t, value)
		assert.False(t, exist)

		// Check that the secret is not present on the secret store
		value, exist, err = secretsStore.Get(context.Background(), dataSourceOrg, dataSourceName, secretType)
		assert.NoError(t, err)
		assert.Empty(t, value)
		assert.False(t, exist)

		// Run the migration
		err = migService.Migrate(context.Background())
		assert.NoError(t, err)

		// Check if the secure json data was deleted
		query = &datasources.GetDataSourceQuery{OrgId: dataSourceOrg, Name: dataSourceName}
		err = sqlStore.GetDataSource(context.Background(), query)
		assert.NoError(t, err)
		assert.NotNil(t, query.Result)
		assert.Empty(t, query.Result.SecureJsonData)

		// Check if the secret was added to the secret store
		value, exist, err = secretsStore.Get(context.Background(), dataSourceOrg, dataSourceName, secretType)
		assert.NoError(t, err)
		assert.NotEmpty(t, value)
		assert.True(t, exist)

		// Check if the migration status key was set
		value, exist, err = kvStore.Get(context.Background(), 0, secretType, secretMigrationStatusKey)
		assert.NoError(t, err)
		assert.Equal(t, completeSecretMigrationValue, value)
		assert.True(t, exist)
	})

	t.Run("should migrate from legacy to unified with compatibility", func(t *testing.T) {
		sqlStore := sqlstore.InitTestDB(t)
		kvStore := kvstore.ProvideService(sqlStore)
		secretsStore := secretsStore.SetupTestService(t)
		migService := SetupTestMigrationService(t, sqlStore, kvStore, secretsStore, true)

		dataSourceName := "Test"
		dataSourceOrg := int64(1)

		// Add test data source
		err := sqlStore.AddDataSource(context.Background(), &datasources.AddDataSourceCommand{
			OrgId:  dataSourceOrg,
			Name:   dataSourceName,
			Type:   datasources.DS_MYSQL,
			Access: datasources.DS_ACCESS_DIRECT,
			Url:    "http://test",
			EncryptedSecureJsonData: map[string][]byte{
				"password": []byte("9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"),
			},
		})
		assert.NoError(t, err)

		// Check if the secret json data was added
		query := &datasources.GetDataSourceQuery{OrgId: dataSourceOrg, Name: dataSourceName}
		err = sqlStore.GetDataSource(context.Background(), query)
		assert.NoError(t, err)
		assert.NotNil(t, query.Result)
		assert.NotEmpty(t, query.Result.SecureJsonData)

		// Check if the migration status key is empty
		value, exist, err := kvStore.Get(context.Background(), 0, secretType, secretMigrationStatusKey)
		assert.NoError(t, err)
		assert.Empty(t, value)
		assert.False(t, exist)

		// Check that the secret is not present on the secret store
		value, exist, err = secretsStore.Get(context.Background(), dataSourceOrg, dataSourceName, secretType)
		assert.NoError(t, err)
		assert.Empty(t, value)
		assert.False(t, exist)

		// Run the migration
		err = migService.Migrate(context.Background())
		assert.NoError(t, err)

		// Check if the secure json data was maintained for compatibility
		query = &datasources.GetDataSourceQuery{OrgId: dataSourceOrg, Name: dataSourceName}
		err = sqlStore.GetDataSource(context.Background(), query)
		assert.NoError(t, err)
		assert.NotNil(t, query.Result)
		assert.NotEmpty(t, query.Result.SecureJsonData)

		// Check if the secret was added to the secret store
		value, exist, err = secretsStore.Get(context.Background(), dataSourceOrg, dataSourceName, secretType)
		assert.NoError(t, err)
		assert.NotEmpty(t, value)
		assert.True(t, exist)

		// Check if the migration status key was set
		value, exist, err = kvStore.Get(context.Background(), 0, secretType, secretMigrationStatusKey)
		assert.NoError(t, err)
		assert.Equal(t, compatibleSecretMigrationValue, value)
		assert.True(t, exist)
	})

	t.Run("should replicate from unified to legacy for compatibility", func(t *testing.T) {
		sqlStore := sqlstore.InitTestDB(t)
		kvStore := kvstore.ProvideService(sqlStore)
		secretsStore := secretsStore.SetupTestService(t)
		migService := SetupTestMigrationService(t, sqlStore, kvStore, secretsStore, false)

		dataSourceName := "Test"
		dataSourceOrg := int64(1)

		// Add test data source
		err := sqlStore.AddDataSource(context.Background(), &datasources.AddDataSourceCommand{
			OrgId:  dataSourceOrg,
			Name:   dataSourceName,
			Type:   datasources.DS_MYSQL,
			Access: datasources.DS_ACCESS_DIRECT,
			Url:    "http://test",
			EncryptedSecureJsonData: map[string][]byte{
				"password": []byte("9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"),
			},
		})
		assert.NoError(t, err)

		// Check if the secret json data was added
		query := &datasources.GetDataSourceQuery{OrgId: dataSourceOrg, Name: dataSourceName}
		err = sqlStore.GetDataSource(context.Background(), query)
		assert.NoError(t, err)
		assert.NotNil(t, query.Result)
		assert.NotEmpty(t, query.Result.SecureJsonData)

		// Check if the migration status key is empty
		value, exist, err := kvStore.Get(context.Background(), 0, secretType, secretMigrationStatusKey)
		assert.NoError(t, err)
		assert.Empty(t, value)
		assert.False(t, exist)

		// Check that the secret is not present on the secret store
		value, exist, err = secretsStore.Get(context.Background(), dataSourceOrg, dataSourceName, secretType)
		assert.NoError(t, err)
		assert.Empty(t, value)
		assert.False(t, exist)

		// Run the migration without compatibility
		err = migService.Migrate(context.Background())
		assert.NoError(t, err)

		// Check if the secure json data was deleted
		query = &datasources.GetDataSourceQuery{OrgId: dataSourceOrg, Name: dataSourceName}
		err = sqlStore.GetDataSource(context.Background(), query)
		assert.NoError(t, err)
		assert.NotNil(t, query.Result)
		assert.Empty(t, query.Result.SecureJsonData)

		// Check if the secret was added to the secret store
		value, exist, err = secretsStore.Get(context.Background(), dataSourceOrg, dataSourceName, secretType)
		assert.NoError(t, err)
		assert.NotEmpty(t, value)
		assert.True(t, exist)

		// Check if the migration status key was set
		value, exist, err = kvStore.Get(context.Background(), 0, secretType, secretMigrationStatusKey)
		assert.NoError(t, err)
		assert.Equal(t, completeSecretMigrationValue, value)
		assert.True(t, exist)

		// Run the migration with compatibility
		migService = SetupTestMigrationService(t, sqlStore, kvStore, secretsStore, true)
		err = migService.Migrate(context.Background())
		assert.NoError(t, err)

		// Check if the secure json data was re-added for compatibility
		query = &datasources.GetDataSourceQuery{OrgId: dataSourceOrg, Name: dataSourceName}
		err = sqlStore.GetDataSource(context.Background(), query)
		assert.NoError(t, err)
		assert.NotNil(t, query.Result)
		assert.NotEmpty(t, query.Result.SecureJsonData)

		// Check if the secret was added to the secret store
		value, exist, err = secretsStore.Get(context.Background(), dataSourceOrg, dataSourceName, secretType)
		assert.NoError(t, err)
		assert.NotEmpty(t, value)
		assert.True(t, exist)

		// Check if the migration status key was set
		value, exist, err = kvStore.Get(context.Background(), 0, secretType, secretMigrationStatusKey)
		assert.NoError(t, err)
		assert.Equal(t, compatibleSecretMigrationValue, value)
		assert.True(t, exist)
	})

	t.Run("should delete from legacy to remove compatibility", func(t *testing.T) {
		sqlStore := sqlstore.InitTestDB(t)
		kvStore := kvstore.ProvideService(sqlStore)
		secretsStore := secretsStore.SetupTestService(t)
		migService := SetupTestMigrationService(t, sqlStore, kvStore, secretsStore, true)

		dataSourceName := "Test"
		dataSourceOrg := int64(1)

		// Add test data source
		err := sqlStore.AddDataSource(context.Background(), &datasources.AddDataSourceCommand{
			OrgId:  dataSourceOrg,
			Name:   dataSourceName,
			Type:   datasources.DS_MYSQL,
			Access: datasources.DS_ACCESS_DIRECT,
			Url:    "http://test",
			EncryptedSecureJsonData: map[string][]byte{
				"password": []byte("9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"),
			},
		})
		assert.NoError(t, err)

		// Check if the secret json data was added
		query := &datasources.GetDataSourceQuery{OrgId: dataSourceOrg, Name: dataSourceName}
		err = sqlStore.GetDataSource(context.Background(), query)
		assert.NoError(t, err)
		assert.NotNil(t, query.Result)
		assert.NotEmpty(t, query.Result.SecureJsonData)

		// Check if the migration status key is empty
		value, exist, err := kvStore.Get(context.Background(), 0, secretType, secretMigrationStatusKey)
		assert.NoError(t, err)
		assert.Empty(t, value)
		assert.False(t, exist)

		// Check that the secret is not present on the secret store
		value, exist, err = secretsStore.Get(context.Background(), dataSourceOrg, dataSourceName, secretType)
		assert.NoError(t, err)
		assert.Empty(t, value)
		assert.False(t, exist)

		// Run the migration with compatibility
		err = migService.Migrate(context.Background())
		assert.NoError(t, err)

		// Check if the secure json data was maintained for compatibility
		query = &datasources.GetDataSourceQuery{OrgId: dataSourceOrg, Name: dataSourceName}
		err = sqlStore.GetDataSource(context.Background(), query)
		assert.NoError(t, err)
		assert.NotNil(t, query.Result)
		assert.NotEmpty(t, query.Result.SecureJsonData)

		// Check if the secret was added to the secret store
		value, exist, err = secretsStore.Get(context.Background(), dataSourceOrg, dataSourceName, secretType)
		assert.NoError(t, err)
		assert.NotEmpty(t, value)
		assert.True(t, exist)

		// Check if the migration status key was set
		value, exist, err = kvStore.Get(context.Background(), 0, secretType, secretMigrationStatusKey)
		assert.NoError(t, err)
		assert.Equal(t, compatibleSecretMigrationValue, value)
		assert.True(t, exist)

		// Run the migration without compatibility
		migService = SetupTestMigrationService(t, sqlStore, kvStore, secretsStore, false)
		err = migService.Migrate(context.Background())
		assert.NoError(t, err)

		// Check if the secure json data was deleted
		query = &datasources.GetDataSourceQuery{OrgId: dataSourceOrg, Name: dataSourceName}
		err = sqlStore.GetDataSource(context.Background(), query)
		assert.NoError(t, err)
		assert.NotNil(t, query.Result)
		assert.Empty(t, query.Result.SecureJsonData)

		// Check if the secret was added to the secret store
		value, exist, err = secretsStore.Get(context.Background(), dataSourceOrg, dataSourceName, secretType)
		assert.NoError(t, err)
		assert.NotEmpty(t, value)
		assert.True(t, exist)

		// Check if the migration status key was set
		value, exist, err = kvStore.Get(context.Background(), 0, secretType, secretMigrationStatusKey)
		assert.NoError(t, err)
		assert.Equal(t, completeSecretMigrationValue, value)
		assert.True(t, exist)
	})
}
