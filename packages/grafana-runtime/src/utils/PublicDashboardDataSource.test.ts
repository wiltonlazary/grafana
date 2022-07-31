import { of } from 'rxjs';
import { BackendSrv, BackendSrvRequest } from 'src/services';

import { DataQueryRequest, DataSourceInstanceSettings, DataSourceRef } from '@grafana/data';

import {
  PUBLIC_DATASOURCE,
  PublicDashboardDataSource,
} from '../../../../public/app/features/dashboard/services/PublicDashboardDataSource';
import { MIXED_DATASOURCE_NAME } from '../../../../public/app/plugins/datasource/mixed/MixedDataSource';

import { DataSourceWithBackend } from './DataSourceWithBackend';

const mockDatasourceRequest = jest.fn();

const backendSrv = {
  fetch: (options: BackendSrvRequest) => {
    return of(mockDatasourceRequest(options));
  },
} as unknown as BackendSrv;

jest.mock('../services', () => ({
  ...jest.requireActual('../services'),
  getBackendSrv: () => backendSrv,
  getDataSourceSrv: () => {
    return {
      getInstanceSettings: (ref?: DataSourceRef) => ({ type: ref?.type ?? '?', uid: ref?.uid ?? '?' }),
    };
  },
}));

describe('PublicDashboardDatasource', () => {
  test('Fetches results from the pubdash query endpoint', () => {
    mockDatasourceRequest.mockReset();
    mockDatasourceRequest.mockReturnValue(Promise.resolve({}));

    const ds = new PublicDashboardDataSource('public');
    const panelId = 1;
    const publicDashboardAccessToken = 'abc123';

    ds.query({
      maxDataPoints: 10,
      intervalMs: 5000,
      targets: [{ refId: 'A' }, { refId: 'B', datasource: { type: 'sample' } }],
      panelId,
      publicDashboardAccessToken,
    } as DataQueryRequest);

    const mock = mockDatasourceRequest.mock;

    expect(mock.calls.length).toBe(1);
    expect(mock.lastCall[0].url).toEqual(
      `/api/public/dashboards/${publicDashboardAccessToken}/panels/${panelId}/query`
    );
  });

  test('returns public datasource uid when datasource passed in is null', () => {
    let ds = new PublicDashboardDataSource(null);
    expect(ds.uid).toBe(PUBLIC_DATASOURCE);
  });

  test('returns datasource when datasource passed in is a string', () => {
    let ds = new PublicDashboardDataSource('theDatasourceUid');
    expect(ds.uid).toBe('theDatasourceUid');
  });

  test('returns datasource uid when datasource passed in is a DataSourceRef implementation', () => {
    const datasource = { type: 'datasource', uid: 'abc123' };
    let ds = new PublicDashboardDataSource(datasource);
    expect(ds.uid).toBe('abc123');
  });

  test('returns datasource uid when datasource passed in is a DatasourceApi instance', () => {
    const settings: DataSourceInstanceSettings = { id: 1, uid: 'abc123' } as DataSourceInstanceSettings;
    const datasource = new DataSourceWithBackend(settings);
    let ds = new PublicDashboardDataSource(datasource);
    expect(ds.uid).toBe('abc123');
  });

  test('isMixedDatasource returns true when datasource is mixed', () => {
    const datasource = new DataSourceWithBackend({ id: 1, uid: MIXED_DATASOURCE_NAME } as DataSourceInstanceSettings);
    let ds = new PublicDashboardDataSource(datasource);
    expect(ds.meta.mixed).toBeTruthy();
  });

  test('isMixedDatasource returns false when datasource is not mixed', () => {
    const datasource = new DataSourceWithBackend({ id: 1, uid: 'abc123' } as DataSourceInstanceSettings);
    let ds = new PublicDashboardDataSource(datasource);
    expect(ds.meta.mixed).toBeFalsy();
  });

  test('isMixedDatasource returns false when datasource is a string', () => {
    let ds = new PublicDashboardDataSource('abc123');
    expect(ds.meta.mixed).toBeFalsy();
  });

  test('isMixedDatasource returns false when datasource is null', () => {
    let ds = new PublicDashboardDataSource(null);
    expect(ds.meta.mixed).toBeFalsy();
  });
});
